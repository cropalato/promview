package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeSessionRepository struct {
	session     Session
	principal   Principal
	stored      bool
	deletedHash []byte
}

func (repository *fakeSessionRepository) StoreSession(_ context.Context, session Session) error {
	repository.session = session
	repository.stored = true
	return nil
}

func (repository *fakeSessionRepository) FindSession(_ context.Context, hash []byte, now time.Time) (Session, error) {
	if !repository.stored || string(hash) != string(repository.session.TokenHash) || !repository.session.ExpiresAt.After(now) {
		return Session{}, ErrUnauthenticated
	}
	session := repository.session
	session.Principal = repository.principal
	return session, nil
}

func (repository *fakeSessionRepository) DeleteSession(_ context.Context, hash []byte) error {
	repository.deletedHash = append([]byte(nil), hash...)
	return nil
}

func TestOpenAuthenticator(t *testing.T) {
	principal, err := (OpenAuthenticator{}).Authenticate(context.Background(), httptest.NewRequest("GET", "/", nil))
	if err != nil || !principal.Anonymous || !principal.HasRole("viewer") {
		t.Fatalf("principal = %#v, error = %v", principal, err)
	}
}

func TestSessionManagerAuthenticatesBearerAndCookie(t *testing.T) {
	repository := &fakeSessionRepository{}
	manager := NewSessionManager(repository, time.Hour)
	want := Principal{UserID: 1, Subject: "user-1", Email: "user@example.com", DisplayName: "User One", Roles: []string{"operator"}, Grants: []Grant{{Role: RoleOperator}}}
	repository.principal = want
	token, err := manager.NewSession(context.Background(), want)
	if err != nil {
		t.Fatal(err)
	}
	if token == "" || string(repository.session.TokenHash) == token {
		t.Fatal("session token was empty or stored in plaintext")
	}

	requests := []*http.Request{
		httptest.NewRequest("GET", "/", nil),
		httptest.NewRequest("GET", "/", nil),
	}
	requests[0].Header.Set("Authorization", "Bearer "+token)
	requests[1].AddCookie(&http.Cookie{Name: SessionCookieName, Value: token})
	for _, request := range requests {
		got, err := manager.Authenticate(context.Background(), request)
		if err != nil || got.Subject != want.Subject || !got.HasRole("operator") {
			t.Fatalf("principal = %#v, error = %v", got, err)
		}
	}
}

func TestSessionManagerRejectsMissingToken(t *testing.T) {
	manager := NewSessionManager(&fakeSessionRepository{}, time.Hour)
	_, err := manager.Authenticate(context.Background(), httptest.NewRequest("GET", "/", nil))
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("error = %v, want ErrUnauthenticated", err)
	}
}
