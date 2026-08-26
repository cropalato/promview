package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cropalato/promview/internal/alertmanager"
	"github.com/cropalato/promview/internal/alerts"
	"github.com/cropalato/promview/internal/auth"
	"github.com/cropalato/promview/internal/config"
	"github.com/cropalato/promview/internal/preferences"
)

const maxWebhookBodyBytes = 4 << 20

var labelNamePattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

type Store interface {
	Ingest(context.Context, []alertmanager.IncomingAlert) error
	AuthenticateSource(context.Context, string, string) (bool, error)
	ListAlerts(context.Context, auth.Principal, alerts.Query) (alerts.ListResult, error)
	GroupAlerts(context.Context, auth.Principal, alerts.Query) (alerts.GroupResult, error)
	GetAlertDetail(context.Context, auth.Principal, int64) (alerts.Detail, error)
	AcknowledgeAlert(context.Context, auth.Principal, int64, bool) (alerts.Detail, error)
	SilenceScopeForAlert(context.Context, auth.Principal, int64) (alerts.SilenceScope, error)
	SilenceScopeForGroup(context.Context, auth.Principal, []string, map[string]string) (alerts.SilenceScope, error)
	RecordSilence(context.Context, alerts.SilenceRecord) error
	StreamEvents(context.Context, auth.Principal, int64, int) (alerts.StreamBatch, error)
	ReadPreferences(context.Context, auth.Principal) (preferences.Preferences, error)
	WritePreferences(context.Context, auth.Principal, preferences.Preferences) error
	Ping(context.Context) error
}

type API struct {
	config        config.Config
	store         Store
	authenticator auth.Authenticator
	// silencer is nil in a deployment that cannot write to an Alertmanager; the
	// silence routes then answer 501 rather than 404, so the console can tell
	// "not built" from "not configured here".
	silencer  Silencer
	observers Observers
}

// Observers are the hooks a caller can supply to watch the transport work.
//
// A struct rather than parameters because the set grows: it started as one
// request hook, and adding each new one positionally would have every existing
// call site naming things it does not care about. Any field may be nil.
type Observers struct {
	// Request records one served request. Route is the pattern that matched,
	// not the path that arrived.
	Request func(route, method string, status int, elapsed time.Duration)
	// StreamOpened and StreamClosed bracket one event-stream connection.
	StreamOpened func()
	StreamClosed func()
	// StreamPolled records one database read made for a stream client, and how
	// many events it returned. Every open console does this on a timer, so it
	// is the polling load rather than a sign of activity.
	StreamPolled func(events int)
}

func New(
	cfg config.Config,
	store Store,
	authenticator auth.Authenticator,
	silencer Silencer,
	authenticationHandlers ...http.Handler,
) http.Handler {
	return NewObserved(Observers{}, cfg, store, authenticator, silencer, authenticationHandlers...)
}

// NewObserved is New with instrumentation. It is a separate constructor rather
// than another parameter because observation is the one thing a caller can
// reasonably not want, and threading an empty struct through every existing
// call site would make every one of them mention it.
//
// Observers with no request hook returns the bare mux, so an uninstrumented
// deployment pays nothing at all - not even a wrapper.
func NewObserved(
	observers Observers,
	cfg config.Config,
	store Store,
	authenticator auth.Authenticator,
	silencer Silencer,
	authenticationHandlers ...http.Handler,
) http.Handler {
	api := &API{config: cfg, store: store, authenticator: authenticator, silencer: silencer, observers: observers}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/config", api.getConfig)
	mux.Handle("GET /api/v1/me", api.requireAuthentication(http.HandlerFunc(api.getMe)))
	mux.Handle("GET /api/v1/alerts", api.requireAuthentication(http.HandlerFunc(api.listAlerts)))
	mux.Handle("GET /api/v1/alerts/{id}", api.requireAuthentication(http.HandlerFunc(api.getAlert)))
	mux.Handle("GET /api/v1/alerts/{id}/events", api.requireAuthentication(http.HandlerFunc(api.getAlertEvents)))
	mux.Handle("POST /api/v1/alerts/{id}/acknowledge", api.requireAuthentication(http.HandlerFunc(api.acknowledgeAlert)))
	mux.Handle("POST /api/v1/alerts/{id}/silence", api.requireAuthentication(http.HandlerFunc(api.silenceAlert)))
	mux.Handle("POST /api/v1/groups/silence", api.requireAuthentication(http.HandlerFunc(api.silenceGroup)))
	mux.Handle("POST /api/v1/groups/silence/preview", api.requireAuthentication(http.HandlerFunc(api.previewGroupSilence)))
	mux.Handle("GET /api/v1/stream", api.requireAuthentication(http.HandlerFunc(api.streamAlerts)))
	mux.Handle("GET /api/v1/preferences", api.requireAuthentication(http.HandlerFunc(api.getPreferences)))
	mux.Handle("PUT /api/v1/preferences", api.requireAuthentication(http.HandlerFunc(api.putPreferences)))
	mux.HandleFunc("POST /api/v1/ingest/alertmanager/{source}", api.ingestAlertmanager)
	if len(authenticationHandlers) > 0 && authenticationHandlers[0] != nil {
		mux.Handle("GET /api/v1/auth/oidc/login", authenticationHandlers[0])
		mux.Handle("GET /api/v1/auth/oidc/callback", authenticationHandlers[0])
		mux.Handle("POST /api/v1/auth/logout", authenticationHandlers[0])
		// The desktop client cannot receive the cookie the browser flow ends
		// in; it redeems a one-time code for the same session instead.
		//
		// Method-prefixed like the routes above, which means a GET here does
		// not match and falls through to the SPA route, answering index.html
		// rather than 405. A method-less pattern would answer correctly but
		// ServeMux refuses one: it conflicts with `GET /`. The handler keeps
		// its own method guard regardless, so it is right when called directly
		// and nothing but a page is served when it is not.
		mux.Handle("POST /api/v1/auth/desktop/exchange", authenticationHandlers[0])
	}
	mux.HandleFunc("GET /health/live", api.live)
	mux.HandleFunc("GET /health/ready", api.ready)
	mux.Handle("GET /", spaHandler(cfg.WebDirectory))
	if observers.Request == nil {
		return mux
	}
	return observeRequests(mux, observers.Request)
}

// observeRequests times each request and reports it against the route that
// matched.
//
// The pattern is asked of the mux before serving rather than read from the
// request afterwards: ServeMux sets Pattern on the request it hands down, which
// is not the one out here, so by the time this sees it again the field is still
// empty. Routing twice costs a trie lookup and buys a label that cannot carry
// an alert id.
func observeRequests(mux *http.ServeMux, observe func(string, string, int, time.Duration)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pattern := mux.Handler(r)
		if pattern == "" {
			// Nothing matched. Reporting the path instead would make every
			// stray request its own time series.
			pattern = "unmatched"
		}
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		started := time.Now()
		mux.ServeHTTP(recorder, r)
		observe(pattern, r.Method, recorder.status, time.Since(started))
	})
}

// statusRecorder remembers the status code on its way out.
//
// It forwards Flush because /api/v1/stream is a long-lived SSE response: a
// wrapper that swallowed Flush would leave every event sitting in a buffer, and
// the console would look connected while receiving nothing.
type statusRecorder struct {
	http.ResponseWriter
	status  int
	written bool
}

func (recorder *statusRecorder) WriteHeader(status int) {
	if !recorder.written {
		recorder.status = status
		recorder.written = true
	}
	recorder.ResponseWriter.WriteHeader(status)
}

func (recorder *statusRecorder) Write(b []byte) (int, error) {
	recorder.written = true
	return recorder.ResponseWriter.Write(b)
}

func (recorder *statusRecorder) Flush() {
	if flusher, ok := recorder.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

type principalContextKey struct{}

func (api *API) requireAuthentication(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, err := api.authenticator.Authenticate(r.Context(), r)
		if err != nil {
			if errors.Is(err, auth.ErrUnauthenticated) {
				writeError(w, http.StatusUnauthorized, "authentication required")
			} else {
				writeServerError(w, r, "authentication failed", err)
			}
			return
		}
		if !principal.CanRead() {
			writeError(w, http.StatusForbidden, "read access denied")
			return
		}
		ctx := context.WithValue(r.Context(), principalContextKey{}, principal)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (api *API) getMe(w http.ResponseWriter, r *http.Request) {
	principal, ok := requestPrincipal(r)
	if !ok {
		writeServerError(w, r, "principal is unavailable", nil)
		return
	}
	writeJSON(w, http.StatusOK, principal)
}

func (api *API) getAlert(w http.ResponseWriter, r *http.Request) {
	detail, ok := api.loadAlertDetail(w, r)
	if !ok {
		return
	}
	principal, ok := requestPrincipal(r)
	if !ok {
		writeServerError(w, r, "principal is unavailable", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"alert":    newAlertDetailResponse(detail.Alert, auth.CanOperateLabels(principal, detail.Alert.Labels), api.silencer != nil),
		"history":  detail.History,
		"silences": silenceRecordsResponse(detail.Silences),
	})
}

// silenceRecordsResponse never emits null: the console distinguishes "no
// promview-created silence explains this" from "the field is missing", and a
// null would make those read the same.
func silenceRecordsResponse(records []alerts.SilenceRecord) []alerts.SilenceRecord {
	if records == nil {
		return []alerts.SilenceRecord{}
	}
	return records
}

func (api *API) acknowledgeAlert(w http.ResponseWriter, r *http.Request) {
	principal, ok := requestPrincipal(r)
	if !ok {
		writeServerError(w, r, "principal is unavailable", nil)
		return
	}
	if !principal.CanOperate() {
		writeError(w, http.StatusForbidden, "operator access required")
		return
	}
	if !validMutationOrigin(r) {
		writeError(w, http.StatusForbidden, "invalid request origin")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "alert id is invalid")
		return
	}
	var body struct {
		Acknowledged *bool `json:"acknowledged"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || body.Acknowledged == nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeError(w, http.StatusBadRequest, "acknowledged must be a boolean")
		return
	}
	detail, err := api.store.AcknowledgeAlert(r.Context(), principal, id, *body.Acknowledged)
	if errors.Is(err, alerts.ErrNotFound) {
		writeError(w, http.StatusNotFound, "alert not found")
		return
	}
	if err != nil {
		writeServerError(w, r, "failed to update acknowledgement", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"alert":    newAlertDetailResponse(detail.Alert, auth.CanOperateLabels(principal, detail.Alert.Labels), api.silencer != nil),
		"history":  detail.History,
		"silences": silenceRecordsResponse(detail.Silences),
	})
}

func (api *API) getAlertEvents(w http.ResponseWriter, r *http.Request) {
	detail, ok := api.loadAlertDetail(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"history": detail.History})
}

func (api *API) loadAlertDetail(w http.ResponseWriter, r *http.Request) (alerts.Detail, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "alert id is invalid")
		return alerts.Detail{}, false
	}
	principal, ok := requestPrincipal(r)
	if !ok {
		writeServerError(w, r, "principal is unavailable", nil)
		return alerts.Detail{}, false
	}
	detail, err := api.store.GetAlertDetail(r.Context(), principal, id)
	if errors.Is(err, alerts.ErrNotFound) {
		writeError(w, http.StatusNotFound, "alert not found")
		return alerts.Detail{}, false
	}
	if err != nil {
		writeServerError(w, r, "failed to query alert", err)
		return alerts.Detail{}, false
	}
	return detail, true
}

func (api *API) listAlerts(w http.ResponseWriter, r *http.Request) {
	query, err := parseAlertQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	principal, ok := requestPrincipal(r)
	if !ok {
		writeServerError(w, r, "principal is unavailable", nil)
		return
	}
	if len(query.GroupBy) > 0 {
		api.listAlertGroups(w, r, principal, query)
		return
	}
	result, err := api.store.ListAlerts(r.Context(), principal, query)
	if err != nil {
		writeServerError(w, r, "failed to query alerts", err)
		return
	}

	items := make([]alertResponse, 0, len(result.Alerts))
	for _, alert := range result.Alerts {
		items = append(items, newAlertResponse(alert))
	}
	nextCursor := ""
	if result.NextCursor != nil {
		nextCursor, err = encodeCursor(*result.NextCursor)
		if err != nil {
			writeServerError(w, r, "failed to encode cursor", err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"alerts":         items,
		"nextCursor":     nextCursor,
		"severityCounts": result.SeverityCounts,
		"streamCursor":   result.StreamCursor,
		"total":          result.Total,
	})
}

// listAlertGroups answers the grouped shape of GET /api/v1/alerts. Expanding a
// group needs no endpoint of its own: the console re-queries this same handler
// with an equality matcher on the group's key, which keeps cursoring, sorting
// and read restrictions identical between a group's members and the flat list.
func (api *API) listAlertGroups(w http.ResponseWriter, r *http.Request, principal auth.Principal, query alerts.Query) {
	result, err := api.store.GroupAlerts(r.Context(), principal, query)
	if err != nil {
		writeServerError(w, r, "failed to group alerts", err)
		return
	}
	groups := make([]alertGroupResponse, 0, len(result.Groups))
	for _, group := range result.Groups {
		groups = append(groups, alertGroupResponse{
			Key:              group.Key,
			Total:            group.Total,
			Acknowledged:     group.Acknowledged,
			Silenced:         group.Silenced,
			SeverityCounts:   group.SeverityCounts,
			WorstSeverity:    group.WorstSeverity,
			LatestLastSeen:   group.LatestLastSeen,
			EarliestStartsAt: group.EarliestStartsAt,
			SampleAlertID:    strconv.FormatInt(group.SampleAlertID, 10),
		})
	}
	nextCursor := ""
	if result.NextCursor != nil {
		nextCursor, err = encodeGroupCursor(*result.NextCursor)
		if err != nil {
			writeServerError(w, r, "failed to encode cursor", err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"groups":         groups,
		"nextCursor":     nextCursor,
		"severityCounts": result.SeverityCounts,
		"streamCursor":   result.StreamCursor,
		"total":          result.TotalAlerts,
		"totalGroups":    result.TotalGroups,
	})
}

// getPreferences returns the caller's console layout. In open mode there is no
// user to key against, so it answers 404 and the console falls back to storing
// layout in the browser. That is a statement about identity, not permission,
// which is why it is not a 401.
func (api *API) getPreferences(w http.ResponseWriter, r *http.Request) {
	principal, ok := requestPrincipal(r)
	if !ok {
		writeServerError(w, r, "principal is unavailable", nil)
		return
	}
	stored, err := api.store.ReadPreferences(r.Context(), principal)
	if errors.Is(err, preferences.ErrNoSubject) {
		writeError(w, http.StatusNotFound, "preferences are unavailable without a signed-in user")
		return
	}
	if err != nil {
		writeServerError(w, r, "failed to read preferences", err)
		return
	}
	writeJSON(w, http.StatusOK, stored)
}

func (api *API) putPreferences(w http.ResponseWriter, r *http.Request) {
	principal, ok := requestPrincipal(r)
	if !ok {
		writeServerError(w, r, "principal is unavailable", nil)
		return
	}
	var value preferences.Preferences
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		writeError(w, http.StatusBadRequest, "preferences payload is invalid")
		return
	}
	if err := preferences.Validate(value); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := api.store.WritePreferences(r.Context(), principal, value); err != nil {
		if errors.Is(err, preferences.ErrNoSubject) {
			writeError(w, http.StatusNotFound, "preferences are unavailable without a signed-in user")
			return
		}
		writeServerError(w, r, "failed to save preferences", err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (api *API) streamAlerts(w http.ResponseWriter, r *http.Request) {
	afterID, err := streamCursor(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeServerError(w, r, "streaming is unsupported", nil)
		return
	}

	if api.observers.StreamOpened != nil {
		api.observers.StreamOpened()
		// Deferred rather than counted at each exit: this loop returns from
		// seven places, and the one that gets forgotten is the one that leaks
		// the gauge upward until it looks like a connection nobody closed.
		defer api.observers.StreamClosed()
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	_, _ = fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	lastKeepalive := time.Now()
	lastAuthentication := time.Now()
	principal, ok := requestPrincipal(r)
	if !ok {
		return
	}
	for {
		if err := r.Context().Err(); err != nil {
			return
		}
		if time.Since(lastAuthentication) >= 15*time.Second {
			principal, err = api.authenticator.Authenticate(r.Context(), r)
			if err != nil || !principal.CanRead() {
				return
			}
			lastAuthentication = time.Now()
		}
		batch, err := api.store.StreamEvents(r.Context(), principal, afterID, 100)
		if err != nil {
			return
		}
		if api.observers.StreamPolled != nil {
			api.observers.StreamPolled(len(batch.Events))
		}
		for _, event := range batch.Events {
			payload := map[string]any{
				"id":         event.ID,
				"type":       event.Type,
				"alertId":    strconv.FormatInt(event.AlertID, 10),
				"occurredAt": event.OccurredAt,
			}
			if !event.Redacted {
				payload["severity"] = event.Severity
				payload["alertName"] = event.AlertName
				payload["summary"] = event.Summary
				payload["source"] = event.SourceSlug
				payload["team"] = event.Team
			}
			data, err := json.Marshal(payload)
			if err != nil {
				return
			}
			_, _ = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.ID, event.Type, data)
		}
		previousAfterID := afterID
		afterID = batch.ScannedThrough
		if len(batch.Events) > 0 {
			flusher.Flush()
			lastKeepalive = time.Now()
		}
		if afterID > previousAfterID {
			continue
		}
		if time.Since(lastKeepalive) >= 15*time.Second {
			_, _ = fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
			lastKeepalive = time.Now()
		}

		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-r.Context().Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func requestPrincipal(r *http.Request) (auth.Principal, bool) {
	principal, ok := r.Context().Value(principalContextKey{}).(auth.Principal)
	return principal, ok
}

func (api *API) getConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"authMode":    api.config.AuthMode,
		"productName": "Promview",
		// The console needs the deployment's window to default and bound its own
		// duration control rather than hardcoding one the server would reject.
		"silenceDefaultSeconds": int64(api.config.SilenceDefaultDuration / time.Second),
		"silenceMaxSeconds":     int64(api.config.SilenceMaxDuration / time.Second),
		"silenceEnabled":        api.silencer != nil,
		// A console ships and updates independently of the server behind it, so
		// it cannot assume an endpoint exists just because it knows the name.
		// Absent means an older server: the console then silences on the
		// grouping key as it always did, rather than sending a field that
		// server's strict decoder would reject outright.
		"silencePreviewSupported": true,
	})
}

func (api *API) ingestAlertmanager(w http.ResponseWriter, r *http.Request) {
	token := requestBearerToken(r)
	authorized, err := api.store.AuthenticateSource(r.Context(), r.PathValue("source"), token)
	if err != nil {
		writeServerError(w, r, "failed to authenticate source", err)
		return
	}
	if !authorized {
		writeError(w, http.StatusUnauthorized, "invalid ingestion credentials")
		return
	}

	source := strings.TrimSpace(r.PathValue("source"))
	if source == "" {
		writeError(w, http.StatusBadRequest, "source is required")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxWebhookBodyBytes)
	payload, err := alertmanager.Decode(json.NewDecoder(r.Body))
	if err != nil {
		status := http.StatusBadRequest
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			status = http.StatusRequestEntityTooLarge
		}
		writeError(w, status, err.Error())
		return
	}

	alerts := alertmanager.Normalize(payload, source, time.Now())
	if err := api.store.Ingest(r.Context(), alerts); err != nil {
		writeServerError(w, r, "failed to persist alerts", err)
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": len(alerts)})
}

func (api *API) live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type alertResponse struct {
	ID           string            `json:"id"`
	Fingerprint  string            `json:"fingerprint"`
	Source       string            `json:"source"`
	Status       string            `json:"status"`
	Severity     string            `json:"severity"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	EndsAt       *time.Time        `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	ExternalURL  string            `json:"externalURL"`
	FirstSeen    time.Time         `json:"firstSeen"`
	LastSeen     time.Time         `json:"lastSeen"`
	RepeatCount  int64             `json:"repeatCount"`
	// Suppressed is on the summary rather than only the detail: an operator
	// scanning the list needs to see that an alert is inside a maintenance
	// window without opening it.
	Suppressed bool `json:"suppressed"`
	// SilencedBy names the silences currently matching. Suppressed with an
	// empty list means inhibited instead, and the console says which: an
	// inhibition lifts itself, a silence was somebody's decision.
	SilencedBy []string `json:"silencedBy"`
}

type alertDetailResponse struct {
	alertResponse
	Occurrence     int             `json:"occurrence"`
	Acknowledged   bool            `json:"acknowledged"`
	AcknowledgedAt *time.Time      `json:"acknowledgedAt"`
	AcknowledgedBy string          `json:"acknowledgedBy"`
	Actions        alertActions    `json:"actions"`
	RawData        json.RawMessage `json:"rawData"`
}

type alertActions struct {
	CanAcknowledge bool `json:"canAcknowledge"`
	// CanSilence carries the same per-alert operator check as CanAcknowledge,
	// and additionally whether this deployment can reach an Alertmanager at
	// all. They are reported separately because they can differ: a deployment
	// with no Alertmanager configured can still acknowledge.
	CanSilence bool `json:"canSilence"`
}

// alertGroupResponse is one collapsed row. SampleAlertID is a string like every
// other id the API emits, so the console never has to care that ids are numeric
// on the server.
type alertGroupResponse struct {
	Key              map[string]string `json:"key"`
	Total            int64             `json:"total"`
	Acknowledged     int64             `json:"acknowledged"`
	Silenced         int64             `json:"silenced"`
	SeverityCounts   map[string]int64  `json:"severityCounts"`
	WorstSeverity    string            `json:"worstSeverity"`
	LatestLastSeen   time.Time         `json:"latestLastSeen"`
	EarliestStartsAt time.Time         `json:"earliestStartsAt"`
	SampleAlertID    string            `json:"sampleAlertId"`
}

func newAlertResponse(alert alerts.Alert) alertResponse {
	severity := alert.Labels["severity"]
	if severity == "" {
		severity = "warning"
	}
	silencedBy := alert.SilencedBy
	if silencedBy == nil {
		silencedBy = []string{}
	}
	return alertResponse{
		ID:           strconv.FormatInt(alert.ID, 10),
		Fingerprint:  alert.Fingerprint,
		Source:       alert.SourceSlug,
		Status:       alert.SourceStatus,
		Severity:     severity,
		Labels:       alert.Labels,
		Annotations:  alert.Annotations,
		StartsAt:     alert.StartsAt,
		EndsAt:       alert.EndsAt,
		GeneratorURL: alert.GeneratorURL,
		ExternalURL:  alert.ExternalURL,
		FirstSeen:    alert.FirstSeen,
		LastSeen:     alert.LastSeen,
		RepeatCount:  alert.RepeatCount,
		Suppressed:   alert.Suppressed,
		SilencedBy:   silencedBy,
	}
}

func newAlertDetailResponse(alert alerts.Alert, canOperate bool, canSilence bool) alertDetailResponse {
	return alertDetailResponse{
		alertResponse:  newAlertResponse(alert),
		Occurrence:     alert.Occurrence,
		Acknowledged:   alert.Acknowledged,
		AcknowledgedAt: alert.AcknowledgedAt,
		AcknowledgedBy: alert.AcknowledgedBy,
		Actions:        alertActions{CanAcknowledge: canOperate, CanSilence: canOperate && canSilence},
		RawData:        alert.RawData,
	}
}

func parseAlertQuery(r *http.Request) (alerts.Query, error) {
	values := r.URL.Query()
	limit := 100
	if raw := values.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 500 {
			return alerts.Query{}, errors.New("limit must be between 1 and 500")
		}
		limit = parsed
	}

	status := values.Get("status")
	switch status {
	case "", alerts.StatusFiring, alerts.StatusResolved, alerts.StatusExpired:
	default:
		return alerts.Query{}, errors.New("status must be firing, resolved, or expired")
	}

	// Absent leaves suppressed alerts in the result. Hiding them by default
	// would make an alert vanish because somebody else silenced it, which is
	// the failure mode silencing is supposed to be an alternative to.
	var suppressed *bool
	switch raw := values.Get("suppressed"); raw {
	case "":
	case "true", "false":
		value := raw == "true"
		suppressed = &value
	default:
		return alerts.Query{}, errors.New("suppressed must be true or false")
	}

	query := alerts.Query{
		Limit:      limit,
		Source:     strings.TrimSpace(values.Get("source")),
		Status:     status,
		Severity:   strings.TrimSpace(values.Get("severity")),
		Team:       strings.TrimSpace(values.Get("team")),
		Suppressed: suppressed,
		Sort:       values.Get("sort"),
		Order:      values.Get("order"),
	}
	if query.Sort != "" && !isAlertSort(query.Sort) {
		return alerts.Query{}, errors.New("sort is invalid")
	}
	if query.Order != "" && query.Order != "asc" && query.Order != "desc" {
		return alerts.Query{}, errors.New("order must be asc or desc")
	}
	if raw := strings.TrimSpace(values.Get("groupBy")); raw != "" {
		for _, key := range strings.Split(raw, ",") {
			query.GroupBy = append(query.GroupBy, strings.TrimSpace(key))
		}
		if err := alerts.ValidateGroupBy(query.GroupBy); err != nil {
			return alerts.Query{}, err
		}
	}
	if len(query.GroupBy) == 0 {
		if query.Sort == "" {
			query.Sort = alerts.DefaultSort
		}
		if query.Order == "" {
			query.Order = alerts.DefaultOrder
		}
	} else if query.Sort != "" && query.Order == "" {
		query.Order = alerts.DefaultOrder
	} else if query.Sort == "" && query.Order != "" {
		return alerts.Query{}, errors.New("order requires sort")
	}
	for _, raw := range values["match"] {
		matcher, err := parseLabelMatcher(raw)
		if err != nil {
			return alerts.Query{}, err
		}
		query.Matches = append(query.Matches, matcher)
	}
	if raw := values.Get("cursor"); raw != "" {
		if len(query.GroupBy) > 0 {
			cursor, err := decodeGroupCursor(raw)
			if err != nil {
				return alerts.Query{}, errors.New("cursor is invalid")
			}
			if cursor.Query != query.CursorIdentity() || len(cursor.Key) != len(query.GroupBy) || !validGroupCursor(cursor, query) {
				return alerts.Query{}, errors.New("cursor does not match query")
			}
			query.GroupCursor = &cursor
			return query, nil
		}
		cursor, err := decodeCursor(raw)
		if err != nil {
			return alerts.Query{}, errors.New("cursor is invalid")
		}
		if cursor.Sort != query.Sort || cursor.Order != query.Order || cursor.Query != query.CursorIdentity() {
			return alerts.Query{}, errors.New("cursor does not match query")
		}
		if !validCursorValue(cursor) {
			return alerts.Query{}, errors.New("cursor is invalid")
		}
		query.Cursor = &cursor
	}
	return query, nil
}

func encodeGroupCursor(cursor alerts.GroupCursor) (string, error) {
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("marshal group cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeGroupCursor(value string) (alerts.GroupCursor, error) {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return alerts.GroupCursor{}, fmt.Errorf("decode group cursor: %w", err)
	}
	var cursor alerts.GroupCursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return alerts.GroupCursor{}, fmt.Errorf("decode group cursor: %w", err)
	}
	return cursor, nil
}

func parseLabelMatcher(raw string) (alerts.LabelMatcher, error) {
	if strings.Contains(raw, "=~") || strings.Contains(raw, "!~") {
		return alerts.LabelMatcher{}, errors.New("match supports only = and !=")
	}
	operatorAt := strings.Index(raw, "!=")
	operator := "!="
	if operatorAt < 0 {
		operatorAt = strings.IndexByte(raw, '=')
		operator = "="
	}
	if operatorAt < 1 {
		return alerts.LabelMatcher{}, errors.New("match must be label=value or label!=value")
	}
	name := raw[:operatorAt]
	if !labelNamePattern.MatchString(name) {
		return alerts.LabelMatcher{}, errors.New("match label is invalid")
	}
	return alerts.LabelMatcher{Name: name, Operator: operator, Value: raw[operatorAt+len(operator):]}, nil
}

func isAlertSort(value string) bool {
	switch value {
	case "lastSeen", "startsAt", "severity", "alertname", "summary", "status", "team", "instance", "source":
		return true
	default:
		return false
	}
}

func validCursorValue(cursor alerts.Cursor) bool {
	switch cursor.Sort {
	case "lastSeen", "startsAt":
		_, err := time.Parse(time.RFC3339Nano, cursor.Value)
		return err == nil
	case "severity":
		value, err := strconv.Atoi(cursor.Value)
		return err == nil && value >= 0 && value <= 3
	default:
		return true
	}
}

func validGroupCursor(cursor alerts.GroupCursor, query alerts.Query) bool {
	sort := query.Sort
	order := query.Order
	if sort == "" {
		sort = "severity"
		order = "desc"
	}
	if cursor.Sort != sort || cursor.Order != order {
		return false
	}
	if query.Sort == "" {
		return !cursor.LatestLastSeen.IsZero()
	}
	switch sort {
	case "lastSeen", "startsAt":
		_, err := time.Parse(time.RFC3339Nano, cursor.Value)
		return err == nil
	case "severity":
		value, err := strconv.Atoi(cursor.Value)
		return err == nil && value >= 0 && value <= 3
	default:
		return true
	}
}

func encodeCursor(cursor alerts.Cursor) (string, error) {
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("marshal cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeCursor(value string) (alerts.Cursor, error) {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return alerts.Cursor{}, fmt.Errorf("decode cursor: %w", err)
	}
	var cursor alerts.Cursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return alerts.Cursor{}, fmt.Errorf("unmarshal cursor: %w", err)
	}
	if cursor.ID < 1 || cursor.LastSeen.IsZero() || cursor.Sort == "" || cursor.Order == "" || cursor.Query == "" {
		return alerts.Cursor{}, errors.New("cursor is incomplete")
	}
	return cursor, nil
}

func streamCursor(r *http.Request) (int64, error) {
	value := r.URL.Query().Get("cursor")
	if value == "" {
		value = r.Header.Get("Last-Event-ID")
	}
	if value == "" {
		return 0, nil
	}
	cursor, err := strconv.ParseInt(value, 10, 64)
	if err != nil || cursor < 0 {
		return 0, errors.New("stream cursor is invalid")
	}
	return cursor, nil
}

func (api *API) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := api.store.Ping(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func requestBearerToken(r *http.Request) string {
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, prefix))
}

func validMutationOrigin(r *http.Request) bool {
	// Bearer credentials are not sent automatically by browsers, so this keeps
	// non-browser and future bearer clients independent of browser CSRF rules.
	if requestBearerToken(r) != "" {
		return true
	}
	if _, err := r.Cookie(auth.SessionCookieName); err != nil {
		return true
	}
	origin, err := url.Parse(r.Header.Get("Origin"))
	return err == nil && origin.Scheme != "" && origin.Host == r.Host
}

// writeServerError answers with a generic message and records the cause.
//
// The client is deliberately told nothing useful: a 500 body is not the place
// to describe a database. But an operator with nothing at all in the log cannot
// tell a schema mismatch from a dead connection pool, which is exactly the hole
// a silent 500 leaves. Both halves matter, and only one of them belongs in the
// response.
func writeServerError(w http.ResponseWriter, r *http.Request, message string, err error) {
	attributes := []any{"method", r.Method, "path", r.URL.Path}
	if err != nil {
		attributes = append(attributes, "error", err)
	}
	slog.Error(message, attributes...)
	writeError(w, http.StatusInternalServerError, message)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func spaHandler(directory string) http.Handler {
	files := http.FileServer(http.Dir(directory))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := filepath.Join(directory, filepath.Clean(r.URL.Path))
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			files.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(directory, "index.html"))
	})
}
