package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/cropalato/promview/internal/auth"
)

/*
Administering role bindings over HTTP, which until now was a shell command on
the server. A deployment whose only way to grant somebody access is SSH is one
where access is granted rarely and by the wrong person.

Administrator only, and never in open mode: a policy change has to be
attributable, and an anonymous principal has no name to attribute it to.
*/

// accessGate is the check every binding endpoint makes first.
func (api *API) accessGate(w http.ResponseWriter, r *http.Request, mutating bool) (auth.Principal, bool) {
	principal, ok := requestPrincipal(r)
	if !ok {
		writeServerError(w, r, "principal is unavailable", nil)
		return auth.Principal{}, false
	}
	if !principal.CanAdminister() {
		// 403 rather than 404: unlike an alert, the existence of the
		// administration API is not something worth hiding, and an operator
		// told "not found" here would reasonably think it was a bug.
		writeError(w, http.StatusForbidden, "administrator access required")
		return auth.Principal{}, false
	}
	if mutating && !validMutationOrigin(r) {
		writeError(w, http.StatusForbidden, "invalid request origin")
		return auth.Principal{}, false
	}
	return principal, true
}

func (api *API) listRoleBindings(w http.ResponseWriter, r *http.Request) {
	if _, ok := api.accessGate(w, r, false); !ok {
		return
	}
	bindings, err := api.store.RoleBindings(r.Context())
	if err != nil {
		writeServerError(w, r, "list role bindings", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"bindings": bindings})
}

func (api *API) setRoleBinding(w http.ResponseWriter, r *http.Request) {
	if _, ok := api.accessGate(w, r, true); !ok {
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "binding name is required")
		return
	}
	var binding auth.RoleBinding
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&binding); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeError(w, http.StatusBadRequest, "request body is invalid")
		return
	}
	// The path names the binding. A body that disagrees is a request the caller
	// cannot have meant, and picking one silently would make PUT to one name
	// able to rewrite another.
	if binding.Name != "" && binding.Name != name {
		writeError(w, http.StatusBadRequest, "binding name in the body must match the path")
		return
	}
	binding.Name = name
	if err := api.store.SetRoleBinding(r.Context(), binding); err != nil {
		api.writeAccessError(w, r, "set role binding", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"binding": binding})
}

func (api *API) deleteRoleBinding(w http.ResponseWriter, r *http.Request) {
	if _, ok := api.accessGate(w, r, true); !ok {
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "binding name is required")
		return
	}
	if err := api.store.DeleteRoleBinding(r.Context(), name); err != nil {
		api.writeAccessError(w, r, "delete role binding", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeAccessError separates what the caller can fix from what they cannot.
//
// Validation failures and the last-administrator guard are both the caller's to
// resolve, and both carry a message worth reading; anything else is ours.
func (api *API) writeAccessError(w http.ResponseWriter, r *http.Request, what string, err error) {
	if errors.Is(err, auth.ErrInvalidRoleBinding) || errors.Is(err, auth.ErrLastAdministrator) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeServerError(w, r, what, err)
}
