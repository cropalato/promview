.PHONY: fmt fmt-check vet test test-postgres build verify-go verify-web verify-desktop verify verify-helm helm-lint helm-template helm-package compose-check migration-check docker-build

fmt:
	gofmt -w $$(find cmd internal -name '*.go')

fmt-check:
	test -z "$$(gofmt -l $$(find cmd internal -name '*.go'))"

vet:
	go vet ./cmd/... ./internal/...

test:
	go test ./cmd/... ./internal/...

test-postgres:
	go test ./internal/postgres -run 'TestPendingMigrations|TestStoreIngestAndList|TestStoreExpireStaleAlerts|TestStoreGroupAlerts|TestStorePreferences|TestStoreReconcileSource|TestStoreUpdateSource|TestStoreSilenceScope|TestStoreSilenceVisibility|TestStoreDesktopAuthCodes'

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

verify: verify-go verify-web verify-desktop compose-check verify-helm
