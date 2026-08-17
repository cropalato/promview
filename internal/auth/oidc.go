package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	coreoidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const (
	oidcStateCookieName = "promview_oidc_state"
	oidcTransactionTTL  = 5 * time.Minute
)

var ErrInvalidOIDCTransaction = errors.New("invalid OIDC login transaction")

type OIDCProviderConfig struct {
	IssuerURL        string
	ClientID         string
	ClientSecret     string
	RedirectURL      string
	Scopes           []string
	UsernameClaim    string
	EmailClaim       string
	DisplayNameClaim string
	GroupsClaim      string
}

type OIDCIdentity struct {
	Issuer      string
	Subject     string
	Username    string
	Email       string
	DisplayName string
	Groups      []string
	Nonce       string
}

type OIDCProvider interface {
	AuthorizationURL(state, nonce, codeChallenge string) string
	Exchange(context.Context, string, string) (OIDCIdentity, error)
}

type OIDCTransaction struct {
	StateHash    []byte
	Nonce        string
	CodeVerifier string
	ExpiresAt    time.Time
}

type OIDCTransactionRepository interface {
	StoreOIDCTransaction(context.Context, OIDCTransaction) error
	ConsumeOIDCTransaction(context.Context, []byte, time.Time) (OIDCTransaction, error)
}

type OIDCIdentityRepository interface {
	ResolveOIDCIdentity(context.Context, OIDCIdentity) (Principal, error)
}

type OIDCHandler struct {
	repository   OIDCTransactionRepository
	identities   OIDCIdentityRepository
	sessions     *SessionManager
	provider     OIDCProvider
	cookieSecure bool
	sessionTTL   time.Duration
}

func NewOIDCHandler(
	repository OIDCTransactionRepository,
	identities OIDCIdentityRepository,
	sessions *SessionManager,
	provider OIDCProvider,
	cookieSecure bool,
	sessionTTL time.Duration,
) *OIDCHandler {
	return &OIDCHandler{
		repository: repository, identities: identities, sessions: sessions, provider: provider,
		cookieSecure: cookieSecure, sessionTTL: sessionTTL,
	}
}

func (handler *OIDCHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	switch request.URL.Path {
	case "/api/v1/auth/oidc/login":
		if request.Method != http.MethodGet {
			response.Header().Set("Allow", http.MethodGet)
			http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handler.login(response, request)
	case "/api/v1/auth/oidc/callback":
		if request.Method != http.MethodGet {
			response.Header().Set("Allow", http.MethodGet)
			http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handler.callback(response, request)
	case "/api/v1/auth/logout":
		if request.Method != http.MethodPost {
			response.Header().Set("Allow", http.MethodPost)
			http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handler.logout(response, request)
	default:
		http.NotFound(response, request)
	}
}

func (handler *OIDCHandler) login(response http.ResponseWriter, request *http.Request) {
	state, err := randomToken()
	if err != nil {
		http.Error(response, "could not start sign-in", http.StatusInternalServerError)
		return
	}
	nonce, err := randomToken()
	if err != nil {
		http.Error(response, "could not start sign-in", http.StatusInternalServerError)
		return
	}
	verifier, err := randomToken()
	if err != nil {
		http.Error(response, "could not start sign-in", http.StatusInternalServerError)
		return
	}
	transaction := OIDCTransaction{
		StateHash: HashSessionToken(state), Nonce: nonce, CodeVerifier: verifier,
		ExpiresAt: time.Now().UTC().Add(oidcTransactionTTL),
	}
	if err := handler.repository.StoreOIDCTransaction(request.Context(), transaction); err != nil {
		http.Error(response, "could not start sign-in", http.StatusInternalServerError)
		return
	}
	http.SetCookie(response, &http.Cookie{
		Name: oidcStateCookieName, Value: state, Path: "/api/v1/auth/oidc/callback",
		HttpOnly: true, Secure: handler.cookieSecure, SameSite: http.SameSiteLaxMode,
		MaxAge: int(oidcTransactionTTL.Seconds()), Expires: transaction.ExpiresAt,
	})
	challengeHash := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeHash[:])
	http.Redirect(response, request, handler.provider.AuthorizationURL(state, nonce, challenge), http.StatusFound)
}

func (handler *OIDCHandler) callback(response http.ResponseWriter, request *http.Request) {
	state := request.URL.Query().Get("state")
	stateCookie, err := request.Cookie(oidcStateCookieName)
	if err != nil || state == "" || subtle.ConstantTimeCompare([]byte(state), []byte(stateCookie.Value)) != 1 {
		http.Error(response, "invalid sign-in response", http.StatusBadRequest)
		return
	}
	transaction, err := handler.repository.ConsumeOIDCTransaction(
		request.Context(), HashSessionToken(state), time.Now().UTC(),
	)
	handler.clearCookie(response, oidcStateCookieName, "/api/v1/auth/oidc/callback")
	if err != nil {
		http.Error(response, "invalid sign-in response", http.StatusBadRequest)
		return
	}
	if request.URL.Query().Get("error") != "" || request.URL.Query().Get("code") == "" {
		http.Error(response, "identity provider rejected sign-in", http.StatusBadRequest)
		return
	}
	identity, err := handler.provider.Exchange(request.Context(), request.URL.Query().Get("code"), transaction.CodeVerifier)
	if err != nil {
		http.Error(response, "identity provider validation failed", http.StatusBadGateway)
		return
	}
	if subtle.ConstantTimeCompare([]byte(transaction.Nonce), []byte(identity.Nonce)) != 1 {
		http.Error(response, "identity provider validation failed", http.StatusBadGateway)
		return
	}
	principal, err := handler.identities.ResolveOIDCIdentity(request.Context(), identity)
	if errors.Is(err, ErrAccessDenied) {
		http.Error(response, "read access denied", http.StatusForbidden)
		return
	}
	if err != nil {
		http.Error(response, "could not resolve identity", http.StatusInternalServerError)
		return
	}
	token, err := handler.sessions.NewSession(request.Context(), principal)
	if err != nil {
		http.Error(response, "could not create session", http.StatusInternalServerError)
		return
	}
	expiresAt := time.Now().UTC().Add(handler.sessionTTL)
	http.SetCookie(response, &http.Cookie{
		Name: SessionCookieName, Value: token, Path: "/", HttpOnly: true,
		Secure: handler.cookieSecure, SameSite: http.SameSiteLaxMode,
		MaxAge: int(handler.sessionTTL.Seconds()), Expires: expiresAt,
	})
	http.Redirect(response, request, "/", http.StatusSeeOther)
}

func (handler *OIDCHandler) logout(response http.ResponseWriter, request *http.Request) {
	if err := handler.sessions.Revoke(request.Context(), request); err != nil {
		http.Error(response, "could not end session", http.StatusInternalServerError)
		return
	}
	handler.clearCookie(response, SessionCookieName, "/")
	response.WriteHeader(http.StatusNoContent)
}

func (handler *OIDCHandler) clearCookie(response http.ResponseWriter, name, path string) {
	http.SetCookie(response, &http.Cookie{
		Name: name, Value: "", Path: path, HttpOnly: true, Secure: handler.cookieSecure,
		SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0),
	})
}

func randomToken() (string, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(random), nil
}

type discoveredOIDCProvider struct {
	oauthConfig      oauth2.Config
	verifier         *coreoidc.IDTokenVerifier
	httpClient       *http.Client
	usernameClaim    string
	emailClaim       string
	displayNameClaim string
	groupsClaim      string
}

func NewDiscoveredOIDCProvider(ctx context.Context, config OIDCProviderConfig) (OIDCProvider, error) {
	httpClient := &http.Client{Timeout: 10 * time.Second}
	provider, err := coreoidc.NewProvider(coreoidc.ClientContext(ctx, httpClient), config.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("discover OIDC provider: %w", err)
	}
	return &discoveredOIDCProvider{
		oauthConfig: oauth2.Config{
			ClientID: config.ClientID, ClientSecret: config.ClientSecret,
			Endpoint: provider.Endpoint(), RedirectURL: config.RedirectURL, Scopes: config.Scopes,
		},
		verifier:      provider.Verifier(&coreoidc.Config{ClientID: config.ClientID}),
		httpClient:    httpClient,
		usernameClaim: config.UsernameClaim, emailClaim: config.EmailClaim,
		displayNameClaim: config.DisplayNameClaim, groupsClaim: config.GroupsClaim,
	}, nil
}

func (provider *discoveredOIDCProvider) AuthorizationURL(state, nonce, codeChallenge string) string {
	return provider.oauthConfig.AuthCodeURL(
		state,
		coreoidc.Nonce(nonce),
		oauth2.SetAuthURLParam("code_challenge", codeChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
}

func (provider *discoveredOIDCProvider) Exchange(ctx context.Context, code, codeVerifier string) (OIDCIdentity, error) {
	ctx = coreoidc.ClientContext(ctx, provider.httpClient)
	ctx = context.WithValue(ctx, oauth2.HTTPClient, provider.httpClient)
	token, err := provider.oauthConfig.Exchange(ctx, code, oauth2.VerifierOption(codeVerifier))
	if err != nil {
		return OIDCIdentity{}, fmt.Errorf("exchange authorization code: %w", err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return OIDCIdentity{}, errors.New("token response has no ID token")
	}
	idToken, err := provider.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return OIDCIdentity{}, fmt.Errorf("verify ID token: %w", err)
	}
	var claims map[string]json.RawMessage
	if err := idToken.Claims(&claims); err != nil {
		return OIDCIdentity{}, fmt.Errorf("decode ID token claims: %w", err)
	}
	nonce := stringClaim(claims, "nonce")
	if nonce == "" {
		return OIDCIdentity{}, errors.New("ID token has no nonce")
	}
	return OIDCIdentity{
		Issuer: idToken.Issuer, Subject: idToken.Subject,
		Username: stringClaim(claims, provider.usernameClaim), Email: stringClaim(claims, provider.emailClaim),
		DisplayName: stringClaim(claims, provider.displayNameClaim), Groups: stringsClaim(claims, provider.groupsClaim),
		Nonce: nonce,
	}, nil
}

func stringClaim(claims map[string]json.RawMessage, name string) string {
	var value string
	_ = json.Unmarshal(claims[name], &value)
	return value
}

func stringsClaim(claims map[string]json.RawMessage, name string) []string {
	var values []string
	if json.Unmarshal(claims[name], &values) == nil {
		return values
	}
	if value := stringClaim(claims, name); value != "" {
		return []string{value}
	}
	return nil
}
