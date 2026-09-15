package alertmanager

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestListSilences(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/silences" {
			t.Errorf("path = %q, want /api/v2/silences", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		// One of each: an active silence promview could have written, a pending
		// preventive one with a regex matcher that matches no alert yet, and an
		// expired one from an Alertmanager old enough to omit isEqual.
		_, _ = w.Write([]byte(`[
			{
				"id": "sil-1",
				"status": {"state": "active"},
				"matchers": [
					{"name": "alertname", "value": "PrometheusTargetMissing", "isRegex": false, "isEqual": true},
					{"name": "team", "value": "infra", "isRegex": false, "isEqual": false}
				],
				"startsAt": "2026-09-15T10:00:00Z",
				"endsAt": "2026-09-15T12:00:00Z",
				"createdBy": "ada@example.com",
				"comment": "maintenance"
			},
			{
				"id": "sil-2",
				"status": {"state": "pending"},
				"matchers": [
					{"name": "instance", "value": "dsm-.*", "isRegex": true, "isEqual": true}
				],
				"startsAt": "2026-09-16T02:00:00Z",
				"endsAt": "2026-09-16T04:00:00Z",
				"createdBy": "ops",
				"comment": "planned window, nothing firing yet"
			},
			{
				"id": "sil-3",
				"status": {"state": "expired"},
				"matchers": [{"name": "severity", "value": "warning", "isRegex": false}],
				"startsAt": "2026-09-14T00:00:00Z",
				"endsAt": "2026-09-14T01:00:00Z",
				"createdBy": "",
				"comment": ""
			}
		]`))
	}))
	defer server.Close()

	client := NewClient(time.Second)
	silences, err := client.ListSilences(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("ListSilences() error = %v", err)
	}
	if len(silences) != 3 {
		t.Fatalf("ListSilences() returned %d silences, want 3", len(silences))
	}

	first := silences[0]
	if first.ID != "sil-1" || first.State != "active" || first.CreatedBy != "ada@example.com" {
		t.Errorf("first silence = %+v", first)
	}
	if len(first.Matchers) != 2 {
		t.Fatalf("first silence has %d matchers, want 2", len(first.Matchers))
	}
	// The negation survives verbatim; flattening it would misreport the match.
	if negated := first.Matchers[1]; negated.Name != "team" || negated.IsEqual || negated.IsRegex {
		t.Errorf("negated matcher = %+v", negated)
	}

	// A pending silence matching nothing yet is still real and still listed.
	second := silences[1]
	if second.State != "pending" || !second.Matchers[0].IsRegex {
		t.Errorf("pending silence = %+v", second)
	}

	// Absent isEqual means equality, the pre-0.22 payload shape.
	third := silences[2]
	if third.State != "expired" || !third.Matchers[0].IsEqual {
		t.Errorf("expired silence = %+v", third)
	}
}

func TestListSilencesRefusesASilenceWithoutAnID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"status": {"state": "active"}, "matchers": []}]`))
	}))
	defer server.Close()

	client := NewClient(time.Second)
	if _, err := client.ListSilences(context.Background(), server.URL); err == nil {
		t.Fatal("ListSilences() accepted a silence without an id")
	}
}

func TestListSilencesReportsAnHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	client := NewClient(time.Second)
	if _, err := client.ListSilences(context.Background(), server.URL); err == nil {
		t.Fatal("ListSilences() ignored an HTTP error")
	}
}

func TestActiveSilenceIDs(t *testing.T) {
	active := ActiveSilenceIDs([]ListedSilence{
		{ID: "a", State: "active"},
		{ID: "b", State: "pending"},
		{ID: "c", State: "expired"},
	})
	if len(active) != 1 || !active["a"] {
		t.Errorf("ActiveSilenceIDs() = %v, want only a", active)
	}
	// Empty is a real answer, distinct from nil, which means the listing failed.
	if ActiveSilenceIDs(nil) == nil {
		t.Error("ActiveSilenceIDs(nil listing) should be an empty map, not nil")
	}
}
