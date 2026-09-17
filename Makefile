.PHONY: fmt fmt-check vet test test-race test-postgres build verify-go verify-web verify-desktop verify verify-helm helm-lint helm-template helm-package compose-check migration-check load-test docker-build changelog-check docs-check vuln vuln-go vuln-web vuln-desktop

fmt:
	gofmt -w $$(find cmd internal -name '*.go')

fmt-check:
	test -z "$$(gofmt -l $$(find cmd internal -name '*.go'))"

vet:
	go vet ./cmd/... ./internal/...

test:
	go test ./cmd/... ./internal/...

# The server fans one ingestion out to every open console over SSE and runs the
# expiry and reconcile loops beside it, so its concurrency is not incidental.
# Kept out of `test` because the race build is several times slower, and out of
# `verify` for the same reason; CI runs it as its own job on every change.
test-race:
	go test -race ./cmd/... ./internal/...

# The scale the project plan commits to, measured rather than assumed. Writes
# tens of thousands of rows and takes minutes, so it is gated and deliberate:
# PROMVIEW_LOAD_ALERTS overrides the count.
load-test:
	PROMVIEW_LOAD_TEST=1 go test ./internal/postgres -run TestLoadAtCommittedScale -v -timeout 20m

test-postgres:
	go test ./internal/postgres -run 'TestPendingMigrations|TestStoreIngestAndList|TestStoreExpireStaleAlerts|TestStoreGroupAlerts|TestStorePreferences|TestStoreReconcileSource|TestStoreReviveExpiredAlerts|TestStoreUpdateSource|TestStoreSilenceScope|TestStoreSilenceVisibility|TestStoreSyncSilences|TestStoreDesktopAuthCodes|TestStorePruneStreamEvents|TestStoreAssignAlert|TestStoreAlertNotes|TestStoreCloseAlert|TestStoreBulkActions|TestStoreRoleBindingAdministration|TestStoreCreatesTheFirstAdministrator'

build:
	mkdir -p build
	go build -o build/promview ./cmd/promview

verify-go: fmt-check vet test build

verify-web:
	npm --prefix web run format:check
	npm --prefix web run lint
	npm --prefix web run typecheck
	npm --prefix web run test
	npm --prefix web run build

# The desktop shell. Kept out of verify-web: it is a Rust crate, and a web
# change should not wait on a cargo build to be checked.
verify-desktop:
	cd desktop/src-tauri && cargo fmt --check
	cd desktop/src-tauri && cargo clippy --all-targets -- -D warnings
	cd desktop/src-tauri && cargo test
	cd desktop/src-tauri && cargo build

compose-check:
	docker compose config --quiet

# The release notes are built from CHANGELOG.md by a script that otherwise
# runs once per release and nowhere else. This is what keeps a change to it
# from being exercised for the first time by the release that needs it.
changelog-check:
	./scripts/check-changelog.sh

# Installation examples that name a release older than the current one are how a
# reader ends up installing something other than what the page describes. It has
# happened four times; this is what stops the fifth.
docs-check:
	./scripts/check-doc-versions.sh

migration-check:
	./scripts/check-migrations.sh

docker-build:
	docker build -t promview:dev .

helm-lint:
	helm lint --strict charts/promview
	helm lint --strict charts/promview --values charts/promview/ci/oidc-values.yaml

helm-template:
	helm template promview charts/promview --namespace promview --kube-version 1.30.0 >/dev/null
	helm template promview charts/promview --namespace promview --kube-version 1.30.0 --values charts/promview/ci/oidc-values.yaml >/dev/null

helm-package:
	mkdir -p build
	helm package charts/promview --destination build

verify-helm: helm-lint helm-template helm-package

verify: verify-go verify-web verify-desktop compose-check verify-helm changelog-check docs-check

# Advisory scanning, deliberately not part of `verify`. It reads a database over
# the network, and an advisory published this morning would otherwise fail a
# local run of work that has nothing to do with it. CI runs it as its own job,
# where going red is the point.

# GOTOOLCHAIN=auto because govulncheck is built from source and its own go
# directive runs ahead of this module's - it currently needs 1.26 while the
# server is on 1.25. setup-go pins GOTOOLCHAIN=local in CI, which turns that
# into a build failure rather than a scan. Switching is scoped to this command,
# and only decides what compiles the scanner: govulncheck loads this module
# under its own go directive, so the analysis still sees the Go version that
# ships.
vuln-go:
	GOTOOLCHAIN=auto go run golang.org/x/vuln/cmd/govulncheck@latest ./cmd/... ./internal/...

# --omit=dev because the question is what ships. A vite or vitest advisory is
# worth knowing about and is not a vulnerability in the console anyone runs.
vuln-web:
	npm --prefix web audit --omit=dev --audit-level=high
	npm --prefix desktop audit --omit=dev --audit-level=high

vuln-desktop:
	cd desktop/src-tauri && cargo audit

vuln: vuln-go vuln-web vuln-desktop
