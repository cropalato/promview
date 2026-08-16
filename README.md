# Promview

Promview is a focused operational console for alerts delivered by Prometheus Alertmanager. The current implementation includes authenticated webhook ingestion, PostgreSQL current-state storage, a cursor-paginated query API, resumable live updates, health endpoints, and a React console that renders firing alerts.

The Go module is `github.com/cropalato/promview`. See [`docs/project-plan.md`](docs/project-plan.md) for the planned lifecycle, authentication, authorization, API, and desktop client work.

## Requirements

- Go 1.25 or newer
- Node.js 22 or newer
- Docker with Compose
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
```

Migration verification requires a disposable PostgreSQL database because it applies up, down, and up migrations:

```sh
export PROMVIEW_TEST_DATABASE_URL='postgres://promview:promview@localhost:5432/promview_test?sslmode=disable'
make migration-check
make test-postgres
```

Every command above has a matching GitHub Actions job in `.github/workflows/ci.yml`.

Do not use `go test ./...` after installing frontend dependencies: Go can discover `.go` files inside `web/node_modules`. Use the package boundaries in `make verify-go` instead.

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

Inspect the current principal:

```sh
curl 'http://localhost:8080/api/v1/me'
```

Open mode returns an anonymous viewer and keeps ingestion authenticated. LDAP mode requires an existing opaque session; its provider login flow is not implemented yet. OIDC mode provides browser sign-in and logout while keeping provider tokens on the server.

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
export PROMVIEW_OIDC_VIEWER_GROUPS='promview-viewers'
export PROMVIEW_OIDC_OPERATOR_GROUPS='promview-operators'
export PROMVIEW_OIDC_ADMIN_GROUPS='promview-administrators'
docker compose up --build
```

Promview uses provider discovery and Authorization Code with PKCE. It validates the ID token signature, issuer, audience, expiry, state, and nonce, then issues its own opaque 12-hour session in an `HttpOnly`, `Secure`, `SameSite=Lax` cookie. Provider access and ID tokens are not persisted.

The default scopes are `openid,profile,email,groups`; the default claims are `preferred_username`, `email`, `name`, and `groups`. Override them with `PROMVIEW_OIDC_SCOPES`, `PROMVIEW_OIDC_USERNAME_CLAIM`, `PROMVIEW_OIDC_EMAIL_CLAIM`, `PROMVIEW_OIDC_DISPLAY_NAME_CLAIM`, and `PROMVIEW_OIDC_GROUPS_CLAIM`. Group lists are comma-separated. Unmapped identities are denied, and administrator mappings take precedence over operator and viewer mappings.

Production issuer and redirect URLs must use HTTPS. Loopback HTTP is supported for provider testing by setting `PROMVIEW_OIDC_COOKIE_SECURE=false`; insecure cookies are rejected for non-loopback redirect hosts.

The list endpoint supports opaque cursor pagination and exact `source`, `status`, `severity`, and `team` filters. The browser console currently applies its free-text search only to rows already loaded from this endpoint.

Live alert changes are available as resumable server-sent events:

```sh
curl --no-buffer 'http://localhost:8080/api/v1/stream?cursor=0'
```

Each alerts snapshot includes `streamCursor`. Start the stream from that value to avoid missing changes between the snapshot and live updates. Reconnects may instead send `Last-Event-ID`.

To reset the development database and rerun initialization:

```sh
docker compose down --volumes
```
