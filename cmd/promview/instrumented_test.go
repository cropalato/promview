package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/cropalato/promview/internal/alertmanager"
	"github.com/cropalato/promview/internal/metrics"
)

/*
The instrumented wrappers are thin, which is exactly why they were untested:
each one is a single call plus a counter. The counter is the point. Writing a
silence to an Alertmanager is the only thing promview does to a system it does
not own, and a failure there is invisible unless something counts it.
*/

func TestCountingSilencerCountsBothOutcomes(t *testing.T) {
	instruments := metrics.New("test")
	silencer := countingSilencer{inner: stubSilencer{id: "sil-1"}, metrics: instruments}
	ctx := context.Background()

	id, err := silencer.CreateSilence(ctx, "http://am-a:9093", "", alertmanager.Silence{})
	if id != "sil-1" || err != nil {
		t.Fatalf("CreateSilence() = %q, %v; want the inner result passed through", id, err)
	}
	if err := silencer.DeleteSilence(ctx, "http://am-a:9093", "", "sil-1"); err != nil {
		t.Fatalf("DeleteSilence() error = %v", err)
	}

	failing := countingSilencer{inner: stubSilencer{err: errors.New("HTTP 401")}, metrics: instruments}
	if _, err := failing.CreateSilence(ctx, "http://am-b:9093", "", alertmanager.Silence{}); err == nil {
		t.Fatal("CreateSilence() succeeded, want the inner failure surfaced")
	}
	if err := failing.DeleteSilence(ctx, "http://am-b:9093", "", "sil-2"); err == nil {
		t.Fatal("DeleteSilence() succeeded, want the inner failure surfaced")
	}

	// Labelled by Alertmanager rather than by source, and by outcome: a
	// deployment whose silences are all failing against one URL is the thing
	// this has to be able to show.
	expected := `
# HELP promview_silence_writes_total Silences promview tried to create, by Alertmanager and outcome.
# TYPE promview_silence_writes_total counter
promview_silence_writes_total{alertmanager="http://am-a:9093",result="ok"} 1
promview_silence_writes_total{alertmanager="http://am-b:9093",result="error"} 1
`
	if err := testutil.GatherAndCompare(instruments.Gatherer(), strings.NewReader(expected), "promview_silence_writes_total"); err != nil {
		t.Error(err)
	}
}

// Every Metrics method tolerates a nil receiver, so the wrappers must work on a
// deployment that disabled the metrics endpoint rather than panicking on the
// first silence somebody writes.
func TestCountingSilencerWorksWithoutMetrics(t *testing.T) {
	silencer := countingSilencer{inner: stubSilencer{id: "sil-1"}}
	if _, err := silencer.CreateSilence(context.Background(), "http://am:9093", "", alertmanager.Silence{}); err != nil {
		t.Fatalf("CreateSilence() error = %v", err)
	}
	if err := silencer.DeleteSilence(context.Background(), "http://am:9093", "", "sil-1"); err != nil {
		t.Fatalf("DeleteSilence() error = %v", err)
	}
}

// poolSnapshot is pure adaptation - the driver's statistics onto the shape
// internal/metrics asks for - and a field mapped to the wrong getter would
// misreport the pool without anything failing. A pool that has never connected
// still reports its configuration, which is enough to check the wiring.
func TestPoolSnapshotMapsTheDriverStatistics(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), "postgres://promview@127.0.0.1:1/promview?sslmode=disable&pool_max_conns=7")
	if err != nil {
		t.Fatalf("pgxpool.New() error = %v", err)
	}
	defer pool.Close()

	snapshot := poolSnapshot(pool)()
	if snapshot.Max != 7 {
		t.Errorf("Max = %d, want the configured 7", snapshot.Max)
	}
	// Nothing has been acquired, so these are the floor rather than a guess.
	if snapshot.Acquired != 0 {
		t.Errorf("Acquired = %d, want 0 on a pool nobody has used", snapshot.Acquired)
	}
	if snapshot.AcquireWait != 0 {
		t.Errorf("AcquireWait = %v, want zero on a pool nobody has used", snapshot.AcquireWait)
	}
}
