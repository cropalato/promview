package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/cropalato/promview/internal/auth"
)

/*
View groups over HTTP. Listing and membership belong to any signed-in user;
defining groups and pinning them is administrator-only. None of it grants
access: a view only ever narrows what scope already allows.
*/

// viewGroupCaller is the check the self-service endpoints make first. Open mode
// and other anonymous callers have no user to hold a membership.
func (api *API) viewGroupCaller(w http.ResponseWriter, r *http.Request, mutating bool) (auth.Principal, bool) {
	principal, ok := requestPrincipal(r)
	if !ok {
		writeServerError(w, r, "principal is unavailable", nil)
		return auth.Principal{}, false
	}
	if principal.Anonymous || principal.UserID == 0 {
		writeError(w, http.StatusNotFound, "view groups are unavailable without a signed-in user")
		return auth.Principal{}, false
	}
	if mutating && !validMutationOrigin(r) {
		writeError(w, http.StatusForbidden, "invalid request origin")
		return auth.Principal{}, false
	}
	return principal, true
}

func decodeStrict(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeError(w, http.StatusBadRequest, "request body is invalid")
		return false
	}
	return true
}

func (api *API) writeViewGroupError(w http.ResponseWriter, r *http.Request, what string, err error) {
	switch {
	case errors.Is(err, auth.ErrInvalidViewGroup):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, auth.ErrViewGroupNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, auth.ErrViewGroupPinned):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeServerError(w, r, what, err)
	}
}

func (api *API) listViewGroups(w http.ResponseWriter, r *http.Request) {
	principal, ok := api.viewGroupCaller(w, r, false)
	if !ok {
		return
	}
	groups, err := api.store.ViewGroups(r.Context(), principal)
	if err != nil {
		writeServerError(w, r, "list view groups", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": groups})
}

func (api *API) setViewGroup(w http.ResponseWriter, r *http.Request) {
	if _, ok := api.accessGate(w, r, true); !ok {
		return
	}
	var group auth.ViewGroup
	if !decodeStrict(w, r, &group) {
		return
	}
	name := r.PathValue("name")
	if group.Name != "" && group.Name != name {
		writeError(w, http.StatusBadRequest, "view group name in the body must match the path")
		return
	}
	group.Name = name
	if err := api.store.SetViewGroup(r.Context(), group); err != nil {
		api.writeViewGroupError(w, r, "set view group", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"group": group})
}

func (api *API) deleteViewGroup(w http.ResponseWriter, r *http.Request) {
	if _, ok := api.accessGate(w, r, true); !ok {
		return
	}
	if err := api.store.DeleteViewGroup(r.Context(), r.PathValue("name")); err != nil {
		api.writeViewGroupError(w, r, "delete view group", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (api *API) joinViewGroup(w http.ResponseWriter, r *http.Request) {
	principal, ok := api.viewGroupCaller(w, r, true)
	if !ok {
		return
	}
	if err := api.store.JoinViewGroup(r.Context(), principal.UserID, r.PathValue("name")); err != nil {
		api.writeViewGroupError(w, r, "join view group", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (api *API) leaveViewGroup(w http.ResponseWriter, r *http.Request) {
	principal, ok := api.viewGroupCaller(w, r, true)
	if !ok {
		return
	}
	if err := api.store.LeaveViewGroup(r.Context(), principal.UserID, r.PathValue("name")); err != nil {
		api.writeViewGroupError(w, r, "leave view group", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (api *API) pinViewGroup(w http.ResponseWriter, r *http.Request) {
	if _, ok := api.accessGate(w, r, true); !ok {
		return
	}
	var pin auth.ViewGroupPin
	if !decodeStrict(w, r, &pin) {
		return
	}
	if err := api.store.PinViewGroup(r.Context(), r.PathValue("name"), pin); err != nil {
		api.writeViewGroupError(w, r, "pin view group", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (api *API) unpinViewGroup(w http.ResponseWriter, r *http.Request) {
	if _, ok := api.accessGate(w, r, true); !ok {
		return
	}
	var pin auth.ViewGroupPin
	if !decodeStrict(w, r, &pin) {
		return
	}
	if err := api.store.UnpinViewGroup(r.Context(), r.PathValue("name"), pin); err != nil {
		api.writeViewGroupError(w, r, "unpin view group", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
