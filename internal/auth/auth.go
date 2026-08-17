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

type OpenAuthenticator struct{}

func (OpenAuthenticator) Authenticate(context.Context, *http.Request) (Principal, error) {
	return Principal{
		Subject:     "anonymous",
		DisplayName: "Anonymous viewer",
		Roles:       []string{"viewer"},
		Anonymous:   true,
		Grants:      []Grant{{Role: RoleViewer}},
	}, nil
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
