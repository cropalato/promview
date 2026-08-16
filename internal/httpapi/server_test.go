package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cropalato/promview/internal/alertmanager"
	"github.com/cropalato/promview/internal/alerts"
	"github.com/cropalato/promview/internal/auth"
	"github.com/cropalato/promview/internal/config"
)

type fakeStore struct {
	alerts      []alertmanager.IncomingAlert
	query       alerts.Query
	result      alerts.ListResult
	events      []alerts.StreamEvent
	detail      alerts.Detail
	detailErr   error
	afterID     int64
	cancel      context.CancelFunc
	pingErr     error
	sourceToken string
}

type fakeAuthenticator struct {
	principal auth.Principal
	err       error
}

func (authenticator fakeAuthenticator) Authenticate(context.Context, *http.Request) (auth.Principal, error) {
	return authenticator.principal, authenticator.err
}

func (store *fakeStore) Ingest(_ context.Context, alerts []alertmanager.IncomingAlert) error {
	store.alerts = append(store.alerts, alerts...)
	return nil
}

func (store *fakeStore) AuthenticateSource(_ context.Context, _ string, token string) (bool, error) {
	expected := store.sourceToken
	if expected == "" {
		expected = "secret"
	}
	return token == expected, nil
}

func (store *fakeStore) Ping(context.Context) error {
	return store.pingErr
}

func (store *fakeStore) ListAlerts(_ context.Context, query alerts.Query) (alerts.ListResult, error) {
	store.query = query
	return store.result, nil
}

func (store *fakeStore) StreamEvents(_ context.Context, afterID int64, _ int) ([]alerts.StreamEvent, error) {
	store.afterID = afterID
	if store.cancel != nil {
		store.cancel()
	}
	return store.events, nil
}

func (store *fakeStore) GetAlertDetail(context.Context, int64) (alerts.Detail, error) {
	return store.detail, store.detailErr
}

func TestIngestAlertmanager(t *testing.T) {
	store := &fakeStore{}
	handler := New(config.Config{AuthMode: "open"}, store, auth.OpenAuthenticator{})
	body := `{"version":"4","alerts":[{"status":"firing","labels":{"alertname":"Down"},"annotations":{},"startsAt":"2026-08-14T12:00:00Z"}]}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/ingest/alertmanager/primary", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusAccepted, response.Body.String())
	}
	if len(store.alerts) != 1 {
		t.Fatalf("persisted alerts = %d, want 1", len(store.alerts))
	}
	if got := store.alerts[0].SourceSlug; got != "primary" {
		t.Fatalf("source = %q, want primary", got)
	}
}

func TestGetMeInOpenMode(t *testing.T) {
	handler := New(config.Config{AuthMode: "open"}, &fakeStore{}, auth.OpenAuthenticator{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/me", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	var principal auth.Principal
	if err := json.NewDecoder(response.Body).Decode(&principal); err != nil {
		t.Fatal(err)
	}
	if !principal.Anonymous || !principal.HasRole("viewer") {
		t.Fatalf("principal = %#v, want anonymous viewer", principal)
	}
}

func TestProtectedAPIRequiresAuthentication(t *testing.T) {
	handler := New(
		config.Config{AuthMode: "oidc"},
		&fakeStore{},
		fakeAuthenticator{err: auth.ErrUnauthenticated},
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/alerts", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestProtectedAPIRequiresReadRole(t *testing.T) {
	handler := New(
		config.Config{AuthMode: "oidc"},
		&fakeStore{},
		fakeAuthenticator{principal: auth.Principal{Subject: "user", Roles: []string{"unmapped"}}},
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/alerts", nil))
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
	}
}

func TestAuthenticationRoutesAreMounted(t *testing.T) {
	called := false
	authenticationHandler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		called = true
		response.WriteHeader(http.StatusNoContent)
	})
	handler := New(config.Config{AuthMode: "oidc"}, &fakeStore{}, fakeAuthenticator{}, authenticationHandler)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/login", nil))
	if response.Code != http.StatusNoContent || !called {
		t.Fatalf("status = %d, called = %v", response.Code, called)
	}
}

func TestListAlerts(t *testing.T) {
	startsAt := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{result: alerts.ListResult{
		Alerts: []alerts.Alert{{
			ID: 42, SourceSlug: "primary", Fingerprint: "abc", SourceStatus: "firing",
			Labels:      map[string]string{"alertname": "Down", "severity": "critical", "team": "platform"},
			Annotations: map[string]string{"summary": "API is down"}, StartsAt: startsAt,
			FirstSeen: startsAt, LastSeen: startsAt,
		}},
		SeverityCounts: map[string]int64{"critical": 1},
		Total:          1,
	}}
	handler := New(config.Config{AuthMode: "open"}, store, auth.OpenAuthenticator{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/alerts?limit=25&status=firing&team=platform", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	if store.query.Limit != 25 || store.query.Status != "firing" || store.query.Team != "platform" {
		t.Fatalf("query = %#v, want parsed filters", store.query)
	}
	var body struct {
		Alerts []alertResponse `json:"alerts"`
		Total  int64           `json:"total"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Total != 1 || len(body.Alerts) != 1 || body.Alerts[0].ID != "42" {
		t.Fatalf("response = %#v, want one alert", body)
	}
}

func TestListAlertsRejectsInvalidQuery(t *testing.T) {
	handler := New(config.Config{AuthMode: "open"}, &fakeStore{}, auth.OpenAuthenticator{})
	for _, target := range []string{
		"/api/v1/alerts?limit=0",
		"/api/v1/alerts?status=closed",
		"/api/v1/alerts?cursor=not-base64",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusBadRequest {
			t.Errorf("%s status = %d, want %d", target, response.Code, http.StatusBadRequest)
		}
	}
}

func TestGetAlertDetail(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{detail: alerts.Detail{
		Alert: alerts.Alert{
			ID: 42, SourceSlug: "primary", Fingerprint: "abc", SourceStatus: "firing",
			Labels:      map[string]string{"alertname": "Down", "severity": "critical"},
			Annotations: map[string]string{"summary": "API is down"}, StartsAt: now,
			FirstSeen: now, LastSeen: now, Occurrence: 2, RawData: json.RawMessage(`{"status":"firing"}`),
		},
		History: []alerts.HistoryEvent{{
			ID: 9, Occurrence: 2, Type: "alert.reopened", SourceStatus: "firing", OccurredAt: now,
		}},
	}}
	handler := New(config.Config{AuthMode: "open"}, store, auth.OpenAuthenticator{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/alerts/42", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	var body struct {
		Alert struct {
			ID         string          `json:"id"`
			Occurrence int             `json:"occurrence"`
			RawData    json.RawMessage `json:"rawData"`
		} `json:"alert"`
		History []alerts.HistoryEvent `json:"history"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Alert.ID != "42" || body.Alert.Occurrence != 2 || len(body.Alert.RawData) == 0 || len(body.History) != 1 {
		t.Fatalf("detail response = %#v", body)
	}
}

func TestGetAlertNotFound(t *testing.T) {
	handler := New(config.Config{AuthMode: "open"}, &fakeStore{detailErr: alerts.ErrNotFound}, auth.OpenAuthenticator{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/alerts/42", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestGetAlertRejectsInvalidID(t *testing.T) {
	handler := New(config.Config{AuthMode: "open"}, &fakeStore{}, auth.OpenAuthenticator{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/alerts/nope", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestCursorRoundTrip(t *testing.T) {
	want := alerts.Cursor{ID: 42, LastSeen: time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)}
	encoded, err := encodeCursor(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeCursor(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("cursor = %#v, want %#v", got, want)
	}
}

func TestStreamAlertsResumesFromCursor(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store := &fakeStore{
		events: []alerts.StreamEvent{{
			ID: 8, Type: "alert.updated", AlertID: 42, Severity: "critical",
			AlertName: "APIUnavailable", Summary: "API is unavailable", SourceSlug: "primary", Team: "platform",
			OccurredAt: time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC),
		}},
		cancel: cancel,
	}
	handler := New(config.Config{AuthMode: "open"}, store, auth.OpenAuthenticator{})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/stream?cursor=7", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if store.afterID != 7 {
		t.Fatalf("after ID = %d, want 7", store.afterID)
	}
	body := response.Body.String()
	for _, want := range []string{
		"id: 8", "event: alert.updated", `"alertId":"42"`, `"type":"alert.updated"`,
		`"severity":"critical"`, `"alertName":"APIUnavailable"`, `"source":"primary"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("stream body = %q, want %q", body, want)
		}
	}
}

func TestStreamCursorUsesLastEventID(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/stream", nil)
	request.Header.Set("Last-Event-ID", "19")
	got, err := streamCursor(request)
	if err != nil || got != 19 {
		t.Fatalf("streamCursor() = %d, %v, want 19, nil", got, err)
	}
}

func TestStreamCursorRejectsInvalidValue(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/stream?cursor=-1", nil)
	if _, err := streamCursor(request); err == nil {
		t.Fatal("streamCursor() error = nil, want error")
	}
}

func TestIngestAlertmanagerRejectsBadToken(t *testing.T) {
	handler := New(config.Config{AuthMode: "open"}, &fakeStore{}, auth.OpenAuthenticator{})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/ingest/alertmanager/primary", strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer wrong")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestReadinessFailure(t *testing.T) {
	handler := New(config.Config{AuthMode: "open"}, &fakeStore{pingErr: errors.New("down")}, auth.OpenAuthenticator{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

func TestServesSPAIndexForClientRoute(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "index.html"), []byte("promview shell"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := New(config.Config{AuthMode: "open", WebDirectory: directory}, &fakeStore{}, auth.OpenAuthenticator{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/alerts/example", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.Contains(response.Body.String(), "promview shell") {
		t.Fatalf("body = %q, want SPA index", response.Body.String())
	}
}
