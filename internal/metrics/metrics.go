// Package metrics is what promview reports about itself.
//
// Promview is the thing an operator looks at when something else breaks, which
// makes its own failures the easiest ones to miss: when it is down, the console
// that would show the problem is the console that is down. It has already
// happened - a schema a migration behind turned every read into a 500, and the
// first person to notice was someone trying to silence an alert, long after the
// desktop client had started failing its polls in silence.
//
// So this covers the failures that produce no user-visible symptom until much
// later, rather than everything that could be counted. Alert counts are
// deliberately absent: Prometheus already knows what is firing, and re-exporting
// it here would mostly be a way to disagree with the source of truth.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Reasons a reconciliation pass did not complete. They are a closed set because
// they are label values: an operator needs to tell "the Alertmanager is
// unreachable" from "the database rejected the write" without reading logs, and
// anything finer belongs in the log line that accompanies it.
const (
	ReasonUnreadable = "unreadable"
	ReasonError      = "error"
	ReasonUntrusted  = "untrusted"
)

// Metrics is promview's own instrumentation.
//
// Every method is safe to call on a nil receiver, so a caller that was not
// given a registry does not have to keep checking whether it was. Instrumenting
// a code path should never be the reason it needs a branch.
type Metrics struct {
	registry *prometheus.Registry

	httpRequests *prometheus.CounterVec
	httpDuration *prometheus.HistogramVec

	reconcileRuns        *prometheus.CounterVec
	reconcileLastSuccess *prometheus.GaugeVec

	silenceWrites  *prometheus.CounterVec
	silenceRecords *prometheus.CounterVec
}

// New builds the collectors on a registry of their own.
//
// Not the default global registry: what promview reports should be what
// promview chose to report, and a dependency that registers a collector into
// the global one should not be able to change that by being linked in.
func New(version string) *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(),
		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "promview_http_requests_total",
			Help: "Requests served, by matched route, method and status code.",
		}, []string{"route", "method", "code"}),
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "promview_http_request_duration_seconds",
			Help:    "Time to serve a request, by matched route and method.",
			Buckets: prometheus.DefBuckets,
		}, []string{"route", "method"}),
		reconcileRuns: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "promview_reconcile_runs_total",
			Help: "Reconciliation passes per source, by outcome.",
		}, []string{"source", "result"}),
		reconcileLastSuccess: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			// The counter says reconciliation is failing; this says it stopped.
			// A loop that quietly gives up produces no errors at all, and the
			// only symptom is alerts that ended hours ago still sitting in the
			// console - which nobody reads as a promview fault.
			Name: "promview_reconcile_last_success_timestamp_seconds",
			Help: "When each source last reconciled successfully, in seconds since the epoch.",
		}, []string{"source"}),
		silenceWrites: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "promview_silence_writes_total",
			Help: "Silences promview tried to create, by Alertmanager and outcome.",
			// Labelled by Alertmanager rather than by promview source: one
			// Alertmanager can serve several sources, and the code that writes
			// the silence knows the URL it wrote to, not the slug behind it.
		}, []string{"alertmanager", "result"}),
		silenceRecords: prometheus.NewCounterVec(prometheus.CounterOpts{
			// A failure here does not fail the silence, which is the point: the
			// silence is already real at the Alertmanager. What is lost is the
			// record of who asked for it, and losing that quietly is how a
			// silence becomes unexplainable months later.
			Name: "promview_silence_records_total",
			Help: "Attempts to record a created silence's provenance, by outcome.",
		}, []string{"result"}),
	}

	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "promview_build_info",
		Help: "Always 1, labelled with the version of the running binary.",
	}, []string{"version"})
	buildInfo.WithLabelValues(version).Set(1)

	m.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.httpRequests,
		m.httpDuration,
		m.reconcileRuns,
		m.reconcileLastSuccess,
		m.silenceWrites,
		m.silenceRecords,
		buildInfo,
	)
	return m
}

// Handler serves the registry.
func (m *Metrics) Handler() http.Handler {
	if m == nil {
		return http.NotFoundHandler()
	}
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{Registry: m.registry})
}

// Gatherer exposes the registry for tests.
func (m *Metrics) Gatherer() prometheus.Gatherer {
	if m == nil {
		return nil
	}
	return m.registry
}

// ObserveRequest records one served request. Route is the pattern that matched,
// never the path that arrived: a path carries alert ids, and one label value per
// alert would cost the Prometheus doing the scraping far more than the metric
// is worth.
func (m *Metrics) ObserveRequest(route, method string, status int, elapsed time.Duration) {
	if m == nil {
		return
	}
	code := strconv.Itoa(status)
	m.httpRequests.WithLabelValues(route, method, code).Inc()
	m.httpDuration.WithLabelValues(route, method).Observe(elapsed.Seconds())
}

// ReconcileSucceeded records a completed pass for one source.
func (m *Metrics) ReconcileSucceeded(source string, at time.Time) {
	if m == nil {
		return
	}
	m.reconcileRuns.WithLabelValues(source, "ok").Inc()
	m.reconcileLastSuccess.WithLabelValues(source).Set(float64(at.Unix()))
}

// ReconcileFailed records a pass that did not complete, and why.
func (m *Metrics) ReconcileFailed(source, reason string) {
	if m == nil {
		return
	}
	m.reconcileRuns.WithLabelValues(source, reason).Inc()
}

// SilenceWritten records an attempt to create a silence on an Alertmanager.
func (m *Metrics) SilenceWritten(alertmanager string, err error) {
	if m == nil {
		return
	}
	m.silenceWrites.WithLabelValues(alertmanager, resultOf(err)).Inc()
}

// SilenceRecorded records an attempt to store a created silence's provenance.
func (m *Metrics) SilenceRecorded(err error) {
	if m == nil {
		return
	}
	m.silenceRecords.WithLabelValues(resultOf(err)).Inc()
}

func resultOf(err error) string {
	if err != nil {
		return "error"
	}
	return "ok"
}
