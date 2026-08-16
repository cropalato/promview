# Repository Guidance

## Current State

- The first vertical slice is implemented: Go API in `cmd/` and `internal/`, React UI in `web/`, SQL in `migrations/`, and deployment in `Dockerfile` and `compose.yaml`.

## Commands

- Run all checks that do not need a database with `make verify`.
- Run backend formatting, vet, tests, and build with `make verify-go`; do not use `go test ./...` because it can discover Go files under `web/node_modules`. Focus a test with `go test ./internal/<package> -run TestName`.
- Run frontend checks with `make verify-web`; focus a test with `npm --prefix web run test -- <test-file>`.
- Validate Compose with `make compose-check` and build the image with `make docker-build`.
- Run `PROMVIEW_TEST_DATABASE_URL=<disposable-url> make migration-check` only against a disposable database; it applies up, down, then up. Then run `PROMVIEW_TEST_DATABASE_URL=<same-url> make test-postgres` for persistence integration coverage.

## Product Constraints

- Use `github.com/cropalato/promview` as the Go module path.
- Initially ingest only Prometheus Alertmanager webhooks and support multiple Alertmanager sources.
- Use Go for the server, PostgreSQL for storage, React/TypeScript for the shared UI, and Docker Compose for the initial deployment.
- Authentication modes are open, LDAP, or OIDC. Open mode is anonymous read-only.
- Enforce viewer, operator, and administrator roles server-side, including team/label-scoped access.
- Keep REST, resumable SSE, and authentication transport client-neutral so the React UI can also run in a lightweight Tauri desktop/tray client.

## Verification

- Every test, lint, typecheck, build, migration check, or code-generation verification added to the repository must also run in GitHub Actions.
- Keep focused local commands and their matching CI jobs synchronized as the toolchain is introduced.
- `.github/workflows/ci.yml` is the executable CI source of truth.

## Architecture Boundaries

- `internal/alertmanager` owns webhook decoding and normalization; `internal/alerts` owns query-domain types; `internal/postgres` owns persistence; `internal/httpapi` owns transport.
- Alert snapshots return a durable `streamCursor`; `/api/v1/stream` resumes after that cursor or `Last-Event-ID`. Preserve this snapshot-before-stream contract in all clients.
- Ingestion writes stream events only for created or materially changed alerts. Repeated identical deliveries update timestamps/counts without producing client refresh events.
- Alertmanager sources use independent opaque bearer tokens stored only as SHA-256 hashes. Bootstrap configuration may initialize an absent or legacy uncredentialed source but must never overwrite a credential rotated with `promview source set`.
- Open mode supplies an anonymous viewer. Protected modes accept hashed opaque sessions from cookies or bearer headers; keep authentication separate from provider-specific LDAP and OIDC flows.
- OIDC uses discovery, Authorization Code with PKCE, one-time database-backed login transactions, and configured group-to-role mappings. Never trust provider role claims directly or persist provider tokens.
- The React app uses same-origin `/api` calls. Keep browser-only transport assumptions out of shared clients so Tauri can use bearer credentials later.
- `promview migrate` applies ordered `migrations/*.up.sql` files and records `schema_migrations`; Compose runs it before the app. Keep down migrations and `scripts/check-migrations.sh` synchronized with every schema change.

## Model Routing (Required)

- Any task that plans UI/frontend work must be delegated to `ui-plan`.
- Any task that implements or edits UI code (`web/src/**`, `src/components/**`, `src/app/**/*.tsx`, `src/**/*.jsx`, `src/styles/**`, `**/*.css`) must be delegated to `ui`.
- Do not edit UI files directly.
