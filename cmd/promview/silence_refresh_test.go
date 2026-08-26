package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cropalato/promview/internal/alertmanager"
	"github.com/cropalato/promview/internal/postgres"
)

// The refresher reads from its own goroutines, so its fakes record under a
// mutex rather than reusing the ticker's single-threaded ones.
type refreshStore struct {
	mu       sync.Mutex
	sources  map[string]string
	calls    []string
	missing  []map[string]bool
	err      error
	released int
}

func (store *refreshStore) ReconcilableSources(context.Context) (map[string]string, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.err != nil {
		return nil, store.err
	}
	copied := make(map[string]string, len(store.sources))
	for slug, url := range store.sources {
		copied[slug] = url
	}
	return copied, nil
}

func (store *refreshStore) FiringFingerprints(context.Context, string) ([]string, error) {
	return nil, nil
}

func (store *refreshStore) ReconcileSource(
	_ context.Context,
	slug string,
	_ []alertmanager.LiveAlert,
	missing map[string]bool,
	_ time.Time,
) (postgres.ReconcileResult, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.calls = append(store.calls, slug)
	store.missing = append(store.missing, missing)
	return postgres.ReconcileResult{Suppressed: 1, Released: store.released}, nil
}

func (store *refreshStore) recorded() ([]string, []map[string]bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]string(nil), store.calls...), append([]map[string]bool(nil), store.missing...)
}

type refreshClient struct {
	mu    sync.Mutex
	reads []string
	err   error
}

func (client *refreshClient) LiveAlerts(_ context.Context, baseURL string) ([]alertmanager.LiveAlert, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.reads = append(client.reads, baseURL)
	return []alertmanager.LiveAlert{{Fingerprint: "a", Suppressed: true, SilencedBy: []string{"sil-1"}}}, client.err
}

func (client *refreshClient) count() int {
	client.mu.Lock()
	defer client.mu.Unlock()
	return len(client.reads)
}

// withFastDelays collapses the settle waits so a test does not sit through the
// real ones. The number of reads is what the tests care about, not their spacing.
func withFastDelays(t *testing.T, steps int) {
	t.Helper()
	original := refreshDelays
	fast := make([]time.Duration, steps)
	for i := range fast {
		// Small, but far enough above the cost of draining the request queue
		// that a coalescing test is measuring coalescing and not scheduling.
		fast[i] = 5 * time.Millisecond
	}
	refreshDelays = fast
	t.Cleanup(func() { refreshDelays = original })
}

// waitFor polls until the condition holds, so a test never depends on how long
// a background goroutine takes to get there.
func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestSilenceRefreshSyncsSuppressionWithoutResolvingAnything(t *testing.T) {
	withFastDelays(t, 2)
	store := &refreshStore{sources: map[string]string{"demo": "http://am-a:9093"}}
	client := &refreshClient{}
	refresher := newSilenceRefresher(store, client, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go refresher.run(ctx)

	refresher.refreshURL(ctx, "http://am-a:9093")

	waitFor(t, "both reads", func() bool {
		calls, _ := store.recorded()
		return len(calls) == 2
	})
	_, missing := store.recorded()
	// The ordinary pass resolves an alert only after consecutive readings omit
	// it. An out-of-band reading that carried a missing set would advance that
	// count on its own schedule and could end an alert an interval early.
	for _, set := range missing {
		if len(set) != 0 {
			t.Errorf("refresh passed missing = %v; it must never resolve anything", set)
		}
	}
}

func TestSilenceRefreshCoversEverySourceBehindOneAlertmanager(t *testing.T) {
	withFastDelays(t, 1)
	store := &refreshStore{sources: map[string]string{
		"demo":  "http://am-a:9093",
		"twin":  "http://am-a:9093",
		"other": "http://am-b:9093",
	}}
	refresher := newSilenceRefresher(store, &refreshClient{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go refresher.run(ctx)

	refresher.refreshURL(ctx, "http://am-a:9093")

	// The caller knows the URL it wrote to, not the promview sources behind it,
	// and a shared Alertmanager suppresses both of theirs at once.
	waitFor(t, "both sources on that alertmanager", func() bool {
		calls, _ := store.recorded()
		return len(calls) == 2
	})
	calls, _ := store.recorded()
	seen := map[string]bool{}
	for _, slug := range calls {
		seen[slug] = true
	}
	if !seen["demo"] || !seen["twin"] || seen["other"] {
		t.Errorf("refreshed %v, want exactly the sources on am-a", calls)
	}
}

func TestSilenceRefreshRunsOneRefreshPerSourceAtATime(t *testing.T) {
	withFastDelays(t, 3)
	store := &refreshStore{sources: map[string]string{"demo": "http://am-a:9093"}}
	client := &refreshClient{}
	refresher := newSilenceRefresher(store, client, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go refresher.run(ctx)

	// Silencing several groups at once writes several silences to one
	// Alertmanager. They settle together, so they need one refresh, not five.
	for range 5 {
		refresher.refreshURL(ctx, "http://am-a:9093")
	}

	waitFor(t, "the refresh to finish", func() bool {
		calls, _ := store.recorded()
		return len(calls) >= 3
	})
	time.Sleep(20 * time.Millisecond)
	if got := client.count(); got > 3 {
		t.Errorf("alertmanager read %d times, want at most one refresh of 3", got)
	}
}

func TestSilenceRefreshSurvivesAnUnreadableAlertmanager(t *testing.T) {
	withFastDelays(t, 2)
	store := &refreshStore{sources: map[string]string{"demo": "http://am-a:9093"}}
	client := &refreshClient{err: errors.New("connection refused")}
	refresher := newSilenceRefresher(store, client, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go refresher.run(ctx)

	refresher.refreshURL(ctx, "http://am-a:9093")

	// A failed read leaves the source exactly as it is and tries the next step;
	// the ticker remains the backstop.
	waitFor(t, "both attempts", func() bool { return client.count() == 2 })
	if calls, _ := store.recorded(); len(calls) != 0 {
		t.Errorf("wrote %v despite reading nothing", calls)
	}
}

func TestSilenceRefreshIgnoresASourceItCannotReach(t *testing.T) {
	withFastDelays(t, 1)
	store := &refreshStore{sources: map[string]string{"demo": "http://am-a:9093"}}
	client := &refreshClient{}
	refresher := newSilenceRefresher(store, client, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go refresher.run(ctx)

	refresher.refreshURL(ctx, "http://am-unknown:9093")
	refresher.refreshURL(ctx, "")

	time.Sleep(20 * time.Millisecond)
	if client.count() != 0 {
		t.Errorf("read an alertmanager %d times for a URL no source carries", client.count())
	}
}

func TestRefreshingSilencerOnlyRefreshesAfterAWriteThatLanded(t *testing.T) {
	withFastDelays(t, 1)
	store := &refreshStore{sources: map[string]string{"demo": "http://am-a:9093"}}
	client := &refreshClient{}
	refresher := newSilenceRefresher(store, client, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go refresher.run(ctx)

	failing := refreshingSilencer{inner: stubSilencer{err: errors.New("HTTP 401")}, refresher: refresher, ctx: ctx}
	if _, err := failing.CreateSilence(ctx, "http://am-a:9093", "", alertmanager.Silence{}); err == nil {
		t.Fatal("CreateSilence() succeeded, want the inner failure surfaced")
	}
	time.Sleep(20 * time.Millisecond)
	// Nothing changed at the Alertmanager, so there is nothing to read back.
	if client.count() != 0 {
		t.Errorf("refreshed %d times after a silence that never landed", client.count())
	}

	ok := refreshingSilencer{inner: stubSilencer{id: "sil-1"}, refresher: refresher, ctx: ctx}
	id, err := ok.CreateSilence(ctx, "http://am-a:9093", "", alertmanager.Silence{})
	if err != nil || id != "sil-1" {
		t.Fatalf("CreateSilence() = %q, %v; want the id the alertmanager assigned", id, err)
	}
	waitFor(t, "the refresh a successful write triggers", func() bool { return client.count() == 1 })
}

// A refreshingSilencer must not wait on the refresh it triggers: the response
// to the operator is not owed to a background read.
func TestRefreshingSilencerDoesNotBlockOnTheRefresh(t *testing.T) {
	withFastDelays(t, 1)
	store := &refreshStore{sources: map[string]string{"demo": "http://am-a:9093"}}
	refresher := newSilenceRefresher(store, &refreshClient{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Deliberately not running the refresher: a queued request nobody is
	// reading must still return immediately.
	silencer := refreshingSilencer{inner: stubSilencer{id: "sil-1"}, refresher: refresher, ctx: ctx}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 200 {
			if _, err := silencer.CreateSilence(ctx, "http://am-a:9093", "", alertmanager.Silence{}); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("CreateSilence blocked on a refresh nobody was draining")
	}
}

type stubSilencer struct {
	id  string
	err error
}

func (silencer stubSilencer) CreateSilence(
	context.Context, string, string, alertmanager.Silence,
) (string, error) {
	return silencer.id, silencer.err
}
