package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cropalato/promview/internal/alerts"
	"github.com/cropalato/promview/internal/auth"
	"github.com/cropalato/promview/internal/config"
)

func silenceConfig() config.Config {
	return config.Config{
		AuthMode:               "oidc",
		SilenceDefaultDuration: 2 * time.Hour,
		SilenceMaxDuration:     30 * 24 * time.Hour,
	}
}

func operator() fakeAuthenticator {
	return fakeAuthenticator{principal: auth.Principal{
		Subject:     "operator-1",
		Email:       "ada@example.com",
		DisplayName: "Ada",
		Roles:       []string{"operator"},
	}}
}

func postSilence(handler http.Handler, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer session-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func oneTarget() alerts.SilenceScope {
	return alerts.SilenceScope{
		Labels:  map[string]string{"alertname": "HighCPU", "instance": "web-01"},
		Targets: []alerts.SilenceTarget{{Source: "demo", AlertmanagerURL: "http://am-a:9093", AlertmanagerToken: "sekret"}},
	}
}

func TestSilenceAlertUsesTheDefaultWindowAndTheSignedInOperator(t *testing.T) {
	store := &fakeStore{silenceScope: oneTarget()}
	silencer := newFakeSilencer()
	handler := New(silenceConfig(), store, operator(), silencer)

	response := postSilence(handler, "/api/v1/alerts/42/silence", `{}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (%s)", response.Code, http.StatusCreated, response.Body)
	}
	silence, ok := silencer.created["http://am-a:9093"]
	if !ok {
		t.Fatal("no silence reached the alertmanager")
	}
	// The person is the author, not the service: a silence nobody can be asked
	// about is the artefact this avoids.
	if silence.CreatedBy != "ada@example.com" {
		t.Errorf("createdBy = %q, want the operator's email", silence.CreatedBy)
	}
	if got := silence.EndsAt.Sub(silence.StartsAt); got != 2*time.Hour {
		t.Errorf("window = %s, want the configured 2h default", got)
	}
	if silencer.tokens["http://am-a:9093"] != "sekret" {
		t.Errorf("token = %q, want the source's credential", silencer.tokens["http://am-a:9093"])
	}
	// Every label, exact match, so only this series is silenced.
	if len(silence.Matchers) != 2 || silence.Matchers[0].Name != "alertname" || !silence.Matchers[0].IsEqual {
		t.Errorf("matchers = %#v, want one equality matcher per label", silence.Matchers)
	}
}

func TestSilenceAlertHonoursAnExplicitWindowWithinTheMaximum(t *testing.T) {
	silencer := newFakeSilencer()
	handler := New(silenceConfig(), &fakeStore{silenceScope: oneTarget()}, operator(), silencer)

	if code := postSilence(handler, "/api/v1/alerts/42/silence", `{"durationSeconds":1800}`).Code; code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", code, http.StatusCreated)
	}
	silence := silencer.created["http://am-a:9093"]
	if got := silence.EndsAt.Sub(silence.StartsAt); got != 30*time.Minute {
		t.Errorf("window = %s, want the requested 30m", got)
	}
}

func TestSilenceAlertRefusesAWindowPastTheMaximum(t *testing.T) {
	silencer := newFakeSilencer()
	handler := New(silenceConfig(), &fakeStore{silenceScope: oneTarget()}, operator(), silencer)

	// A silence longer than the ceiling is a deleted rule wearing a disguise.
	response := postSilence(handler, "/api/v1/alerts/42/silence", `{"durationSeconds":99999999}`)
	if response.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	if len(silencer.created) != 0 {
		t.Error("an over-long silence reached the alertmanager")
	}
}

func TestSilenceRequiresOperatorRights(t *testing.T) {
	silencer := newFakeSilencer()
	viewer := fakeAuthenticator{principal: auth.Principal{Subject: "v", Roles: []string{"viewer"}}}
	handler := New(silenceConfig(), &fakeStore{silenceScope: oneTarget()}, viewer, silencer)

	if code := postSilence(handler, "/api/v1/alerts/42/silence", `{}`).Code; code != http.StatusForbidden {
		t.Errorf("viewer status = %d, want %d", code, http.StatusForbidden)
	}
	// Open mode has no user to attribute a silence to, and no operator rights.
	open := New(silenceConfig(), &fakeStore{silenceScope: oneTarget()}, auth.OpenAuthenticator{}, silencer)
	if code := postSilence(open, "/api/v1/alerts/42/silence", `{}`).Code; code != http.StatusForbidden {
		t.Errorf("anonymous status = %d, want %d", code, http.StatusForbidden)
	}
	if len(silencer.created) != 0 {
		t.Error("a silence was created without operator rights")
	}
}

func TestSilenceReportsAMissingAlertmanagerRatherThanSucceeding(t *testing.T) {
	store := &fakeStore{silenceErr: alerts.ErrNoSilenceTarget}
	handler := New(silenceConfig(), store, operator(), newFakeSilencer())

	if code := postSilence(handler, "/api/v1/alerts/42/silence", `{}`).Code; code != http.StatusConflict {
		t.Errorf("status = %d, want %d", code, http.StatusConflict)
	}
}

func TestSilenceIsUnavailableWhenNotConfigured(t *testing.T) {
	handler := New(silenceConfig(), &fakeStore{silenceScope: oneTarget()}, operator(), nil)
	if code := postSilence(handler, "/api/v1/alerts/42/silence", `{}`).Code; code != http.StatusNotImplemented {
		t.Errorf("status = %d, want %d", code, http.StatusNotImplemented)
	}
}

func TestSilenceGroupFansOutToEveryAlertmanagerItSpans(t *testing.T) {
	store := &fakeStore{silenceScope: alerts.SilenceScope{
		Labels: map[string]string{"alertname": "HighCPU"},
		Targets: []alerts.SilenceTarget{
			{Source: "demo", AlertmanagerURL: "http://am-a:9093"},
			{Source: "edge", AlertmanagerURL: "http://am-b:9093"},
		},
	}}
	silencer := newFakeSilencer()
	handler := New(silenceConfig(), store, operator(), silencer)

	body := `{"groupBy":["alertname"],"key":{"alertname":"HighCPU"},"comment":"maintenance"}`
	response := postSilence(handler, "/api/v1/groups/silence", body)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (%s)", response.Code, http.StatusCreated, response.Body)
	}
	if len(silencer.created) != 2 {
		t.Fatalf("silences created = %d, want one per alertmanager", len(silencer.created))
	}
	if store.groupKey["alertname"] != "HighCPU" || len(store.groupBy) != 1 {
		t.Errorf("scope resolved with groupBy=%v key=%v", store.groupBy, store.groupKey)
	}
	if silencer.created["http://am-b:9093"].Comment != "maintenance" {
		t.Error("the comment did not reach every alertmanager")
	}
}

func TestSilenceGroupReportsPartialApplicationRatherThanClaimingSuccess(t *testing.T) {
	store := &fakeStore{silenceScope: alerts.SilenceScope{
		Labels: map[string]string{"alertname": "HighCPU"},
		Targets: []alerts.SilenceTarget{
			{Source: "demo", AlertmanagerURL: "http://am-a:9093"},
			{Source: "edge", AlertmanagerURL: "http://am-b:9093"},
		},
	}}
	silencer := newFakeSilencer()
	silencer.failFor = "http://am-b:9093"
	silencer.failWith = errors.New("alertmanager returned HTTP 401")
	handler := New(silenceConfig(), store, operator(), silencer)

	body := `{"groupBy":["alertname"],"key":{"alertname":"HighCPU"}}`
	response := postSilence(handler, "/api/v1/groups/silence", body)
	// An operator who thinks a group is handled while half of it still fires is
	// worse off than one told which half failed.
	if response.Code != http.StatusMultiStatus {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusMultiStatus)
	}
	var payload struct {
		Results []alerts.SilenceResult `json:"results"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Results) != 2 {
		t.Fatalf("results = %d, want one per target", len(payload.Results))
	}
	var failed, succeeded int
	for _, result := range payload.Results {
		if result.Error != "" {
			failed++
			if result.Source != "edge" {
				t.Errorf("failure attributed to %q, want edge", result.Source)
			}
			continue
		}
		succeeded++
		if result.SilenceID == "" {
			t.Error("a success carried no silence id")
		}
	}
	if failed != 1 || succeeded != 1 {
		t.Errorf("succeeded = %d, failed = %d, want one of each", succeeded, failed)
	}
}

func TestSilenceGroupFailsWhenEveryAlertmanagerRefuses(t *testing.T) {
	store := &fakeStore{silenceScope: oneTarget()}
	silencer := newFakeSilencer()
	silencer.failFor = "http://am-a:9093"
	handler := New(silenceConfig(), store, operator(), silencer)

	body := `{"groupBy":["alertname"],"key":{"alertname":"HighCPU"}}`
	if code := postSilence(handler, "/api/v1/groups/silence", body).Code; code != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", code, http.StatusBadGateway)
	}
}

func TestSilenceGroupRequiresAKey(t *testing.T) {
	handler := New(silenceConfig(), &fakeStore{silenceScope: oneTarget()}, operator(), newFakeSilencer())
	if code := postSilence(handler, "/api/v1/groups/silence", `{"groupBy":["alertname"]}`).Code; code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", code, http.StatusBadRequest)
	}
}

func TestConfigAdvertisesTheSilenceWindow(t *testing.T) {
	handler := New(silenceConfig(), &fakeStore{}, operator(), newFakeSilencer())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/config", nil))

	var payload struct {
		SilenceDefaultSeconds int64 `json:"silenceDefaultSeconds"`
		SilenceMaxSeconds     int64 `json:"silenceMaxSeconds"`
		SilenceEnabled        bool  `json:"silenceEnabled"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	// The console defaults and bounds its own control from this rather than
	// hardcoding a window the server would reject.
	if payload.SilenceDefaultSeconds != 7200 {
		t.Errorf("default = %d, want 7200", payload.SilenceDefaultSeconds)
	}
	if payload.SilenceMaxSeconds != 30*24*3600 {
		t.Errorf("max = %d, want 30 days", payload.SilenceMaxSeconds)
	}
	if !payload.SilenceEnabled {
		t.Error("silenceEnabled = false with a silencer configured")
	}
}
