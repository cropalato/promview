# Alerta Reference Investigation

Investigation date: 2026-08-14

## Maintenance Status

Alerta is not abandoned. At the time of investigation:

- Alerta `v9.1.0` was released on 2026-03-28.
- `docker-alerta v9.1.0` moved to Python 3.13 and Debian Bookworm and added AMD64/ARM64 images.
- The Web UI received changes in April 2026.
- The Web UI remains based on Vue 2, Vuetify 1, Vuex 3, Moment, and Vue CLI.

Promview is therefore not a replacement for an unmaintained project. It is a narrower Prometheus-focused product with a modern shared web/desktop UI and simpler runtime architecture.

## Runtime Architecture

The Alerta Docker image combines:

- nginx for static files and API proxying
- a Flask API served by uWSGI workers
- Supervisor for process management
- a separately built Vue Web UI
- the Alerta CLI for initialization and housekeeping
- optional authentication and plugin dependencies
- PostgreSQL or MongoDB as external storage

The image is convenient to run, but internally contains more processes and dependencies than Promview needs.

## Prometheus Ingestion

Alertmanager sends grouped webhook notifications to `/api/webhooks/prometheus`. The built-in adapter performs these transformations for each alert:

- `status=firing` keeps the supplied severity, defaulting to `warning`.
- `status=resolved` maps to Alerta's normal severity and closes the alert through its lifecycle rules.
- `instance` or `exported_instance` becomes the resource.
- `alertname` becomes the event.
- `environment`, `service`, `group`, `customer`, `correlate`, `monitor`, and `timeout` receive special handling.
- Remaining labels become string tags such as `team=payments`.
- Remaining annotations become flexible alert attributes.
- Alertmanager and Prometheus links are preserved in attributes.

Promview should preserve all labels and annotations natively instead of assigning special semantics to most label names.

## Identity And Lifecycle

Alerta upserts alerts by `environment + resource + event + customer`. It stores current state and history in one alert record, tracks duplicate notifications, and supports states including open, acknowledged, shelved, closed, and expired.

Useful concepts to retain:

- one current record per logical alert
- repeated notifications refresh current state instead of creating duplicate active rows
- resolved notifications automatically clear alerts
- meaningful source and operator changes create history
- local actions include acknowledge, close, assignment, and notes

Promview should instead use `Alertmanager source ID + Alertmanager fingerprint` as identity. This follows the source protocol and prevents collisions between independent Alertmanager installations.

## Authentication And Authorization

Alerta supports open access, built-in accounts, LDAP, OIDC, SAML, OAuth variants, API keys, and HMAC. Its authorization model is flat RBAC with scopes such as `read:alerts`, `write:alerts`, and `admin:users`. Customer views provide an additional partitioning mechanism.

Promview only needs open, LDAP, and generic OIDC modes initially. Its authorization must combine a role with Prometheus label selectors so users cannot list, count, stream, or mutate out-of-scope alerts.

## API And UI Separation

Alerta's Web UI is a static application configured with an API endpoint. The Docker image packages both components under one origin. This separation is worth retaining because it enables browser and desktop clients to share the same API.

Promview should keep a client-neutral REST API and resumable SSE stream while embedding the compiled React assets in the Go server for simple deployment.

## Ideas To Reuse

- Alertmanager webhook ingestion rather than polling Prometheus.
- Current-state records plus lifecycle history.
- Flexible labels, annotations, and links back to source systems.
- Self-clearing alerts through resolved notifications.
- Dense at-a-glance alert scanning.
- Easy Docker Compose deployment.
- Separate machine credentials and interactive user authentication.
- Server-enforced authorization.

## Ideas Not To Reuse Initially

- Generic monitoring-source adapters and plugin runtime.
- PostgreSQL and MongoDB abstraction.
- nginx, uWSGI, and Supervisor in the application image.
- Label-to-tag conversion.
- Alert identity inferred from selected semantic fields.
- Browser-only authentication assumptions.
- Alertmanager routing, inhibition, silence, or notification responsibilities.

## Primary References

- [Alerta core](https://github.com/alerta/alerta)
- [Alerta Prometheus webhook](https://github.com/alerta/alerta/blob/master/alerta/webhooks/prometheus.py)
- [Alerta Docker image](https://github.com/alerta/docker-alerta)
- [Alerta authentication](https://docs.alerta.io/authentication.html)
- [Alerta authorization](https://docs.alerta.io/authorization.html)
- [Alerta lifecycle](https://docs.alerta.io/lifecycle.html)
- [Alertmanager webhook data model](https://prometheus.io/docs/alerting/latest/notifications/)
