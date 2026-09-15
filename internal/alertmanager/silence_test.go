package alertmanager

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMatchersFromLabelsIsStableAndExact(t *testing.T) {
	matchers := MatchersFromLabels(map[string]string{
		"severity":  "critical",
		"alertname": "HighCPU",
		"instance":  "web-01",
	})
	if len(matchers) != 3 {
		t.Fatalf("matchers = %d, want 3", len(matchers))
	}
	// Sorted, so the same alert always produces the same body.
	if matchers[0].Name != "alertname" || matchers[1].Name != "instance" || matchers[2].Name != "severity" {
		t.Errorf("matchers are not sorted by name: %#v", matchers)
	}
	for _, matcher := range matchers {
		if matcher.IsRegex {
			t.Errorf("matcher %q is a regex, want exact", matcher.Name)
		}
		if !matcher.IsEqual {
			t.Errorf("matcher %q is a negation, want equality", matcher.Name)
		}
	}
}

func TestCreateSilencePostsTheSilenceAndReturnsItsID(t *testing.T) {
	var got struct {
		Matchers  []Matcher `json:"matchers"`
		StartsAt  string    `json:"startsAt"`
		EndsAt    string    `json:"endsAt"`
		CreatedBy string    `json:"createdBy"`
		Comment   string    `json:"comment"`
	}
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v2/silences" {
			t.Errorf("request = %s %s, want POST /api/v2/silences", r.Method, r.URL.Path)
		}
		authorization = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"silenceID":"4f2c8e1a"}`))
	}))
	defer server.Close()

	start := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	id, err := NewClient(2*time.Second).CreateSilence(context.Background(), server.URL, "sekret", Silence{
		Matchers:  MatchersFromLabels(map[string]string{"alertname": "HighCPU"}),
		StartsAt:  start,
		EndsAt:    start.Add(2 * time.Hour),
		CreatedBy: "ada@example.com",
		Comment:   "maintenance",
	})
	if err != nil {
		t.Fatalf("CreateSilence() error = %v", err)
	}
	if id != "4f2c8e1a" {
		t.Errorf("silence id = %q, want 4f2c8e1a", id)
	}
	if authorization != "Bearer sekret" {
		t.Errorf("Authorization = %q, want the source's bearer token", authorization)
	}
	if got.CreatedBy != "ada@example.com" {
		t.Errorf("createdBy = %q, want the operator", got.CreatedBy)
	}
	if got.EndsAt != "2026-08-21T14:00:00Z" {
		t.Errorf("endsAt = %q, want the start plus the window", got.EndsAt)
	}
	if len(got.Matchers) != 1 || got.Matchers[0].Name != "alertname" {
		t.Errorf("matchers = %#v, want the alert's labels", got.Matchers)
	}
}

func TestCreateSilenceSendsNoCredentialWhenTheSourceHasNone(t *testing.T) {
	seen := "unset"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"silenceID":"x"}`))
	}))
	defer server.Close()

	now := time.Now()
	if _, err := NewClient(2*time.Second).CreateSilence(context.Background(), server.URL, "", Silence{
		Matchers:  MatchersFromLabels(map[string]string{"alertname": "HighCPU"}),
		StartsAt:  now,
		EndsAt:    now.Add(time.Hour),
		CreatedBy: "ada",
	}); err != nil {
		t.Fatal(err)
	}
	if seen != "" {
		t.Errorf("Authorization = %q, want no header when the source has no token", seen)
	}
}

func TestCreateSilenceRefusesAnUnsafeOrUnattributableSilence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("an invalid silence reached the Alertmanager")
	}))
	defer server.Close()
	now := time.Now()
	client := NewClient(2 * time.Second)

	for _, test := range []struct {
		name    string
		silence Silence
	}{
		{
			// No matchers silences the entire deployment.
			name:    "no matchers",
			silence: Silence{StartsAt: now, EndsAt: now.Add(time.Hour), CreatedBy: "ada"},
		},
		{
			name: "no author",
			silence: Silence{
				Matchers: MatchersFromLabels(map[string]string{"alertname": "X"}),
				StartsAt: now, EndsAt: now.Add(time.Hour),
			},
		},
		{
			name: "ends before it starts",
			silence: Silence{
				Matchers: MatchersFromLabels(map[string]string{"alertname": "X"}),
				StartsAt: now, EndsAt: now.Add(-time.Hour), CreatedBy: "ada",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := client.CreateSilence(context.Background(), server.URL, "", test.silence); err == nil {
				t.Fatal("CreateSilence() error = nil, want error")
			}
		})
	}
}

func TestCreateSilenceFailsWhenAlertmanagerRejectsOrReturnsNoID(t *testing.T) {
	rejecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer rejecting.Close()
	silent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer silent.Close()

	now := time.Now()
	valid := Silence{
		Matchers:  MatchersFromLabels(map[string]string{"alertname": "X"}),
		StartsAt:  now,
		EndsAt:    now.Add(time.Hour),
		CreatedBy: "ada",
	}
	client := NewClient(2 * time.Second)
	if _, err := client.CreateSilence(context.Background(), rejecting.URL, "", valid); err == nil {
		t.Error("a 401 was reported as success")
	}
	// An id-less response cannot be reported or later expired.
	if _, err := client.CreateSilence(context.Background(), silent.URL, "", valid); err == nil {
		t.Error("a silence with no id was reported as success")
	}
}

func TestDeleteSilenceExpiresItOnTheAlertmanager(t *testing.T) {
	var method, path, authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		authorization = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	if err := NewClient(2*time.Second).DeleteSilence(context.Background(), server.URL, "sekret", "4f2c8e1a"); err != nil {
		t.Fatalf("DeleteSilence() error = %v", err)
	}
	if method != http.MethodDelete || path != "/api/v2/silence/4f2c8e1a" {
		t.Errorf("request = %s %s, want DELETE /api/v2/silence/4f2c8e1a", method, path)
	}
	// Writes are the direction deployments protect, and removal is a write.
	if authorization != "Bearer sekret" {
		t.Errorf("authorization = %q, want the source's bearer credential", authorization)
	}
}

func TestDeleteSilenceEscapesTheIDIntoThePath(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.EscapedPath()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// The id comes from the Alertmanager rather than from a person, but it lands
	// in a URL path, and a path separator inside it must not become one.
	if err := NewClient(2*time.Second).DeleteSilence(context.Background(), server.URL, "", "a/b"); err != nil {
		t.Fatalf("DeleteSilence() error = %v", err)
	}
	if path != "/api/v2/silence/a%2Fb" {
		t.Errorf("path = %q, want the id escaped into one segment", path)
	}
}

func TestDeleteSilenceTreatsAnAbsentSilenceAsDone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	// The operator asked for it to stop suppressing, and it is not suppressing.
	// Failing here would only invite a retry of something already done.
	if err := NewClient(2*time.Second).DeleteSilence(context.Background(), server.URL, "", "gone"); err != nil {
		t.Errorf("a silence already gone was reported as an error: %v", err)
	}
}

func TestDeleteSilenceReportsARefusal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	client := NewClient(2 * time.Second)
	// The silence is still in place, so the alert is still hidden; saying
	// otherwise would promise noise that is not coming back.
	if err := client.DeleteSilence(context.Background(), server.URL, "", "sil-1"); err == nil {
		t.Error("a 401 was reported as success")
	}
	if err := client.DeleteSilence(context.Background(), server.URL, "", "  "); err == nil {
		t.Error("an empty silence id was accepted")
	}
}
