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

Send an Alertmanager-compatible webhook using the development token:

```sh
curl -X POST http://localhost:8080/api/v1/ingest/alertmanager/local \
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

The shared token is only a bootstrap mechanism for the first implementation slice. Source-specific hashed credentials are planned before production use.

List firing alerts:

```sh
curl 'http://localhost:8080/api/v1/alerts?status=firing&limit=100'
```

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
