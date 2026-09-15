package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cropalato/promview/internal/alertmanager"
	"github.com/cropalato/promview/internal/postgres"
)

type fakeReconcileStore struct {
	sources      map[string]string
	firing       map[string][]string
	missingCalls []map[string]bool
	liveCalls    [][]alertmanager.LiveAlert
	silenceSets  []map[string]bool
	synced       [][]alertmanager.ListedSilence
	sourcesErr   error
	firingErr    error
	reconcileErr error
	syncErr      error
}

func (store *fakeReconcileStore) ReconcilableSources(context.Context) (map[string]string, error) {
	return store.sources, store.sourcesErr
}

func (store *fakeReconcileStore) FiringFingerprints(_ context.Context, slug string) ([]string, error) {
	return store.firing[slug], store.firingErr
}

func (store *fakeReconcileStore) ReconcileSource(
	_ context.Context,
	_ string,
	live []alertmanager.LiveAlert,
	missing map[string]bool,
	activeSilences map[string]bool,
	_ time.Time,
) (postgres.ReconcileResult, error) {
	store.missingCalls = append(store.missingCalls, missing)
	store.liveCalls = append(store.liveCalls, live)
	store.silenceSets = append(store.silenceSets, activeSilences)
	return postgres.ReconcileResult{Resolved: len(missing)}, store.reconcileErr
}

func (store *fakeReconcileStore) SyncSilences(
	_ context.Context,
	_ string,
	listed []alertmanager.ListedSilence,
	_ time.Time,
) error {
	store.synced = append(store.synced, listed)
	return store.syncErr
}

type fakeAlertmanager struct {
	live        []alertmanager.LiveAlert
	err         error
	nth         int
	silences    []alertmanager.ListedSilence
	silencesErr error
	// responses, when set, is used per call so a restart can be simulated.
	responses [][]alertmanager.LiveAlert
}

func (client *fakeAlertmanager) LiveAlerts(context.Context, string) ([]alertmanager.LiveAlert, error) {
	if client.err != nil {
		return nil, client.err
	}
	if client.responses != nil {
		response := client.responses[min(client.nth, len(client.responses)-1)]
		client.nth++
		return response, nil
	}
	return client.live, nil
}

func (client *fakeAlertmanager) ListSilences(context.Context, string) ([]alertmanager.ListedSilence, error) {
	if client.silencesErr != nil {
		return nil, client.silencesErr
	}
	return client.silences, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestReconcilerRequiresConsecutiveAbsences(t *testing.T) {
	store := &fakeReconcileStore{
		sources: map[string]string{"yul": "http://am:9093"},
		firing:  map[string][]string{"yul": {"present", "gone"}},
	}
	client := &fakeAlertmanager{live: []alertmanager.LiveAlert{{Fingerprint: "present"}}}
	r := newReconciler(store, client, nil)
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)

	// One absent reading is not evidence: a request can be dropped, and an
	// Alertmanager mid-restart holds nothing at all.
	r.reconcileOnce(context.Background(), now)
	if len(store.missingCalls[0]) != 0 {
		t.Fatalf("first pass reported %v missing, want none", store.missingCalls[0])
	}

	r.reconcileOnce(context.Background(), now)
	if !store.missingCalls[1]["gone"] {
		t.Fatalf("second pass missing = %v, want the absent alert", store.missingCalls[1])
	}
	if store.missingCalls[1]["present"] {
		t.Error("an alert the alertmanager still holds was reported missing")
	}
}

func TestReconcilerForgetsAnAlertThatComesBack(t *testing.T) {
	store := &fakeReconcileStore{
		sources: map[string]string{"yul": "http://am:9093"},
		firing:  map[string][]string{"yul": {"flapping"}},
	}
	// Absent, then back, then absent: the counter must restart, otherwise a
	// single old absence eventually resolves a live alert.
	client := &fakeAlertmanager{responses: [][]alertmanager.LiveAlert{
		{},
		{{Fingerprint: "flapping"}},
		{},
	}}
	r := newReconciler(store, client, nil)
	now := time.Now().UTC()

	for i := 0; i < 3; i++ {
		r.reconcileOnce(context.Background(), now)
	}
	for pass, missing := range store.missingCalls {
		if len(missing) != 0 {
			t.Errorf("pass %d reported %v missing, want none", pass+1, missing)
		}
	}
}

func TestReconcilerNeverResolvesOnAnEmptyAlertmanager(t *testing.T) {
	// The dangerous case: a restarting Alertmanager holds nothing. However many
	// consecutive empty readings arrive, none of them may resolve anything -
	// two-strikes alone is not enough here, because a restart easily outlasts
	// two intervals and would then wipe the console in one go.
	store := &fakeReconcileStore{
		sources: map[string]string{"yul": "http://am:9093"},
		firing:  map[string][]string{"yul": {"a", "b", "c"}},
	}
	client := &fakeAlertmanager{live: nil}
	r := newReconciler(store, client, nil)
	now := time.Now().UTC()

	for pass := 0; pass < 5; pass++ {
		r.reconcileOnce(context.Background(), now)
	}
	for pass, missing := range store.missingCalls {
		if len(missing) != 0 {
			t.Fatalf("empty reading %d resolved %v", pass+1, missing)
		}
	}
}

func TestReconcilerResumesResolvingOnceTheAlertmanagerReportsAgain(t *testing.T) {
	// After a restart the counters must start clean, so an alert absent during
	// the outage is not resolved on the first healthy reading.
	store := &fakeReconcileStore{
		sources: map[string]string{"yul": "http://am:9093"},
		firing:  map[string][]string{"yul": {"a", "gone"}},
	}
	client := &fakeAlertmanager{responses: [][]alertmanager.LiveAlert{
		{},                   // restarting
		{},                   // still restarting
		{{Fingerprint: "a"}}, // back, "gone" genuinely absent
		{{Fingerprint: "a"}}, // second consecutive absence
	}}
	r := newReconciler(store, client, nil)
	now := time.Now().UTC()

	for pass := 0; pass < 3; pass++ {
		r.reconcileOnce(context.Background(), now)
		if len(store.missingCalls[pass]) != 0 {
			t.Fatalf("pass %d resolved %v too early", pass+1, store.missingCalls[pass])
		}
	}
	r.reconcileOnce(context.Background(), now)
	if !store.missingCalls[3]["gone"] {
		t.Fatalf("fourth pass missing = %v, want the genuinely absent alert", store.missingCalls[3])
	}
}

func TestReconcilerLeavesASourceAloneWhenItCannotBeRead(t *testing.T) {
	store := &fakeReconcileStore{
		sources: map[string]string{"yul": "http://am:9093"},
		firing:  map[string][]string{"yul": {"a"}},
	}
	client := &fakeAlertmanager{err: errors.New("connection refused")}
	r := newReconciler(store, client, nil)

	r.reconcileOnce(context.Background(), time.Now().UTC())

	// An unreachable Alertmanager must not touch the source; expiry remains its
	// backstop.
	if len(store.missingCalls) != 0 {
		t.Fatalf("an unreachable source was reconciled: %v", store.missingCalls)
	}
}

func TestReconcilerPassesSuppressionThrough(t *testing.T) {
	store := &fakeReconcileStore{
		sources: map[string]string{"yul": "http://am:9093"},
		firing:  map[string][]string{"yul": {"silenced"}},
	}
	client := &fakeAlertmanager{live: []alertmanager.LiveAlert{{Fingerprint: "silenced", Suppressed: true}}}
	r := newReconciler(store, client, nil)

	r.reconcileOnce(context.Background(), time.Now().UTC())

	if len(store.liveCalls) != 1 || !store.liveCalls[0][0].Suppressed {
		t.Fatalf("suppression did not reach the store: %#v", store.liveCalls)
	}
}

func TestReconcilerSyncsSilencesAndPassesTheActiveIDs(t *testing.T) {
	store := &fakeReconcileStore{
		sources: map[string]string{"yul": "http://am:9093"},
		firing:  map[string][]string{"yul": {"a"}},
	}
	client := &fakeAlertmanager{
		live: []alertmanager.LiveAlert{{Fingerprint: "a"}},
		silences: []alertmanager.ListedSilence{
			{ID: "active-1", State: "active"},
			// A pending preventive silence and an expired one are stored like any
			// other, but neither is suppressing, so neither reaches the release set.
			{ID: "pending-1", State: "pending"},
			{ID: "expired-1", State: "expired"},
		},
	}
	r := newReconciler(store, client, nil)

	r.reconcileOnce(context.Background(), time.Now().UTC())

	if len(store.synced) != 1 || len(store.synced[0]) != 3 {
		t.Fatalf("silences synced = %#v, want one call carrying all three", store.synced)
	}
	want := map[string]bool{"active-1": true}
	if len(store.silenceSets) != 1 || len(store.silenceSets[0]) != 1 || !store.silenceSets[0]["active-1"] {
		t.Fatalf("active silences = %v, want %v", store.silenceSets, want)
	}
}

func TestReconcilerPassesNoSilenceClaimWhenTheListingFails(t *testing.T) {
	store := &fakeReconcileStore{
		sources: map[string]string{"yul": "http://am:9093"},
		firing:  map[string][]string{"yul": {"a"}},
	}
	client := &fakeAlertmanager{
		live:        []alertmanager.LiveAlert{{Fingerprint: "a"}},
		silencesErr: errors.New("connection refused"),
	}
	r := newReconciler(store, client, nil)

	r.reconcileOnce(context.Background(), time.Now().UTC())

	// Nil, not empty: a failed listing makes no claim, where an empty one says
	// nothing is suppressing. The alert half of the pass still ran.
	if len(store.silenceSets) != 1 || store.silenceSets[0] != nil {
		t.Fatalf("active silences = %v, want nil", store.silenceSets)
	}
	if len(store.synced) != 0 {
		t.Fatalf("a failed listing was synced: %#v", store.synced)
	}
}

func TestReconcilerReadsSilencesEvenWhenTheAlertListIsUntrusted(t *testing.T) {
	// The user-visible failure this closes: every alert cleared inside a
	// maintenance window, the Alertmanager now reports no alerts at all, and the
	// empty-list guard rightly refuses to resolve anything. The silence listing
	// is persisted across restarts, so it is still read, still synced, and still
	// allowed to release suppression that nothing is holding any more.
	store := &fakeReconcileStore{
		sources: map[string]string{"yul": "http://am:9093"},
		firing:  map[string][]string{"yul": {"a", "b"}},
	}
	client := &fakeAlertmanager{live: nil, silences: []alertmanager.ListedSilence{}}
	r := newReconciler(store, client, nil)

	r.reconcileOnce(context.Background(), time.Now().UTC())

	if len(store.synced) != 1 {
		t.Fatalf("silences were not synced on an untrusted alert reading: %#v", store.synced)
	}
	if len(store.silenceSets) != 1 || store.silenceSets[0] == nil || len(store.silenceSets[0]) != 0 {
		t.Fatalf("active silences = %#v, want an empty, non-nil set", store.silenceSets)
	}
	if len(store.missingCalls) != 1 || len(store.missingCalls[0]) != 0 {
		t.Fatalf("the untrusted reading resolved alerts: %v", store.missingCalls)
	}
}

func TestRunReconciliationSkipsWhenDisabled(t *testing.T) {
	store := &fakeReconcileStore{}
	done := make(chan struct{})
	go func() {
		runReconciliation(context.Background(), store, &fakeAlertmanager{}, nil, 0)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runReconciliation did not return when disabled")
	}
}
