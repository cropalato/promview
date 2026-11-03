package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cropalato/promview/internal/alerts"
	"github.com/cropalato/promview/internal/auth"
	"github.com/cropalato/promview/internal/config"
	"github.com/cropalato/promview/internal/preferences"
)

func signedInViewer() fakeAuthenticator {
	return fakeAuthenticator{principal: auth.Principal{UserID: 7, Subject: "ada", Roles: []string{"viewer"}}}
}

func TestListAlertsResolvesTheViewFromPreferences(t *testing.T) {
	saved := func(enabled, bypass bool) preferences.Preferences {
		value := preferences.Default()
		value.View = &preferences.ViewPreferences{Enabled: enabled, CriticalBypass: bypass}
		return value
	}
	legacy := preferences.Default()
	legacy.View = nil

	tests := []struct {
		name               string
		prefs              preferences.Preferences
		query              string
		wantShow           bool
		wantFilterCritical bool
	}{
		{"defaults narrow and let critical through", preferences.Default(), "", false, false},
		{"a legacy row means the defaults", legacy, "", false, false},
		{"the saved switch shows everything", saved(false, true), "", true, false},
		{"an explicit mine overrides the saved switch", saved(false, true), "?view=mine", false, false},
		{"an explicit all overrides the default", preferences.Default(), "?view=all", true, false},
		{"critical is filtered only when the user chose it", saved(true, false), "", false, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeStore{preferences: test.prefs}
			handler := New(config.Config{AuthMode: "oidc"}, store, signedInViewer(), nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/alerts"+test.query, nil))
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d; body = %s", response.Code, response.Body.String())
			}
			view := store.query.View
			if view == nil {
				t.Fatal("query has no view for a signed-in user")
			}
			if view.ShowAll != test.wantShow || view.FilterCritical != test.wantFilterCritical {
				t.Fatalf("view = %+v, want ShowAll=%t FilterCritical=%t", *view, test.wantShow, test.wantFilterCritical)
			}
		})
	}
}

func TestListAlertsHasNoViewWithoutAUser(t *testing.T) {
	store := &fakeStore{}
	handler := New(config.Config{AuthMode: "open"}, store, auth.OpenAuthenticator{}, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/alerts?view=mine", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", response.Code, response.Body.String())
	}
	if store.query.View != nil {
		t.Fatalf("view = %+v, want none: open mode has nothing to filter by", *store.query.View)
	}
}

func TestListAlertsRejectsAnUnknownView(t *testing.T) {
	handler := New(config.Config{AuthMode: "oidc"}, &fakeStore{preferences: preferences.Default()}, signedInViewer(), nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/alerts?view=everything", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestListAlertsReportsWhatTheViewDid(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{
		preferences: preferences.Default(),
		result: alerts.ListResult{
			Alerts: []alerts.Alert{{
				ID: 1, SourceSlug: "primary", Fingerprint: "a", SourceStatus: "firing",
				Labels: map[string]string{"severity": "critical", "team": "db"}, StartsAt: now, FirstSeen: now, LastSeen: now,
				ViewMatch: alerts.ViewMatchCritical,
			}},
			Total: 1,
			View:  &alerts.ViewSummary{Active: true, Hidden: 4, CriticalBypass: true},
		},
	}
	handler := New(config.Config{AuthMode: "oidc"}, store, signedInViewer(), nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/alerts", nil))
	var body struct {
		Alerts []struct {
			ViewMatch string `json:"viewMatch"`
		} `json:"alerts"`
		View *alerts.ViewSummary `json:"view"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.View == nil || body.View.Hidden != 4 || !body.View.Active {
		t.Fatalf("view = %+v, want the summary the store reported", body.View)
	}
	if len(body.Alerts) != 1 || body.Alerts[0].ViewMatch != "critical" {
		t.Fatalf("alerts = %+v, want the viewMatch the store reported", body.Alerts)
	}
}

func TestListAlertsOmitsViewForAnOpenReader(t *testing.T) {
	handler := New(config.Config{AuthMode: "open"}, &fakeStore{}, auth.OpenAuthenticator{}, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/alerts", nil))
	if strings.Contains(response.Body.String(), `"view"`) || strings.Contains(response.Body.String(), "viewMatch") {
		t.Fatalf("body = %s, want no view fields for a reader with no view", response.Body.String())
	}
}

func TestAPageCursorDoesNotSurviveAChangeOfView(t *testing.T) {
	// Issued under no view, as an open-mode or older client would have it.
	query := alerts.Query{Sort: alerts.DefaultSort, Order: alerts.DefaultOrder}
	encoded, err := encodeCursor(alerts.Cursor{
		ID: 42, LastSeen: time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC),
		Sort: query.Sort, Order: query.Order, Query: query.CursorIdentity(), Value: "2026-08-14T12:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := New(config.Config{AuthMode: "oidc"}, &fakeStore{preferences: preferences.Default()}, signedInViewer(), nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/alerts?cursor="+encoded, nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: a cursor from another view must be refused", response.Code, http.StatusBadRequest)
	}
}

func TestStreamAppliesTheViewAndLabelsEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store := &fakeStore{
		preferences: preferences.Default(),
		events: []alerts.StreamEvent{{
			ID: 8, Type: "alert.updated", AlertID: 42, Severity: "critical", AlertName: "Down",
			SourceSlug: "primary", Team: "db", ViewMatch: alerts.ViewMatchCritical,
			OccurredAt: time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC),
		}},
		cancel: cancel,
	}
	handler := New(config.Config{AuthMode: "oidc"}, store, signedInViewer(), nil)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/stream?cursor=7&view=all", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if store.streamView == nil || !store.streamView.ShowAll {
		t.Fatalf("stream view = %+v, want the requested view=all", store.streamView)
	}
	if !strings.Contains(response.Body.String(), `"viewMatch":"critical"`) {
		t.Fatalf("stream body = %q, want the event's viewMatch", response.Body.String())
	}
}

func TestStreamRejectsAnUnknownView(t *testing.T) {
	handler := New(config.Config{AuthMode: "oidc"}, &fakeStore{preferences: preferences.Default()}, signedInViewer(), nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/stream?view=nope", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}
