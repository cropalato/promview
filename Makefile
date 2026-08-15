.PHONY: fmt fmt-check vet test test-postgres build verify-go verify-web verify compose-check migration-check docker-build

fmt:
	gofmt -w $$(find cmd internal -name '*.go')

fmt-check:
	test -z "$$(gofmt -l $$(find cmd internal -name '*.go'))"

vet:
	go vet ./cmd/... ./internal/...

test:
	go test ./cmd/... ./internal/...

test-postgres:
	go test ./internal/postgres -run TestStoreIngestAndList

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

compose-check:
	docker compose config --quiet

migration-check:
	./scripts/check-migrations.sh

docker-build:
	docker build -t promview:dev .

verify: verify-go verify-web compose-check
