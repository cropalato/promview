package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type recordingExpiryStore struct {
	mu        sync.Mutex
	calls     []time.Duration
	err       error
	expired   int
	triggered chan struct{}
}

func (store *recordingExpiryStore) ExpireStaleAlerts(_ context.Context, defaultStaleAfter time.Duration, _ time.Time) (int, error) {
	store.mu.Lock()
	store.calls = append(store.calls, defaultStaleAfter)
	store.mu.Unlock()
	select {
	case store.triggered <- struct{}{}:
	default:
	}
	return store.expired, store.err
}

func (store *recordingExpiryStore) callCount() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return len(store.calls)
}

func TestRunExpirySweepsSkipsWhenDisabled(t *testing.T) {
	store := &recordingExpiryStore{triggered: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		runExpirySweeps(ctx, store, 0, time.Millisecond)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runExpirySweeps did not return with expiry disabled")
	}
	if got := store.callCount(); got != 0 {
		t.Fatalf("sweep calls = %d, want 0 when the window is zero", got)
	}
}

func TestRunExpirySweepsKeepsSweepingAfterFailure(t *testing.T) {
	store := &recordingExpiryStore{err: errors.New("boom"), triggered: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		runExpirySweeps(ctx, store, 12*time.Hour, time.Millisecond)
		close(done)
	}()

	// A failing sweep must not end the loop; the console keeps serving and the
	// next tick retries.
	for i := 0; i < 2; i++ {
		select {
		case <-store.triggered:
		case <-time.After(2 * time.Second):
			t.Fatalf("sweep %d did not run after a failure", i+1)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runExpirySweeps did not return after cancellation")
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	for _, window := range store.calls {
		if window != 12*time.Hour {
			t.Fatalf("sweep window = %v, want the configured 12h", window)
		}
	}
}
