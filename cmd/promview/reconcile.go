package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/cropalato/promview/internal/alertmanager"
	"github.com/cropalato/promview/internal/metrics"
	"github.com/cropalato/promview/internal/postgres"
)

/*
The reconciliation loop asks each source's Alertmanager what it still holds and
brings promview in line. It is the precise half of the staleness story: the TTL
sweep infers an ending from silence after hours, this confirms one in seconds.

An alert is only resolved after it has been missing from consecutive readings.
A restarting Alertmanager briefly holds nothing at all, and a single empty
reading would otherwise resolve every alert the source has - a far worse failure
than the staleness this exists to fix.
*/

// missesBeforeResolving is how many consecutive readings must omit an alert
// before promview believes it. Two is enough to survive a restart or a dropped
// request while still confirming within a couple of intervals.
const missesBeforeResolving = 2

type reconcileStore interface {
	ReconcilableSources(ctx context.Context) (map[string]string, error)
	FiringFingerprints(ctx context.Context, sourceSlug string) ([]string, error)
	ReconcileSource(ctx context.Context, sourceSlug string, live []alertmanager.LiveAlert, missing map[string]bool, now time.Time) (postgres.ReconcileResult, error)
}

type alertmanagerReader interface {
	LiveAlerts(ctx context.Context, baseURL string) ([]alertmanager.LiveAlert, error)
}

// reconciler carries the miss counters between passes, which is what makes the
// consecutive-readings rule possible. Counters live in memory deliberately:
// after a promview restart the rule simply starts over, costing one extra
// interval rather than risking a wrong resolution.
type reconciler struct {
	store   reconcileStore
	client  alertmanagerReader
	metrics *metrics.Metrics
	misses  map[string]map[string]int
}

func newReconciler(store reconcileStore, client alertmanagerReader, instruments *metrics.Metrics) *reconciler {
	return &reconciler{
		store:   store,
		client:  client,
		metrics: instruments,
		misses:  map[string]map[string]int{},
	}
}

func (r *reconciler) reconcileOnce(ctx context.Context, now time.Time) {
	sources, err := r.store.ReconcilableSources(ctx)
	if err != nil {
		if ctx.Err() == nil {
			slog.Error("alert reconciliation failed to list sources", "error", err)
		}
		return
	}
	for slug := range r.misses {
		if _, ok := sources[slug]; !ok {
			delete(r.misses, slug)
		}
	}
	for slug, baseURL := range sources {
		r.reconcileSource(ctx, slug, baseURL, now)
	}
}

func (r *reconciler) reconcileSource(ctx context.Context, slug, baseURL string, now time.Time) {
	live, err := r.client.LiveAlerts(ctx, baseURL)
	if err != nil {
		if ctx.Err() == nil {
			// A source that cannot be read is left exactly as it is; the TTL
			// sweep remains the backstop for it.
			slog.Warn("alert reconciliation could not read alertmanager", "source", slug, "error", err)
		}
		r.metrics.ReconcileFailed(slug, metrics.ReasonUnreadable)
		return
	}
	stored, err := r.store.FiringFingerprints(ctx, slug)
	if err != nil {
		if ctx.Err() == nil {
			slog.Error("alert reconciliation failed to read stored alerts", "source", slug, "error", err)
		}
		r.metrics.ReconcileFailed(slug, metrics.ReasonError)
		return
	}

	present := make(map[string]bool, len(live))
	for _, alert := range live {
		present[alert.Fingerprint] = true
	}

	counters, ok := r.misses[slug]
	if !ok {
		counters = map[string]int{}
		r.misses[slug] = counters
	}

	// An Alertmanager holding nothing while promview holds firing alerts is far
	// more likely to be one that just restarted than a fleet that went quiet all
	// at once. Resolving on that reading would wipe the console, so this pass
	// only syncs suppression and leaves the ending to a reading that shows
	// something - or, failing that, to the expiry sweep.
	trustworthy := len(live) > 0 || len(stored) == 0
	if !trustworthy {
		slog.Warn("alertmanager reported no alerts while promview holds firing ones; not resolving",
			"source", slug, "firing", len(stored))
		// Suppression still syncs below, but nothing is allowed to end on this
		// reading. Counting it apart from a clean pass is what makes a source
		// stuck in this state visible rather than merely quiet.
		r.metrics.ReconcileFailed(slug, metrics.ReasonUntrusted)
		for fingerprint := range counters {
			delete(counters, fingerprint)
		}
	}

	missing := make(map[string]bool)
	if trustworthy {
		for _, fingerprint := range stored {
			if present[fingerprint] {
				delete(counters, fingerprint)
				continue
			}
			counters[fingerprint]++
			if counters[fingerprint] >= missesBeforeResolving {
				missing[fingerprint] = true
			}
		}
	}
	// Counters for alerts promview no longer holds are dead weight.
	storedSet := make(map[string]bool, len(stored))
	for _, fingerprint := range stored {
		storedSet[fingerprint] = true
	}
	for fingerprint := range counters {
		if !storedSet[fingerprint] {
			delete(counters, fingerprint)
		}
	}

	result, err := r.store.ReconcileSource(ctx, slug, live, missing, now)
	if err != nil {
		if ctx.Err() == nil {
			slog.Error("alert reconciliation failed", "source", slug, "error", err)
		}
		r.metrics.ReconcileFailed(slug, metrics.ReasonError)
		return
	}
	if trustworthy {
		// Only a pass that could have resolved something counts as success. A
		// suppression-only pass leaves endings undetected, and a timestamp that
		// moved anyway would report health this loop did not deliver.
		r.metrics.ReconcileSucceeded(slug, now)
	}
	for fingerprint := range missing {
		delete(counters, fingerprint)
	}
	if result.Resolved > 0 || result.Suppressed > 0 || result.Released > 0 {
		slog.Info("reconciled alerts with alertmanager",
			"source", slug,
			"resolved", result.Resolved,
			"suppressed", result.Suppressed,
			"released", result.Released,
		)
	}
}

// runReconciliation reconciles every source on a ticker until the context is
// cancelled. A zero interval disables it, leaving expiry as the only backstop.
func runReconciliation(
	ctx context.Context,
	store reconcileStore,
	client alertmanagerReader,
	instruments *metrics.Metrics,
	interval time.Duration,
) {
	if interval == 0 {
		slog.Info("alert reconciliation disabled", "reason", "PROMVIEW_RECONCILE_INTERVAL is zero")
		return
	}
	r := newReconciler(store, client, instruments)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.reconcileOnce(ctx, time.Now().UTC())
		}
	}
}
