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
		Labels: map[string]string{"alertname": "HighCPU", "instance": "web-01"},
		Targets: []alerts.SilenceTarget{{
			Source:            "demo",
			AlertmanagerURL:   "http://am-a:9093",
			AlertmanagerToken: "sekret",
			Labels:            map[string]string{"alertname": "HighCPU", "instance": "web-01"},
			Members:           1,
		}},
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
			{
				Source:          "demo",
				AlertmanagerURL: "http://am-a:9093",
				Labels:          map[string]string{"alertname": "HighCPU"},
				Members:         2,
			},
			{
				Source:          "edge",
				AlertmanagerURL: "http://am-b:9093",
				Labels:          map[string]string{"alertname": "HighCPU"},
				Members:         1,
			},
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
			{
				Source:          "demo",
				AlertmanagerURL: "http://am-a:9093",
				Labels:          map[string]string{"alertname": "HighCPU"},
				Members:         2,
			},
			{
				Source:          "edge",
				AlertmanagerURL: "http://am-b:9093",
				Labels:          map[string]string{"alertname": "HighCPU"},
				Members:         1,
			},
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

// twoTargetsDisagreeing is the case the fold exists for: a group whose members
// live on two Alertmanagers and agree on more than the grouping key, but agree
// on different things at each one.
func twoTargetsDisagreeing() alerts.SilenceScope {
	return alerts.SilenceScope{
		Labels: map[string]string{"alertname": "HighCPU"},
		Targets: []alerts.SilenceTarget{
			{
				Source:          "demo",
				AlertmanagerURL: "http://am-a:9093",
				Labels:          map[string]string{"alertname": "HighCPU", "cluster": "a"},
				Members:         3,
			},
			{
				Source:          "edge",
				AlertmanagerURL: "http://am-b:9093",
				Labels:          map[string]string{"alertname": "HighCPU", "cluster": "b"},
				Members:         2,
			},
		},
	}
}

func TestSilenceGroupWritesEachAlertmanagerItsOwnNarrowerMatch(t *testing.T) {
	store := &fakeStore{silenceScope: twoTargetsDisagreeing()}
	silencer := newFakeSilencer()
	handler := New(silenceConfig(), store, operator(), silencer)

	body := `{"groupBy":["alertname"],"key":{"alertname":"HighCPU"}}`
	if response := postSilence(handler, "/api/v1/groups/silence", body); response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (%s)", response.Code, http.StatusCreated, response.Body)
	}

	// A single shared match would drop `cluster` and silence both clusters at
	// both Alertmanagers, which is more than the group covers.
	for url, cluster := range map[string]string{"http://am-a:9093": "a", "http://am-b:9093": "b"} {
		silence, ok := silencer.created[url]
		if !ok {
			t.Fatalf("no silence reached %s", url)
		}
		found := ""
		for _, matcher := range silence.Matchers {
			if matcher.Name == "cluster" {
				found = matcher.Value
			}
		}
		if found != cluster {
			t.Errorf("%s matched cluster=%q, want %q", url, found, cluster)
		}
	}
}

func TestSilenceGroupRecordsWhoAskedAndUntilWhen(t *testing.T) {
	store := &fakeStore{silenceScope: twoTargetsDisagreeing()}
	handler := New(silenceConfig(), store, operator(), newFakeSilencer())

	body := `{"groupBy":["alertname"],"key":{"alertname":"HighCPU"},"comment":"maintenance"}`
	if response := postSilence(handler, "/api/v1/groups/silence", body); response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (%s)", response.Code, http.StatusCreated, response.Body)
	}
	if len(store.recorded) != 2 {
		t.Fatalf("recorded = %d silences, want one per target", len(store.recorded))
	}
	for _, record := range store.recorded {
		if record.CreatedBy != "ada@example.com" {
			t.Errorf("createdBy = %q, want the signed-in operator", record.CreatedBy)
		}
		if record.Comment != "maintenance" {
			t.Errorf("comment = %q, want the operator's own words", record.Comment)
		}
		if !record.EndsAt.After(record.StartsAt) {
			t.Errorf("silence %s does not end after it starts", record.SilenceID)
		}
		// The match is per target, so the record has to be too, or the console
		// would later explain the silence with somebody else's matchers.
		if record.Matchers["alertname"] != "HighCPU" || record.Matchers["cluster"] == "" {
			t.Errorf("recorded matchers = %v, want this target's own match", record.Matchers)
		}
	}
}

func TestSilenceGroupFailingToRecordStillReportsTheSilenceItCreated(t *testing.T) {
	store := &fakeStore{silenceScope: oneTarget(), recordErr: errors.New("database is down")}
	handler := New(silenceConfig(), store, operator(), newFakeSilencer())

	// The silence exists on the Alertmanager either way. Reporting a failure
	// would tell the operator to try again against something already silenced.
	body := `{"groupBy":["alertname"],"key":{"alertname":"HighCPU"}}`
	if response := postSilence(handler, "/api/v1/groups/silence", body); response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (%s)", response.Code, http.StatusCreated, response.Body)
	}
}

func TestPreviewGroupSilenceAnswersWhatWouldActuallyMatch(t *testing.T) {
	store := &fakeStore{silenceScope: twoTargetsDisagreeing()}
	handler := New(silenceConfig(), store, operator(), newFakeSilencer())

	body := `{"groupBy":["alertname"],"key":{"alertname":"HighCPU"}}`
	response := postSilence(handler, "/api/v1/groups/silence/preview", body)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (%s)", response.Code, http.StatusOK, response.Body)
	}
	var payload struct {
		Matchers    map[string]string `json:"matchers"`
		MemberCount int               `json:"memberCount"`
		Targets     []struct {
			Source   string            `json:"source"`
			Matchers map[string]string `json:"matchers"`
			Members  int               `json:"members"`
		} `json:"targets"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.MemberCount != 5 {
		t.Errorf("memberCount = %d, want every in-scope member", payload.MemberCount)
	}
	if len(payload.Targets) != 2 {
		t.Fatalf("targets = %#v, want one per alertmanager", payload.Targets)
	}
	// The common match is what the dialog shows on one line; each target's own
	// match is at least this narrow, and the dialog spells out the difference.
	if len(payload.Matchers) != 1 || payload.Matchers["alertname"] != "HighCPU" {
		t.Errorf("matchers = %v, want what every target agrees on", payload.Matchers)
	}
}

func TestPreviewGroupSilenceNeedsOperatorRights(t *testing.T) {
	store := &fakeStore{silenceScope: oneTarget()}
	viewer := fakeAuthenticator{principal: auth.Principal{Subject: "v", Roles: []string{"viewer"}}}
	handler := New(silenceConfig(), store, viewer, newFakeSilencer())

	body := `{"groupBy":["alertname"],"key":{"alertname":"HighCPU"}}`
	// A preview names the labels of alerts the caller may not act on; it runs
	// the same gate the silence itself does.
	if response := postSilence(handler, "/api/v1/groups/silence/preview", body); response.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", response.Code, http.StatusForbidden)
	}
}

func TestSilenceGroupRefusesWhenTheMatchMovedSinceThePreview(t *testing.T) {
	store := &fakeStore{silenceScope: twoTargetsDisagreeing()}
	silencer := newFakeSilencer()
	handler := New(silenceConfig(), store, operator(), silencer)

	// The console read `alertname` plus `cluster`; a member joined that does
	// not carry `cluster`, so the silence would now be broader than what was
	// confirmed. Refusing is the whole point of echoing the match back.
	body := `{"groupBy":["alertname"],"key":{"alertname":"HighCPU"},` +
		`"expectedMatchers":{"alertname":"HighCPU","cluster":"a"}}`
	response := postSilence(handler, "/api/v1/groups/silence", body)
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d (%s)", response.Code, http.StatusConflict, response.Body)
	}
	if len(silencer.created) != 0 {
		t.Error("a silence was written despite the scope having moved")
	}
	var payload struct {
		Matchers map[string]string `json:"matchers"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Matchers["alertname"] != "HighCPU" {
		t.Errorf("conflict returned %v, want the match the operator now has to review", payload.Matchers)
	}
}

func TestSilenceGroupAcceptsAMatchThatHasNotMoved(t *testing.T) {
	store := &fakeStore{silenceScope: twoTargetsDisagreeing()}
	handler := New(silenceConfig(), store, operator(), newFakeSilencer())

	body := `{"groupBy":["alertname"],"key":{"alertname":"HighCPU"},` +
		`"expectedMatchers":{"alertname":"HighCPU"}}`
	if response := postSilence(handler, "/api/v1/groups/silence", body); response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (%s)", response.Code, http.StatusCreated, response.Body)
	}
}

func TestSilenceGroupAcceptsABodyWithNoExpectedMatchers(t *testing.T) {
	store := &fakeStore{silenceScope: twoTargetsDisagreeing()}
	silencer := newFakeSilencer()
	handler := New(silenceConfig(), store, operator(), silencer)

	// A console that could not resolve the match sends no guard rather than
	// echoing back the grouping key. Asking the server to compare its own
	// folded match against something it never produced could only ever
	// disagree, which would make silencing permanently impossible.
	body := `{"groupBy":["alertname"],"key":{"alertname":"HighCPU"}}`
	if response := postSilence(handler, "/api/v1/groups/silence", body); response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (%s)", response.Code, http.StatusCreated, response.Body)
	}
	if len(silencer.created) != 2 {
		t.Errorf("silences created = %d, want one per alertmanager", len(silencer.created))
	}
}

func TestConfigAdvertisesThatTheServerCanResolveASilenceScope(t *testing.T) {
	handler := New(silenceConfig(), &fakeStore{}, operator(), newFakeSilencer())
	request := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	// A console cannot probe for the endpoint: an older server rejects the
	// whole request rather than ignoring the field it does not know. The
	// capability has to be advertised or it cannot be used safely.
	if payload["silencePreviewSupported"] != true {
		t.Errorf("silencePreviewSupported = %v, want true", payload["silencePreviewSupported"])
	}
}
