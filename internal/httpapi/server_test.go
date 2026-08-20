package httpapi

import (
	"bytes"
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
	"github.com/cropalato/promview/internal/preferences"
)

type fakeStore struct {
	alerts       []alertmanager.IncomingAlert
	query        alerts.Query
	result       alerts.ListResult
	events       []alerts.StreamEvent
	detail       alerts.Detail
	detailErr    error
	acknowledged *bool
	groupResult  alerts.GroupResult
	groupErr     error
	preferences  preferences.Preferences
	prefErr      error
	written      *preferences.Preferences
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

func (store *fakeStore) GroupAlerts(_ context.Context, principal auth.Principal, query alerts.Query) (alerts.GroupResult, error) {
	store.principal = principal
	store.query = query
	return store.groupResult, store.groupErr
}

func (store *fakeStore) ReadPreferences(_ context.Context, principal auth.Principal) (preferences.Preferences, error) {
	store.principal = principal
	return store.preferences, store.prefErr
}

func (store *fakeStore) WritePreferences(_ context.Context, principal auth.Principal, value preferences.Preferences) error {
	store.principal = principal
	if store.prefErr != nil {
		return store.prefErr
	}
	store.written = &value
	return nil
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

func TestListAlertGroups(t *testing.T) {
	latest := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{groupResult: alerts.GroupResult{
		Groups: []alerts.Group{{
			Key:              map[string]string{"alertname": "Cardinality", "source": "yul"},
			Total:            52,
			Acknowledged:     3,
			SeverityCounts:   map[string]int64{"critical": 1, "warning": 51},
			WorstSeverity:    "critical",
			LatestLastSeen:   latest,
			EarliestStartsAt: latest.Add(-time.Hour),
			SampleAlertID:    42,
		}},
		NextCursor: &alerts.GroupCursor{
			SeverityRank: 3, LatestLastSeen: latest, Key: []string{"Cardinality", "yul"},
			Sort: "severity", Order: "desc",
			Query: alerts.Query{Status: alerts.StatusFiring, GroupBy: []string{"alertname", "source"}}.CursorIdentity(),
		},
		TotalGroups:    18,
		TotalAlerts:    236,
		SeverityCounts: map[string]int64{"critical": 1, "warning": 235},
		StreamCursor:   7,
	}}
	handler := New(config.Config{AuthMode: "open"}, store, auth.OpenAuthenticator{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/alerts?groupBy=alertname,source&status=firing", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	if got := store.query.GroupBy; len(got) != 2 || got[0] != "alertname" || got[1] != "source" {
		t.Fatalf("parsed groupBy = %v, want [alertname source]", got)
	}
	// Filters still apply; grouping is a shape, not a replacement for the query.
	if store.query.Status != "firing" {
		t.Errorf("status = %q, want firing", store.query.Status)
	}

	var body struct {
		Groups []struct {
			Key            map[string]string `json:"key"`
			Total          int64             `json:"total"`
			Acknowledged   int64             `json:"acknowledged"`
			WorstSeverity  string            `json:"worstSeverity"`
			SampleAlertID  string            `json:"sampleAlertId"`
			SeverityCounts map[string]int64  `json:"severityCounts"`
		} `json:"groups"`
		NextCursor  string `json:"nextCursor"`
		TotalGroups int64  `json:"totalGroups"`
		Total       int64  `json:"total"`
		Stream      int64  `json:"streamCursor"`
		Alerts      *[]any `json:"alerts"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(body.Groups))
	}
	group := body.Groups[0]
	if group.Key["alertname"] != "Cardinality" || group.Total != 52 || group.Acknowledged != 3 {
		t.Errorf("group = %#v, want the collapsed Cardinality row", group)
	}
	if group.WorstSeverity != "critical" || group.SeverityCounts["warning"] != 51 {
		t.Errorf("group severity = %q %v, want critical with 51 warnings", group.WorstSeverity, group.SeverityCounts)
	}
	// Ids are strings everywhere else in this API; a group's sample is no different.
	if group.SampleAlertID != "42" {
		t.Errorf("sample alert id = %q, want \"42\"", group.SampleAlertID)
	}
	if body.TotalGroups != 18 || body.Total != 236 || body.Stream != 7 {
		t.Errorf("totals = %d groups, %d alerts, stream %d; want 18, 236, 7", body.TotalGroups, body.Total, body.Stream)
	}
	if body.Alerts != nil {
		t.Error("grouped response carried an alerts array, which would double the payload")
	}
	if body.NextCursor == "" {
		t.Fatal("next cursor is empty, want an encoded group cursor")
	}

	// The cursor has to survive a round trip through the client.
	cursor, err := decodeGroupCursor(body.NextCursor)
	if err != nil {
		t.Fatalf("decodeGroupCursor() error = %v", err)
	}
	if cursor.SeverityRank != 3 || cursor.Sort != "severity" || cursor.Order != "desc" || len(cursor.Key) != 2 || cursor.Key[0] != "Cardinality" {
		t.Errorf("decoded cursor = %#v, want the last group's ordering values", cursor)
	}
}

func TestListAlertGroupsAcceptsCustomLabel(t *testing.T) {
	store := &fakeStore{}
	handler := New(config.Config{AuthMode: "open"}, store, auth.OpenAuthenticator{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/alerts?groupBy=prometheus_cluster,source&status=firing", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	if got := store.query.GroupBy; len(got) != 2 || got[0] != "prometheus_cluster" || got[1] != "source" {
		t.Fatalf("parsed groupBy = %v, want [prometheus_cluster source]", got)
	}
	if store.query.Status != alerts.StatusFiring {
		t.Errorf("status = %q, want firing", store.query.Status)
	}
}

func TestListAlertGroupsRejectsUnusableRequests(t *testing.T) {
	store := &fakeStore{}
	handler := New(config.Config{AuthMode: "open"}, store, auth.OpenAuthenticator{})
	for _, test := range []struct{ name, target string }{
		{name: "malformed label", target: "/api/v1/alerts?groupBy=not-a-label"},
		{name: "injection attempt", target: "/api/v1/alerts?groupBy=alertname%3B+DROP+TABLE+alerts"},
		{name: "duplicate key", target: "/api/v1/alerts?groupBy=alertname,alertname"},
		{name: "too many keys", target: "/api/v1/alerts?groupBy=alertname,source,team,severity"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.target, nil))
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusBadRequest, response.Body.String())
			}
		})
	}
}

func TestListAlertGroupsRejectsForeignCursors(t *testing.T) {
	store := &fakeStore{}
	handler := New(config.Config{AuthMode: "open"}, store, auth.OpenAuthenticator{})

	// A cursor from a different grouping would silently return the wrong page.
	foreign, err := encodeGroupCursor(alerts.GroupCursor{
		SeverityRank: 3, Key: []string{"Cardinality"}, Query: "some-other-query",
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/alerts?groupBy=alertname&cursor="+foreign, nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("foreign cursor status = %d, want %d", response.Code, http.StatusBadRequest)
	}

	// A flat-list cursor is not a group cursor either, even though both are
	// opaque base64 to the client.
	flat, err := encodeCursor(alerts.Cursor{Sort: "lastSeen", Order: "desc", Query: "whatever"})
	if err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/alerts?groupBy=alertname&cursor="+flat, nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("flat cursor status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestListAlertGroupsAcceptsItsOwnCursor(t *testing.T) {
	store := &fakeStore{}
	handler := New(config.Config{AuthMode: "open"}, store, auth.OpenAuthenticator{})
	identity := alerts.Query{GroupBy: []string{"alertname"}}.CursorIdentity()
	cursor, err := encodeGroupCursor(alerts.GroupCursor{
		SeverityRank: 2, LatestLastSeen: time.Now().UTC(), Key: []string{"Cardinality"},
		Sort: "severity", Order: "desc", Query: identity,
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/alerts?groupBy=alertname&cursor="+cursor, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	if store.query.GroupCursor == nil || store.query.GroupCursor.Key[0] != "Cardinality" {
		t.Fatalf("group cursor reached the store as %#v, want the decoded cursor", store.query.GroupCursor)
	}
}

func TestListAlertGroupsParsesExplicitSortingAndBindsCursor(t *testing.T) {
	store := &fakeStore{}
	handler := New(config.Config{AuthMode: "open"}, store, auth.OpenAuthenticator{})
	query := alerts.Query{GroupBy: []string{"alertname"}, Sort: "team", Order: "asc"}
	cursor, err := encodeGroupCursor(alerts.GroupCursor{
		Key: []string{"Cardinality"}, Sort: "team", Order: "asc", Value: "platform", Query: query.CursorIdentity(),
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/alerts?groupBy=alertname&sort=team&order=asc&cursor="+cursor, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	if store.query.Sort != "team" || store.query.Order != "asc" || store.query.GroupCursor == nil || store.query.GroupCursor.Value != "platform" {
		t.Fatalf("parsed grouped query = %#v", store.query)
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/alerts?groupBy=alertname&sort=team&order=desc&cursor="+cursor, nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("sort-mismatched cursor status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestListAlertsWithoutGroupingKeepsFlatShape(t *testing.T) {
	// Existing clients send no groupBy and must keep getting exactly what they
	// got before grouping existed.
	store := &fakeStore{result: alerts.ListResult{Total: 1, SeverityCounts: map[string]int64{"warning": 1}}}
	handler := New(config.Config{AuthMode: "open"}, store, auth.OpenAuthenticator{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/alerts", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["alerts"]; !ok {
		t.Error("flat response lost its alerts array")
	}
	if _, ok := body["groups"]; ok {
		t.Error("flat response carried a groups key")
	}
}

func TestGetPreferences(t *testing.T) {
	stored := preferences.Default()
	stored.Density = "compact"
	stored.Columns = []preferences.Column{{ID: "severity"}, {ID: "alert"}, {ID: "label:prometheus_cluster", Width: 180}}
	store := &fakeStore{preferences: stored}
	handler := New(config.Config{AuthMode: "oidc"}, store, fakeAuthenticator{principal: auth.Principal{UserID: 7, Subject: "ada", Roles: []string{"viewer"}}})

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/preferences", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	var body preferences.Preferences
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Density != "compact" || len(body.Columns) != 3 || body.Columns[2].ID != "label:prometheus_cluster" {
		t.Fatalf("preferences = %#v, want the stored layout", body)
	}
	if store.principal.UserID != 7 {
		t.Errorf("store saw user %d, want 7", store.principal.UserID)
	}
}

func TestPreferencesAreUnavailableWithoutAUser(t *testing.T) {
	// Open mode has one anonymous principal for everyone, so there is nothing to
	// key a layout against. The console falls back to browser storage, which it
	// can only do if it can tell this case apart from a failure.
	store := &fakeStore{prefErr: preferences.ErrNoSubject}
	handler := New(config.Config{AuthMode: "open"}, store, auth.OpenAuthenticator{})

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/preferences", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("GET status = %d, want %d", response.Code, http.StatusNotFound)
	}

	body, err := json.Marshal(preferences.Default())
	if err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/api/v1/preferences", bytes.NewReader(body)))
	if response.Code != http.StatusNotFound {
		t.Fatalf("PUT status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestPutPreferences(t *testing.T) {
	store := &fakeStore{}
	handler := New(config.Config{AuthMode: "oidc"}, store, fakeAuthenticator{principal: auth.Principal{UserID: 7, Subject: "ada", Roles: []string{"viewer"}}})

	wanted := preferences.Default()
	wanted.Density = "comfortable"
	wanted.Grouping = preferences.Grouping{Enabled: true, Keys: []string{"alertname"}}
	body, err := json.Marshal(wanted)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/api/v1/preferences", bytes.NewReader(body)))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	if store.written == nil || store.written.Density != "comfortable" {
		t.Fatalf("stored preferences = %#v, want the submitted layout", store.written)
	}
	if len(store.written.Grouping.Keys) != 1 || store.written.Grouping.Keys[0] != "alertname" {
		t.Errorf("stored grouping = %#v, want grouping by alertname", store.written.Grouping)
	}
}

func TestPutPreferencesRejectsUnusableLayouts(t *testing.T) {
	store := &fakeStore{}
	handler := New(config.Config{AuthMode: "oidc"}, store, fakeAuthenticator{principal: auth.Principal{UserID: 7, Subject: "ada", Roles: []string{"viewer"}}})

	for _, test := range []struct{ name, payload string }{
		{name: "not json", payload: "{"},
		{name: "unknown field", payload: `{"columns":[{"id":"severity"}],"density":"normal","surprise":true}`},
		{name: "unknown column", payload: `{"columns":[{"id":"nonsense"}],"density":"normal"}`},
		{name: "unknown density", payload: `{"columns":[{"id":"severity"}],"density":"tiny"}`},
		{name: "grouping by a malformed label", payload: `{"columns":[{"id":"severity"}],"density":"normal","grouping":{"enabled":true,"keys":["not-a-label"]}}`},
		{name: "empty layout", payload: `{"columns":[],"density":"normal"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/api/v1/preferences", strings.NewReader(test.payload)))
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusBadRequest, response.Body.String())
			}
			if store.written != nil {
				t.Fatalf("a rejected layout reached the store: %#v", store.written)
			}
		})
	}
}
