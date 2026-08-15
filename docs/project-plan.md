# Promview Project Plan

## Confirmed Decisions

- Go module: `github.com/cropalato/promview`
- Backend: Go
- Browser UI: React and TypeScript
- Storage: PostgreSQL
- Initial deployment: Docker Compose
- Initial source: Prometheus Alertmanager webhooks only
- Source topology: multiple Alertmanager installations
- Expected scale: up to 50,000 active alerts and 100 received alerts per second
- Authentication modes: open, LDAP, or OIDC
- Open mode: anonymous read-only
- Roles: viewer, operator, and administrator
- Authorization scope: Prometheus label selectors, including team-scoped access
- Operator actions: acknowledge, assign, close, and notes
- Live transport: resumable server-sent events
- Desktop direction: shared React UI in a lightweight Tauri client

## Implementation Status

The first two implementation slices provide:

- authenticated Alertmanager webhook decoding and normalization
- deterministic fallback fingerprints
- PostgreSQL current-state upserts and repeat counts
- cursor-paginated current-alert queries
- exact source, status, severity, and team filters
- severity counts for the active query
- durable created, updated, and resolved stream events
- resumable SSE using snapshot cursors and `Last-Event-ID`
- debounced live console refresh and visible connection state
- versioned migration runner with existing-volume baselining
- occurrence-aware immutable source lifecycle history
- latest raw Alertmanager payload storage
- alert detail and history APIs
- deep-linkable responsive detail drawer with overview, timeline, and raw views
- health, readiness, and runtime configuration endpoints
- a responsive React console connected to firing alerts
- migration, persistence, API, frontend, image, and Compose verification in GitHub Actions

Operator actions, notes, scoped authorization, stream retention, and source-specific credentials remain planned work.

## Goals

Promview is a focused operational console for receiving, retaining, viewing, and acting on Prometheus Alertmanager alerts. It keeps Alertmanager responsible for routing, grouping, inhibition, silences, and notifications.

The first release must:

- ingest authenticated webhooks from multiple Alertmanager sources
- expose current and historical alert state
- filter, sort, search, and count alerts by labels, severity, state, source, and time
- support acknowledge, assign, close, notes, and bulk actions
- enforce role and label scope on every query, count, stream, and mutation
- provide real-time browser and desktop updates
- run as an application container plus PostgreSQL

The first release will not:

- poll Prometheus or Alertmanager APIs for active alerts
- accept non-Prometheus alert formats
- create or manage Alertmanager silences
- implement a generic plugin system
- ship Kubernetes resources

## Architecture

```text
Prometheus -> Alertmanager instances -> authenticated webhooks
                                            |
                                            v
                                      Go application
                                      - ingestion
                                      - lifecycle
                                      - REST API
                                      - resumable SSE
                                      - auth and policy
                                      - housekeeping
                                      - embedded React UI
                                            |
                                            v
                                       PostgreSQL
```

Use one Go application binary. Build the React application separately and embed its output in the binary. Do not add nginx or a process supervisor unless a concrete deployment requirement appears.

## Alert Sources

Each Alertmanager source has:

- stable ID and URL slug
- display name
- hashed ingestion token
- optional external URL override
- enabled state
- last successful delivery timestamp

Initial endpoint:

```text
POST /api/v1/ingest/alertmanager/{source}
Authorization: Bearer <source-token>
```

Apply body-size limits and per-source rate limits. Never place source tokens in query parameters.

## Identity And Occurrences

Use `source_id + Alertmanager fingerprint` as the current alert identity. If a supported legacy payload lacks a fingerprint, derive one from canonical sorted labels and record that it was derived.

Separate logical alert identity from occurrences:

- first firing creates an occurrence
- repeated firing updates `last_seen`, labels, annotations, and repeat count
- repeated notifications do not clear assignment or acknowledgement
- resolved closes the current occurrence
- firing after resolution creates a new occurrence and clears prior operator state
- operator close is local; a later firing notification reopens the current occurrence
- only material source changes and operator actions create lifecycle events

Do not retain a raw history row for every repeated notification. Store the latest raw source object and use metrics for total delivery volume.

## Data Model

Initial tables:

- `alert_sources`
- `alerts`
- `alert_occurrences`
- `alert_events`
- `alert_notes`
- `users`
- `auth_identities`
- `sessions`
- `role_bindings`
- `audit_events`
- `stream_events`

Important indexes:

- unique `(source_id, fingerprint)` for current alerts
- GIN index on labels JSONB
- state, severity, `last_seen`, assignee, source, and team indexes
- monotonic cursor index for stream events
- time indexes for retention jobs

## Authentication

Only one interactive mode is active per deployment.

### Open

- no login flow
- anonymous viewer identity
- all mutations denied
- source ingestion remains authenticated

### LDAP

- bind-and-search authentication
- TLS and custom CA support
- configurable user and group filters
- map LDAP groups to role bindings
- never persist LDAP passwords

### OIDC

- Authorization Code flow with PKCE
- discovery from issuer metadata
- validate issuer, audience, signature, expiry, state, and nonce
- configurable username, email, display name, and group claims
- map OIDC groups to role bindings

### Sessions And Desktop Tokens

Browser sessions use random opaque IDs in `HttpOnly`, `Secure`, `SameSite=Lax` cookies. Store only token hashes and require CSRF protection for browser mutations.

Desktop clients use revocable opaque bearer credentials stored in the operating system keychain. REST and SSE must accept browser sessions or desktop credentials without changing authorization semantics.

## Authorization

Built-in roles:

| Role | Access |
| --- | --- |
| Viewer | Read matching alerts and history |
| Operator | Viewer access plus acknowledge, assign, close, and notes |
| Administrator | Global access, source and policy management, and audit access |

A role binding combines subject, role, and label selector. Initial selector operators are `=`, `!=`, `=~`, and `!~`.

Authorization constraints must be part of database queries. Post-query filtering can leak counts, stream events, pagination information, and timing. Mutation endpoints must independently confirm the target is in scope.

## API

Initial endpoints:

```text
GET    /api/v1/config
GET    /api/v1/me
GET    /api/v1/alerts
GET    /api/v1/alerts/{id}
GET    /api/v1/alerts/{id}/events
GET    /api/v1/alerts/{id}/notes
GET    /api/v1/alert-counts
GET    /api/v1/labels
GET    /api/v1/labels/{name}/values
POST   /api/v1/alerts/{id}/acknowledge
POST   /api/v1/alerts/{id}/assign
POST   /api/v1/alerts/{id}/close
POST   /api/v1/alerts/{id}/notes
POST   /api/v1/alerts/actions
GET    /api/v1/stream
POST   /api/v1/ingest/alertmanager/{source}
GET    /health/live
GET    /health/ready
GET    /metrics
```

Use cursor pagination. Filtering, sorting, counts, and autocomplete remain server-side because clients never own the complete authorized dataset. Bulk actions return one result per target.

## Real-Time Stream

SSE events use a monotonic ID and typed payload:

```json
{
  "id": 123456,
  "type": "alert.updated",
  "alertId": "...",
  "occurredAt": "...",
  "data": {}
}
```

Support `Last-Event-ID`, detect retention gaps, and instruct clients to refresh their snapshot when a cursor can no longer be resumed. Frontends should batch high-volume updates rather than render once per event.

## Browser UI

The primary experience is one dense operational console, not a card dashboard:

- sticky identity, source, user, and connection bar
- Prometheus-style label filter with autocomplete
- compact severity and lifecycle summary strip
- server-filtered, virtualized alert table
- keyboard navigation and multi-selection
- resizable detail drawer on desktop
- full-screen detail view on mobile
- labels, annotations, raw payload, source links, timeline, and notes
- single and bulk operator actions
- visible stale/reconnecting state

Default columns are severity, lifecycle state, alert name, summary, team, instance, source, age, assignee, and note count.

Use color, shape, icon, and text together for severity. Target WCAG 2.2 AA, keyboard-only operation, reduced motion, and touch targets appropriate for mobile triage.

## Deployment

Docker Compose initially contains:

```text
app
postgres
```

The application image must:

- use a multi-stage Go and React build
- contain no compiler or Node runtime in the final stage
- run as a non-root user
- expose one application port
- support AMD64 and ARM64
- read secrets from environment variables or mounted files
- run schema migrations through an explicit, observable step

## Observability And Security

Expose Prometheus metrics for webhook processing, rejected payloads, source status, lifecycle transitions, database latency, HTTP errors, authentication outcomes, authorization denials, SSE clients, and housekeeping.

Required controls include strict JSON decoding, request limits, login throttling, safe URL and Markdown rendering, CSRF protection, OIDC replay protections, token hashing, audit records for every mutation, and redaction of secrets from logs.

## Verification And GitHub Actions

Every verification introduced locally must run in GitHub Actions. This includes:

- Go formatting, vetting, linting, unit tests, integration tests, and builds
- frontend formatting, linting, typechecking, unit tests, accessibility checks, and builds
- database migration validation
- OpenAPI or other generated-code drift checks
- Docker image builds
- end-to-end tests and load checks when they become part of the supported local workflow

CI configuration and documented local commands must be updated together. PostgreSQL-dependent tests should use a service container in Actions.

## Delivery Phases

1. Foundation: module, workspace, Compose, migrations, OpenAPI, local checks, and matching Actions workflows.
2. Ingestion: source credentials, Alertmanager parser, identity, occurrence lifecycle, and contract fixtures.
3. Query API: scoped filtering, cursor pagination, counts, details, history, and realistic database performance tests.
4. Authentication: open, LDAP, OIDC, browser sessions, desktop credentials, and provider security tests.
5. Authorization and actions: scoped RBAC, acknowledge, assign, close, notes, bulk actions, and audit events.
6. Browser UI: virtualized console, detail timeline, actions, responsive triage, SSE, accessibility, and keyboard flows.
7. Hardening: load, recovery, retention, backup, multi-architecture images, and release documentation.
8. Desktop client: Tauri tray application using the stable shared API, SSE contract, and React components.

## MVP Acceptance Criteria

- Webhook retries do not create duplicate alerts.
- Identical fingerprints from different sources do not collide.
- Resolved and subsequently firing alerts create correct occurrence transitions.
- Acknowledgements survive repeated firing deliveries.
- Open mode cannot mutate alerts.
- LDAP and OIDC group mappings produce expected roles and label scopes.
- A scoped operator cannot list, count, stream, or mutate another team's alerts.
- The console remains usable with 50,000 active alerts and 100 received alerts per second.
- Restarting the application loses no alert or operator state.
- Docker Compose starts a usable installation using documented configuration.
- All documented verification runs in GitHub Actions.
