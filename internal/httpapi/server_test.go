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
	alerts       []alertmanager.IncomingAlert
	query        alerts.Query
	result       alerts.ListResult
	events       []alerts.StreamEvent
	detail       alerts.Detail
	detailErr    error
	acknowledged *bool
	afterID      int64
	cancel       context.CancelFunc
	pingErr      error
	sourceToken  string
	principal    auth.Principal
}

type fakeAuthenticator struct {
	principal auth.Principal
	err       error
}

func (authenticator fakeAuthenticator) Authenticate(context.Context, *http.Request) (auth.Principal, error) {
	principal := authenticator.principal
	if len(principal.Grants) == 0 {
		for _, role := range principal.Roles {
			principal.Grants = append(principal.Grants, auth.Grant{Role: auth.Role(role)})
		}
	}
	return principal, authenticator.err
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

func (store *fakeStore) ListAlerts(_ context.Context, principal auth.Principal, query alerts.Query) (alerts.ListResult, error) {
	store.principal = principal
	store.query = query
	return store.result, nil
}

func (store *fakeStore) StreamEvents(_ context.Context, principal auth.Principal, afterID int64, _ int) (alerts.StreamBatch, error) {
	store.principal = principal
	store.afterID = afterID
	if store.cancel != nil {
		store.cancel()
	}
	return alerts.StreamBatch{Events: store.events, ScannedThrough: afterID + int64(len(store.events))}, nil
}

func (store *fakeStore) GetAlertDetail(_ context.Context, principal auth.Principal, _ int64) (alerts.Detail, error) {
	store.principal = principal
	return store.detail, store.detailErr
}

func (store *fakeStore) AcknowledgeAlert(_ context.Context, principal auth.Principal, _ int64, acknowledged bool) (alerts.Detail, error) {
	store.principal = principal
	store.acknowledged = &acknowledged
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

func TestListAlertsParsesMatchersAndSorting(t *testing.T) {
	store := &fakeStore{}
	handler := New(config.Config{AuthMode: "open"}, store, auth.OpenAuthenticator{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/alerts?match=team%3Dplatform&match=instance%21%3Dapi-2&sort=severity&order=asc", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	if store.query.Sort != "severity" || store.query.Order != "asc" || len(store.query.Matches) != 2 {
		t.Fatalf("query = %#v, want sorting and two matchers", store.query)
	}
	if store.query.Matches[0] != (alerts.LabelMatcher{Name: "team", Operator: "=", Value: "platform"}) ||
		store.query.Matches[1] != (alerts.LabelMatcher{Name: "instance", Operator: "!=", Value: "api-2"}) {
		t.Fatalf("matchers = %#v", store.query.Matches)
	}
}

func TestListAlertsRejectsInvalidQuery(t *testing.T) {
	handler := New(config.Config{AuthMode: "open"}, &fakeStore{}, auth.OpenAuthenticator{})
	for _, target := range []string{
		"/api/v1/alerts?limit=0",
		"/api/v1/alerts?status=closed",
		"/api/v1/alerts?sort=unknown",
		"/api/v1/alerts?order=sideways",
		"/api/v1/alerts?match=team%3D~platform",
		"/api/v1/alerts?match=bad-label%3Dplatform",
		"/api/v1/alerts?cursor=not-base64",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusBadRequest {
			t.Errorf("%s status = %d, want %d", target, response.Code, http.StatusBadRequest)
		}
	}
}

func TestListAlertsRejectsCursorForDifferentQuery(t *testing.T) {
	query := alerts.Query{Sort: alerts.DefaultSort, Order: alerts.DefaultOrder}
	cursor := alerts.Cursor{
		ID: 42, LastSeen: time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC),
		Sort: query.Sort, Order: query.Order, Query: query.CursorIdentity(), Value: "2026-08-14T12:00:00Z",
	}
	encoded, err := encodeCursor(cursor)
	if err != nil {
		t.Fatal(err)
	}
	handler := New(config.Config{AuthMode: "open"}, &fakeStore{}, auth.OpenAuthenticator{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/alerts?status=firing&cursor="+encoded, nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusBadRequest, response.Body.String())
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

func TestAcknowledgeAlert(t *testing.T) {
	store := &fakeStore{detail: alerts.Detail{Alert: alerts.Alert{
		ID: 42, Labels: map[string]string{"team": "platform"}, Acknowledged: true,
	}}}
	handler := New(config.Config{AuthMode: "oidc"}, store, fakeAuthenticator{principal: auth.Principal{
		Subject: "operator-1", Roles: []string{"operator"},
	}})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/alerts/42/acknowledge", strings.NewReader(`{"acknowledged":true}`))
	request.Header.Set("Authorization", "Bearer session-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || store.acknowledged == nil || !*store.acknowledged {
		t.Fatalf("status = %d, acknowledged = %v; body = %s", response.Code, store.acknowledged, response.Body.String())
	}
	var body struct {
		Alert struct {
			Acknowledged bool `json:"acknowledged"`
			Actions      struct {
				CanAcknowledge bool `json:"canAcknowledge"`
			} `json:"actions"`
		} `json:"alert"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Alert.Acknowledged || !body.Alert.Actions.CanAcknowledge {
		t.Fatalf("response alert = %#v", body.Alert)
	}
}

func TestAcknowledgeAlertRejectsViewerAndCookieCSRF(t *testing.T) {
	for _, test := range []struct {
		name       string
		principal  auth.Principal
		cookie     bool
		origin     string
		wantStatus int
	}{
		{name: "viewer", principal: auth.Principal{Subject: "viewer", Roles: []string{"viewer"}}, wantStatus: http.StatusForbidden},
		{name: "cookie without origin", principal: auth.Principal{Subject: "operator", Roles: []string{"operator"}}, cookie: true, wantStatus: http.StatusForbidden},
		{name: "cookie cross origin", principal: auth.Principal{Subject: "operator", Roles: []string{"operator"}}, cookie: true, origin: "https://attacker.example", wantStatus: http.StatusForbidden},
		{name: "cookie same origin", principal: auth.Principal{Subject: "operator", Roles: []string{"operator"}}, cookie: true, origin: "https://example.test", wantStatus: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeStore{detail: alerts.Detail{Alert: alerts.Alert{ID: 42, Labels: map[string]string{}}}}
			handler := New(config.Config{AuthMode: "oidc"}, store, fakeAuthenticator{principal: test.principal})
			request := httptest.NewRequest(http.MethodPost, "https://example.test/api/v1/alerts/42/acknowledge", strings.NewReader(`{"acknowledged":false}`))
			if test.cookie {
				request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "session"})
			}
			request.Header.Set("Origin", test.origin)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, test.wantStatus, response.Body.String())
			}
		})
	}
}

func TestCursorRoundTrip(t *testing.T) {
	want := alerts.Cursor{ID: 42, LastSeen: time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC), Sort: "summary", Order: "asc", Query: "identity", Value: "API is down"}
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

func TestStreamAlertsRedactsScopeExit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store := &fakeStore{
		events: []alerts.StreamEvent{{
			ID: 9, Type: "alert.removed", AlertID: 42, Redacted: true,
			Severity: "critical", AlertName: "SecretAlert", Summary: "secret summary", SourceSlug: "private", Team: "private",
			OccurredAt: time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC),
		}},
		cancel: cancel,
	}
	handler := New(config.Config{AuthMode: "open"}, store, auth.OpenAuthenticator{})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/stream?cursor=8", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	body := response.Body.String()
	if !strings.Contains(body, "event: alert.removed") || !strings.Contains(body, `"alertId":"42"`) {
		t.Fatalf("stream body = %q", body)
	}
	for _, leaked := range []string{"SecretAlert", "secret summary", `"severity"`, `"source"`, `"team"`} {
		if strings.Contains(body, leaked) {
			t.Fatalf("stream body leaked %q: %s", leaked, body)
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
