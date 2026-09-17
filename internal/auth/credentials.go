package auth

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"time"
)

// DirectoryIdentity is who a directory says somebody is.
//
// OIDC, LDAP and local accounts all produce one; everything downstream of here
// - the user upsert, the group replacement, the grant resolution, the session -
// is the same code for all three. Nothing about a particular protocol belongs
// in it, which is why the OIDC nonce is returned beside it rather than in it.
type DirectoryIdentity struct {
	// Issuer names the directory, and is what a group binding is written
	// against: an OIDC issuer URL, or the canonical URL of an LDAP server.
	Issuer      string
	Subject     string
	Username    string
	Email       string
	DisplayName string
	Groups      []string
}

// DirectoryIdentityRepository turns an identity into a principal, creating or
// updating the user and its group memberships on the way through.
type DirectoryIdentityRepository interface {
	ResolveDirectoryIdentity(context.Context, DirectoryIdentity) (Principal, error)
}

// CredentialVerifier turns a username and password into an identity.
//
// Implementations must take the same time for an unknown username as for a
// known one with a wrong password, and must return ErrInvalidCredentials for
// both. Anything that distinguishes them is an oracle for which accounts exist,
// and it is free to collect.
type CredentialVerifier interface {
	Verify(ctx context.Context, username, password string) (DirectoryIdentity, error)
}

// CredentialHandlerConfig wires POST /api/v1/auth/login.
type CredentialHandlerConfig struct {
	Mode         string
	Verifier     CredentialVerifier
	Identities   DirectoryIdentityRepository
	Sessions     *SessionManager
	Limiter      *LoginLimiter
	CookieSecure bool
	SessionTTL   time.Duration
	// DesktopCodes is nil where desktop sign-in is not wired; the loopback
	// branch then answers 501 rather than pretending.
	DesktopCodes DesktopCodeRepository
	// Observe is called once per attempt. Nil is fine: instrumenting a path
	// should never be the reason it needs a branch.
	Observe func(mode, result string, elapsed time.Duration)
	// MaxConcurrentVerifications bounds password derivations in flight. Zero
	// takes a default from the CPU count.
	MaxConcurrentVerifications int
	Now                        func() time.Time
}

// CredentialHandler serves username-and-password sign-in for the local and LDAP
// modes.
type CredentialHandler struct {
	config CredentialHandlerConfig
	// verifications bounds how many password hashes are computed at once. The
	// work is deliberately expensive, which makes this the cheapest way to
	// spend the server's CPU, and the console it would starve is the one
	// somebody is watching an incident on.
	verifications chan struct{}
}

func NewCredentialHandler(config CredentialHandlerConfig) *CredentialHandler {
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	concurrency := config.MaxConcurrentVerifications
	if concurrency < 1 {
		concurrency = max(2, runtime.NumCPU())
	}
	return &CredentialHandler{config: config, verifications: make(chan struct{}, concurrency)}
}

func (handler *CredentialHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/api/v1/auth/login" {
		http.NotFound(response, request)
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	started := handler.config.Now()
	result := handler.login(response, request)
	if handler.config.Observe != nil {
		handler.config.Observe(handler.config.Mode, result, handler.config.Now().Sub(started))
	}
}

// loginResults are the outcomes reported to the metric. Kept here rather than
// imported from the metrics package so this one stays free of it.
const (
	loginSucceeded = "success"
	loginInvalid   = "invalid"
	loginThrottled = "throttled"
	loginDenied    = "denied"
	loginError     = "error"
)

func (handler *CredentialHandler) login(response http.ResponseWriter, request *http.Request) string {
	// Login needs its own origin check. The server's usual one passes any
	// request that arrives without a session cookie, which is every sign-in -
	// and a cross-site login POST is how an attacker silently signs a victim
	// into an account the attacker controls, so that everything the victim does
	// next lands somewhere they can read it.
	if !sameOriginRequest(request) {
		http.Error(response, "cross-origin sign-in is refused", http.StatusForbidden)
		return loginDenied
	}

	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		// DesktopRedirect is the loopback address a desktop client asked for
		// the result at, instead of a cookie it cannot receive.
		DesktopRedirect string `json:"desktopRedirect"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || body.Username == "" {
		http.Error(response, "username and password are required", http.StatusBadRequest)
		return loginInvalid
	}

	keys := []string{
		LoginKey(LoginKeyUser, NormalizeUsername(body.Username)),
		// The peer address, never X-Forwarded-For. Nothing here knows which
		// proxies to trust, and honouring an attacker-settable header would
		// make this bucket free to bypass.
		LoginKey(LoginKeyAddress, requestHost(request.RemoteAddr)),
	}
	if handler.config.Limiter != nil {
		// Before the derivation, not after: a limiter that runs afterwards has
		// already paid for the attack it is refusing. In LDAP mode it is also
		// what stops this endpoint being used to lock every directory account.
		allowed, retryAfter := handler.config.Limiter.Allow(keys...)
		if !allowed {
			response.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())+1))
			http.Error(response, "too many sign-in attempts", http.StatusTooManyRequests)
			return loginThrottled
		}
	}

	identity, err := handler.verify(request.Context(), body.Username, body.Password)
	switch {
	case errors.Is(err, ErrInvalidCredentials):
		// One message for an unknown username, a wrong password, a disabled
		// account and a locked one. Being helpful here is an oracle.
		http.Error(response, "invalid credentials", http.StatusUnauthorized)
		return loginInvalid
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return loginError
	case err != nil:
		// A broken directory is not a wrong password, and saying so would send
		// an operator hunting for a typo during an outage.
		//
		// Logged because the response deliberately says nothing: the directory's
		// own error text names the search filter, which contains the username,
		// so it must not reach the wire. Without this line the cause exists
		// nowhere at all, and "could not complete sign-in" is the only thing
		// anyone has to debug an outage with.
		slog.Error("sign-in could not reach the directory", "mode", handler.config.Mode, "error", err)
		http.Error(response, "could not complete sign-in", http.StatusBadGateway)
		return loginError
	}

	principal, err := handler.config.Identities.ResolveDirectoryIdentity(request.Context(), identity)
	if errors.Is(err, ErrAccessDenied) {
		// 403 rather than 401, matching OIDC, so the console can show its
		// existing no-read-access panel. This does say the account exists - to
		// somebody who has already proved they know its password, which is who
		// the 401 was protecting it from.
		http.Error(response, "read access denied", http.StatusForbidden)
		return loginDenied
	}
	if err != nil {
		slog.Error("sign-in could not resolve the identity", "mode", handler.config.Mode, "error", err)
		http.Error(response, "could not resolve identity", http.StatusInternalServerError)
		return loginError
	}

	if handler.config.Limiter != nil {
		// A signed-in operator should not be closer to a refusal than somebody
		// who has never tried.
		handler.config.Limiter.Reset(keys...)
	}

	if body.DesktopRedirect != "" {
		return handler.completeDesktopSignIn(response, request, body.DesktopRedirect, principal)
	}

	token, err := handler.config.Sessions.NewSession(request.Context(), principal)
	if err != nil {
		http.Error(response, "could not create session", http.StatusInternalServerError)
		return loginError
	}
	writeSessionCookie(response, token, handler.config.SessionTTL, handler.config.CookieSecure)
	response.WriteHeader(http.StatusNoContent)
	return loginSucceeded
}

// verify runs the directory under the concurrency bound.
//
// The slot is taken with the request's context, so a client that gives up frees
// it rather than leaving the queue to grow into the attack it was meant to
// prevent.
func (handler *CredentialHandler) verify(
	ctx context.Context,
	username, password string,
) (DirectoryIdentity, error) {
	select {
	case handler.verifications <- struct{}{}:
		defer func() { <-handler.verifications }()
	case <-ctx.Done():
		return DirectoryIdentity{}, ctx.Err()
	}
	return handler.config.Verifier.Verify(ctx, username, password)
}

// completeDesktopSignIn hands the desktop a one-time code rather than a cookie
// it cannot receive, reusing the exchange the OIDC flow already ends in.
func (handler *CredentialHandler) completeDesktopSignIn(
	response http.ResponseWriter,
	request *http.Request,
	redirect string,
	principal Principal,
) string {
	if handler.config.DesktopCodes == nil {
		http.Error(response, "desktop sign-in is not configured", http.StatusNotImplemented)
		return loginError
	}
	if _, err := ValidateDesktopRedirect(redirect); err != nil {
		http.Error(response, "invalid desktop redirect", http.StatusBadRequest)
		return loginInvalid
	}
	code, err := randomToken()
	if err != nil {
		http.Error(response, "could not complete sign-in", http.StatusInternalServerError)
		return loginError
	}
	stored := DesktopCode{
		CodeHash:  HashSessionToken(code),
		UserID:    principal.UserID,
		ExpiresAt: handler.config.Now().Add(DesktopCodeTTL),
	}
	if err := handler.config.DesktopCodes.StoreDesktopCode(request.Context(), stored); err != nil {
		http.Error(response, "could not complete sign-in", http.StatusInternalServerError)
		return loginError
	}
	// Answered as JSON rather than redirected: the caller is a desktop client
	// posting a form, not a browser following one.
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(response).Encode(map[string]any{"code": code})
	return loginSucceeded
}

// sameOriginRequest reports whether a state-changing request came from this
// server's own pages.
//
// A request carrying a bearer token is exempt: browsers do not attach those
// automatically, so it cannot be a cross-site forgery. A request with neither
// Origin nor a token is refused rather than allowed - unlike an ordinary
// mutation, a sign-in has no session to fall back on as evidence of intent.
func sameOriginRequest(request *http.Request) bool {
	if bearerToken(request) != "" {
		return true
	}
	origin, err := url.Parse(request.Header.Get("Origin"))
	return err == nil && origin.Scheme != "" && origin.Host == request.Host
}

func requestHost(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}
