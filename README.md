# Promview

Promview is a focused operational console for alerts delivered by Prometheus Alertmanager. The current implementation includes authenticated webhook ingestion, PostgreSQL current-state storage, a cursor-paginated query API, resumable live updates, browser notifications for critical alerts, health endpoints, and a React console that renders firing alerts.

The Go module is `github.com/cropalato/promview`. See [`docs/project-plan.md`](docs/project-plan.md) for the planned lifecycle, authentication, authorization, API, and desktop client work.

## Requirements

- Go 1.25 or newer
- Node.js 22 or newer
- Docker with Compose
- Helm 3.14 or newer when packaging or installing the Kubernetes chart
- PostgreSQL client tools only when running migration verification directly

## Local Verification

```sh
make verify
```

This runs Go formatting checks, tests, and build; frontend formatting, linting, typechecking, tests, and build; and Docker Compose configuration validation.

Focused commands:

```sh
make verify-go
make verify-web
go test ./internal/alertmanager -run TestDecodeAndNormalize
npm --prefix web run test -- src/config/runtimeConfig.test.ts
make compose-check
make docker-build
make verify-helm
```

Migration verification requires a disposable PostgreSQL database because it applies up, down, and up migrations:

```sh
export PROMVIEW_TEST_DATABASE_URL='postgres://promview:promview@localhost:5432/promview_test?sslmode=disable'
make migration-check
make test-postgres
```

Every command above has a matching GitHub Actions job in `.github/workflows/ci.yml`.

Do not use `go test ./...` after installing frontend dependencies: Go can discover `.go` files inside `web/node_modules`. Use the package boundaries in `make verify-go` instead.

## Run On Kubernetes

Promview ships a Helm chart for an external PostgreSQL database:

```sh
helm upgrade --install promview oci://ghcr.io/cropalato/charts/promview \
  --namespace promview \
  --version 0.1.0-alpha.5
```

Create the required database Secret before installation. See [`docs/kubernetes.md`](docs/kubernetes.md) and [`charts/promview/README.md`](charts/promview/README.md) for the complete procedure, OIDC values, migration lifecycle, and production checklist.

## Run With Docker Compose

```sh
docker compose up --build
```

Open <http://localhost:8080>. The development stack uses open anonymous read-only mode.

Send an Alertmanager-compatible webhook to the bootstrapped `demo` source:

```sh
curl -X POST http://localhost:8080/api/v1/ingest/alertmanager/demo \
  -H 'Authorization: Bearer development-token' \
  -H 'Content-Type: application/json' \
  -d '{
    "version": "4",
    "alerts": [{
      "status": "firing",
      "labels": {"alertname": "ExampleAlert", "severity": "warning"},
      "annotations": {"summary": "Example alert delivery"},
      "startsAt": "2026-08-14T12:00:00Z"
    }]
  }'
```

Each source has its own opaque bearer token. Promview stores only its SHA-256 hash. The Compose environment bootstraps `demo` when it is absent or has no credential; restarting the app does not overwrite a token rotated later.

Provision or rotate another source with the CLI:

```sh
docker compose run --rm app source set \
  --slug production \
  --name 'Production Alertmanager' \
  --token 'replace-with-at-least-16-characters'
```

The webhook URL source slug and bearer token must identify the same enabled source.

For the complete Prometheus rule, Alertmanager routing, authentication, TLS, validation, and token-rotation procedure, see [`docs/prometheus-alertmanager.md`](docs/prometheus-alertmanager.md).

List firing alerts:

```sh
curl 'http://localhost:8080/api/v1/alerts?status=firing&limit=100'
```

Collapse a fan-out into one row per alert name and source, and expand one group
by asking for its members:

```sh
curl 'http://localhost:8080/api/v1/alerts?groupBy=alertname,source&status=firing'
curl 'http://localhost:8080/api/v1/alerts?match=alertname%3DPrometheusTimeseriesCardinality'
```

Grouping accepts `alertname`, `source`, `team`, `severity` and `instance`, up to three
at once. Group counts are computed under the caller's own read restrictions, so a group
never reports members the caller cannot open. Expanding a group is the ordinary alerts
query with a matcher, which is why sorting, cursors and the detail view behave
identically inside a group.

Inspect the current principal:

```sh
curl 'http://localhost:8080/api/v1/me'
```

Open mode returns an anonymous global viewer and keeps ingestion authenticated. OIDC is the only interactive authentication mode; provider tokens remain on the server.

## Alert Expiry

Alertmanager suppresses resolved notifications for silenced alerts, so an alert that
clears inside a maintenance window is never announced and would otherwise stay firing
forever. A background sweep marks alerts whose source went quiet as `expired`, which is
a weaker claim than `resolved`: the source stopped reporting, it never said the alert
was over. History records `alert.expired`; the alert stream carries `alert.resolved`,
since consumers only need to know it left the firing view.

```sh
export PROMVIEW_ALERT_STALE_AFTER=12h   # default; 0 disables expiry entirely
export PROMVIEW_ALERT_EXPIRY_INTERVAL=1m
```

The window must exceed the source Alertmanager's `repeat_interval`, or a live alert
expires between repeat notifications and flaps back on the next one. The default of 12h
is three times Alertmanager's own 4h default. Sources with a different `repeat_interval`
set their own window, which wins over the server default:

```sh
promview source set --slug primary --name Primary --token "$TOKEN" --stale-after 6h
```

An individual alert can shorten its own window with a numeric `timeout` label (in
seconds) on the rule, matching how Alerta reads the same label.

## Alertmanager Reconciliation

Expiry infers an ending from silence; reconciliation confirms one. Given a source's
Alertmanager URL, promview reads `GET /api/v2/alerts` on a loop and aligns what it
holds with what still exists: an alert the Alertmanager no longer lists is resolved,
and one it reports as `suppressed` is flagged silenced while remaining firing.

```sh
promview source set --slug primary --name Primary --token "$TOKEN" \
  --alertmanager-url http://alertmanager.monitoring:9093

export PROMVIEW_RECONCILE_INTERVAL=1m   # 0 disables reconciliation
export PROMVIEW_RECONCILE_TIMEOUT=10s
```

The API is read-only and used unauthenticated, so a source behind authentication is
not supported yet. A source without a URL is left to expiry alone.

Two rules keep a healthy Alertmanager from emptying the console. An alert must be
absent from two consecutive readings before it is resolved, so a dropped request
changes nothing. And an Alertmanager reporting *no* alerts at all while promview holds
firing ones is treated as untrustworthy — a restarting Alertmanager looks exactly like
a fleet that went silent — so that reading only syncs suppression and leaves endings
to a reading that shows something.

## OIDC Authentication

For an Okta-specific walkthrough, see [`docs/okta-oidc.md`](docs/okta-oidc.md).

Register this exact callback URL with the identity provider:

```text
https://promview.example.com/api/v1/auth/oidc/callback
```

Configure Promview through the Compose environment:

```sh
export PROMVIEW_AUTH_MODE=oidc
export PROMVIEW_OIDC_ISSUER_URL='https://identity.example.com'
export PROMVIEW_OIDC_CLIENT_ID='promview'
export PROMVIEW_OIDC_CLIENT_SECRET='replace-with-client-secret'
export PROMVIEW_OIDC_REDIRECT_URL='https://promview.example.com/api/v1/auth/oidc/callback'
docker compose up --build
```

Create at least one server-owned binding before the first OIDC login:

```sh
docker compose run --rm app access set \
  --name promview-administrators \
  --role administrator \
  --oidc-issuer 'https://identity.example.com' \
  --oidc-group 'promview-administrators'
```

Create a scoped operator binding by repeating `--selector` for AND semantics:

```sh
docker compose run --rm app access set \
  --name platform-operators \
  --role operator \
  --oidc-issuer 'https://identity.example.com' \
  --oidc-group 'promview-platform' \
  --selector 'team=platform' \
  --selector 'environment!=development'
```

Promview uses provider discovery and Authorization Code with PKCE. It validates the ID token signature, issuer, audience, expiry, state, and nonce, then issues its own opaque 12-hour session in an `HttpOnly`, `Secure`, `SameSite=Lax` cookie. Provider access and ID tokens are not persisted.

The default scopes are `openid,profile,email,groups`; the default claims are `preferred_username`, `email`, `name`, and `groups`. Override them with `PROMVIEW_OIDC_SCOPES`, `PROMVIEW_OIDC_USERNAME_CLAIM`, `PROMVIEW_OIDC_EMAIL_CLAIM`, `PROMVIEW_OIDC_DISPLAY_NAME_CLAIM`, and `PROMVIEW_OIDC_GROUPS_CLAIM`. Unbound identities are denied. Bindings are evaluated from the database on every request, so policy changes affect existing sessions immediately. Provider group changes take effect when Promview next observes them during a successful login.

Selectors support `=`, `!=`, `=~`, and `!~`. Selectors within one binding are ANDed; multiple matching bindings are ORed. Viewer and operator bindings may be scoped, while administrator bindings are always global.

See [`docs/authorization.md`](docs/authorization.md) for binding administration, selector semantics, revocation behavior, and deployment-specific commands.

Production issuer and redirect URLs must use HTTPS. Loopback HTTP is supported for provider testing by setting `PROMVIEW_OIDC_COOKIE_SECURE=false`; insecure cookies are rejected for non-loopback redirect hosts.

The list endpoint supports opaque cursor pagination, source/status filters, and repeated label matchers. Use `match=label=value` or `match=label!=value` for ANDed positive and negative label filters, plus `sort` and `order=asc|desc` for supported columns. The browser console applies these filters and sorts server-side; detail labels can add or replace a positive or negative filter.

Authorized operators can acknowledge or unacknowledge an alert from its detail view. This records Promview-local state and timeline history but does not alter Alertmanager routing, notifications, or silences.

Live alert changes are available as resumable server-sent events:

```sh
curl --no-buffer 'http://localhost:8080/api/v1/stream?cursor=0'
```

Each alerts snapshot includes `streamCursor`. Start the stream from that value to avoid missing changes between the snapshot and live updates. Reconnects may instead send `Last-Event-ID`.

The top bar can enable browser notifications for newly created critical alerts. Permission is requested only after the user selects the notification control. Page-open notifications require HTTPS or localhost and an open Promview tab; closed-browser delivery would require a future Web Push service worker.

To reset the development database and rerun initialization:

```sh
docker compose down --volumes
```
