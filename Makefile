.PHONY: verify-layout dev down test test-go test-python test-web test-integration test-e2e smoke-claude purge-deleted

verify-layout:
	@test -f services/control-plane/go.mod
	@test -f services/agent-runtime/pyproject.toml
	@test -f apps/web/package.json
	@test -f docker-compose.yaml

dev:
	docker compose -f docker-compose.yaml up --build

down:
	docker compose -f docker-compose.yaml down

test: test-go test-python test-web

test-go:
	cd services/control-plane && go test ./...

test-python:
	cd services/agent-runtime && uv run pytest

test-web:
	cd apps/web && pnpm test -- --run

test-integration:
	docker compose -f docker-compose.yaml up -d --wait postgres minio
	TEST_DATABASE_URL='postgres://harness_forge:local-dev-only@localhost:5432/harness_forge?sslmode=disable' env -u GOROOT go -C services/control-plane test -tags=integration ./internal/postgres -v
