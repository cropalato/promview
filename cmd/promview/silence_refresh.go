package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/cropalato/promview/internal/alertmanager"
)

/*
Re-reading a source right after promview writes a silence to it.

Without this the console shows nothing for up to a whole reconcile interval: the
alert is silenced at the Alertmanager, and promview still lists it as plainly
firing because the only thing that learns otherwise is the ticker. An operator
who has just silenced something and sees no change assumes it did not work.

Two properties matter more than speed here:

  - This pass never resolves anything. The ordinary pass concludes an alert has
    ended only after consecutive readings omit it; an extra out-of-band reading
    would advance that count on a schedule the operator's silence had nothing to
    do with, and could resolve an alert an interval early. Passing no missing
    set makes that structurally impossible rather than merely unlikely.
  - It does not touch the miss counters, which belong to the ticker's goroutine
    alone. That is what lets this run concurrently without sharing state.
*/

// refreshDelays are the waits before each read, one after the other.
//
// Measured against Alertmanager 0.27, a silence is reflected in what
// /api/v2/alerts serves within about 50ms of being accepted, so the first read
// is a short pause rather than a real settle. The second exists because that
// timing is an observation about one version, not a guarantee any version
// makes: it costs one request that writes nothing when the first already saw
// the change, and it is far cheaper than an operator staring at an alert that
// still looks unsilenced. Past that, the reconcile ticker is the backstop.
var refreshDelays = []time.Duration{250 * time.Millisecond, 3 * time.Second}

// silenceRefresher re-reads sources shortly after a silence is written to them.
type silenceRefresher struct {
	store  reconcileStore
	client alertmanagerReader
	// requests is buffered and never blocks a caller: a refresh is an
	// optimisation, and an HTTP handler must not wait on one. A full buffer
	// means passes are already queued, and the ticker covers anything dropped.
	requests chan string

	mu       sync.Mutex
	inFlight map[string]bool
}

func newSilenceRefresher(store reconcileStore, client alertmanagerReader) *silenceRefresher {
	return &silenceRefresher{
		store:    store,
		client:   client,
		requests: make(chan string, 64),
		inFlight: map[string]bool{},
	}
}

// refreshURL queues a re-read of every source served by this Alertmanager. The
// caller knows the URL it wrote to rather than the promview source behind it,
// and two sources may share one Alertmanager, so the mapping is resolved here.
func (refresher *silenceRefresher) refreshURL(ctx context.Context, baseURL string) {
	if baseURL == "" {
		return
	}
	sources, err := refresher.store.ReconcilableSources(ctx)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("could not resolve the source behind a silenced alertmanager", "error", err)
		}
		return
	}
	for slug, url := range sources {
		if url != baseURL {
			continue
		}
		select {
		case refresher.requests <- slug:
		default:
		}
	}
}

// run handles queued refreshes until the context is cancelled. Each source is
// refreshed in its own goroutine so one slow Alertmanager cannot hold up the
// rest, and at most one refresh runs per source at a time: a second silence
// written while the first is still settling is covered by the same reads.
func (refresher *silenceRefresher) run(ctx context.Context) {
	var running sync.WaitGroup
	defer running.Wait()
	for {
		select {
		case <-ctx.Done():
			return
		case slug := <-refresher.requests:
			if !refresher.claim(slug) {
				continue
			}
			running.Add(1)
			go func() {
				defer running.Done()
				defer refresher.release(slug)
				refresher.syncSuppression(ctx, slug)
			}()
		}
	}
}

func (refresher *silenceRefresher) claim(slug string) bool {
	refresher.mu.Lock()
	defer refresher.mu.Unlock()
	if refresher.inFlight[slug] {
		return false
	}
	refresher.inFlight[slug] = true
	return true
}

func (refresher *silenceRefresher) release(slug string) {
	refresher.mu.Lock()
	defer refresher.mu.Unlock()
	delete(refresher.inFlight, slug)
}

// syncSuppression walks the delays, bringing suppression in line with the
// source each time. Every step runs even after one reports a change: a silence
// covering several alerts need not reach all of them in the same instant, and a
// later step costs one read that writes nothing when there is nothing left to
// change.
func (refresher *silenceRefresher) syncSuppression(ctx context.Context, slug string) {
	for _, delay := range refreshDelays {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}

		sources, err := refresher.store.ReconcilableSources(ctx)
		if err != nil {
			if ctx.Err() == nil {
				slog.Warn("silence refresh could not list sources", "source", slug, "error", err)
			}
			return
		}
		baseURL, ok := sources[slug]
		if !ok {
			// Disabled or unconfigured since the silence was written; the
			// ticker does not read it either.
			return
		}
		live, err := refresher.client.LiveAlerts(ctx, baseURL)
		if err != nil {
			if ctx.Err() == nil {
				slog.Warn("silence refresh could not read alertmanager", "source", slug, "error", err)
			}
			continue
		}
		// No missing set: this pass syncs suppression and must never conclude
		// that an alert has ended.
		result, err := refresher.store.ReconcileSource(ctx, slug, live, nil, time.Now().UTC())
		if err != nil {
			if ctx.Err() == nil {
				slog.Error("silence refresh failed", "source", slug, "error", err)
			}
			continue
		}
		if result.Suppressed > 0 || result.Released > 0 {
			slog.Info("refreshed suppression after a silence",
				"source", slug,
				"suppressed", result.Suppressed,
				"released", result.Released,
			)
		}
	}
}

// refreshingSilencer writes a silence and then asks for the source it landed on
// to be re-read.
//
// The trigger sits here rather than in the API because this is the only place
// that knows a silence actually reached an Alertmanager: the handler sees a
// scope and a result list, and wiring the refresh through it would put a
// background loop's lifetime inside a request handler.
type refreshingSilencer struct {
	inner     httpSilencer
	refresher *silenceRefresher
	// ctx bounds the refresh to the process, not to the request that triggered
	// it. A request context is cancelled as soon as the response is written,
	// which is before the first read would even happen.
	ctx context.Context
}

// httpSilencer is what internal/httpapi asks for, restated so this decorator
// does not depend on the transport package.
type httpSilencer interface {
	CreateSilence(ctx context.Context, baseURL string, token string, silence alertmanager.Silence) (string, error)
}

func (silencer refreshingSilencer) CreateSilence(
	ctx context.Context,
	baseURL string,
	token string,
	silence alertmanager.Silence,
) (string, error) {
	id, err := silencer.inner.CreateSilence(ctx, baseURL, token, silence)
	if err != nil {
		return "", err
	}
	silencer.refresher.refreshURL(silencer.ctx, baseURL)
	return id, nil
}
