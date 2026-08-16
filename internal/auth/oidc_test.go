package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

type fakeOIDCTransactionRepository struct {
	transaction OIDCTransaction
	stored      bool
}

func (repository *fakeOIDCTransactionRepository) StoreOIDCTransaction(_ context.Context, transaction OIDCTransaction) error {
	repository.transaction = transaction
	repository.stored = true
	return nil
}

func (repository *fakeOIDCTransactionRepository) ConsumeOIDCTransaction(_ context.Context, hash []byte, now time.Time) (OIDCTransaction, error) {
	if !repository.stored || subtle.ConstantTimeCompare(hash, repository.transaction.StateHash) != 1 || !repository.transaction.ExpiresAt.After(now) {
		return OIDCTransaction{}, ErrInvalidOIDCTransaction
	}
	repository.stored = false
	return repository.transaction, nil
}

type fakeOIDCProvider struct {
	state        string
	nonce        string
	challenge    string
	code         string
	codeVerifier string
	identity     OIDCIdentity
	err          error
}

func (provider *fakeOIDCProvider) AuthorizationURL(state, nonce, challenge string) string {
	provider.state = state
	provider.nonce = nonce
	provider.challenge = challenge
	query := url.Values{"state": {state}, "nonce": {nonce}, "code_challenge": {challenge}}
	return "https://identity.example.com/authorize?" + query.Encode()
}

func (provider *fakeOIDCProvider) Exchange(_ context.Context, code, verifier string) (OIDCIdentity, error) {
	provider.code = code
	provider.codeVerifier = verifier
	return provider.identity, provider.err
}

func TestOIDCLoginAndCallback(t *testing.T) {
	transactions := &fakeOIDCTransactionRepository{}
	sessions := &fakeSessionRepository{}
	provider := &fakeOIDCProvider{}
	handler := NewOIDCHandler(
		transactions, NewSessionManager(sessions, time.Hour), provider,
		OIDCRoleMapping{ViewerGroups: []string{"viewers"}, AdminGroups: []string{"admins"}}, true, time.Hour,
	)

	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/login", nil))
	if loginResponse.Code != http.StatusFound || provider.state == "" || provider.nonce == "" || len(provider.challenge) != 43 {
		t.Fatalf("login status = %d, state = %q, nonce = %q, challenge = %q", loginResponse.Code, provider.state, provider.nonce, provider.challenge)
	}
	stateCookie := responseCookie(t, loginResponse, oidcStateCookieName)
	if !stateCookie.HttpOnly || !stateCookie.Secure || stateCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("state cookie = %#v", stateCookie)
	}

	provider.identity = OIDCIdentity{
		Issuer: "https://identity.example.com", Subject: "user-1", Email: "user@example.com",
		DisplayName: "User One", Groups: []string{"viewers", "admins"}, Nonce: provider.nonce,
	}
	callback := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/callback?state="+url.QueryEscape(provider.state)+"&code=authorization-code", nil)
	callback.AddCookie(stateCookie)
	callbackResponse := httptest.NewRecorder()
	handler.ServeHTTP(callbackResponse, callback)
	if callbackResponse.Code != http.StatusSeeOther || callbackResponse.Header().Get("Location") != "/" {
		t.Fatalf("callback status = %d, location = %q; body = %s", callbackResponse.Code, callbackResponse.Header().Get("Location"), callbackResponse.Body.String())
	}
	if provider.code != "authorization-code" || provider.codeVerifier != transactions.transaction.CodeVerifier {
		t.Fatalf("exchange code = %q, verifier = %q", provider.code, provider.codeVerifier)
	}
	if !sessions.stored || sessions.session.Subject != "https://identity.example.com|user-1" || sessions.session.Roles[0] != "administrator" {
		t.Fatalf("session = %#v", sessions.session)
	}
	sessionCookie := responseCookie(t, callbackResponse, SessionCookieName)
	if !sessionCookie.HttpOnly || !sessionCookie.Secure || sessionCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie = %#v", sessionCookie)
	}

	replayResponse := httptest.NewRecorder()
	handler.ServeHTTP(replayResponse, callback)
	if replayResponse.Code != http.StatusBadRequest {
		t.Fatalf("replay status = %d, want %d", replayResponse.Code, http.StatusBadRequest)
	}
}

func TestOIDCCallbackRejectsNonceAndUnmappedGroups(t *testing.T) {
	for _, test := range []struct {
		name     string
		identity func(string) OIDCIdentity
		want     int
	}{
		{name: "nonce", identity: func(string) OIDCIdentity {
			return OIDCIdentity{Issuer: "https://identity.example.com", Subject: "user", Groups: []string{"viewers"}, Nonce: "wrong"}
		}, want: http.StatusBadGateway},
		{name: "groups", identity: func(nonce string) OIDCIdentity {
			return OIDCIdentity{Issuer: "https://identity.example.com", Subject: "user", Groups: []string{"other"}, Nonce: nonce}
		}, want: http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			transactions := &fakeOIDCTransactionRepository{}
			sessions := &fakeSessionRepository{}
			provider := &fakeOIDCProvider{}
			handler := NewOIDCHandler(transactions, NewSessionManager(sessions, time.Hour), provider, OIDCRoleMapping{ViewerGroups: []string{"viewers"}}, false, time.Hour)
			loginResponse := httptest.NewRecorder()
			handler.ServeHTTP(loginResponse, httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/login", nil))
			provider.identity = test.identity(provider.nonce)
			request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/callback?state="+url.QueryEscape(provider.state)+"&code=code", nil)
			request.AddCookie(responseCookie(t, loginResponse, oidcStateCookieName))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want || sessions.stored {
				t.Fatalf("status = %d, stored = %v, want %d and false", response.Code, sessions.stored, test.want)
			}
		})
	}
}

func TestOIDCLogoutRevokesSession(t *testing.T) {
	repository := &fakeSessionRepository{}
	handler := NewOIDCHandler(&fakeOIDCTransactionRepository{}, NewSessionManager(repository, time.Hour), &fakeOIDCProvider{}, OIDCRoleMapping{}, true, time.Hour)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "session-token"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || subtle.ConstantTimeCompare(repository.deletedHash, HashSessionToken("session-token")) != 1 {
		t.Fatalf("status = %d, deleted hash = %x", response.Code, repository.deletedHash)
	}
	cookie := responseCookie(t, response, SessionCookieName)
	if cookie.MaxAge >= 0 {
		t.Fatalf("cleared cookie = %#v", cookie)
	}
}

func TestOIDCCallbackRejectsProviderFailure(t *testing.T) {
	transactions := &fakeOIDCTransactionRepository{}
	provider := &fakeOIDCProvider{err: errors.New("provider failed")}
	handler := NewOIDCHandler(transactions, NewSessionManager(&fakeSessionRepository{}, time.Hour), provider, OIDCRoleMapping{}, false, time.Hour)
	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/login", nil))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/callback?state="+url.QueryEscape(provider.state)+"&code=secret-code", nil)
	request.AddCookie(responseCookie(t, loginResponse, oidcStateCookieName))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadGateway || response.Body.String() == "provider failed" {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
}

func TestDiscoveredOIDCProviderValidatesAndMapsIDToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	publicKey := jose.JSONWebKey{Key: &key.PublicKey, KeyID: "test-key", Algorithm: string(jose.RS256), Use: "sig"}
	var issuer string
	var expectedNonce string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/.well-known/openid-configuration":
			writeTestJSON(response, map[string]any{
				"issuer": issuer, "authorization_endpoint": issuer + "/authorize",
				"token_endpoint": issuer + "/token", "jwks_uri": issuer + "/keys",
				"id_token_signing_alg_values_supported": []string{"RS256"},
			})
		case "/keys":
			writeTestJSON(response, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{publicKey}})
		case "/token":
			if err := request.ParseForm(); err != nil {
				t.Error(err)
			}
			if request.Form.Get("code") != "authorization-code" || request.Form.Get("code_verifier") != "verifier" {
				t.Errorf("token form = %v", request.Form)
			}
			signer, err := jose.NewSigner(
				jose.SigningKey{Algorithm: jose.RS256, Key: key},
				(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", publicKey.KeyID),
			)
			if err != nil {
				t.Error(err)
				return
			}
			rawToken, err := jwt.Signed(signer).Claims(map[string]any{
				"iss": issuer, "sub": "user-1", "aud": "promview", "exp": time.Now().Add(time.Minute).Unix(),
				"iat": time.Now().Add(-time.Second).Unix(), "nonce": expectedNonce,
				"preferred_username": "user", "email": "user@example.com", "name": "User One",
				"custom_groups": []string{"promview-viewers"},
			}).Serialize()
			if err != nil {
				t.Error(err)
				return
			}
			writeTestJSON(response, map[string]any{
				"access_token": "access-token", "token_type": "Bearer", "expires_in": 60, "id_token": rawToken,
			})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	issuer = server.URL

	provider, err := NewDiscoveredOIDCProvider(context.Background(), OIDCProviderConfig{
		IssuerURL: issuer, ClientID: "promview", ClientSecret: "secret", RedirectURL: "http://localhost/callback",
		Scopes: []string{"openid", "profile", "email"}, UsernameClaim: "preferred_username",
		EmailClaim: "email", DisplayNameClaim: "name", GroupsClaim: "custom_groups",
	})
	if err != nil {
		t.Fatal(err)
	}
	authorizationURL := provider.AuthorizationURL("state", "nonce", "challenge")
	parsed, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	expectedNonce = parsed.Query().Get("nonce")
	if parsed.Query().Get("state") != "state" || parsed.Query().Get("code_challenge") != "challenge" || parsed.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("authorization query = %v", parsed.Query())
	}
	identity, err := provider.Exchange(context.Background(), "authorization-code", "verifier")
	if err != nil {
		t.Fatal(err)
	}
	if identity.Issuer != issuer || identity.Subject != "user-1" || identity.Nonce != "nonce" || identity.Username != "user" || len(identity.Groups) != 1 {
		t.Fatalf("identity = %#v", identity)
	}
}

func writeTestJSON(response http.ResponseWriter, value any) {
	response.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(response).Encode(value)
}

func responseCookie(t *testing.T, response *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("response has no %s cookie", name)
	return nil
}
