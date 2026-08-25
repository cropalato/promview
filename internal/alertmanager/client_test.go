package alertmanager

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLiveAlertsReadsSuppressionAndAsksForEveryState(t *testing.T) {
	var requested string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = r.URL.RequestURI()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"fingerprint":"a","status":{"state":"active"}},
			{"fingerprint":"b","status":{"state":"suppressed"}}
		]`))
	}))
	defer server.Close()

	live, err := NewClient(2*time.Second).LiveAlerts(context.Background(), server.URL+"/")
	if err != nil {
		t.Fatalf("LiveAlerts() error = %v", err)
	}
	// Silenced alerts must be included: they are still firing, and treating
	// their absence from notifications as an ending is the bug being fixed.
	if requested != "/api/v2/alerts?active=true&silenced=true&inhibited=true" {
		t.Errorf("requested %q, want every state included", requested)
	}
	if len(live) != 2 || live[0].Suppressed || !live[1].Suppressed {
		t.Fatalf("live = %#v, want the second alert marked suppressed", live)
	}
}

func TestLiveAlertsRejectsUnusableResponses(t *testing.T) {
	for _, test := range []struct {
		name    string
		status  int
		payload string
	}{
		{name: "server error", status: http.StatusInternalServerError, payload: `[]`},
		{name: "not json", status: http.StatusOK, payload: `<html>`},
		// Without a fingerprint an alert cannot be matched to a stored one, and
		// a partial match would resolve the wrong alert.
		{name: "alert without fingerprint", status: http.StatusOK, payload: `[{"status":{"state":"active"}}]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.payload))
			}))
			defer server.Close()

			if _, err := NewClient(2*time.Second).LiveAlerts(context.Background(), server.URL); err == nil {
				t.Fatal("LiveAlerts() error = nil, want error")
			}
		})
	}
}

func TestLiveAlertsFailsWhenAlertmanagerIsUnreachable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	address := server.URL
	server.Close()

	if _, err := NewClient(time.Second).LiveAlerts(context.Background(), address); err == nil {
		t.Fatal("LiveAlerts() against a closed server error = nil, want error")
	}
}

func TestLiveAlertsTellsASilenceApartFromAnInhibition(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"fingerprint":"a","status":{"state":"suppressed","silencedBy":["sil-1","sil-2"],"inhibitedBy":[]},
			 "labels":{"alertname":"HighCPU"}},
			{"fingerprint":"b","status":{"state":"suppressed","silencedBy":[],"inhibitedBy":["fp-parent"]}},
			{"fingerprint":"c","status":{"state":"active"}}
		]`))
	}))
	defer server.Close()

	live, err := NewClient(2*time.Second).LiveAlerts(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("LiveAlerts() error = %v", err)
	}
	if len(live) != 3 {
		t.Fatalf("live = %#v, want three alerts", live)
	}
	if len(live[0].SilencedBy) != 2 || live[0].SilencedBy[0] != "sil-1" {
		t.Errorf("silencedBy = %v, want the ids alertmanager reported", live[0].SilencedBy)
	}
	// Suppressed with no silence ids means inhibited: nobody chose it, and it
	// lifts itself when the parent alert clears. Collapsing the two would tell
	// an operator somebody silenced an alert nobody silenced.
	if !live[1].Suppressed || len(live[1].SilencedBy) != 0 {
		t.Errorf("inhibited alert = %#v, want suppressed with no silence ids", live[1])
	}
	// Never nil: an absent list and an empty one have to compare equal, or
	// every reconciliation pass would rewrite the row.
	if live[2].SilencedBy == nil {
		t.Error("an unsuppressed alert reported nil silence ids rather than an empty list")
	}
}
