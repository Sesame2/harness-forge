.PHONY: verify-layout dev down test test-go test-python test-web test-integration test-e2e smoke-claude purge-deleted purge-deleted-dry-run

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
	cd apps/web && node --test scripts/smoke-claude.test.mjs

smoke-claude:
	@test -n "$$ANTHROPIC_API_KEY" || (echo 'ANTHROPIC_API_KEY is required' && exit 1)
	@export SANDBOX_PROVIDER=docker; status=0; \
	  docker compose -f docker-compose.yaml up -d --build --wait && \
	  (cd apps/web && pnpm exec playwright install chromium && node scripts/smoke-claude.mjs) || status=$$?; \
	  if [ $$status -ne 0 ]; then docker compose -f docker-compose.yaml logs --tail=200 control-plane agent-runtime; fi; \
	  $(MAKE) purge-deleted || { cleanup_status=$$?; if [ $$status -eq 0 ]; then status=$$cleanup_status; fi; }; \
	  exit $$status

test-integration:
	docker compose -f docker-compose.yaml up -d --wait postgres minio
	TEST_DATABASE_URL='postgres://harness_forge:local-dev-only@localhost:5432/harness_forge?sslmode=disable' env -u GOROOT go -C services/control-plane test -tags=integration ./internal/postgres -v

purge-deleted:
	docker compose -f docker-compose.yaml build control-plane
	docker compose -f docker-compose.yaml up -d --wait postgres minio agent-runtime
	docker compose -f docker-compose.yaml run --rm --no-deps minio-init
	docker compose -f docker-compose.yaml run --rm --no-deps control-plane /usr/local/bin/purge-deleted --apply

purge-deleted-dry-run:
	docker compose -f docker-compose.yaml build control-plane
	docker compose -f docker-compose.yaml up -d --wait postgres minio agent-runtime
	docker compose -f docker-compose.yaml run --rm --no-deps minio-init
	docker compose -f docker-compose.yaml run --rm --no-deps control-plane /usr/local/bin/purge-deleted --dry-run
