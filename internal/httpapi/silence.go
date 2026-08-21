package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/cropalato/promview/internal/alertmanager"
	"github.com/cropalato/promview/internal/alerts"
	"github.com/cropalato/promview/internal/auth"
)

/*
Silencing is the only action here that hides alerts rather than surfacing them,
and the only one that writes to a system promview does not own. Three things
follow from that, and they are enforced here rather than left to the caller:

  - It needs operator rights, and the scope query re-checks them in SQL, so an
    operator scoped to one team cannot silence another team's alerts by naming a
    group that spans both.
  - It is always attributed to the signed-in person. Open mode has no user and
    no operator rights, so it cannot reach this at all.
  - It always ends. The window is bounded by configuration, because a silence
    long enough to outlast the on-call rotation is a deleted rule wearing a
    disguise.
*/

const maxSilenceBodyBytes = 4096

// Silencer creates a silence on one Alertmanager. Narrow on purpose: the
// transport should not be able to read alerts back out of the Alertmanager.
type Silencer interface {
	CreateSilence(ctx context.Context, baseURL string, token string, silence alertmanager.Silence) (string, error)
}

type silenceRequest struct {
	// Seconds the silence lasts. Zero or absent takes the deployment default.
	DurationSeconds int64  `json:"durationSeconds"`
	Comment         string `json:"comment"`
}

type groupSilenceRequest struct {
	GroupBy         []string          `json:"groupBy"`
	Key             map[string]string `json:"key"`
	DurationSeconds int64             `json:"durationSeconds"`
	Comment         string            `json:"comment"`
}

func (api *API) silenceAlert(w http.ResponseWriter, r *http.Request) {
	principal, ok := api.silenceRequestPrincipal(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "alert id is invalid")
		return
	}
	var body silenceRequest
	if !decodeSilenceBody(w, r, &body) {
		return
	}
	scope, err := api.store.SilenceScopeForAlert(r.Context(), principal, id)
	if !api.writeScopeError(w, err) {
		return
	}
	api.createSilences(w, r, principal, scope, body.DurationSeconds, body.Comment)
}

func (api *API) silenceGroup(w http.ResponseWriter, r *http.Request) {
	principal, ok := api.silenceRequestPrincipal(w, r)
	if !ok {
		return
	}
	var body groupSilenceRequest
	if !decodeSilenceBody(w, r, &body) {
		return
	}
	if len(body.GroupBy) == 0 || len(body.Key) == 0 {
		writeError(w, http.StatusBadRequest, "groupBy and key are required")
		return
	}
	scope, err := api.store.SilenceScopeForGroup(r.Context(), principal, body.GroupBy, body.Key)
	if !api.writeScopeError(w, err) {
		return
	}
	api.createSilences(w, r, principal, scope, body.DurationSeconds, body.Comment)
}

// silenceRequestPrincipal runs the checks every silence shares: who is asking,
// whether they may operate, and whether the request came from this console.
func (api *API) silenceRequestPrincipal(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	principal, ok := requestPrincipal(r)
	if !ok {
		writeError(w, http.StatusInternalServerError, "principal is unavailable")
		return auth.Principal{}, false
	}
	if api.silencer == nil {
		writeError(w, http.StatusNotImplemented, "silencing is not configured")
		return auth.Principal{}, false
	}
	if !principal.CanOperate() {
		writeError(w, http.StatusForbidden, "operator access required")
		return auth.Principal{}, false
	}
	if !validMutationOrigin(r) {
		writeError(w, http.StatusForbidden, "invalid request origin")
		return auth.Principal{}, false
	}
	if silenceAuthor(principal) == "" {
		// Every silence is attributable or it is not created. Nothing should
		// reach here — operator rights imply a user — but an unattributed
		// silence is exactly the artefact nobody can later explain.
		writeError(w, http.StatusForbidden, "a silence needs a named author")
		return auth.Principal{}, false
	}
	return principal, true
}

func decodeSilenceBody(w http.ResponseWriter, r *http.Request, into any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSilenceBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request body is invalid")
		return false
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		writeError(w, http.StatusBadRequest, "request body is invalid")
		return false
	}
	return true
}

// writeScopeError maps a scope failure onto a response, returning false when it
// wrote one.
func (api *API) writeScopeError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, alerts.ErrNotFound):
		writeError(w, http.StatusNotFound, "alert not found")
	case errors.Is(err, alerts.ErrNoSilenceTarget):
		writeError(w, http.StatusConflict, "no source in scope has an alertmanager url configured")
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
	return false
}

func (api *API) createSilences(
	w http.ResponseWriter,
	r *http.Request,
	principal auth.Principal,
	scope alerts.SilenceScope,
	durationSeconds int64,
	comment string,
) {
	window, err := api.silenceWindow(durationSeconds)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	startsAt := time.Now().UTC()
	silence := alertmanager.Silence{
		Matchers:  alertmanager.MatchersFromLabels(scope.Labels),
		StartsAt:  startsAt,
		EndsAt:    startsAt.Add(window),
		CreatedBy: silenceAuthor(principal),
		Comment:   comment,
	}

	results := make([]alerts.SilenceResult, 0, len(scope.Targets))
	created := 0
	for _, target := range scope.Targets {
		id, err := api.silencer.CreateSilence(r.Context(), target.AlertmanagerURL, target.AlertmanagerToken, silence)
		if err != nil {
			// One Alertmanager refusing must not hide that the others accepted;
			// an operator who thinks a group is handled when half of it is still
			// firing is worse off than one told exactly which half failed.
			results = append(results, alerts.SilenceResult{Source: target.Source, Error: err.Error()})
			continue
		}
		created++
		results = append(results, alerts.SilenceResult{Source: target.Source, SilenceID: id})
	}

	status := http.StatusCreated
	switch {
	case created == 0:
		status = http.StatusBadGateway
	case created < len(scope.Targets):
		status = http.StatusMultiStatus
	}
	writeJSON(w, status, map[string]any{
		"endsAt":    silence.EndsAt.Format(time.RFC3339),
		"createdBy": silence.CreatedBy,
		"matchers":  silence.Matchers,
		"results":   results,
	})
}

// silenceWindow resolves the requested duration against the deployment's
// default and ceiling.
func (api *API) silenceWindow(durationSeconds int64) (time.Duration, error) {
	if durationSeconds == 0 {
		return api.config.SilenceDefaultDuration, nil
	}
	if durationSeconds < 0 {
		return 0, errors.New("durationSeconds must be positive")
	}
	window := time.Duration(durationSeconds) * time.Second
	if window > api.config.SilenceMaxDuration {
		return 0, errors.New("durationSeconds exceeds the configured maximum silence duration")
	}
	return window, nil
}

// silenceAuthor is who the Alertmanager will record. Email first: it is what
// identifies a person across the tools an incident actually spans.
func silenceAuthor(principal auth.Principal) string {
	if principal.Anonymous {
		return ""
	}
	for _, candidate := range []string{principal.Email, principal.Subject, principal.DisplayName} {
		if candidate != "" {
			return candidate
		}
	}
	return ""
}
