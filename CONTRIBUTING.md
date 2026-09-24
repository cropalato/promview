# Contributing to Promview

Promview is alpha and young. Deployment reports are worth as much as patches
right now: if you pointed an Alertmanager at it and something was wrong,
confusing, or missing, that is a useful issue.

## Before a large change

Open an issue first for anything beyond a fix. Promview is deliberately narrow —
it receives Alertmanager webhooks and nothing else, it does not poll Prometheus,
and it leaves routing, grouping, inhibition and notification to Alertmanager.
[`docs/project-plan.md`](docs/project-plan.md) records what is planned and what
is explicitly out of scope. A change that widens the scope is a conversation, not
a surprise in a pull request.

## Setting up

```sh
git clone https://github.com/cropalato/promview.git
cd promview
docker compose up --build
```

You need Go 1.26+, Node 22+, Docker with Compose, and Helm 3.14+ if you touch the
chart. The README's [Development](README.md#development) section has the rest.

## Verification

Run this before opening a pull request:

```sh
make verify
```

It covers Go formatting, vet, tests and build; frontend formatting, lint,
typecheck, tests and build; the Tauri crate's fmt, clippy, tests and build;
Compose validation; and the Helm chart. Focused targets (`make verify-go`,
`make verify-web`, `make verify-desktop`) exist for iterating.

Anything touching the schema also needs a disposable PostgreSQL:

```sh
export PROMVIEW_TEST_DATABASE_URL='postgres://promview:promview@localhost:5432/promview_test?sslmode=disable'
make migration-check
make test-postgres
```

`make migration-check` applies up, down, then up. Point it at a database you do
not mind losing.

Two things that bite people:

- **Do not run `go test ./...`.** After `npm ci`, Go discovers `.go` files inside
  `web/node_modules`. Use the package boundaries in `make verify-go`.
- **Every check must run in CI.** `.github/workflows/ci.yml` is the executable
  source of truth. A test that only runs on your machine is a test that stops
  running. If you add a verification step, add its job.

## Migrations

Schema changes ship as an ordered pair in `migrations/`: `NNNNNN_name.up.sql` and
a matching `.down.sql`. Both are required, and `scripts/check-migrations.sh`
verifies the pair applies and reverses. Down migrations exist so a rollback is
possible; they are never run automatically during one.

## Architecture boundaries

Keep these intact, or say why you are changing them:

- `internal/alertmanager` decodes and normalizes webhooks; `internal/alerts`
  owns query-domain types; `internal/postgres` owns persistence;
  `internal/httpapi` owns transport.
- Alert snapshots return a durable `streamCursor`, and `/api/v1/stream` resumes
  from it or from `Last-Event-ID`. Preserve that snapshot-before-stream contract
  in every client.
- Ingestion writes stream events only for created or materially changed alerts.
  A repeated identical delivery updates timestamps and counts without waking
  every open console.
- Authorization is enforced in SQL, not in the UI. A scoped principal must not be
  able to list, count, stream, or open an alert outside its scope.
- The React app uses same-origin `/api` calls, but shared client code must not
  assume a browser: the Tauri client authenticates with bearer credentials.

[`AGENTS.md`](AGENTS.md) carries the full set.

## Commits and changelog

The project uses [Conventional Commits](https://www.conventionalcommits.org/):

```
feat(alerts): revive an expired alert the Alertmanager still holds
fix(desktop): open the system browser for sign-in
docs: reorganize the README around evaluating Promview
```

Scopes in use include `alerts`, `silence`, `desktop`, `web`, `packaging`,
`httpapi`, and `postgres`. Common types are `feat`, `fix`, `docs`, `chore`,
`ci`, `style`, and `refactor`.

Write the body for someone who will read it in a year without the context you
have today. Say what was wrong and why the change is the right shape, not what
the diff already shows.

Anything a user or operator would notice goes in [`CHANGELOG.md`](CHANGELOG.md)
under `## [Unreleased]`, in the section matching its type. Internal refactors
that change no behavior do not need an entry.

## Pull requests

- One subject per pull request.
- `make verify` passes, and the CI jobs pass.
- New behavior comes with tests. The repository keeps roughly one line of Go test
  per two lines of Go, and that is not an accident.
- Describe what you observed, not only what you changed. A bug report inside the
  pull request is what makes the fix reviewable.

## Security

Do not open a public issue for a vulnerability. [`SECURITY.md`](SECURITY.md)
has the private reporting channel.

## Licensing

Contributions are accepted under the [MIT License](LICENSE).
