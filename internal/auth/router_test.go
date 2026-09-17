package auth

import (
	"crypto/subtle"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRouterLogoutRevokesSession(t *testing.T) {
	repository := &fakeSessionRepository{}
	router := NewRouter(RouterConfig{Sessions: NewSessionManager(repository, time.Hour), CookieSecure: true})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "session-token"})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || subtle.ConstantTimeCompare(repository.deletedHash, HashSessionToken("session-token")) != 1 {
		t.Fatalf("status = %d, deleted hash = %x", response.Code, repository.deletedHash)
	}
	cookie := responseCookie(t, response, SessionCookieName)
	if cookie.MaxAge >= 0 {
		t.Fatalf("cleared cookie = %#v", cookie)
	}
}

// Logout is the route that must not depend on the mode. A router with no OIDC
// handler behind it is what local and LDAP will be, and this is the test that
// keeps logout from drifting back into the mode that used to own it.
func TestRouterLogoutWorksWithoutAnOIDCHandler(t *testing.T) {
	repository := &fakeSessionRepository{}
	router := NewRouter(RouterConfig{Sessions: NewSessionManager(repository, time.Hour)})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "session-token"})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", response.Code)
	}
}

func TestRouterRejectsLogoutOverGET(t *testing.T) {
	router := NewRouter(RouterConfig{Sessions: NewSessionManager(&fakeSessionRepository{}, time.Hour)})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/auth/logout", nil))
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != http.MethodPost {
		t.Fatalf("status = %d, allow = %q", response.Code, response.Header().Get("Allow"))
	}
}

// A mode that does not implement a route answers as a server that predates it
// would, rather than reporting an error the console has no way to act on.
func TestRouterNotFoundForRoutesTheModeDoesNotImplement(t *testing.T) {
	router := NewRouter(RouterConfig{Sessions: NewSessionManager(&fakeSessionRepository{}, time.Hour)})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/login", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
}

func TestRouterDelegatesToTheOIDCHandler(t *testing.T) {
	provider := &fakeOIDCProvider{}
	oidc := NewOIDCHandler(
		&fakeOIDCTransactionRepository{}, &fakeOIDCIdentityRepository{},
		NewSessionManager(&fakeSessionRepository{}, time.Hour), provider, false, time.Hour, nil,
	)
	router := NewRouter(RouterConfig{Sessions: NewSessionManager(&fakeSessionRepository{}, time.Hour), OIDC: oidc})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/login", nil))
	if response.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", response.Code)
	}
}
