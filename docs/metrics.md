# Promview Metrics

Promview is what an operator looks at when something else breaks, which makes its own
failures the easiest ones to miss: when it is down, the console that would show the
problem is the console that is down. That has already happened once — a schema one
migration behind turned every read into a 500, and the first person to notice was
someone trying to silence an alert, long after the desktop client had started failing
its polls in silence.

What is exported is aimed at that: failures that produce no user-visible symptom until
much later. Alert counts are deliberately absent. Prometheus already knows what is
firing, and re-exporting it here would mostly be a way to disagree with the source of
truth.

## Endpoint

`GET /metrics` on a listener of its own, `PROMVIEW_METRICS_ADDRESS`, default `:9090`.
Setting it to an empty string disables the endpoint entirely.

It is **not** a route on the main listener, and the chart does not put it on the
Service or the Ingress. The labels name sources and Alertmanagers, and Promview is
commonly reachable from the internet; a port nothing publishes cannot leak them by
being forgotten. Prometheus is expected to scrape the pod directly, which the chart's
default `metrics.annotations` ask it to do.

## What is exported

| Metric | Type | Labels | Why |
| --- | --- | --- | --- |
| `promview_http_requests_total` | counter | `route`, `method`, `code` | A rising 5xx rate is the fastest signal that Promview itself is broken. |
| `promview_http_request_duration_seconds` | histogram | `route`, `method` | Pool exhaustion shows up as latency long before it shows up as errors. |
| `promview_reconcile_runs_total` | counter | `source`, `result` | Separates "the Alertmanager is unreachable" from "the database rejected the write" without reading logs. |
| `promview_reconcile_last_success_timestamp_seconds` | gauge | `source` | The one that matters most. See below. |
| `promview_silence_writes_total` | counter | `alertmanager`, `result` | Whether silences are reaching the Alertmanager at all. |
| `promview_silence_records_total` | counter | `result` | A provenance write that fails does not fail the silence, so this is the only place its failure is visible. |
| `promview_build_info` | gauge | `version` | Always 1. Confirms which build is actually running. |

Go runtime and process collectors are registered alongside them.

`result` is `ok` on success. A reconciliation failure is `unreadable` when the
Alertmanager could not be read, `error` when the database refused the work, and
`untrusted` when the Alertmanager reported no alerts at all while Promview still held
firing ones — a reading that syncs suppression but is never allowed to end anything.

### The one worth alerting on first

`promview_reconcile_last_success_timestamp_seconds` is the reason this exists.
Reconciliation is what learns that an alert ended while it was silenced. If the loop
stops, it produces no errors — it simply stops — and the only symptom is alerts that
finished hours ago still sitting in the console, which nobody reads as a Promview
fault. Only a pass that could have resolved something counts as a success, so a source
stuck syncing suppression without ever confirming an ending still looks stale here.

```yaml
- alert: PromviewReconciliationStalled
  expr: time() - promview_reconcile_last_success_timestamp_seconds > 900
  for: 5m
```

Route this somewhere that does not need Promview to read it. Alertmanager notifies
through its own receivers regardless, so this works today — it is worth being
deliberate about rather than accidental.

## Cardinality

`route` is the **matched pattern**, never the path: a request to `/api/v1/alerts/42`
reports as `GET /api/v1/alerts/{id}`, and anything that matches no route at all
reports as `unmatched` rather than naming itself. Nothing user-controlled reaches a
label value — no alert names, no instances, no label values from the alerts
themselves. `source` and `alertmanager` are bounded by configuration.

This matters more than usual here: the Prometheus being protected from a cardinality
explosion is the same Prometheus whose alerts Promview exists to display.
