package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/cropalato/promview/internal/alerts"
	"github.com/cropalato/promview/internal/auth"
)

/*
Bulk endpoints mirror the single-alert ones, taking the same body plus the ids
to apply it to. They report per alert rather than succeeding or failing as a
whole: one alert outside the operator's scope must not cost them the other
thirty-nine, and it comes back as notFound - the same answer the single-alert
endpoint gives - so the reply cannot be read to discover what exists outside a
scope.
*/

// bulkRequest is the shared half of every bulk body.
type bulkRequest struct {
	IDs []string `json:"ids"`
}

// bulkIDs parses the selection. Ids are strings on the wire like every other
// alert id in this API, because a JSON number loses precision in a browser
// before a bigint does in the database.
func bulkIDs(raw []string) ([]int64, error) {
	if len(raw) == 0 {
		return nil, errors.New("ids must be a non-empty array")
	}
	ids := make([]int64, 0, len(raw))
	for _, value := range raw {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id < 1 {
			return nil, errors.New("ids must be alert ids")
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// bulkGate is the checking every bulk endpoint does before reading its body.
func (api *API) bulkGate(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	principal, ok := requestPrincipal(r)
	if !ok {
		writeServerError(w, r, "principal is unavailable", nil)
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
	return principal, true
}

// decodeBulk reads a bulk body into value and returns the parsed ids.
func decodeBulk(w http.ResponseWriter, r *http.Request, value any, raw *[]string) ([]int64, bool) {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeError(w, http.StatusBadRequest, "request body is invalid")
		return nil, false
	}
	ids, err := bulkIDs(*raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	return ids, true
}

// writeBulkOutcomes answers with one entry per requested alert. 207 when the
// request was not uniformly successful, so a caller can tell without walking
// the list, matching how a partly applied group silence answers.
func writeBulkOutcomes(w http.ResponseWriter, outcomes []alerts.BulkOutcome) {
	applied, unchanged, missing := 0, 0, 0
	for _, outcome := range outcomes {
		switch outcome.Status {
		case alerts.BulkApplied:
			applied++
		case alerts.BulkUnchanged:
			unchanged++
		default:
			missing++
		}
	}
	status := http.StatusOK
	if missing > 0 {
		status = http.StatusMultiStatus
	}
	writeJSON(w, status, map[string]any{
		"applied":   applied,
		"unchanged": unchanged,
		"notFound":  missing,
		"results":   outcomes,
	})
}

func (api *API) writeBulkResult(w http.ResponseWriter, r *http.Request, what string, outcomes []alerts.BulkOutcome, err error) {
	if errors.Is(err, alerts.ErrNotFound) {
		writeError(w, http.StatusForbidden, "operator access required")
		return
	}
	if errors.Is(err, alerts.ErrInvalid) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		writeServerError(w, r, what, err)
		return
	}
	writeBulkOutcomes(w, outcomes)
}

func (api *API) bulkAcknowledge(w http.ResponseWriter, r *http.Request) {
	principal, ok := api.bulkGate(w, r)
	if !ok {
		return
	}
	var body struct {
		bulkRequest
		Acknowledged *bool `json:"acknowledged"`
	}
	ids, ok := decodeBulk(w, r, &body, &body.IDs)
	if !ok {
		return
	}
	if body.Acknowledged == nil {
		writeError(w, http.StatusBadRequest, "acknowledged must be a boolean")
		return
	}
	outcomes, err := api.store.BulkAcknowledge(r.Context(), principal, ids, *body.Acknowledged)
	api.writeBulkResult(w, r, "bulk acknowledge", outcomes, err)
}

func (api *API) bulkAssign(w http.ResponseWriter, r *http.Request) {
	principal, ok := api.bulkGate(w, r)
	if !ok {
		return
	}
	var body struct {
		bulkRequest
		Assignee *string `json:"assignee"`
	}
	ids, ok := decodeBulk(w, r, &body, &body.IDs)
	if !ok {
		return
	}
	if body.Assignee == nil {
		writeError(w, http.StatusBadRequest, "assignee must be a string")
		return
	}
	outcomes, err := api.store.BulkAssign(r.Context(), principal, ids, *body.Assignee)
	api.writeBulkResult(w, r, "bulk assign", outcomes, err)
}

func (api *API) bulkClose(w http.ResponseWriter, r *http.Request) {
	principal, ok := api.bulkGate(w, r)
	if !ok {
		return
	}
	var body struct {
		bulkRequest
		Closed *bool `json:"closed"`
	}
	ids, ok := decodeBulk(w, r, &body, &body.IDs)
	if !ok {
		return
	}
	if body.Closed == nil {
		writeError(w, http.StatusBadRequest, "closed must be a boolean")
		return
	}
	outcomes, err := api.store.BulkClose(r.Context(), principal, ids, *body.Closed)
	api.writeBulkResult(w, r, "bulk close", outcomes, err)
}

func (api *API) bulkNote(w http.ResponseWriter, r *http.Request) {
	principal, ok := api.bulkGate(w, r)
	if !ok {
		return
	}
	var body struct {
		bulkRequest
		Body *string `json:"body"`
	}
	ids, ok := decodeBulk(w, r, &body, &body.IDs)
	if !ok {
		return
	}
	if body.Body == nil {
		writeError(w, http.StatusBadRequest, "body must be a string")
		return
	}
	outcomes, err := api.store.BulkNote(r.Context(), principal, ids, *body.Body)
	api.writeBulkResult(w, r, "bulk note", outcomes, err)
}
