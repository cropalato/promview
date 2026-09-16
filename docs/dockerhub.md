# Promview

Operational console for alerts delivered by Prometheus Alertmanager. Promview
ingests authenticated webhooks from multiple Alertmanager installations, keeps
current alert state in PostgreSQL, and serves a live React console over REST and
resumable server-sent events. Alertmanager stays responsible for routing,
grouping, inhibition and notification.

- **Source:** https://github.com/cropalato/promview
- **Documentation:** https://github.com/cropalato/promview/tree/main/docs
- **License:** MIT

## Status

Promview is **alpha**. The API, the schema and the configuration surface may
change between releases. Pin an exact tag; do not track a moving one in
production.

## Tags and architecture

| Tag | Meaning |
| --- | --- |
| `0.1.0-alpha.N` | An exact release. Pin this. |
| `alpha` | Moves to the newest pre-release. |
| `latest` | Moves to the newest stable release. None exists yet. |

Images are built for **linux/amd64 only**. There is no arm64 image: the project
publishes the one architecture it tests on rather than an emulated build nobody
exercises.

## Requirements

Promview needs an externally managed PostgreSQL database. It neither bundles nor
manages one, in any deployment.

## Quick start

```yaml
services:
  postgres:
    image: postgres:17-alpine
    environment:
      POSTGRES_DB: promview
      POSTGRES_USER: promview
      POSTGRES_PASSWORD: promview
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U promview -d promview"]
      interval: 2s
      timeout: 3s
      retries: 15
    volumes:
      - postgres-data:/var/lib/postgresql/data

  migrate:
    image: cropalato/promview:alpha
    command: ["migrate"]
    depends_on:
      postgres:
        condition: service_healthy
    environment: &app-environment
      PROMVIEW_DATABASE_URL: postgres://promview:promview@postgres:5432/promview?sslmode=disable
      PROMVIEW_BOOTSTRAP_SOURCE_SLUG: demo
      PROMVIEW_BOOTSTRAP_SOURCE_NAME: Development Alertmanager
      PROMVIEW_BOOTSTRAP_SOURCE_TOKEN: development-token

  app:
    image: cropalato/promview:alpha
    depends_on:
      migrate:
        condition: service_completed_successfully
    environment: *app-environment
    ports:
      - "8080:8080"

volumes:
  postgres-data:
```

```sh
docker compose up -d
```

Open <http://localhost:8080>. This stack runs in open mode: anonymous
read-only, with ingestion still authenticated.

Migrations are a separate `migrate` command rather than something the server
does on start, so two replicas cannot race the schema. Run it to completion
before the application starts, on every upgrade.

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

Each source carries its own opaque bearer token, stored only as a SHA-256 hash.
Provision or rotate one with the CLI in the same image:

```sh
docker compose run --rm app source set \
  --slug production \
  --name 'Production Alertmanager' \
  --token 'replace-with-at-least-16-characters'
```

## Ports

| Port | Purpose |
| --- | --- |
| `8080` | HTTP API and console. |
| `9090` | Prometheus metrics and the health endpoints. |

## Configuration

All configuration is environment variables. The container runs as UID/GID
`65532` and holds no state on disk.

### Required

| Variable | Description |
| --- | --- |
| `PROMVIEW_DATABASE_URL` | PostgreSQL connection URL. No default. |

### Server

| Variable | Default | Description |
| --- | --- | --- |
| `PROMVIEW_LISTEN_ADDRESS` | `:8080` | API and console listener. |
| `PROMVIEW_METRICS_ADDRESS` | `:9090` | Metrics and health listener. |
| `PROMVIEW_WEB_DIRECTORY` | `/app/web` | Built console assets. |
| `PROMVIEW_MIGRATIONS_DIRECTORY` | `/app/migrations` | Migration files. |
| `PROMVIEW_AUTH_MODE` | `open` | `open` or `oidc`. |

### Source bootstrap

Set the slug and the token together, or neither. Bootstrap initializes a source
that is absent or has no credential; it never overwrites a token rotated with
`promview source set`.

| Variable | Description |
| --- | --- |
| `PROMVIEW_BOOTSTRAP_SOURCE_SLUG` | URL slug of the source to initialize. |
| `PROMVIEW_BOOTSTRAP_SOURCE_NAME` | Display name. |
| `PROMVIEW_BOOTSTRAP_SOURCE_TOKEN` | Ingestion bearer token. |

### Alert lifecycle

| Variable | Default | Description |
| --- | --- | --- |
| `PROMVIEW_ALERT_STALE_AFTER` | `12h` | Default window before an unreported alert expires. `0` disables expiry. Must exceed the Alertmanager `repeat_interval`. |
| `PROMVIEW_ALERT_EXPIRY_INTERVAL` | `1m` | How often the expiry sweep runs. |
| `PROMVIEW_STREAM_RETENTION` | `24h` | How long a stream event is kept so a disconnected client can resume. `0` keeps every event. |
| `PROMVIEW_RECONCILE_INTERVAL` | `1m` | How often each source's Alertmanager is read to confirm what is still firing. `0` disables reconciliation. |
| `PROMVIEW_RECONCILE_TIMEOUT` | `10s` | Bounds one Alertmanager request. |
| `PROMVIEW_SILENCE_DEFAULT_DURATION` | `2h` | Silence length when the operator does not say. |
| `PROMVIEW_SILENCE_MAX_DURATION` | `720h` | Longest silence an operator may ask for. |

### OIDC

Required when `PROMVIEW_AUTH_MODE=oidc`. Promview uses discovery and
Authorization Code with PKCE, keeps provider tokens server-side, and never
trusts provider role claims directly: groups map to roles through
server-owned bindings.

| Variable | Default | Description |
| --- | --- | --- |
| `PROMVIEW_OIDC_ISSUER_URL` | | Issuer. HTTPS except on loopback. |
| `PROMVIEW_OIDC_CLIENT_ID` | | Client ID. |
| `PROMVIEW_OIDC_CLIENT_SECRET` | | Client secret. |
| `PROMVIEW_OIDC_REDIRECT_URL` | | Must end at `/api/v1/auth/oidc/callback`. |
| `PROMVIEW_OIDC_SCOPES` | `openid,profile,email,groups` | Must include `openid`. |
| `PROMVIEW_OIDC_USERNAME_CLAIM` | `preferred_username` | |
| `PROMVIEW_OIDC_EMAIL_CLAIM` | `email` | |
| `PROMVIEW_OIDC_DISPLAY_NAME_CLAIM` | `name` | |
| `PROMVIEW_OIDC_GROUPS_CLAIM` | `groups` | |
| `PROMVIEW_OIDC_COOKIE_SECURE` | `true` | May be `false` only on loopback hosts. |

See
[`docs/okta-oidc.md`](https://github.com/cropalato/promview/blob/main/docs/okta-oidc.md)
for a worked provider setup.

## Kubernetes

A Helm chart is published beside the image and runs this exact image as a
serialized pre-install and pre-upgrade migration hook:

```sh
helm upgrade --install promview oci://ghcr.io/cropalato/charts/promview \
  --namespace promview \
  --create-namespace \
  --version 0.1.0-alpha.40
```

The chart version is the application version. Create the PostgreSQL Secret
before installing. See
[`docs/kubernetes.md`](https://github.com/cropalato/promview/blob/main/docs/kubernetes.md).

## Also published at

```sh
docker pull ghcr.io/cropalato/promview:alpha
```

## Desktop client

The same console ships as a Tauri desktop and tray client for Linux and
Windows. Installers are attached to each
[GitHub release](https://github.com/cropalato/promview/releases).
