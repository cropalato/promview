package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeDesktopCodes is deliberately race-safe and delete-on-read, because
// single use under concurrency is the property being tested.
type fakeDesktopCodes struct {
	mu     sync.Mutex
	codes  map[string]DesktopCode
	stores int
}

func newFakeDesktopCodes() *fakeDesktopCodes {
	return &fakeDesktopCodes{codes: map[string]DesktopCode{}}
}

func (repository *fakeDesktopCodes) StoreDesktopCode(_ context.Context, code DesktopCode) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.codes[string(code.CodeHash)] = code
	repository.stores++
	return nil
}

func (repository *fakeDesktopCodes) ConsumeDesktopCode(_ context.Context, hash []byte, now time.Time) (int64, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	stored, ok := repository.codes[string(hash)]
	if !ok || !stored.ExpiresAt.After(now) {
		return 0, ErrDesktopCodeInvalid
	}
	delete(repository.codes, string(hash))
	return stored.UserID, nil
}

func desktopHandler(codes DesktopCodeRepository) (*OIDCHandler, *fakeOIDCTransactionRepository) {
	transactions := &fakeOIDCTransactionRepository{}
	return NewOIDCHandler(
		transactions,
		&fakeOIDCIdentityRepository{},
		NewSessionManager(&fakeSessionRepository{}, time.Hour),
		&fakeOIDCProvider{},
		false,
		time.Hour,
		codes,
	), transactions
}

func TestLoginRecordsAValidatedDesktopRedirect(t *testing.T) {
	handler, transactions := desktopHandler(newFakeDesktopCodes())
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/auth/oidc/login?desktop_redirect=http%3A%2F%2F127.0.0.1%3A53123%2Fcallback",
		nil,
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusFound)
	}
	if transactions.transaction.DesktopRedirect != "http://127.0.0.1:53123/callback" {
		t.Errorf("stored redirect = %q", transactions.transaction.DesktopRedirect)
	}
}

func TestLoginRefusesARedirectThatIsNotLoopback(t *testing.T) {
	// The server sends a freshly minted credential to whatever this stores, so
	// a rejected redirect must not reach the transaction at all.
	handler, transactions := desktopHandler(newFakeDesktopCodes())
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/auth/oidc/login?desktop_redirect=http%3A%2F%2Fevil.example%2Fsteal",
		nil,
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	if transactions.stored {
		t.Error("a refused redirect still started a sign-in")
	}
}

func TestLoginWithoutADesktopRedirectStillSetsACookie(t *testing.T) {
	// The browser flow must be untouched by any of this.
	handler, transactions := desktopHandler(newFakeDesktopCodes())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/login", nil))

	if response.Code != http.StatusFound {
		t.Fatalf("status = %d", response.Code)
	}
	if transactions.transaction.DesktopRedirect != "" {
		t.Errorf("browser sign-in recorded a desktop redirect: %q", transactions.transaction.DesktopRedirect)
	}
	if len(response.Result().Cookies()) == 0 {
		t.Error("browser sign-in set no state cookie")
	}
}

func TestExchangeRedeemsACodeExactlyOnce(t *testing.T) {
	codes := newFakeDesktopCodes()
	handler, _ := desktopHandler(codes)
	code := "one-time-code"
	if err := codes.StoreDesktopCode(context.Background(), DesktopCode{
		CodeHash: HashSessionToken(code), UserID: 42,
		ExpiresAt: time.Now().UTC().Add(DesktopCodeTTL),
	}); err != nil {
		t.Fatal(err)
	}

	exchange := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(
			http.MethodPost,
			"/api/v1/auth/desktop/exchange",
			strings.NewReader(`{"code":"`+code+`"}`),
		)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	first := exchange()
	if first.Code != http.StatusOK {
		t.Fatalf("first exchange status = %d (%s)", first.Code, first.Body)
	}
	var payload struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expiresAt"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Token == "" || payload.ExpiresAt == "" {
		t.Fatalf("exchange returned %#v, want a token and an expiry", payload)
	}

	// The credential is single use: a code recovered from browser history or a
	// proxy log after the desktop redeemed it must buy nothing.
	second := exchange()
	if second.Code != http.StatusUnauthorized {
		t.Errorf("second exchange status = %d, want %d", second.Code, http.StatusUnauthorized)
	}
}

func TestExchangeRefusesAnUnknownOrExpiredCode(t *testing.T) {
	codes := newFakeDesktopCodes()
	handler, _ := desktopHandler(codes)
	if err := codes.StoreDesktopCode(context.Background(), DesktopCode{
		CodeHash: HashSessionToken("stale"), UserID: 42,
		ExpiresAt: time.Now().UTC().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	for _, body := range []string{`{"code":"never-issued"}`, `{"code":"stale"}`} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/desktop/exchange", strings.NewReader(body))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		// Unknown and expired answer identically, so a caller cannot probe for
		// codes that have existed.
		if response.Code != http.StatusUnauthorized {
			t.Errorf("status for %s = %d, want %d", body, response.Code, http.StatusUnauthorized)
		}
	}
}

func TestExchangeRejectsAMalformedRequest(t *testing.T) {
	handler, _ := desktopHandler(newFakeDesktopCodes())
	for _, body := range []string{`{}`, `{"code":""}`, `not json`, `{"code":"x","extra":1}`} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/desktop/exchange", strings.NewReader(body))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Errorf("status for %q = %d, want %d", body, response.Code, http.StatusBadRequest)
		}
	}
}

func TestExchangeRequiresPost(t *testing.T) {
	handler, _ := desktopHandler(newFakeDesktopCodes())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/auth/desktop/exchange", nil))
	// Over POST only: a GET would put the credential in a URL, which is the
	// one thing this whole flow exists to avoid.
	if response.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
}

func TestDesktopSignInIsUnavailableWhenNotConfigured(t *testing.T) {
	handler, _ := desktopHandler(nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(
		http.MethodPost, "/api/v1/auth/desktop/exchange", strings.NewReader(`{"code":"x"}`),
	))
	if response.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}
