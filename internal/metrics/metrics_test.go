package metrics

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestNilMetricsRecordNothingAndPanicNowhere(t *testing.T) {
	// Every call site should be able to instrument unconditionally. If a nil
	// receiver panicked, instrumenting a code path would mean adding a branch
	// to it, and the branch would be the thing that gets forgotten.
	var m *Metrics
	m.ObserveRequest("GET /api/v1/alerts", "GET", 200, time.Second)
	m.ReconcileSucceeded("demo", time.Unix(1, 0))
	m.ReconcileFailed("demo", ReasonUnreadable)
	m.SilenceWritten("http://am:9093", nil)
	m.SilenceRecorded(errors.New("database is down"))
	if m.Gatherer() != nil {
		t.Error("a nil Metrics offered a gatherer")
	}
}

func TestBuildInfoNamesTheRunningBinary(t *testing.T) {
	m := New("0.1.0-alpha.31")
	expected := `
# HELP promview_build_info Always 1, labelled with the version of the running binary.
# TYPE promview_build_info gauge
promview_build_info{version="0.1.0-alpha.31"} 1
`
	if err := testutil.GatherAndCompare(m.Gatherer(), strings.NewReader(expected), "promview_build_info"); err != nil {
		t.Error(err)
	}
}

func TestReconcileSuccessRecordsWhenItLastWorked(t *testing.T) {
	m := New("test")
	m.ReconcileSucceeded("demo", time.Unix(1_700_000_000, 0))

	// The timestamp is the point: a loop that stops produces no errors at all,
	// so only its silence is evidence. A counter alone cannot express that.
	expected := `
# HELP promview_reconcile_last_success_timestamp_seconds When each source last reconciled successfully, in seconds since the epoch.
# TYPE promview_reconcile_last_success_timestamp_seconds gauge
promview_reconcile_last_success_timestamp_seconds{source="demo"} 1.7e+09
`
	if err := testutil.GatherAndCompare(m.Gatherer(), strings.NewReader(expected),
		"promview_reconcile_last_success_timestamp_seconds"); err != nil {
		t.Error(err)
	}
	if got := testutil.ToFloat64(m.reconcileRuns.WithLabelValues("demo", "ok")); got != 1 {
		t.Errorf("ok runs = %v, want 1", got)
	}
}

func TestFailuresAreCountedApartFromEachOther(t *testing.T) {
	m := New("test")
	m.ReconcileFailed("demo", ReasonUnreadable)
	m.ReconcileFailed("demo", ReasonUnreadable)
	m.ReconcileFailed("demo", ReasonError)

	// "The Alertmanager is unreachable" and "the database rejected the write"
	// want different responses from whoever is paged, so they cannot share a
	// counter.
	if got := testutil.ToFloat64(m.reconcileRuns.WithLabelValues("demo", ReasonUnreadable)); got != 2 {
		t.Errorf("unreadable = %v, want 2", got)
	}
	if got := testutil.ToFloat64(m.reconcileRuns.WithLabelValues("demo", ReasonError)); got != 1 {
		t.Errorf("error = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.reconcileLastSuccess.WithLabelValues("demo")); got != 0 {
		t.Errorf("last success = %v; a failing pass must not look like a healthy one", got)
	}
}

func TestSilenceOutcomesAreSplitBySuccess(t *testing.T) {
	m := New("test")
	m.SilenceWritten("http://am-a:9093", nil)
	m.SilenceWritten("http://am-b:9093", errors.New("HTTP 401"))
	m.SilenceRecorded(nil)
	m.SilenceRecorded(errors.New("relation does not exist"))

	if got := testutil.ToFloat64(m.silenceWrites.WithLabelValues("http://am-a:9093", "ok")); got != 1 {
		t.Errorf("accepted writes = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.silenceWrites.WithLabelValues("http://am-b:9093", "error")); got != 1 {
		t.Errorf("refused writes = %v, want 1", got)
	}
	// A provenance write that fails does not fail the silence, so this counter
	// is the only place its failure is ever visible.
	if got := testutil.ToFloat64(m.silenceRecords.WithLabelValues("error")); got != 1 {
		t.Errorf("failed records = %v, want 1", got)
	}
}

func TestTheHandlerServesWhatWasRecorded(t *testing.T) {
	m := New("test")
	m.ObserveRequest("GET /api/v1/alerts", "GET", 500, 250*time.Millisecond)

	response := httptest.NewRecorder()
	m.Handler().ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
	if response.Code != 200 {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	body := response.Body.String()
	for _, want := range []string{
		`promview_http_requests_total{code="500",method="GET",route="GET /api/v1/alerts"} 1`,
		"promview_http_request_duration_seconds_bucket",
		"go_goroutines",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape does not contain %q", want)
		}
	}
}

func TestStreamClientsReturnToZero(t *testing.T) {
	m := New("test")
	m.StreamOpened()
	m.StreamOpened()
	if got := testutil.ToFloat64(m.streamClients); got != 2 {
		t.Fatalf("clients = %v, want 2", got)
	}
	m.StreamClosed()
	m.StreamClosed()
	// A gauge that only ever rises reads as connections nobody closed, which is
	// indistinguishable from a leak in the thing being measured.
	if got := testutil.ToFloat64(m.streamClients); got != 0 {
		t.Errorf("clients = %v after every client left, want 0", got)
	}
}

func TestPollsAndEventsAdvanceIndependently(t *testing.T) {
	m := New("test")
	m.StreamPolled(0)
	m.StreamPolled(0)
	m.StreamPolled(3)

	// The ratio between these is the point: polling that almost never finds
	// anything is continuous work being paid for, and only both numbers
	// together say so.
	if got := testutil.ToFloat64(m.streamPolls); got != 3 {
		t.Errorf("polls = %v, want 3", got)
	}
	if got := testutil.ToFloat64(m.streamEventsSent); got != 3 {
		t.Errorf("events = %v, want 3", got)
	}
}

func TestPoolIsSampledAtScrapeRatherThanCached(t *testing.T) {
	m := New("test")
	current := PoolSnapshot{Acquired: 2, Idle: 3, Total: 5, Max: 10, EmptyAcquires: 7, AcquireWait: 250 * time.Millisecond}
	m.WatchPool(func() PoolSnapshot { return current })

	expected := `
# HELP promview_db_connections Database pool connections, by state.
# TYPE promview_db_connections gauge
promview_db_connections{state="acquired"} 2
promview_db_connections{state="idle"} 3
promview_db_connections{state="max"} 10
promview_db_connections{state="total"} 5
`
	if err := testutil.GatherAndCompare(m.Gatherer(), strings.NewReader(expected), "promview_db_connections"); err != nil {
		t.Fatal(err)
	}

	// Read on collection, so a scrape reflects the pool now rather than
	// whenever some background timer last happened to look.
	current.Acquired = 9
	expected = `
# HELP promview_db_connections Database pool connections, by state.
# TYPE promview_db_connections gauge
promview_db_connections{state="acquired"} 9
promview_db_connections{state="idle"} 3
promview_db_connections{state="max"} 10
promview_db_connections{state="total"} 5
`
	if err := testutil.GatherAndCompare(m.Gatherer(), strings.NewReader(expected), "promview_db_connections"); err != nil {
		t.Error(err)
	}
}

func TestPoolSaturationIsReported(t *testing.T) {
	m := New("test")
	m.WatchPool(func() PoolSnapshot {
		return PoolSnapshot{EmptyAcquires: 12, AcquireWait: 1500 * time.Millisecond}
	})
	// A pool under pressure answers slowly long before it answers with an
	// error, so waiting is the signal worth having.
	expected := `
# HELP promview_db_acquire_waits_total Acquisitions that had to wait for a free connection.
# TYPE promview_db_acquire_waits_total counter
promview_db_acquire_waits_total 12
# HELP promview_db_acquire_duration_seconds_total Cumulative time spent waiting to acquire a connection.
# TYPE promview_db_acquire_duration_seconds_total counter
promview_db_acquire_duration_seconds_total 1.5
`
	if err := testutil.GatherAndCompare(m.Gatherer(), strings.NewReader(expected),
		"promview_db_acquire_waits_total", "promview_db_acquire_duration_seconds_total"); err != nil {
		t.Error(err)
	}
}

func TestWatchPoolIgnoresAMissingSource(t *testing.T) {
	m := New("test")
	m.WatchPool(nil)
	var nilMetrics *Metrics
	nilMetrics.WatchPool(func() PoolSnapshot { return PoolSnapshot{} })
}
