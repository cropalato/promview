package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

type fakeVerifier struct {
	identity DirectoryIdentity
	err      error
	calls    int
	username string
	password string
}

func (fake *fakeVerifier) Verify(_ context.Context, username, password string) (DirectoryIdentity, error) {
	fake.calls++
	fake.username, fake.password = username, password
	return fake.identity, fake.err
}

func testCredentialHandler(config CredentialHandlerConfig) (*CredentialHandler, *fakeSessionRepository) {
	sessions := &fakeSessionRepository{}
	if config.Mode == "" {
		config.Mode = "local"
	}
	if config.Verifier == nil {
		config.Verifier = &fakeVerifier{identity: DirectoryIdentity{Issuer: LocalIssuer, Subject: "42"}}
	}
	if config.Identities == nil {
		config.Identities = &fakeDirectoryIdentityRepository{principal: Principal{
			UserID: 42, Subject: "local|42", Grants: []Grant{{Role: RoleViewer}},
		}}
	}
	config.Sessions = NewSessionManager(sessions, time.Hour)
	if config.SessionTTL == 0 {
		config.SessionTTL = time.Hour
	}
	return NewCredentialHandler(config), sessions
}

func loginRequest(body string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	request.Header.Set("Origin", "http://"+request.Host)
	return request
}

func TestCredentialLoginIssuesASession(t *testing.T) {
	verifier := &fakeVerifier{identity: DirectoryIdentity{Issuer: LocalIssuer, Subject: "42"}}
	handler, sessions := testCredentialHandler(CredentialHandlerConfig{Verifier: verifier, CookieSecure: true})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, loginRequest(`{"username":"Operator","password":"correct horse battery staple"}`))
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body = %s", response.Code, response.Body.String())
	}
	if !sessions.stored || sessions.session.UserID != 42 {
		t.Fatalf("session = %#v", sessions.session)
	}
	// The directory decides how to fold case; the handler must not pre-empt it.
	if verifier.username != "Operator" || verifier.password != "correct horse battery staple" {
		t.Fatalf("verifier saw %q / %q", verifier.username, verifier.password)
	}
	cookie := responseCookie(t, response, SessionCookieName)
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie = %#v", cookie)
	}
}

// Every credential failure answers with the same status and the same bytes. A
// difference of any kind is a list of which usernames exist.
func TestCredentialLoginAnswersEveryFailureIdentically(t *testing.T) {
	var bodies []string
	for _, name := range []string{"unknown username", "wrong password", "disabled", "locked"} {
		handler, sessions := testCredentialHandler(CredentialHandlerConfig{
			Verifier: &fakeVerifier{err: ErrInvalidCredentials},
		})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, loginRequest(`{"username":"operator","password":"whatever"}`))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s: status = %d, want 401", name, response.Code)
		}
		if sessions.stored {
			t.Fatalf("%s: a session was issued", name)
		}
		bodies = append(bodies, response.Body.String())
	}
	for _, body := range bodies {
		if body != bodies[0] {
			t.Fatalf("failure bodies differ: %q vs %q", body, bodies[0])
		}
	}
}

// An account with no read role has already proved it knows the password, so
// naming the problem tells an attacker nothing they could not already confirm -
// and the alternative is an operator staring at "invalid credentials" while
// typing a password they know is right.
func TestCredentialLoginReportsAccessDeniedAsItself(t *testing.T) {
	handler, sessions := testCredentialHandler(CredentialHandlerConfig{
		Identities: &fakeDirectoryIdentityRepository{err: ErrAccessDenied},
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, loginRequest(`{"username":"operator","password":"correct horse battery staple"}`))
	if response.Code != http.StatusForbidden || sessions.stored {
		t.Fatalf("status = %d, stored = %v", response.Code, sessions.stored)
	}
}

// A broken directory is not a wrong password. Reporting it as one sends an
// operator hunting for a typo during an outage.
func TestCredentialLoginSeparatesADirectoryFailureFromABadPassword(t *testing.T) {
	handler, _ := testCredentialHandler(CredentialHandlerConfig{
		Verifier: &fakeVerifier{err: errors.New("directory is unreachable")},
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, loginRequest(`{"username":"operator","password":"whatever"}`))
	if response.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", response.Code)
	}
	if strings.Contains(response.Body.String(), "unreachable") {
		t.Fatalf("the directory's own error reached the wire: %q", response.Body.String())
	}
}

// A cross-site login POST signs a victim into an account the attacker controls,
// so everything the victim does next lands somewhere the attacker can read.
// The server's usual origin check passes any request without a session cookie,
// which is every sign-in, so this endpoint needs its own.
func TestCredentialLoginRefusesCrossOriginAndOriginlessRequests(t *testing.T) {
	for name, origin := range map[string]string{
		"cross-origin": "https://attacker.example",
		"absent":       "",
	} {
		t.Run(name, func(t *testing.T) {
			verifier := &fakeVerifier{}
			handler, _ := testCredentialHandler(CredentialHandlerConfig{Verifier: verifier})
			request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
				strings.NewReader(`{"username":"operator","password":"whatever"}`))
			if origin != "" {
				request.Header.Set("Origin", origin)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", response.Code)
			}
			// Refused before any work is done, so this is not also a way to
			// spend the server's CPU.
			if verifier.calls != 0 {
				t.Fatalf("the verifier ran %d times for a refused origin", verifier.calls)
			}
		})
	}
}

// Browsers do not attach bearer tokens automatically, so a token-carrying
// request cannot be a cross-site forgery.
func TestCredentialLoginAllowsABearerClientWithoutAnOrigin(t *testing.T) {
	handler, sessions := testCredentialHandler(CredentialHandlerConfig{})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		strings.NewReader(`{"username":"operator","password":"correct horse battery staple"}`))
	request.Header.Set("Authorization", "Bearer some-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || !sessions.stored {
		t.Fatalf("status = %d, stored = %v", response.Code, sessions.stored)
	}
}

func TestCredentialLoginThrottles(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)}
	limiter := NewLoginLimiter(LoginLimiterConfig{
		Budgets: map[string]Budget{LoginKeyUser: {Burst: 2, Refill: time.Minute}},
		Now:     clock.Now,
	})
	verifier := &fakeVerifier{err: ErrInvalidCredentials}
	handler, _ := testCredentialHandler(CredentialHandlerConfig{Verifier: verifier, Limiter: limiter})
	for attempt := 1; attempt <= 2; attempt++ {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, loginRequest(`{"username":"operator","password":"whatever"}`))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want 401", attempt, response.Code)
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, loginRequest(`{"username":"operator","password":"whatever"}`))
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", response.Code)
	}
	retryAfter, err := strconv.Atoi(response.Header().Get("Retry-After"))
	if err != nil || retryAfter < 1 {
		t.Fatalf("Retry-After = %q", response.Header().Get("Retry-After"))
	}
	// Throttled before the derivation, or the limiter has already paid for the
	// attack it is refusing.
	if verifier.calls != 2 {
		t.Fatalf("the verifier ran %d times across three attempts", verifier.calls)
	}
}

// A signed-in operator should not be closer to a refusal than somebody who has
// never tried.
func TestCredentialLoginResetsTheLimiterOnSuccess(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)}
	limiter := NewLoginLimiter(LoginLimiterConfig{
		Budgets: map[string]Budget{LoginKeyUser: {Burst: 2, Refill: time.Minute}},
		Now:     clock.Now,
	})
	handler, _ := testCredentialHandler(CredentialHandlerConfig{Limiter: limiter})
	for attempt := 1; attempt <= 5; attempt++ {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, loginRequest(`{"username":"operator","password":"correct horse battery staple"}`))
		if response.Code != http.StatusNoContent {
			t.Fatalf("attempt %d status = %d, want 204", attempt, response.Code)
		}
	}
}

func TestCredentialLoginRejectsMalformedRequests(t *testing.T) {
	for name, test := range map[string]struct {
		method, body string
		want         int
	}{
		"GET":            {method: http.MethodGet, body: "", want: http.StatusMethodNotAllowed},
		"not json":       {method: http.MethodPost, body: "nonsense", want: http.StatusBadRequest},
		"no username":    {method: http.MethodPost, body: `{"password":"x"}`, want: http.StatusBadRequest},
		"unknown field":  {method: http.MethodPost, body: `{"username":"a","role":"administrator"}`, want: http.StatusBadRequest},
		"oversized body": {method: http.MethodPost, body: `{"username":"` + strings.Repeat("a", 8192) + `"}`, want: http.StatusBadRequest},
	} {
		t.Run(name, func(t *testing.T) {
			handler, _ := testCredentialHandler(CredentialHandlerConfig{})
			request := httptest.NewRequest(test.method, "/api/v1/auth/login", strings.NewReader(test.body))
			request.Header.Set("Origin", "http://"+request.Host)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, test.want, response.Body.String())
			}
			if test.want == http.StatusMethodNotAllowed && response.Header().Get("Allow") != http.MethodPost {
				t.Fatalf("Allow = %q", response.Header().Get("Allow"))
			}
		})
	}
}

// The desktop client cannot receive the cookie, so it gets a one-time code -
// the same exchange the OIDC flow already ends in.
func TestCredentialLoginHandsTheDesktopAOneTimeCode(t *testing.T) {
	codes := newFakeDesktopCodes()
	handler, sessions := testCredentialHandler(CredentialHandlerConfig{DesktopCodes: codes})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, loginRequest(
		`{"username":"operator","password":"correct horse battery staple","desktopRedirect":"http://127.0.0.1:8765/callback"}`))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", response.Code, response.Body.String())
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Code == "" {
		t.Fatalf("body = %s", response.Body.String())
	}
	// A cookie here would be a credential the desktop cannot use and the
	// browser would keep.
	if sessions.stored {
		t.Fatal("a session was issued alongside the desktop code")
	}
	if len(response.Result().Cookies()) != 0 {
		t.Fatalf("cookies = %#v", response.Result().Cookies())
	}
}

func TestCredentialLoginRefusesANonLoopbackDesktopRedirect(t *testing.T) {
	codes := newFakeDesktopCodes()
	handler, _ := testCredentialHandler(CredentialHandlerConfig{DesktopCodes: codes})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, loginRequest(
		`{"username":"operator","password":"correct horse battery staple","desktopRedirect":"https://attacker.example/callback"}`))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
}

func TestCredentialLoginReportsEveryOutcome(t *testing.T) {
	var results []string
	observe := func(_, result string, _ time.Duration) { results = append(results, result) }
	for name, test := range map[string]struct {
		config CredentialHandlerConfig
		want   string
	}{
		"success": {want: loginSucceeded},
		"invalid": {config: CredentialHandlerConfig{Verifier: &fakeVerifier{err: ErrInvalidCredentials}}, want: loginInvalid},
		"denied":  {config: CredentialHandlerConfig{Identities: &fakeDirectoryIdentityRepository{err: ErrAccessDenied}}, want: loginDenied},
		"error":   {config: CredentialHandlerConfig{Verifier: &fakeVerifier{err: errors.New("broken")}}, want: loginError},
	} {
		t.Run(name, func(t *testing.T) {
			results = nil
			config := test.config
			config.Observe = observe
			handler, _ := testCredentialHandler(config)
			handler.ServeHTTP(httptest.NewRecorder(),
				loginRequest(`{"username":"operator","password":"correct horse battery staple"}`))
			if len(results) != 1 || results[0] != test.want {
				t.Fatalf("results = %v, want [%s]", results, test.want)
			}
		})
	}
}

// The response for a broken directory deliberately says nothing useful, because
// the directory's own error names the search filter and the filter contains the
// username. That makes the log the only place the cause exists.
func TestCredentialLoginLogsWhyTheDirectoryFailed(t *testing.T) {
	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelError})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	handler, _ := testCredentialHandler(CredentialHandlerConfig{
		Verifier: &fakeVerifier{err: errors.New("directory is unreachable")},
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, loginRequest(`{"username":"operator","password":"whatever"}`))
	if response.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", response.Code)
	}
	if !strings.Contains(logged.String(), "directory is unreachable") {
		t.Fatalf("the cause was not logged: %q", logged.String())
	}
	// And it still must not be in the response.
	if strings.Contains(response.Body.String(), "unreachable") {
		t.Fatalf("the cause reached the wire: %q", response.Body.String())
	}
}
