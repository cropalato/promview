## What this changes

<!-- What was wrong, and why this is the right shape of fix. The diff already
     says what the code does; say what it does not. -->

## How it was verified

<!-- What you actually ran, and what you observed. "make verify passes" is the
     floor, not the answer, for anything touching behavior. -->

- [ ] `make verify` passes
- [ ] `make migration-check` and `make test-postgres` pass, or the change is not schema-related
- [ ] New behavior has tests, or there is no new behavior

## Checklist

- [ ] Conventional Commit subject (`feat(alerts):`, `fix(desktop):`, `docs:`, …)
- [ ] `CHANGELOG.md` updated under `## [Unreleased]`, or the change is invisible to users and operators
- [ ] Any new verification step has a matching job in `.github/workflows/ci.yml`
- [ ] Migrations ship as an up/down pair

## Anything to watch

<!-- Authorization scope, the snapshot-before-stream cursor contract, the
     Alertmanager write path, or an upgrade that needs an ordering. Say so here
     rather than leaving it to be found. -->
