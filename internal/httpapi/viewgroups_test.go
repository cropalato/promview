package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cropalato/promview/internal/auth"
	"github.com/cropalato/promview/internal/config"
)

func serve(handler http.Handler, method, target, body string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, operatorRequest(method, target, body))
	return response
}

func TestViewGroupSelfServiceUsesTheCallersOwnUser(t *testing.T) {
	store := &fakeStore{}
	handler := New(config.Config{AuthMode: "oidc"}, store, signedInViewer(), nil)

	if response := serve(handler, http.MethodPut, "/api/v1/view-groups/platform/membership", ""); response.Code != http.StatusNoContent {
		t.Fatalf("join status = %d; body = %s", response.Code, response.Body.String())
	}
	if response := serve(handler, http.MethodDelete, "/api/v1/view-groups/platform/membership", ""); response.Code != http.StatusNoContent {
		t.Fatalf("leave status = %d", response.Code)
	}
	want := []string{"join platform 7", "leave platform 7"}
	if strings.Join(store.viewGroupCalls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want %v", store.viewGroupCalls, want)
	}
}

func TestViewGroupLeavingAPinnedGroupIs409(t *testing.T) {
	store := &fakeStore{viewGroupErr: auth.ErrViewGroupPinned}
	handler := New(config.Config{AuthMode: "oidc"}, store, signedInViewer(), nil)
	if response := serve(handler, http.MethodDelete, "/api/v1/view-groups/platform/membership", ""); response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", response.Code)
	}
}

func TestViewGroupErrorsAreTheCallersToFix(t *testing.T) {
	for _, test := range []struct {
		err    error
		status int
	}{
		{auth.ErrInvalidViewGroup, http.StatusBadRequest},
		{auth.ErrViewGroupNotFound, http.StatusNotFound},
	} {
		handler := adminHandler(&fakeStore{viewGroupErr: test.err})
		response := serve(handler, http.MethodPut, "/api/v1/view-groups/x",
			`{"matchers":[{"name":"team","operator":"=","value":"x"}]}`)
		if response.Code != test.status {
			t.Errorf("%v -> %d, want %d", test.err, response.Code, test.status)
		}
	}
}

func TestViewGroupDefinitionAndPinsAreAdministratorOnly(t *testing.T) {
	targets := []struct{ method, path string }{
		{http.MethodPut, "/api/v1/view-groups/x"},
		{http.MethodDelete, "/api/v1/view-groups/x"},
		{http.MethodPost, "/api/v1/view-groups/x/pins"},
		{http.MethodDelete, "/api/v1/view-groups/x/pins"},
	}
	for _, principal := range []auth.Principal{
		{UserID: 7, Subject: "viewer", Roles: []string{"viewer"}},
		{UserID: 8, Subject: "operator", Roles: []string{"operator"}},
	} {
		for _, target := range targets {
			store := &fakeStore{}
			handler := New(config.Config{AuthMode: "oidc"}, store, fakeAuthenticator{principal: principal}, nil)
			response := serve(handler, target.method, target.path, `{}`)
			if response.Code != http.StatusForbidden {
				t.Errorf("%s %s as %v -> %d, want 403", target.method, target.path, principal.Roles, response.Code)
			}
			if len(store.viewGroupCalls) != 0 {
				t.Errorf("%s %s reached the store: %v", target.method, target.path, store.viewGroupCalls)
			}
		}
	}
}

func TestViewGroupsAreUnavailableWithoutASignedInUser(t *testing.T) {
	store := &fakeStore{}
	handler := New(config.Config{AuthMode: "open"}, store, fakeAuthenticator{principal: auth.Principal{
		Anonymous: true, Roles: []string{"viewer"},
	}}, nil)
	for _, target := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/view-groups"},
		{http.MethodPut, "/api/v1/view-groups/x/membership"},
	} {
		if response := serve(handler, target.method, target.path, ""); response.Code != http.StatusNotFound {
			t.Errorf("%s %s -> %d, want 404", target.method, target.path, response.Code)
		}
	}
	if len(store.viewGroupCalls) != 0 {
		t.Fatalf("anonymous reached the store: %v", store.viewGroupCalls)
	}
}

func TestSetViewGroupAndPin(t *testing.T) {
	store := &fakeStore{}
	handler := adminHandler(store)
	response := serve(handler, http.MethodPut, "/api/v1/view-groups/platform",
		`{"description":"platform team","matchers":[{"name":"team","operator":"=","value":"platform"}]}`)
	if response.Code != http.StatusOK || store.viewGroup.Name != "platform" || len(store.viewGroup.Matchers) != 1 {
		t.Fatalf("status = %d; group = %#v", response.Code, store.viewGroup)
	}
	if response := serve(handler, http.MethodPut, "/api/v1/view-groups/platform",
		`{"name":"other","matchers":[]}`); response.Code != http.StatusBadRequest {
		t.Fatalf("mismatched name -> %d, want 400", response.Code)
	}
	if response := serve(handler, http.MethodPut, "/api/v1/view-groups/platform", `{"bogus":1}`); response.Code != http.StatusBadRequest {
		t.Fatalf("unknown field -> %d, want 400", response.Code)
	}
	response = serve(handler, http.MethodPost, "/api/v1/view-groups/platform/pins",
		`{"subjectKind":"oidc_group","subjectIssuer":"https://idp.example","subjectGroup":"platform"}`)
	if response.Code != http.StatusNoContent || store.viewGroupPin.SubjectGroup != "platform" {
		t.Fatalf("pin status = %d; pin = %#v", response.Code, store.viewGroupPin)
	}
}
