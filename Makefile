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

test-e2e:
	@set -eu; \
	  project=harness-forge-e2e; compose='$(abspath docker-compose.yaml)'; \
	  for resource in container volume network; do \
	    flags=-q; if [ "$$resource" = container ]; then flags=-aq; fi; \
	    existing=$$(docker $$resource ls $$flags --filter label=com.docker.compose.project=$$project); \
	    if [ -n "$$existing" ]; then \
	      echo "Refusing existing $$project $$resource resources; ownership must be checked before cleanup" >&2; exit 1; \
	    fi; \
	  done; \
	  export SANDBOX_PROVIDER=fake ANTHROPIC_API_KEY='' ANTHROPIC_BASE_URL=''; \
	  export WEB_PORT=15173 CONTROL_PLANE_PORT=18080 ARTIFACT_PORT=18081 RUNTIME_PORT=18090 POSTGRES_PORT=15432 MINIO_PORT=19000 MINIO_CONSOLE_PORT=19001; \
	  export WEB_ORIGIN=http://localhost:15173 ARTIFACT_PUBLIC_ORIGIN=http://localhost:18081; \
	  export POSTGRES_DB=harness_forge POSTGRES_USER=harness_forge POSTGRES_PASSWORD=local-dev-only; \
	  export DATABASE_URL='postgres://harness_forge:local-dev-only@postgres:5432/harness_forge?sslmode=disable'; \
	  export MINIO_ENDPOINT=http://minio:9000 MINIO_ROOT_USER=harness_forge MINIO_ROOT_PASSWORD=local-dev-only MINIO_ACCESS_KEY=harness_forge MINIO_SECRET_KEY=local-dev-only MINIO_BUCKET=harness-forge; \
	  export RUNTIME_URL=http://agent-runtime:8090 WORKSPACE_ROOT=/workspaces RUN_WORKSPACE_ROOT=/workspaces RUNTIME_STATE_ROOT=/sessions/executions CLAUDE_CONFIG_DIR=/sessions/claude; \
	  cleanup() { status=$$?; trap - EXIT; docker compose --env-file /dev/null -p $$project -f "$$compose" down -v --remove-orphans || { if [ $$status -eq 0 ]; then status=1; fi; }; exit $$status; }; \
	  trap cleanup EXIT; trap 'exit 130' INT; trap 'exit 143' TERM; \
	  docker compose --env-file /dev/null -p $$project -f "$$compose" up -d --build --wait; \
	  docker compose --env-file /dev/null -p $$project -f "$$compose" exec -T control-plane sh -ec 'test "$$SANDBOX_PROVIDER" = fake'; \
	  (cd tests/e2e && pnpm install --frozen-lockfile && pnpm exec playwright install chromium && BASE_URL=http://localhost:15173 pnpm exec playwright test $(E2E_ARGS))

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
