package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"
)

const SessionCookieName = "promview_session"

var ErrUnauthenticated = errors.New("authentication required")

type Principal struct {
	UserID      int64    `json:"id,omitempty"`
	Subject     string   `json:"subject"`
	Email       string   `json:"email"`
	DisplayName string   `json:"displayName"`
	Roles       []string `json:"roles"`
	Anonymous   bool     `json:"anonymous"`
	Grants      []Grant  `json:"grants,omitempty"`
}

func (principal Principal) HasRole(role string) bool {
	for _, assigned := range principal.Roles {
		if assigned == role {
			return true
		}
	}
	return false
}

type Authenticator interface {
	Authenticate(context.Context, *http.Request) (Principal, error)
}

// OpenAuthenticator hands every unauthenticated reader the same principal.
//
// Role is viewer unless a deployment has deliberately said otherwise. An
// elevated open mode is a lab arrangement: everybody who can reach the port can
// acknowledge, assign and silence, all under one shared name, and nothing
// recorded can be attributed to a person.
type OpenAuthenticator struct {
	Role Role
	// Author is what actions are recorded under. Empty falls back to a name
	// that reads as a mode rather than a person, because that is honestly all
	// the deployment knows.
	Author string
}

func (authenticator OpenAuthenticator) Authenticate(context.Context, *http.Request) (Principal, error) {
	role := authenticator.Role
	if role == "" {
		role = RoleViewer
	}
	author := authenticator.Author
	if author == "" {
		author = DefaultOpenModeAuthor
	}
	// Always a viewer grant, plus the elevated one where there is one. Keeping
	// them separate means the label-scoped read path sees exactly what it saw
	// before elevation, and only the operator checks see anything new.
	grants := []Grant{{Role: RoleViewer}}
	if role != RoleViewer {
		grants = append(grants, Grant{Role: role})
	}
	displayName := "Anonymous viewer"
	if role != RoleViewer {
		displayName = "Open mode " + string(role)
	}
	return Principal{
		Subject:     author,
		DisplayName: displayName,
		Roles:       RolesFromGrants(grants),
		Anonymous:   true,
		Grants:      grants,
	}, nil
}

// DefaultOpenModeAuthor is what open-mode actions are recorded under when the
// deployment does not name one. Greppable on purpose: it should be obvious in
// an audit trail that nobody signed in for this.
const DefaultOpenModeAuthor = "promview-open-mode"

// writeSessionCookie issues the session cookie every authentication mode ends
// in. One writer, so promview_session means exactly one thing no matter which
// mode minted it - a per-mode cookie is a per-mode set of flags to get wrong.
func writeSessionCookie(response http.ResponseWriter, token string, ttl time.Duration, secure bool) {
	http.SetCookie(response, &http.Cookie{
		Name: SessionCookieName, Value: token, Path: "/", HttpOnly: true,
		Secure: secure, SameSite: http.SameSiteLaxMode,
		MaxAge: int(ttl.Seconds()), Expires: time.Now().UTC().Add(ttl),
	})
}

// clearCookie expires a cookie this package set. The flags have to match the
// ones it was written with or the browser keeps the original alongside it.
func clearCookie(response http.ResponseWriter, name, path string, secure bool) {
	http.SetCookie(response, &http.Cookie{
		Name: name, Value: "", Path: path, HttpOnly: true, Secure: secure,
		SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0),
	})
}

type Session struct {
	TokenHash []byte
	UserID    int64
	ExpiresAt time.Time
	Principal Principal
}

type SessionRepository interface {
	StoreSession(context.Context, Session) error
	FindSession(context.Context, []byte, time.Time) (Session, error)
	DeleteSession(context.Context, []byte) error
}

type SessionManager struct {
	repository SessionRepository
	ttl        time.Duration
}

func NewSessionManager(repository SessionRepository, ttl time.Duration) *SessionManager {
	return &SessionManager{repository: repository, ttl: ttl}
}

func (manager *SessionManager) NewSession(ctx context.Context, principal Principal) (string, error) {
	if principal.UserID < 1 {
		return "", errors.New("persistent user is required for a session")
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(random)
	session := Session{
		TokenHash: HashSessionToken(token),
		UserID:    principal.UserID,
		ExpiresAt: time.Now().UTC().Add(manager.ttl),
	}
	if err := manager.repository.StoreSession(ctx, session); err != nil {
		return "", err
	}
	return token, nil
}

func (manager *SessionManager) Authenticate(ctx context.Context, request *http.Request) (Principal, error) {
	token := requestSessionToken(request)
	if token == "" {
		return Principal{}, ErrUnauthenticated
	}
	session, err := manager.repository.FindSession(ctx, HashSessionToken(token), time.Now().UTC())
	if err != nil {
		if errors.Is(err, ErrUnauthenticated) {
			return Principal{}, ErrUnauthenticated
		}
		return Principal{}, err
	}
	return session.Principal, nil
}

func (manager *SessionManager) Revoke(ctx context.Context, request *http.Request) error {
	token := requestSessionToken(request)
	if token == "" {
		return nil
	}
	return manager.repository.DeleteSession(ctx, HashSessionToken(token))
}

func requestSessionToken(request *http.Request) string {
	token := bearerToken(request)
	if token == "" {
		if cookie, err := request.Cookie(SessionCookieName); err == nil {
			token = cookie.Value
		}
	}
	return token
}

func HashSessionToken(token string) []byte {
	digest := sha256.Sum256([]byte(token))
	return digest[:]
}

func bearerToken(request *http.Request) string {
	const prefix = "Bearer "
	header := request.Header.Get("Authorization")
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, prefix))
}
