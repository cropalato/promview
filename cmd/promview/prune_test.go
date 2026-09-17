package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// The retention sweep deletes stream events past the window. It is the same
// shape of loop as expiry - a ticker, a store call, and a failure that must not
// end it - and it was the one with no test.
type recordingPruneStore struct {
	mu        sync.Mutex
	windows   []time.Duration
	pruned    int
	err       error
	triggered chan struct{}
}

func (store *recordingPruneStore) PruneStreamEvents(_ context.Context, retention time.Duration, _ time.Time) (int, error) {
	store.mu.Lock()
	store.windows = append(store.windows, retention)
	store.mu.Unlock()
	select {
	case store.triggered <- struct{}{}:
	default:
	}
	return store.pruned, store.err
}

func (store *recordingPruneStore) callCount() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return len(store.windows)
}

// Zero is the documented escape hatch: keep every event forever rather than
// ever ask a client to re-snapshot. It has to actually stop the loop, not run
// it with a zero window.
func TestRunStreamPruningSkipsWhenDisabled(t *testing.T) {
	store := &recordingPruneStore{triggered: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		runStreamPruning(ctx, store, nil, 0, time.Millisecond)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runStreamPruning did not return with retention disabled")
	}
	if got := store.callCount(); got != 0 {
		t.Fatalf("prune calls = %d, want 0 when retention is zero", got)
	}
}

func TestRunStreamPruningKeepsSweepingAfterFailure(t *testing.T) {
	store := &recordingPruneStore{err: errors.New("boom"), triggered: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		runStreamPruning(ctx, store, nil, 24*time.Hour, time.Millisecond)
		close(done)
	}()

	// A failed prune must not end the loop. Stream events would then grow
	// without bound while the server looked healthy, which is the failure the
	// retention window exists to prevent.
	for i := 0; i < 2; i++ {
		select {
		case <-store.triggered:
		case <-time.After(2 * time.Second):
			t.Fatalf("prune %d did not run after a failure", i+1)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runStreamPruning did not return after cancellation")
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.windows) == 0 {
		t.Fatal("no prune ran")
	}
	for _, window := range store.windows {
		if window != 24*time.Hour {
			t.Fatalf("prune window = %v, want the configured 24h", window)
		}
	}
}

// The metrics handle is nil in these tests on purpose: every Metrics method
// tolerates a nil receiver, and a loop that panicked without one would take the
// server down on a deployment that disabled metrics.
func TestRunStreamPruningCountsWithoutMetrics(t *testing.T) {
	store := &recordingPruneStore{pruned: 7, triggered: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		runStreamPruning(ctx, store, nil, time.Hour, time.Millisecond)
		close(done)
	}()
	select {
	case <-store.triggered:
	case <-time.After(2 * time.Second):
		t.Fatal("prune did not run")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runStreamPruning did not return after cancellation")
	}
}
