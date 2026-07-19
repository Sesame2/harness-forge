# Harness Forge V0 实施计划

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 2–4 周内交付本机单用户的 Harness Forge V0，跑通“上传 CSV → 多会话对话 → Claude Agent SDK 地理分析 → 不可变 HTML/ECharts 制品”的完整链路。

**Architecture:** Vue 3 Web 通过 REST/SSE 调用 Go 模块化单体；Go 以 PostgreSQL 为产品状态权威、以 MinIO 保存输入和制品，并通过版本化 NDJSON 协议调用常驻 Python Runtime。Runtime 每个 Run 启动一个 worker，使用 Claude SDK Session fork，并通过 commit/abort finalize 协议与 Go 协调。

**Tech Stack:** Go 1.25、chi、pgx、MinIO Go SDK、Vue 3、TypeScript、Vite、Pinia、Vitest、Playwright、Python 3.12、FastAPI、Pydantic、Claude Agent SDK、PostgreSQL、MinIO、Docker Compose。

---

## 执行约定

- 每个 Task 开始前使用 `@test-driven-development`；出现非预期失败时使用 `@systematic-debugging`。
- Web 视觉实现使用 `@frontend-design`，但不得扩大已批准的三栏 V0 范围。
- 每个 Task 结束前使用 `@verification-before-completion`，只提交该 Task 列出的文件。
- Go module path 固定为 `harness-forge.local/control-plane`；Python package 固定为 `harness_forge_runtime`。
- 所有命令从仓库根目录执行，除非步骤明确指定工作目录。
- 默认测试禁止访问真实 Claude API；只有 `make smoke-claude` 可以使用真实凭证。

## 文件职责总览

| 路径 | 职责 |
|---|---|
| `contracts/` | 浏览器、Go 与 Python 之间的稳定协议 |
| `services/control-plane/internal/runs` | Run 状态机、队列和 finalization 规则 |
| `services/control-plane/internal/agentexec` | HTTP Runtime 与 Fake Runtime 的唯一 seam |
| `services/control-plane/internal/artifacts` | Manifest 接受、对象发布与元数据可见性 |
| `services/agent-runtime/.../api.py` | Runtime 私有 HTTP 接口与单并发门禁 |
| `services/agent-runtime/.../execution_store.py` | execution record、tombstone 与幂等 disposition |
| `services/agent-runtime/.../runner.py` | 每 Run worker 入口与 NDJSON stdout |
| `services/agent-runtime/.../claude.py` | Claude Agent SDK adapter |
| `apps/web/src/features/*` | 按 Project、Conversation、Chat、Run、Artifact 切分的 UI 功能 |
| `tests/e2e/` | 使用 Fake Runtime 的完整用户链路 |

## Chunk 1：工程底座与可运行骨架

### Task 1：初始化三个语言模块与根命令

**Files:**
- Create: `Makefile`
- Create: `.env.example`
- Create: `docker-compose.yaml`
- Create: `services/control-plane/go.mod`
- Create: `services/control-plane/go.sum`
- Create: `services/control-plane/cmd/harness-forge/main.go`
- Create: `services/agent-runtime/pyproject.toml`
- Create: `services/agent-runtime/uv.lock`
- Create: `services/agent-runtime/src/harness_forge_runtime/__init__.py`
- Create: `apps/web/`（Vite Vue TypeScript scaffold）
- Create: `apps/web/pnpm-lock.yaml`
- Modify: `.gitignore`

- [ ] **Step 1: 记录失败的布局验证**

Run: `make verify-layout`

Expected: FAIL，提示 `No rule to make target 'verify-layout'`。

- [ ] **Step 2: 初始化并锁定依赖**

```bash
mkdir -p services/control-plane/cmd/harness-forge
cd services/control-plane
go mod init harness-forge.local/control-plane
go get github.com/go-chi/chi/v5 github.com/jackc/pgx/v5/pgxpool github.com/minio/minio-go/v7 github.com/stretchr/testify
cd ../..
uv init --lib --name harness-forge-runtime services/agent-runtime
cd services/agent-runtime
uv add fastapi 'uvicorn[standard]' pydantic pydantic-settings anyio httpx
uv add --dev pytest pytest-asyncio ruff mypy
uv sync --frozen
cd ../..
pnpm create vite apps/web --template vue-ts
cd apps/web
pnpm add pinia vue-router
pnpm add -D vitest @vue/test-utils jsdom @playwright/test
pnpm pkg set scripts.test=vitest
cd ../..
```

保留 `go.sum`、`services/agent-runtime/uv.lock` 与 `pnpm-lock.yaml`，不手写浮动依赖。

- [ ] **Step 3: 创建最小入口和合法 Compose 文件**

`services/control-plane/cmd/harness-forge/main.go`：

```go
package main

func main() {}
```

`uv init --lib` 生成带 build backend 的 `pyproject.toml` 和 `src/harness_forge_runtime/__init__.py`；确认 `uv run python -c 'import harness_forge_runtime'` 成功。将 Vite 生成的 `src/main.ts` 保持为可 build 的默认入口。`docker-compose.yaml` 在本 Task 先使用合法空拓扑：

```yaml
name: harness-forge
services: {}
```

- [ ] **Step 4: 实现根 Makefile 最小命令**

```make
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
```

- [ ] **Step 5: 增加环境示例和忽略项**

`.env.example`：

```dotenv
POSTGRES_DB=harness_forge
POSTGRES_USER=harness_forge
POSTGRES_PASSWORD=local-dev-only
MINIO_ROOT_USER=harness_forge
MINIO_ROOT_PASSWORD=local-dev-only
MINIO_BUCKET=harness-forge
ANTHROPIC_API_KEY=
ANTHROPIC_BASE_URL=
```

`.gitignore` 增加 `.venv/`、`node_modules/`、`dist/`、`.data/`、Playwright 输出和 Python cache。

- [ ] **Step 6: 验证并提交工程骨架**

```bash
make verify-layout
go -C services/control-plane test ./...
cd services/agent-runtime && uv run python -c 'import harness_forge_runtime'
cd ../../apps/web && pnpm build
cd ../..
git add Makefile .env.example docker-compose.yaml .gitignore services apps
git diff --cached --check
git diff --cached --name-status
git commit -m "build: initialize Harness Forge modules"
```

Expected: 验证命令 exit 0，提交只包含本 Task 文件。

### Task 2：固化版本化协议

**Files:**
- Create: `contracts/runtime/v1/run-request.schema.json`
- Create: `contracts/runtime/v1/runtime-event.schema.json`
- Create: `contracts/artifacts/v1/artifact-manifest.schema.json`
- Create: `contracts/control-plane.openapi.yaml`
- Create: `services/control-plane/internal/contracts/contracts.go`
- Create: `services/control-plane/internal/contracts/contracts_test.go`
- Create: `services/agent-runtime/src/harness_forge_runtime/models.py`
- Create: `services/agent-runtime/tests/test_contract_fixtures.py`
- Create: `tests/fixtures/contracts/run-request.json`
- Create: `tests/fixtures/contracts/runtime-events.ndjson`
- Create: `tests/fixtures/contracts/artifact-manifest.json`
- Modify: `services/control-plane/go.mod`
- Modify: `services/control-plane/go.sum`

- [ ] **Step 1: 写失败的跨语言 fixture 测试**

先添加用于测试 JSON Schema 与 OpenAPI 的依赖：

```bash
cd services/control-plane
go get github.com/santhosh-tekuri/jsonschema/v6 github.com/getkin/kin-openapi/openapi3
cd ../..
```

Go 测试必须分别加载并验证 Run Request、每一行 Runtime Event、Artifact Manifest 和 OpenAPI。Runtime Event 断言示例：

```go
func TestRuntimeEventFixture(t *testing.T) {
    events := LoadNDJSONFixture(t, "runtime-events.ndjson")
    require.Equal(t, "1", events[0].Version)
    require.Equal(t, "phase.changed", events[0].Type)
    require.Equal(t, "agent.completed", events[len(events)-1].Type)
}
```

Python 测试逐行调用 `RuntimeEvent.model_validate_json`，并用 `RunRequest`、`ArtifactManifest` 解析另外两个 fixture。另增加 unknown event fixture，证明 envelope 可解析但不会被当成 terminal event。

- [ ] **Step 2: 运行测试确认失败**

```bash
go -C services/control-plane test ./internal/contracts -v
cd services/agent-runtime && uv run pytest tests/test_contract_fixtures.py -v
```

Expected: FAIL，fixture loader 或 model 未定义。

- [ ] **Step 3: 实现完整 Run Request schema**

`run-request.schema.json` 必须要求：

```json
{
  "version": "1",
  "run_id": "uuid",
  "project_id": "uuid",
  "conversation_id": "uuid",
  "prompt": "non-empty string",
  "source_sdk_session_id": null,
  "profile": {"id": "geo-analysis", "version": "1", "digest": "sha256", "config": {}},
  "paths": {"inputs": "/runs/id/inputs", "workspace": "/runs/id/workspace", "outputs": "/runs/id/outputs"},
  "limits": {"max_turns": 8, "max_budget_usd": 2.0}
}
```

所有 ID 使用 UUID format，容器路径要求绝对路径，prompt 非空，`source_sdk_session_id` 可空，profile digest/config 与 limits 必须存在；禁止未知顶层字段。

- [ ] **Step 4: 实现前向兼容 V1 event envelope**

```json
{
  "version": "1",
  "run_id": "00000000-0000-0000-0000-000000000001",
  "sequence": 1,
  "type": "phase.changed",
  "occurred_at": "2026-07-19T00:00:00Z",
  "payload": {"phase": "agent"}
}
```

`type` 在基础 envelope 中是非空字符串，不使用封闭 enum。对八种已知 type，JSON Schema 使用 `if type=... then payload=$ref`，Go 语义 validator 与 Pydantic model 使用同一组 typed payload；未知 type 保留通用 object payload，能解析但作为非终态忽略。

| Event type | Required payload |
|---|---|
| `phase.changed` | `{phase: "preparing"\|"agent"}` |
| `assistant.delta` | `{text: string}` |
| `assistant.message` | `{text: string}` |
| `tool.started` | `{tool_call_id: string, name: string, input: object}` |
| `tool.completed` | `{tool_call_id: string, name: string, outcome: "succeeded"\|"failed", output?: string, error?: string}`；failed 必须有 error |
| `artifact.candidate` | `{artifacts: ArtifactCandidate[]}` |
| `agent.completed` | `{candidate_sdk_session_id: non-empty string, artifacts: ArtifactCandidate[]}` |
| `agent.failed` | `{code: non-empty string, message: non-empty string, retryable: boolean}` |

`ArtifactCandidate` 固定字段为 `{name,title,type,entry,primary}`，其中 `type` 仅允许 `html|markdown|image|data`，其余含义和限制与 Manifest item 相同；Manifest item 使用相同 enum。`agent.completed.artifacts` 必须与最后一个 `artifact.candidate.artifacts` 一致；没有可发布制品时两者均为空数组。`runtime-events.ndjson` 至少各含一条八种已知事件，合同测试逐条验证 typed payload 的正例和“缺少必填字段/错误类型/未知 Artifact type”的反例。只有已知 `agent.completed/agent.failed` 能结束 Agent execution。

- [ ] **Step 5: 实现 Manifest schema 和完整 Browser OpenAPI**

Manifest 要求 `schema_version: 1`、唯一非空 `name`、相对 `entry`、最多一个 primary。JSON Schema 能限制结构和路径形状；“唯一 name/最多一个 primary”由 contracts 测试中的语义 validator 验证。

`control-plane.openapi.yaml` 固定下列接口，所有 JSON response 都引用 components 中的 schema，而非自由对象：

| Method | Path | Request | Success | Required errors |
|---|---|---|---|---|
| GET, POST | `/api/v1/projects` | POST `CreateProject{name,profile_id}` | 200 list / 201 Project | 400 |
| GET, PATCH, DELETE | `/api/v1/projects/{project_id}` | PATCH `Rename{name}` | 200 Project / 204 | 404, 409 |
| GET, POST | `/api/v1/projects/{project_id}/inputs` | POST multipart `file` | 200 list / 201 InputFile | 400, 404, 409, 413 |
| GET, POST | `/api/v1/projects/{project_id}/conversations` | POST `CreateConversation{title?}` | 200 list / 201 Conversation | 400, 404, 409 |
| GET, PATCH, DELETE | `/api/v1/conversations/{conversation_id}` | PATCH `RenameConversation{title}` | 200 Conversation / 204 | 404, 409 |
| GET, POST | `/api/v1/conversations/{conversation_id}/messages` | POST `SubmitMessage{content}` | 200 list / 201 `SubmitMessageResult{message,run}` | 400, 404, 409 |
| GET | `/api/v1/conversations/{conversation_id}/runs` | — | 200 `Run[]`（created_at 升序） | 404 |
| GET | `/api/v1/runs/{run_id}` | — | 200 Run | 404 |
| GET | `/api/v1/runs/{run_id}/events` | query `after_sequence` optional uint64 | 200 `RunEvent[]` | 400, 404 |
| GET | `/api/v1/runs/{run_id}/events/stream` | query `after_sequence` or `Last-Event-ID` header | 200 `text/event-stream` | 400, 404 |
| POST | `/api/v1/runs/{run_id}/cancel` | empty | 202 Run | 404, 409 |
| GET | `/api/v1/runs/{run_id}/artifacts` | — | 200 `Artifact[]` | 404 |

OpenAPI 版本使用 3.1.0；所有对象 `additionalProperties: false`，下表字段全部 required，标为 `?` 的字段使用 `type: [<base>, "null"]`：

| Component | Exact fields |
|---|---|
| `Project` | `id:uuid`, `name:string`, `profile_id:string`, `profile_version:string`, `accepted_input_media_types:string[]`, `created_at:date-time`, `updated_at:date-time` |
| `InputFile` | `id:uuid`, `project_id:uuid`, `display_name:string`, `media_type:string`, `size_bytes:int64`, `sha256_digest:string`, `created_at:date-time` |
| `Conversation` | `id:uuid`, `project_id:uuid`, `title:string`, `active_sdk_session_id?:string`, `created_at:date-time`, `updated_at:date-time` |
| `Message` | `id:uuid`, `conversation_id:uuid`, `role:user\|assistant`, `content:string`, `created_at:date-time` |
| `Run` | `id:uuid`, `conversation_id:uuid`, `trigger_message_id:uuid`, `status:queued\|running\|succeeded\|failed\|cancelled\|interrupted`, `phase?:preparing\|agent\|publishing`, `error?:Error`, `source_sdk_session_id?:string`, `candidate_sdk_session_id?:string`, `finalized_at?:date-time`, `created_at:date-time`, `updated_at:date-time` |
| `RunEvent` | `run_id:uuid`, `sequence:uint64`, `type:string`, `payload:object`, `occurred_at:date-time` |
| `Artifact` | `id:uuid`, `run_id:uuid`, `title:string`, `type:html\|markdown\|image\|data`, `entry_path:string`, `is_primary:boolean`, `gateway_url:uri`, `created_at:date-time` |
| `SubmitMessageResult` | `message:Message`, `run:Run` |
| `Error` | `code:string`, `message:string`, `details?:object`, `request_id:string` |

Request components 精确定义为 `CreateProject{name,profile_id}`、`RenameProject{name}`、`CreateConversation{title?}`、`RenameConversation{title}`、`SubmitMessage{content}`，字符串 trim 后非空（可选初始 title 除外）。列表 response 是对应 component 的 JSON array，不增加分页 envelope。所有 mutation 的 400/404/409/413 使用统一 Error。SSE operation 描述恢复优先级：同时提供时 `Last-Event-ID` 优先，返回 frame 的 `id` 等于 durable sequence。Go 测试用 `openapi3.Loader` 加载、执行 `Validate`，并逐项断言上表 operation/status/media type、每个 component 的 required/nullability 和 `$ref` 关系存在。

- [ ] **Step 6: 验证所有协议及反例并提交**

```bash
go -C services/control-plane test ./internal/contracts -v
cd services/agent-runtime && uv run pytest tests/test_contract_fixtures.py -v
cd ../..
go -C services/control-plane test ./internal/contracts -run 'RejectsInvalid' -v
git add contracts tests/fixtures services/control-plane/go.mod services/control-plane/go.sum services/control-plane/internal/contracts services/agent-runtime/src/harness_forge_runtime/models.py services/agent-runtime/tests/test_contract_fixtures.py
git diff --cached --check
git commit -m "feat: define versioned Harness contracts"
```

### Task 3：启动基础设施与 health

**Files:**
- Modify: `.env.example`
- Modify: `docker-compose.yaml`
- Modify: `services/control-plane/cmd/harness-forge/main.go`
- Create: `infra/minio/create-bucket.sh`
- Create: `services/control-plane/Dockerfile`
- Create: `services/agent-runtime/Dockerfile`
- Create: `apps/web/Dockerfile`
- Create: `services/control-plane/internal/config/config.go`
- Create: `services/control-plane/internal/config/config_test.go`
- Create: `services/control-plane/internal/httpapi/router.go`
- Create: `services/control-plane/internal/httpapi/health.go`
- Create: `services/control-plane/internal/httpapi/health_test.go`
- Create: `services/agent-runtime/src/harness_forge_runtime/api.py`
- Create: `services/agent-runtime/tests/test_health.py`
- Create: `apps/web/src/app/health.ts`
- Create: `apps/web/src/app/health.test.ts`
- Modify: `apps/web/vite.config.ts`

- [ ] **Step 1: 写配置和三个 health 失败测试**

- Go 配置测试使用注入的 `getenv`，覆盖：完整变量映射、`HTTP_ADDR=:8080`/`ARTIFACT_ADDR=:8081` 默认值、缺少 `DATABASE_URL`/MinIO 凭证的具名错误、非法 `WEB_ORIGIN` URL。
- Go `GET /health` 期望 `200 {"status":"ok"}`。
- Python `GET /health` 期望 `200 {"status":"ok","active_run_id":null}`。
- Web `loadHealth()` 固定请求同源 `/health`，对非 2xx 抛出带 status 的错误；测试断言请求 URL。

- [ ] **Step 2: 运行测试确认失败**

```bash
go -C services/control-plane test ./internal/config ./internal/httpapi -v
cd services/agent-runtime && uv run pytest tests/test_health.py -v
cd ../../apps/web && pnpm test -- --run src/app/health.test.ts
```

- [ ] **Step 3: 实现配置、handler 与 Compose**

Go 使用 `ConfigFromEnv(getenv func(string) string) (Config, error)`，字段固定为 `HTTPAddr`、`ArtifactAddr`、`DatabaseURL`、`MinIOEndpoint`、`MinIOAccessKey`、`MinIOSecretKey`、`MinIOBucket`、`RuntimeURL`、`WebOrigin`。`httpapi.NewRouter()` 注册 `/health`，`main.go` 加载配置并在 `HTTPAddr` 启动 HTTP server。Python 使用 FastAPI application factory，容器入口固定为：

```bash
uv run uvicorn harness_forge_runtime.api:create_app --factory --host 0.0.0.0 --port 8090
```

三个 Dockerfile 的内容必须落到以下确定版本和命令：

| File | Build/runtime | Install | Command |
|---|---|---|---|
| control-plane | `golang:1.25.0-alpine` builder → `alpine:3.22` + curl | `go mod download`，`CGO_ENABLED=0 go build ./cmd/harness-forge` | `/usr/local/bin/harness-forge` |
| agent-runtime | `python:3.12.11-slim-bookworm` + curl + `uv` | `uv sync --frozen --no-dev` | 上述 `uv run --no-sync uvicorn ...` |
| web | `node:22.17.1-alpine` + Corepack | `pnpm install --frozen-lockfile` | `pnpm dev --host 0.0.0.0` |

Vite 将 `/api` 和 `/health` 都代理到 `control-plane:8080`；`/health` 仅用于 Compose/dev readiness。三个 Dockerfile 只复制对应模块，均使用已提交 lockfile。

`infra/minio/create-bucket.sh` 必须可执行且幂等：

```sh
#!/bin/sh
set -eu
until mc alias set local http://minio:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD"; do
  sleep 1
done
mc mb --ignore-existing "local/$MINIO_BUCKET"
```

创建后执行 `chmod +x infra/minio/create-bucket.sh`。Compose 拓扑严格按下表实现：

| Service | Image/build and command | Ports | Environment | Volumes/dependency/health |
|---|---|---|---|---|
| postgres | `postgres:17.5-alpine` | 5432 | 三个 `POSTGRES_*` | `postgres-data`; `pg_isready` |
| minio | `quay.io/minio/minio:RELEASE.2025-04-22T22-12-26Z server /data --console-address :9001` | 9000, 9001 | 两个 `MINIO_ROOT_*` | `minio-data`; `curl /minio/health/live` |
| minio-init | `quay.io/minio/mc:RELEASE.2025-04-16T18-13-26Z` + script | — | root credentials + bucket | depends MinIO healthy; bind script read-only; restart `no` |
| agent-runtime | local Dockerfile | 8090 | `ANTHROPIC_*`, workspace/session roots | `run-workspaces:/workspaces`, `runtime-sessions:/sessions`; HTTP health |
| control-plane | local Dockerfile | 8080, 8081 | `DATABASE_URL`, all MinIO client values, `RUNTIME_URL`, `WEB_ORIGIN` | shared workspaces; waits Postgres healthy + minio-init completed; HTTP health |
| web | local Dockerfile | 5173 | none | waits Go healthy; `wget http://localhost:5173/` |

`.env.example` 补全 `DATABASE_URL`、`MINIO_ENDPOINT`、`MINIO_ACCESS_KEY`、`MINIO_SECRET_KEY`、`RUNTIME_URL`、`WEB_ORIGIN`。只有 Runtime 接收 Claude 变量；只有 Go 接收 PostgreSQL/MinIO 变量。命名 volumes 为 `postgres-data`、`minio-data`、`run-workspaces`、`runtime-sessions`。

- [ ] **Step 4: 验证并提交服务拓扑**

```bash
docker compose -f docker-compose.yaml config --quiet
docker compose -f docker-compose.yaml up -d --build --wait
curl -fsS http://localhost:8080/health
curl -fsS http://localhost:8090/health
curl -fsS http://localhost:5173/
curl -fsS http://localhost:5173/health
docker compose -f docker-compose.yaml ps
docker compose -f docker-compose.yaml down
git add .env.example docker-compose.yaml infra services/control-plane/cmd services/control-plane/Dockerfile services/control-plane/internal/config services/control-plane/internal/httpapi services/agent-runtime apps/web
git diff --cached --check
git commit -m "feat: add local service topology and health checks"
```

Expected: Go、Runtime 及经 Web 同源代理访问的 health 均返回 `status=ok`，Web 首页返回 2xx，`minio-init` 为 `Exited (0)`，其余服务 healthy；Compose 正常清理。

### Task 4：建立数据库迁移与集成测试夹具

**Files:**
- Create: `services/control-plane/migrations/00001_initial.sql`
- Create: `services/control-plane/migrations/embed.go`
- Create: `services/control-plane/internal/postgres/db.go`
- Create: `services/control-plane/internal/postgres/migrate.go`
- Create: `services/control-plane/internal/postgres/integration_test.go`
- Create: `services/control-plane/internal/testsupport/postgres.go`
- Modify: `services/control-plane/cmd/harness-forge/main.go`
- Modify: `Makefile`

- [ ] **Step 1: 写隔离且可重复的 migration 集成测试**

`testsupport.NewPostgresSchema(t, TEST_DATABASE_URL)` 为每个测试创建随机 schema、设置 `search_path`，并通过 `t.Cleanup` 删除 schema，避免复用本地数据库状态造成假阳性。测试对同一 schema 连续运行 migration 两次，断言 schema version 不增加，并检查以下不变量：

`integration_test.go` 第一行必须是 `//go:build integration`，确保默认 `go test ./...` 不访问数据库。

- `projects`、`input_files`、`conversations`、`messages`、`runs`、`run_events`、`artifacts` 七张表存在；
- 所有父子关系有外键，Project/Conversation 有 `deleted_at`；
- Run 有 `status`、`phase`、`finalized_at`、source/candidate SDK Session ID；
- Run status/phase 有 CHECK，`run_events(run_id, sequence)` 唯一；
- 待运行队列存在以 `status, created_at` 开头的 FIFO 索引。

- [ ] **Step 2: 启动测试数据库并确认失败**

```bash
docker compose -f docker-compose.yaml up -d --wait postgres
TEST_DATABASE_URL='postgres://harness_forge:local-dev-only@localhost:5432/harness_forge?sslmode=disable' go -C services/control-plane test -tags=integration ./internal/postgres -v
```

Expected: FAIL，migration runner 或表不存在。

- [ ] **Step 3: 实现 schema、嵌入迁移与显式 runner**

创建设计规格中的七张表及上一步全部约束。状态使用 text + CHECK。`migrations/embed.go` 通过 `//go:embed *.sql` 暴露 migration FS；runner 以 advisory lock + schema version table 保证并发安全和幂等，并让测试可传入目标 schema。`main.go` 在监听端口前连接数据库并显式调用 migration，不在任何领域 package `init()` 中执行。

在 Makefile 实现确定性的集成测试命令：

```make
test-integration:
	docker compose -f docker-compose.yaml up -d --wait postgres minio
	TEST_DATABASE_URL='postgres://harness_forge:local-dev-only@localhost:5432/harness_forge?sslmode=disable' go -C services/control-plane test -tags=integration ./internal/postgres -v
```

- [ ] **Step 4: 验证并提交数据库底座**

```bash
make test-integration
TEST_DATABASE_URL='postgres://harness_forge:local-dev-only@localhost:5432/harness_forge?sslmode=disable' go -C services/control-plane test -tags=integration ./internal/postgres -v
find services/control-plane -name '*.go' -print0 | xargs -0 gofmt -w
go -C services/control-plane test ./...
git add services/control-plane/migrations services/control-plane/internal/postgres services/control-plane/internal/testsupport services/control-plane/cmd Makefile
git diff --cached --check
git commit -m "feat: add initial product database schema"
```

## Chunk 2：Go 控制平面垂直链路

### Task 5：实现 Project、Input File 与对象存储

**Files:**
- Modify: `.env.example`
- Modify: `docker-compose.yaml`
- Modify: `services/control-plane/Dockerfile`
- Modify: `services/control-plane/go.mod`
- Modify: `services/control-plane/go.sum`
- Modify: `services/control-plane/internal/config/config.go`
- Modify: `services/control-plane/internal/config/config_test.go`
- Create: `services/control-plane/internal/profiles/profile.go`
- Create: `services/control-plane/internal/profiles/resolver.go`
- Create: `services/control-plane/internal/profiles/resolver_test.go`
- Create: `profiles/geo-analysis/profile.yaml`
- Create: `profiles/geo-analysis/system-prompt.md`
- Create: `profiles/geo-analysis/workspace-template/README.md`
- Create: `services/control-plane/internal/projects/model.go`
- Create: `services/control-plane/internal/projects/service.go`
- Create: `services/control-plane/internal/projects/service_test.go`
- Create: `services/control-plane/internal/projects/store.go`
- Create: `services/control-plane/internal/projects/store_integration_test.go`
- Create: `services/control-plane/internal/objectstore/store.go`
- Create: `services/control-plane/internal/objectstore/minio.go`
- Create: `services/control-plane/internal/objectstore/memory.go`
- Create: `services/control-plane/internal/httpapi/projects.go`
- Create: `services/control-plane/internal/httpapi/projects_test.go`
- Modify: `services/control-plane/internal/httpapi/router.go`
- Modify: `services/control-plane/cmd/harness-forge/main.go`

- [ ] **Step 1: 写 Profile、Project、上传和删除保护的失败测试**

先执行 `go -C services/control-plane get gopkg.in/yaml.v3`。Profile resolver 测试覆盖：读取 `profile.yaml`/`system-prompt.md`/`workspace-template`、解析 ID/version/accepted input types/allowed Artifact types/agent limits，以及 Artifact `max_file_bytes=10485760`、`max_total_bytes=52428800`；覆盖不存在 ID、YAML 内 ID 与目录不一致，以及修改任一 config/prompt/template byte 都改变 digest。这两个 byte limit 是 immutable Profile snapshot 的必填字段，并随 ExecuteRequest 传给 Runtime。

Project 测试覆盖 `CreateProject` 将 resolver 返回的 `profile_id/profile_version` 固化到 row、未知 Profile 返回 400、`ReadProject`、`RenameProject`、默认列表隐藏已删除记录、同名但 ID 不同；所有 Project response 由 resolver 补充 `accepted_input_media_types`，供浏览器选择文件，DB 不复制该数组。上传覆盖允许的 `text/csv`/`application/geo+json`、拒绝 Profile 未允许 media type、流式写入、SHA-256 digest、文件元数据事务失败时删除已上传对象、逻辑删除后拒绝上传。删除 Project 时，只要其任一 Run 为 queued/running 或 `finalized_at IS NULL` 就返回 typed conflict；安全时设置 `deleted_at`，重复删除幂等。

`store_integration_test.go` 首行为 `//go:build integration`，使用 Task 4 的随机 schema 夹具验证真实 PostgreSQL 查询、回滚和删除保护，不用 mock SQL。并发 race test 固定锁顺序为 Project row：上传先分配 Input ID 并把 object 直接写到规格最终 key，再在 transaction 中 `SELECT Project FOR UPDATE`、确认 `deleted_at IS NULL`、写 metadata；删除也先锁 Project row。若删除先提交，上传拒绝并删除这个精确 object key；若上传先提交，删除随后可见完整 Input，不得出现 deleted Project 的 late Input。

- [ ] **Step 2: 运行目标测试确认失败**

Run: `go -C services/control-plane test ./internal/profiles ./internal/projects ./internal/httpapi -run 'Profile|Project|InputFile' -v`

Run: `TEST_DATABASE_URL='postgres://harness_forge:local-dev-only@localhost:5432/harness_forge?sslmode=disable' go -C services/control-plane test -tags=integration ./internal/projects -run 'UploadDeleteRace' -v`

Expected: FAIL，service/handler 未定义。

- [ ] **Step 3: 实现深 Project module、对象 seam 和真实 adapter**

调用方只使用 `CreateProject`、`ReadProject`、`RenameProject`、`ListProjects`、`UploadInput`、`ListInputs`、`DeleteProject`。Create 与 Upload 都经同一个 immutable Profile snapshot resolver；Upload 根据 Project 已固化版本解析，版本目录/配置已丢失时 fail closed。对象 `Store` 只含 `Put(key, reader, PutOptions{ContentType})`、`Open`、`Delete(key)`、`DeletePrefix`、`Stat`；MinIO adapter 用最终 `projects/{project_id}/inputs/{input_file_id}/{filename}` key，DB failure/deleted race 用 `Delete` 精确补偿，memory adapter 仅用于模块测试。不创建通用 Repository interface，`projects.Store` 是具体 pgx store。

- [ ] **Step 4: 注册 handler、注入真实依赖并验证**

在 router 注册 Task 2 OpenAPI 的 Project/Input 全部路由。`main.go` 创建 Profile resolver、pgx pool 和 MinIO client，将具体 `projects.Service` 注入 router；启动失败必须返回非零且不监听半初始化服务。

本 Task 把 Control Plane build context 改为仓库根；Dockerfile 只复制 `services/control-plane/`、`contracts/`、`profiles/` 到 `/app`。Compose/`.env.example` 增加 `PROFILE_ROOT=/app/profiles`，config 测试覆盖该绝对路径。Task 8 只需追加 Fake fixture COPY。

```bash
go -C services/control-plane test ./internal/profiles ./internal/projects ./internal/httpapi -run 'Profile|Project|InputFile' -v
TEST_DATABASE_URL='postgres://harness_forge:local-dev-only@localhost:5432/harness_forge?sslmode=disable' go -C services/control-plane test -tags=integration ./internal/projects -v
go -C services/control-plane test ./...
git add .env.example docker-compose.yaml profiles/geo-analysis services/control-plane/Dockerfile services/control-plane/go.mod services/control-plane/go.sum services/control-plane/internal/config services/control-plane/internal/profiles services/control-plane/internal/projects services/control-plane/internal/objectstore services/control-plane/internal/httpapi services/control-plane/cmd
git diff --cached --check
git commit -m "feat: manage projects and input files"
```

### Task 6：实现 Run 状态机、持久队列与 Event store

**Files:**
- Create: `services/control-plane/internal/runs/model.go`
- Create: `services/control-plane/internal/runs/state.go`
- Create: `services/control-plane/internal/runs/state_test.go`
- Create: `services/control-plane/internal/runs/store.go`
- Create: `services/control-plane/internal/runs/store_integration_test.go`
- Create: `services/control-plane/internal/runs/events.go`
- Create: `services/control-plane/internal/runs/events_test.go`
- Create: `services/control-plane/migrations/00002_run_event_sequences.sql`

- [ ] **Step 1: 将终态矩阵写成失败的表驱动单元测试**

每个 case 明确 initial status/phase、Runtime disposition、expected status 和 expected finalized；完整覆盖 queued cancel、prepare fail、agent fail、publish fail、active cancel、restart interrupt、success commit，以及所有非法转换。

- [ ] **Step 2: 运行纯状态测试，确认红灯后实现**

```bash
go -C services/control-plane test ./internal/runs -run 'Transition|Finalization' -v
```

实现无数据库副作用的转换函数；返回新 Run 值和 typed domain error。`finalized_at` 只能由规格矩阵允许的 acknowledgement/无 Runtime state 路径设置。重跑并确认通过。

- [ ] **Step 3: 写数据库队列/Event 失败测试并确认红灯**

`store_integration_test.go` 首行为 `//go:build integration`。覆盖：两个连接并发 claim 时全局只领取一个 Run；存在 status 非 queued 且未 finalized 的当前 Run 时不领取；等待中的 queued Runs 不触发该阻断条件；`CreateQueuedTx` 可加入调用方事务；Event durable sequence 严格递增；重复 `(run_id,runtime_sequence)` 幂等返回既有 Event。

```bash
docker compose -f docker-compose.yaml up -d --wait postgres
TEST_DATABASE_URL='postgres://harness_forge:local-dev-only@localhost:5432/harness_forge?sslmode=disable' go -C services/control-plane test -tags=integration ./internal/runs -v
```

Expected: FAIL，migration/store 尚不存在。

- [ ] **Step 4: 新增 migration 并实现 store**

不得修改已执行的 `00001_initial.sql`。`00002` 增加 `runs.next_event_sequence BIGINT NOT NULL DEFAULT 1`、`run_events.runtime_sequence BIGINT NULL` 和 partial unique `(run_id,runtime_sequence) WHERE runtime_sequence IS NOT NULL`。Append Event 时锁定 Run row、分配并递增 durable sequence。

每次 claim transaction 先取得固定 transaction advisory lock `pg_advisory_xact_lock(hashtextextended('harness-forge:run-claim', 0))`，再检查 `status <> 'queued' AND finalized_at IS NULL`，最后对最早 queued row 使用 `FOR UPDATE SKIP LOCKED`。并发测试让第一个 transaction 持锁暂停，证明第二个无法越过锁 claim 另一项；第一个提交 running 后，第二个醒来并因 unfinalized running Run 返回 no claim。

- [ ] **Step 5: 验证并提交 Run 底座**

```bash
go -C services/control-plane test ./internal/runs -v
TEST_DATABASE_URL='postgres://harness_forge:local-dev-only@localhost:5432/harness_forge?sslmode=disable' go -C services/control-plane test -tags=integration ./internal/runs -v
git add services/control-plane/internal/runs services/control-plane/migrations/00002_run_event_sequences.sql
git diff --cached --check
git commit -m "feat: add durable run state machine and queue"
```

### Task 7：实现 Conversation、Message 与原子建 Run

**Files:**
- Create: `services/control-plane/internal/conversations/model.go`
- Create: `services/control-plane/internal/conversations/service.go`
- Create: `services/control-plane/internal/conversations/service_test.go`
- Create: `services/control-plane/internal/conversations/store.go`
- Create: `services/control-plane/internal/conversations/store_integration_test.go`
- Create: `services/control-plane/internal/httpapi/conversations.go`
- Create: `services/control-plane/internal/httpapi/conversations_test.go`
- Modify: `services/control-plane/internal/httpapi/router.go`
- Modify: `services/control-plane/cmd/harness-forge/main.go`

- [ ] **Step 1: 写 module、HTTP 和真实事务失败测试**

覆盖一个 Project 多个 Conversation、按 `updated_at` 排序、读取、重命名、逻辑删除、第一条 Message 自动标题、删除后拒绝新 Message。删除目标有 queued/running/unfinalized Run 时返回 409，不存在返回 404，重复逻辑删除返回 204。

integration test 首行为 `//go:build integration`：强制 queued Run insert 失败时 Message 不存在；强制 Message insert 失败时 Run 不存在；成功时二者同时可见。并发 Submit/Delete race 统一按 Project row → Conversation row 加 `FOR UPDATE`：delete 先提交时 Submit 返回 deleted conflict；Submit 先提交时 delete 看到 queued Run 返回 conflict，绝不产生已删除 Conversation 的 late Message/Run。

- [ ] **Step 2: 运行测试确认失败**

```bash
go -C services/control-plane test ./internal/conversations ./internal/httpapi -run 'Conversation|Message' -v
TEST_DATABASE_URL='postgres://harness_forge:local-dev-only@localhost:5432/harness_forge?sslmode=disable' go -C services/control-plane test -tags=integration ./internal/conversations -v
```

- [ ] **Step 3: 实现 Conversation module 和原子提交**

`SubmitMessage` 开启一个 pgx transaction，按固定顺序锁 Project 和 Conversation row 并确认二者 `deleted_at IS NULL`，写 user Message，再调用 Task 6 的 `runs.Store.CreateQueuedTx`，提交后返回 `{message,run}`。Project/Conversation delete 使用同一锁顺序。标题算法只截取规范化后第一条 Message 的前 40 个 Unicode code point，不调用模型。不得在 Conversation package 重写 Runs SQL。

- [ ] **Step 4: 注册全部 Conversation/Message 路由并提交**

```bash
go -C services/control-plane test ./internal/conversations ./internal/httpapi -v
TEST_DATABASE_URL='postgres://harness_forge:local-dev-only@localhost:5432/harness_forge?sslmode=disable' go -C services/control-plane test -tags=integration ./internal/conversations -v
git add services/control-plane/internal/conversations services/control-plane/internal/httpapi services/control-plane/cmd
git diff --cached --check
git commit -m "feat: add project conversations and messages"
```

### Task 8：实现 Runtime seam、Fake Runtime 与 SSE

**Files:**
- Modify: `.env.example`
- Modify: `docker-compose.yaml`
- Modify: `services/control-plane/Dockerfile`
- Modify: `services/control-plane/internal/config/config.go`
- Modify: `services/control-plane/internal/config/config_test.go`
- Create: `services/control-plane/internal/agentexec/executor.go`
- Create: `services/control-plane/internal/agentexec/http.go`
- Create: `services/control-plane/internal/agentexec/http_test.go`
- Create: `services/control-plane/internal/agentexec/fake.go`
- Create: `services/control-plane/internal/runs/broker.go`
- Modify: `services/control-plane/internal/runs/store.go`
- Modify: `services/control-plane/internal/runs/store_integration_test.go`
- Create: `services/control-plane/internal/runs/cancel.go`
- Create: `services/control-plane/internal/runs/cancel_test.go`
- Create: `services/control-plane/internal/httpapi/runs.go`
- Create: `services/control-plane/internal/httpapi/runs_test.go`
- Create: `services/control-plane/internal/httpapi/run_events.go`
- Create: `services/control-plane/internal/httpapi/run_events_test.go`
- Modify: `services/control-plane/internal/httpapi/router.go`
- Modify: `services/control-plane/cmd/harness-forge/main.go`
- Create: `tests/fixtures/fake-runtime/geo-report/events.ndjson`
- Create: `tests/fixtures/fake-runtime/geo-report/outputs/artifact-manifest.json`
- Create: `tests/fixtures/fake-runtime/geo-report/outputs/report/index.html`

- [ ] **Step 1: 写完整 HTTP Runtime adapter 失败测试**

使用 `httptest.Server` 验证 Execute request 包含 Profile snapshot、source Session 与绝对路径；逐行解析 typed event；非 2xx 转 typed Runtime error。逐一覆盖 `Cancel`、幂等 `Finalize`、`ListExecutions` active/unfinalized 解析、`SessionExists` 的 200/404/异常、`DeleteExecution` 和 `DeleteSession`。

配置测试新增 `RUNTIME_MODE` 只允许 `http|fake`，默认 `http`；Fake 测试除流式发 event 外，还必须把 fixture `outputs/` 递归复制到 ExecuteRequest 的本次 Run output path，复制失败返回 execution error，保证后续 Manifest/Artifact 链路使用真实文件。

Run: `go -C services/control-plane test ./internal/config ./internal/agentexec -run 'Runtime|Executor|Fake' -v`

Expected: FAIL，Executor/adapter/config 字段尚不存在。

- [ ] **Step 2: 写 Run API、取消和无缝 SSE 失败测试**

单元/HTTP 测试覆盖 `GET /conversations/{id}/runs`；integration test 使用两个 Conversation 的交错 Run，断言只返回目标归属且严格 `created_at,id` 升序，不存在 Conversation 404。另覆盖 `GET /runs/{id}` 与 `POST /cancel`：queued 在单一 DB transaction 内设 cancelled+finalized；agent phase 先 Runtime cancel 后等待 Coordinator finalize abort；publishing 返回 409。预置 sequence 1–3，`Last-Event-ID: 1` 只返回 2–3；frame `id` 等于 durable sequence；浏览器断开不触发 cancel。

SSE race test 在 replay 读到 2 后、切换 live 前 append 3，并在订阅后 append 4，断言收到 2/3/4 各一次。实现顺序固定为“先订阅 broker → 按 durable sequence replay → 每次通知后从 DB 查询 `sequence > last`”，因此 replay/live 无窗口且 DB 始终是权威。

Run: `go -C services/control-plane test ./internal/runs ./internal/httpapi -run 'Run|Cancel|SSE' -v`

Run: `TEST_DATABASE_URL='postgres://harness_forge:local-dev-only@localhost:5432/harness_forge?sslmode=disable' go -C services/control-plane test -tags=integration ./internal/runs -run 'ListByConversation' -v`

Expected: FAIL，broker/cancellation/handlers 尚不存在。

- [ ] **Step 3: 实现最小 Executor interface**

```go
type Executor interface {
    Execute(context.Context, ExecuteRequest) (<-chan Event, <-chan error)
    Cancel(context.Context, RunID) error
    Finalize(context.Context, RunID, Decision) error
    ListExecutions(context.Context) ([]Execution, error)
    SessionExists(context.Context, SessionID) (bool, error)
    DeleteExecution(context.Context, RunID) error
    DeleteSession(context.Context, SessionID) error
}
```

Fake adapter 从 fixture 流式发送相同 V1 event，不添加仅测试可见的业务接口。

- [ ] **Step 4: 实现 Run 路由和 cancellation service**

在 Run store 实现 `ListByConversation` 并注册 `GET /api/v1/conversations/{conversation_id}/runs`，不存在 Conversation 404。另注册 `GET /api/v1/runs/{run_id}`、events list/stream、`POST /cancel`。Queued cancel 在同一 transaction 写 `cancelled`、`finalized_at` 与 `run.cancelled` Event；active cancellation 的终态完成由 Coordinator callback 执行，handler 只返回 202；不允许 handler 自行伪造 `finalized_at`。`main.go` 根据 `RUNTIME_MODE=http|fake` 注入唯一 `agentexec.Executor`，Fake 使用已提交 fixture。

`.env.example` 和 Compose 增加 `RUNTIME_MODE=http`。在 Task 5 已使用仓库根 build context 的 Dockerfile 中追加 `tests/fixtures/fake-runtime/` 到 `/app/fixtures/fake-runtime`；不得复制整个仓库或 `.env`。Runtime contract loader 和 Fake fixture root 从 `/app` 固定目录读取。

- [ ] **Step 5: 验证并提交**

```bash
go -C services/control-plane test ./internal/agentexec ./internal/runs ./internal/httpapi -run 'Runtime|Run|Cancel|SSE' -v
docker compose -f docker-compose.yaml up -d --wait postgres
TEST_DATABASE_URL='postgres://harness_forge:local-dev-only@localhost:5432/harness_forge?sslmode=disable' go -C services/control-plane test -tags=integration ./internal/runs -run 'ListByConversation' -v
go -C services/control-plane test ./...
git add .env.example docker-compose.yaml services/control-plane/Dockerfile services/control-plane/internal/config services/control-plane/internal/agentexec services/control-plane/internal/runs services/control-plane/internal/httpapi services/control-plane/cmd tests/fixtures/fake-runtime
git diff --cached --check
git commit -m "feat: stream runs through the runtime executor seam"
```

### Task 9：实现 Artifact 发布与隔离 Gateway

**Files:**
- Modify: `.env.example`
- Modify: `docker-compose.yaml`
- Modify: `services/control-plane/internal/config/config.go`
- Modify: `services/control-plane/internal/config/config_test.go`
- Create: `services/control-plane/internal/artifacts/manifest.go`
- Create: `services/control-plane/internal/artifacts/manifest_test.go`
- Create: `services/control-plane/internal/artifacts/publisher.go`
- Create: `services/control-plane/internal/artifacts/publisher_test.go`
- Create: `services/control-plane/internal/artifacts/publication_lock.go`
- Create: `services/control-plane/internal/artifacts/publication_lock_test.go`
- Create: `services/control-plane/internal/artifacts/store.go`
- Create: `services/control-plane/internal/artifacthttp/server.go`
- Create: `services/control-plane/internal/artifacthttp/server_test.go`
- Create: `services/control-plane/internal/httpapi/artifacts.go`
- Create: `services/control-plane/internal/httpapi/artifacts_test.go`
- Modify: `services/control-plane/internal/httpapi/router.go`
- Modify: `services/control-plane/cmd/harness-forge/main.go`

- [ ] **Step 1: 写 Manifest、发布与 media type 失败测试**

覆盖 schema 错误、重复 name、多个 primary、entry 缺失、绝对路径、`..`、逃逸 symlink、超过 Profile snapshot `max_file_bytes=10485760`/`max_total_bytes=52428800`、部分上传失败、DB commit 失败留下不可见前缀。Publisher 不读另一套环境变量，使用与 Runtime 相同 snapshot limit。每个 object 都写显式 Content-Type：`.html=text/html; charset=utf-8`、`.js=text/javascript; charset=utf-8`、`.css=text/css; charset=utf-8`、`.json=application/json`、常见图片使用标准 image type，其余为 `application/octet-stream`；不得依赖 MinIO sniff。

Run: `go -C services/control-plane test ./internal/artifacts -run 'Manifest|Publish|ContentType' -v`

Expected: FAIL，validator/publisher/lock 尚不存在。

- [ ] **Step 2: 写列表 API 与 Gateway 隔离失败测试**

`GET /runs/{run_id}/artifacts` 只返回 committed metadata 并标识 primary。Gateway 未提交 Artifact 404；已提交入口和同一 Artifact 下的 `assets/chart.js`、CSS、图片、JSON data 文件可读，并逐一断言响应 Content-Type 与上传 metadata 一致；绝对路径/`..`/编码穿越/跨 prefix 访问 400；响应含 CSP；不设置控制平面 cookie/CORS。本 Task 用 Go `httptest` 精确验证 header/body；真实浏览器在 Task 21 Playwright 场景中验证本地 ECharts 脚本在 `nosniff` 下执行。

Run: `go -C services/control-plane test ./internal/artifacthttp ./internal/httpapi -run 'Artifact|Gateway' -v`

Expected: FAIL，Gateway/store/handler 尚不存在。

- [ ] **Step 3: 实现 metadata-gated publisher**

`Publisher.Prepare` 先在专用 pgx connection 取得 session advisory lock `pg_advisory_lock(hashtextextended('harness-forge:artifact-maintenance', 0))`，再分配 Artifact ID、上传最终前缀，返回含待提交 records 与幂等 `Release()` 的 `PreparedPublication`。Coordinator 必须持有该 handle 直到 metadata transaction commit/rollback 后才 release；上传失败则清 prefix 并 release。Publisher 不自行写 Artifact metadata 或把 Run 标记成功。DB 失败留下 metadata-gated、不可见的 orphan，交给 Task 11 扫描。测试断言 lock 在上传前取得、metadata 完成信号后释放，并覆盖所有 error path 不泄漏连接/锁。

- [ ] **Step 4: 实现第二 listener**

```text
Content-Security-Policy: default-src 'self' data: blob:; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; connect-src 'none'; frame-ancestors <configured-web-origin>
X-Content-Type-Options: nosniff
Referrer-Policy: no-referrer
```

Gateway route 为 `/artifacts/{artifact_id}/{relative_path...}`：先按 Artifact ID 查询 committed `object_prefix`，再 URL decode 一次并用 `path.Clean` 验证相对路径，最终 object key 必须仍在该 prefix 下。未提供 path 时重定向/服务已存 `entry_path`；不得把 URL path 直接当 MinIO key，也不得限制为仅 entry 文件。Gateway 从 Object Store `Stat` 读取已存 Content-Type 并原样设置后再写 body，未知/缺失值强制为 `application/octet-stream`。

Config 增加必填绝对 URL `ARTIFACT_PUBLIC_ORIGIN`（本地示例 `http://localhost:8081`）；本 Task 同时把它注入 Compose Control Plane，保证后续 Task 启动服务。Artifact API 只用它加上 escaped Artifact ID/entry 构造 `gateway_url`，不相信 request Host/X-Forwarded-Host。config test 覆盖非法/带 path origin 拒绝。

- [ ] **Step 5: 验证并提交**

```bash
go -C services/control-plane test ./internal/artifacts ./internal/artifacthttp ./internal/httpapi -run 'Artifact|Gateway' -v
git add .env.example docker-compose.yaml services/control-plane/internal/config services/control-plane/internal/artifacts services/control-plane/internal/artifacthttp services/control-plane/internal/httpapi services/control-plane/cmd
git diff --cached --check
git commit -m "feat: publish and isolate immutable artifacts"
```

### Task 10：实现 Scheduler、finalize 与启动协调

**Files:**
- Modify: `docker-compose.yaml`
- Modify: `services/control-plane/Dockerfile`
- Modify: `services/agent-runtime/Dockerfile`
- Create: `services/control-plane/internal/runs/scheduler.go`
- Create: `services/control-plane/internal/runs/scheduler_test.go`
- Create: `services/control-plane/internal/runs/reconcile.go`
- Create: `services/control-plane/internal/runs/reconcile_test.go`
- Create: `services/control-plane/internal/runs/coordinator.go`
- Create: `services/control-plane/internal/runs/coordinator_test.go`
- Create: `services/control-plane/internal/runs/coordinator_integration_test.go`
- Create: `services/control-plane/internal/workspaces/materializer.go`
- Create: `services/control-plane/internal/workspaces/materializer_test.go`
- Create: `services/control-plane/internal/workspaces/permissions_integration_test.go`
- Create: `services/control-plane/migrations/00003_message_and_event_idempotency.sql`
- Modify: `services/control-plane/cmd/harness-forge/main.go`

- [ ] **Step 1: 写 Workspace 准备失败测试**

复用 Task 5 的 immutable Profile snapshot。Materializer 在 `/workspaces/{run_id}` 下先以 mode `0770` 建立 `inputs/workspace/outputs`，复制 template 并将 Project Input 流式落到 `inputs/{input_id}-{sanitized_name}`；materialize 全部成功后把 input files 设 `0440`、`inputs` directory 设 `0550`，只有 workspace/outputs 保持 `0770`。chmod 失败视为 preparation failure；Runtime 启动前必须已经只读。拒绝 symlink/逃逸路径。任一失败将 Run 标记 preparation failed，但保留已有 Workspace 与诊断 metadata；只有显式 `purge-deleted`/orphan cleanup 删除，测试断言失败现场仍存在。

Go 与 Python image 都创建并运行同一个 `app` UID/GID `10001:10001`。Compose 新增一次性 `runtime-volume-init`（精确 `alpine:3.22`，以 root 运行），在 `run-workspaces` 创建 `/workspaces`、在 `runtime-sessions` 创建 `/sessions/executions` 与 `/sessions/claude`，全部 owner 10001:10001/mode 0770；Control Plane 和 Runtime 必须等 init 成功。`permissions_integration_test.go` 首行为 `//go:build integration`，Compose 验证两容器都能读 inputs、写 workspace/outputs及各自 execution/Session 目录，且 inputs 对 Runtime 不可写。

Run: `go -C services/control-plane test ./internal/workspaces -run 'Workspace|Materialize' -v`

Expected: FAIL，Materializer 尚不存在。

实现后运行：

```bash
docker compose -f docker-compose.yaml up -d --build --wait runtime-volume-init control-plane agent-runtime
go -C services/control-plane test -tags=integration ./internal/workspaces -run Permissions -v
```

Expected: PASS，两个非 root 容器共享目录权限正确。

- [ ] **Step 2: 写成功两阶段收尾失败测试**

Coordinator 先按 Run 读取 trigger Message、Conversation、Project 和所有未删除 Input；断言 Message 确属 Conversation、Conversation 确属 Project。它用 trigger Message `content` 作为新 prompt、Task 5 immutable Profile snapshot/digest 和绝对 Workspace paths 构造 ExecuteRequest。首轮 Conversation `active_sdk_session_id=NULL` 时 source 为 null；第二轮精确传当前 active Session，测试两种 request，并证明 candidate 未在成功 transaction 前覆盖 source pointer。

Fake Executor 发 `assistant.message` 与 `agent.completed(candidate_session_id, artifacts)`。`00003` 给 assistant Message 增加 nullable `run_id/runtime_sequence` 和 partial unique，并给 Run Event 增加 nullable `dedupe_key` 与 partial unique `(run_id,dedupe_key)`。Coordinator 幂等地把 normalized assistant reply 同时持久化为 Run Event 与 Conversation Message，刷新 Messages API 后仍可见。

成功路径断言 Go 验证 Manifest、在共享 Artifact publication advisory lock 下上传 objects，然后开启 PostgreSQL transaction，按 Project row → Conversation row 加锁并再次确认未删除；在同一个 transaction 中插入全部 Artifact metadata、更新 Conversation active Session、写 Run candidate Session/status=succeeded。强制任一步失败时三类产品状态都回滚并走 abort。promotion/delete race test 证明 delete 先提交则 promotion 被拒绝并 abort，promotion 先提交则 delete 看到 unfinalized Run 返回 conflict。

产品状态 transaction 提交后调用 `finalize(commit)`；ack 后用一个 PostgreSQL transaction 同时写 `finalized_at`、所有 `artifact.published` Event 和 `run.succeeded` Event。agent/publish failure、active cancel、restart interrupt 同理：Runtime abort ack 后在一个 transaction 写 `finalized_at` 与唯一对应的 `run.failed|run.cancelled|run.interrupted`；preparation failure/queued cancel 的无 Runtime 路径也在终态 transaction 内写 Event。任何 `run.*` terminal Event 都不得早于 `finalized_at`；dedupe key 固定为 `terminal:{status}`，Artifact 为 `artifact:{artifact_id}:published`，重试依赖 `00003` 唯一约束幂等。

Run: `go -C services/control-plane test ./internal/runs -run 'Coordinator|ExecuteRequest|Promotion|AssistantMessage|Finalize' -v`

Expected: FAIL，Coordinator/transaction choreography 尚不存在。

- [ ] **Step 3: 将 reconcile 决策表写成失败测试**

按下表逐行断言调用顺序、DB 结果及 scheduler 是否保持暂停：

协调先遍历 PostgreSQL 的 unfinalized Runs，再处理 Runtime 中未被前一步消费的 records，优先级固定且各行互斥：

| Priority / PostgreSQL | Runtime record | Required action |
|---|---|---|
| 1. succeeded，candidate 等于 Conversation active pointer | 任意/未列出 tombstone | 幂等 finalize(commit) → `HEAD candidate_session` → 同事务 finalized + published/succeeded Events |
| 1b. succeeded，但 candidate 未 promotion | 任意 | 幂等 finalize(abort)；ack 后报告 hard consistency error，保留 DB unfinalized 并暂停，绝不 commit candidate |
| 2. failed/cancelled/interrupted | 任意 | active 则 cancel 并确认 inactive → 幂等 finalize(abort) → 同事务 finalized + 对应 terminal Event |
| 3. running | active/unfinalized | cancel → 确认 inactive → finalize(abort) → 同事务 interrupted+finalized+Event |
| 4. running | absent | 同事务 interrupted+finalized+Event（无 disposition） |
| 5. Runtime record 未匹配以上任何 DB Run | active/unfinalized | cancel → 确认 inactive → finalize(abort)，保留 tombstone待 purge |

若 succeeded Run 的 candidate 未被 promotion，按规格明确选择 abort；abort acknowledgement 后仍保留 hard consistency error 等待人工修复 DB product state，不把该 candidate 变为 active。Runtime `ListExecutions` 不返回的 terminal tombstone由幂等 `Finalize(decision)` 响应其既有 disposition；commit 后必须 `HEAD` Session 成功。这样 succeeded 不会落入 orphan 分支。

Runtime 不可用、ack 丢失或 HEAD 异常时保留原状态并返回 retryable reconciliation error；启动不开放 scheduler，后台以 5 秒起始、最长 1 分钟的指数退避重试相同幂等 decision。矛盾 tombstone decision 是 hard error，必须保持暂停。测试还覆盖 prepare failure 无 Runtime disposition、publish failure abort、active cancel、DB commit 前崩溃、commit 后 ack 前崩溃、ack 后写 `finalized_at`/terminal Event 前崩溃。

Run: `go -C services/control-plane test ./internal/runs -run 'Scheduler|Reconcile|Crash|Ack' -v`

Expected: FAIL，reconciler/scheduler 尚不存在。

- [ ] **Step 4: 实现唯一 Coordinator、scheduler 和周期协调**

Coordinator 是唯一组合 Inputs、Profile resolver、Workspace materializer、Executor、Artifact publisher 和 Run store 的 module。Scheduler 只 claim/调用 Coordinator，不复制状态机。启动先 reconcile 到“无 active worker 且无 retryable disposition”；运行中 reconciliation ticker 持续处理 unfinalized terminal Runs。下一 Run 只能在当前 Run `finalized_at` 非空后领取。

Event pump 必须按下表处理并用 `runtime_sequence` 幂等；每次 durable append 后通知 Task 8 broker，SSE 只从 DB 补读：

| Runtime event | Product action |
|---|---|
| `phase.changed` | 验证只允许 Runtime 拥有的 preparing/agent 转换；同事务更新 Run phase并 append Event |
| `assistant.delta` | append Run Event；不写 Message |
| `assistant.message` | 同事务 append Run Event + 幂等 Conversation assistant Message |
| `tool.started/tool.completed` | append Run Event |
| `artifact.candidate` | append Run Event 并缓存 typed summary；不写 Artifact metadata |
| unknown non-terminal | 记录 debug log 后忽略，不更新状态、不结束 stream |
| `agent.failed` | 同事务 append Runtime Event并写 status=failed/error、`finalized_at=NULL`；再 finalize(abort)，ack 后同事务 finalized+`run.failed` |
| stream/protocol error or EOF without known terminal | 映射 typed failure；若 execute 未接受则同事务 failed+finalized+Event；否则先同事务 failed/error 且保持 unfinalized，再 abort，ack 后 finalized+Event |
| `agent.completed` | 校验 candidate ID/summary 与 Manifest；在任何 object upload 前同事务切 phase=`publishing` 并 append phase Event，然后执行成功两阶段发布 |

Active cancel 先在 transaction 写 status=cancelled、诊断原因和 `finalized_at=NULL`，再 cancel worker/确认 inactive/finalize(abort)，ack 后同事务写 finalized+`run.cancelled`。Artifact validation/publication failure 同样先持久化 status=failed/error/unfinalized 再 abort。这样 Go 在 abort ack 前崩溃时，reconcile 会按原 failed/cancelled status 重试正确 disposition，而不会误改为 interrupted。`agent.completed/agent.failed` 后出现额外已知 event 是 protocol error。进入 `publishing` 后 Cancel service 必须返回 409。

`coordinator_integration_test.go` 首行为 `//go:build integration`，在真实 PostgreSQL 中注入每个 statement 的失败点，证明 Artifact metadata、active Session 和 succeeded Run 全有或全无；并覆盖 assistant Message/Event 幂等、promotion/delete race，以及 finalize ack 后 `finalized_at` 与 terminal Events 的同事务原子性。

- [ ] **Step 5: 验证并提交**

```bash
go -C services/control-plane test ./internal/profiles ./internal/workspaces ./internal/runs -run 'Profile|Workspace|Scheduler|Coordinator|Reconcile' -v
TEST_DATABASE_URL='postgres://harness_forge:local-dev-only@localhost:5432/harness_forge?sslmode=disable' go -C services/control-plane test -tags=integration ./internal/runs -run 'Coordinator' -v
go -C services/control-plane test ./...
git add docker-compose.yaml services/control-plane/Dockerfile services/agent-runtime/Dockerfile services/control-plane/migrations/00003_message_and_event_idempotency.sql services/control-plane/internal/workspaces services/control-plane/internal/runs services/control-plane/cmd
git diff --cached --check
git commit -m "feat: coordinate durable agent runs"
```

### Task 11：实现逻辑删除与幂等 purge

**Files:**
- Modify: `docker-compose.yaml`
- Modify: `services/control-plane/Dockerfile`
- Create: `services/control-plane/internal/cleanup/purge.go`
- Create: `services/control-plane/internal/cleanup/purge_test.go`
- Create: `services/control-plane/internal/cleanup/orphans.go`
- Create: `services/control-plane/internal/cleanup/orphans_test.go`
- Create: `services/control-plane/internal/cleanup/purge_integration_test.go`
- Create: `services/control-plane/cmd/purge-deleted/main.go`
- Modify: `Makefile`
- Modify: `services/control-plane/internal/objectstore/store.go`
- Modify: `services/control-plane/internal/objectstore/minio.go`
- Modify: `services/control-plane/internal/objectstore/memory.go`
- Modify: `services/control-plane/internal/artifacts/publication_lock.go`
- Modify: `services/control-plane/internal/httpapi/projects.go`
- Modify: `services/control-plane/internal/httpapi/conversations.go`

- [ ] **Step 1: 写清理顺序与幂等失败测试**

断言顺序：MinIO prefix → SDK Session → Workspace → execution tombstone → PostgreSQL hard delete。外部资源“不存在”为成功；中途失败后重跑不得删除共享 Input File。再次覆盖 Project/Conversation 有 queued/running/unfinalized Run 时 HTTP 删除 409。

另写 orphan 测试：存在 `projects/{project_id}/artifacts/{artifact_id}/` prefix 但 DB 无该 Artifact metadata 时删除；有 metadata 时保留；路径层级异常或 artifact_id 非 UUID 时跳过并报告。Scanner 必须先取得 Task 9 同一个 `artifact-maintenance` session advisory lock，持锁完成 list/DB compare/delete 后再 release；并发测试让 publication 在 upload 后暂停，证明 scanner 无法进入，metadata commit/release 后 scanner 保留合法 prefix。`purge_integration_test.go` 首行为 `//go:build integration`，用真实 MinIO/PostgreSQL 验证 DB commit 失败遗留 prefix 能被发现、不可通过 Gateway 读取并最终清除。

```bash
go -C services/control-plane test ./internal/cleanup ./internal/httpapi -run 'Delete|Purge|Orphan|PublicationRace' -v
docker compose -f docker-compose.yaml up -d --wait postgres minio minio-init agent-runtime
TEST_DATABASE_URL='postgres://harness_forge:local-dev-only@localhost:5432/harness_forge?sslmode=disable' go -C services/control-plane test -tags=integration ./internal/cleanup -v
```

Expected: FAIL，cleanup scanner/command/Object Store listing 尚不存在。

- [ ] **Step 2: 实现 purge command**

扩展 Object Store seam 增加受 prefix 约束的 `ListPrefixes`，先安全扫描 metadata orphan，再处理已 `deleted_at` 且无 queued/running/unfinalized Run 的根；默认 dry-run，`--apply` 执行。输出结构化 summary，任一清理失败时非零退出并保留 DB 根记录。

Makefile 必须实现规格入口：

```make
purge-deleted:
	docker compose -f docker-compose.yaml build control-plane
	docker compose -f docker-compose.yaml up -d --wait postgres minio minio-init agent-runtime
	docker compose -f docker-compose.yaml run --rm --no-deps control-plane /usr/local/bin/purge-deleted --apply

purge-deleted-dry-run:
	docker compose -f docker-compose.yaml build control-plane
	docker compose -f docker-compose.yaml up -d --wait postgres minio minio-init agent-runtime
	docker compose -f docker-compose.yaml run --rm --no-deps control-plane /usr/local/bin/purge-deleted --dry-run
```

Control Plane Dockerfile 同时构建 `/usr/local/bin/harness-forge` 与 `/usr/local/bin/purge-deleted`。Purge CLI 与 server 共用 Task 4 migration runner：连接数据库后先取得 migration lock 并升级 schema，再查询待清理根，因此可从空数据库启动。Compose 的 one-shot `run` 继承 Control Plane 的内部 PostgreSQL/MinIO/Runtime environment 和 `run-workspaces:/workspaces` volume；`minio-init` 确保 bucket 存在，因此命令能从干净环境按规格清 Workspace、Session 和 tombstone。不得在宿主机直接 `go run` maintenance command。

- [ ] **Step 3: 验证并提交**

```bash
go -C services/control-plane test ./internal/cleanup ./internal/httpapi -run 'Delete|Purge' -v
make purge-deleted-dry-run
make purge-deleted
TEST_DATABASE_URL='postgres://harness_forge:local-dev-only@localhost:5432/harness_forge?sslmode=disable' go -C services/control-plane test -tags=integration ./internal/cleanup -v
git add docker-compose.yaml services/control-plane/Dockerfile services/control-plane/internal/cleanup services/control-plane/internal/objectstore services/control-plane/internal/artifacts/publication_lock.go services/control-plane/cmd/purge-deleted services/control-plane/internal/httpapi Makefile
git diff --cached --check
git commit -m "feat: purge logically deleted project data"
```

## Chunk 3：Python Claude Agent Runtime

### Task 12：实现 execution store、单并发和 tombstone

**Files:**
- Modify: `.env.example`
- Modify: `docker-compose.yaml`
- Create: `services/agent-runtime/src/harness_forge_runtime/settings.py`
- Create: `services/agent-runtime/tests/test_settings.py`
- Create: `services/agent-runtime/src/harness_forge_runtime/execution_store.py`
- Create: `services/agent-runtime/src/harness_forge_runtime/errors.py`
- Create: `services/agent-runtime/tests/test_execution_store.py`
- Modify: `services/agent-runtime/src/harness_forge_runtime/api.py`
- Create: `services/agent-runtime/tests/test_execution_api.py`

- [ ] **Step 1: 写 execution lifecycle 失败测试**

Settings 测试固定 `RUNTIME_STATE_ROOT=/sessions/executions`、`CLAUDE_CONFIG_DIR=/sessions/claude`、`RUN_WORKSPACE_ROOT=/workspaces`，要求均为绝对路径，前两者互不嵌套且位于 mounted `/sessions` root。Record 固定字段为 `run_id`、source/candidate Session ID、可空 `candidate_durable_at`、启动前 `baseline_session_ids`、`worker_pid/pgid`、`lifecycle`、timestamps 和可空 disposition；`lifecycle` 只允许 `starting|running|awaiting_finalize|committed|aborted`。覆盖原子 reserve、同/不同 Run 冲突、starting→running→awaiting_finalize、candidate/PGID/durable marker 更新、commit/abort tombstone、冲突 decision、删除 tombstone、unfinalized 时拒删、重启扫描，以及最多一个 starting/running/awaiting_finalize。

- [ ] **Step 2: 运行测试确认失败**

Run: `cd services/agent-runtime && uv run pytest tests/test_settings.py tests/test_execution_store.py tests/test_execution_api.py -v`

Expected: FAIL，ExecutionStore/API 尚不存在。

- [ ] **Step 3: 实现原子文件模型和 API**

只有 FastAPI 父进程可修改 execution record；worker 只能经 Task 15 control pipe 报告数据。每次写入同目录临时 JSON，flush + `os.fsync(file)`，`os.replace` 后打开父目录 `fsync`。Store 使用 async lock，启动扫描验证最多一个 unfinalized record；record root 由配置固定，run_id 先按 UUID 解析。

本 Task 实现 `GET /v1/executions`（只返回 starting/running/awaiting_finalize）和幂等 `DELETE /v1/executions/{run_id}`（缺失/terminal 204，unfinalized 409）。Finalize 与 Session 生命周期在 Task 14 一次性交付，避免先实现无 Session 语义的 endpoint。

Compose 和 `.env.example` 给 Runtime 注入上述三个目录；前两个落在 `runtime-sessions` named volume，workspace root 落在共享 `run-workspaces` volume。SDK 进程继承 `CLAUDE_CONFIG_DIR`，因此 Session 可跨 container restart且不落到 ephemeral home。

- [ ] **Step 4: 验证并提交**

```bash
cd services/agent-runtime
uv run pytest tests/test_settings.py tests/test_execution_store.py tests/test_execution_api.py -v
uv run ruff check .
cd ../..
git add .env.example docker-compose.yaml services/agent-runtime
git commit -m "feat: persist runtime execution dispositions"
```

### Task 13：验证 Go 所有的 Workspace 与 Manifest

**Files:**
- Create: `services/agent-runtime/src/harness_forge_runtime/workspaces.py`
- Create: `services/agent-runtime/src/harness_forge_runtime/artifacts.py`
- Create: `services/agent-runtime/tests/test_workspaces.py`
- Create: `services/agent-runtime/tests/test_artifacts.py`

- [ ] **Step 1: 写路径和 Manifest 失败测试**

使用 `tmp_path` 预先创建 Go materialized 的 `inputs/workspace/outputs`，对 request `run_id` 断言三条 resolve 后必须精确等于 `${RUN_WORKSPACE_ROOT}/{run_id}/inputs|workspace|outputs`，拒绝同 root 下另一 Run 的目录；覆盖 inputs 不可写、workspace/outputs 可写、缺目录、`..` 和 symlink root。Manifest 覆盖空 outputs 且无 Manifest返回零 Artifact（合法）、有 publishable file 却无 Manifest、错误 schema version、Profile snapshot 不允许的 type、缺失 entry、逃逸 symlink、重复 name、多个 primary、单文件/总大小上限。

- [ ] **Step 2: 运行测试确认失败**

Run: `cd services/agent-runtime && uv run pytest tests/test_workspaces.py tests/test_artifacts.py -v`

Expected: FAIL，Workspace/Manifest validators 尚不存在。

- [ ] **Step 3: 实现 Workspace preparation**

只接受 Go 传入且位于配置 Run root 下的既有绝对路径；用 `Path.resolve()` 验证归属并检查权限。不创建目录、不复制 Profile template、不下载对象，也不在失败时删除 Workspace。Validator 返回 immutable paths value object。

- [ ] **Step 4: 实现 Manifest validator**

先用已提交 V1 JSON Schema/Pydantic 验证结构，再逐 entry `lstat/resolve`；将 Manifest version/Artifact type 与 ExecuteRequest 的 Profile snapshot policy 交叉验证，并直接使用 snapshot 的 `max_file_bytes=10485760`、`max_total_bytes=52428800`；拒绝目录、逃逸 symlink 和超限输出；返回 typed candidate summary，不上传文件。

- [ ] **Step 5: 验证并提交**

```bash
cd services/agent-runtime
uv run pytest tests/test_workspaces.py tests/test_artifacts.py -v
uv run ruff check .
cd ../..
git add services/agent-runtime/src/harness_forge_runtime services/agent-runtime/tests
git commit -m "feat: validate runtime workspaces and artifacts"
```

### Task 14：实现 SDK Session adapter、fork 与 finalize API

**Files:**
- Create: `services/agent-runtime/src/harness_forge_runtime/sessions.py`
- Create: `services/agent-runtime/src/harness_forge_runtime/claude.py`
- Create: `services/agent-runtime/tests/test_sessions.py`
- Create: `services/agent-runtime/tests/test_claude_adapter.py`
- Modify: `services/agent-runtime/src/harness_forge_runtime/api.py`
- Modify: `services/agent-runtime/tests/test_execution_api.py`
- Create: `services/agent-runtime/tests/test_session_api.py`
- Modify: `services/agent-runtime/pyproject.toml`
- Modify: `services/agent-runtime/uv.lock`

- [ ] **Step 1: 安装并锁定 Claude Agent SDK**

```bash
cd services/agent-runtime
uv add 'claude-agent-sdk==0.2.120'
uv lock --check
```

- [ ] **Step 2: 写 SDK adapter/fork 失败测试并确认红灯**

覆盖首轮 `resume=None/fork_session=False`、后续 `resume=source_sdk_session_id/fork_session=True`（绝不 resume candidate）、`cwd=workspace`、Profile system prompt、allowed/disallowed tools、`permission_mode=default`、max turns/budget 和 Base URL env。Fail-closed `can_use_tool` callback 只批准 Profile allowed list，显式拒绝 disallowed 和任何未知新增 tool；测试 Read/Bash 获批、WebFetch/unknown 被拒。SDK partial `StreamEvent` 文本映射为 `assistant.delta`。Adapter 从首个 SDK init `SystemMessage` 提取 candidate Session ID，经 callback/control channel 上报，再规范化 assistant/tool/Result messages；缺 init、candidate 与 Result session 不一致、SDK error 均失败且不报告 completed。

Run: `uv run pytest tests/test_claude_adapter.py -v`

Expected: FAIL，窄 adapter 未实现。

- [ ] **Step 3: 实现窄 Claude adapter**

调用方只依赖 `stream_turn(request, on_candidate) -> AsyncIterator[NormalizedEvent]`。SDK 类型、System init、异常分类、tool block 映射和 ResultMessage 处理全部留在 `claude.py`；其他 module 禁止 import SDK message class。candidate callback 完成前不得产出 public agent event。

Run: `uv run pytest tests/test_claude_adapter.py -v`

Expected: PASS。确认绿色后才进入 Session/finalize 下一轮红测。

- [ ] **Step 4: 写 Session/finalize HTTP 失败测试并确认红灯**

测试 Runtime 私有 Session root 下的 `list_ids/exists/delete/sync_transcript`：不可信 ID、绝对路径、`..`、symlink escape 全拒绝；sync 必须 fsync transcript regular files、Session directory 和受影响父目录；缺失 Session 的 DELETE 幂等 204。覆盖 `HEAD /v1/sessions/{id}` 200/404、`DELETE /v1/sessions/{id}` 204，以及 finalize：starting/running 409，只允许 awaiting_finalize；commit 要求 candidate 已记录、存在且 `candidate_durable_at` 非空，否则 409；abort 先删除 candidate，成功后才写 aborted tombstone；同 decision 幂等，矛盾 decision 409。

Run: `uv run pytest tests/test_sessions.py tests/test_session_api.py tests/test_execution_api.py -v`

Expected: FAIL，Session store/HTTP/finalize 尚不存在。

- [ ] **Step 5: 实现 Session operations 与无泄漏 abort**

`sessions.py` 是 SDK Session 目录唯一 adapter，Session ID 解析后 resolve 必须仍在 Runtime-owned root。Execute reserve 在启动 worker 前持久化 `baseline_session_ids`；worker 从 init 取得 candidate 后经 control pipe 让父进程原子写 record，随后才 relay public events。SDK Result 后，worker 调用 `sync_transcript(candidate)`，再发送 `candidate_durable` control message；父进程重新验证 Session、把 `candidate_durable_at` 原子写 record并 ack，worker 之后才可发 `artifact.candidate/agent.completed`。

若 SIGKILL/SDK failure 发生在 candidate 上报前，global concurrency=1 且 Session root 只属于 Runtime，abort 用 `current_ids - baseline_ids` 删除本 Run 新建 Session；测试模拟 init 前/后取消，均无新 Session 泄漏。另做 restart/resume test：收到 `agent.completed` 后立即重建 ExecutionStore/SessionStore，以该 candidate 作为下一 Run source，mock SDK 断言能读到已 fsync transcript 并用 resume+fork 启动。

在 `api.py` 注册 HEAD/DELETE Session 和 finalize endpoints。Finalize 所有外部 Session 操作成功后才写 tombstone；进程在删除后、写 tombstone前崩溃时重试 abort 仍成功。

Run: `uv run pytest tests/test_sessions.py tests/test_session_api.py tests/test_execution_api.py -v`

Expected: PASS。确认绿色后执行本 Task 全量验证。

- [ ] **Step 6: 验证并提交**

```bash
cd services/agent-runtime
uv run pytest tests/test_sessions.py tests/test_claude_adapter.py tests/test_session_api.py tests/test_execution_api.py -v
uv run mypy src
uv run ruff check .
cd ../..
git add services/agent-runtime
git commit -m "feat: adapt Claude Agent SDK sessions"
```

### Task 15：实现 worker、流式 execute、取消与重启清理

**Files:**
- Create: `services/agent-runtime/src/harness_forge_runtime/runner.py`
- Create: `services/agent-runtime/src/harness_forge_runtime/processes.py`
- Modify: `services/agent-runtime/src/harness_forge_runtime/api.py`
- Create: `services/agent-runtime/tests/test_runner.py`
- Create: `services/agent-runtime/tests/test_processes.py`
- Create: `services/agent-runtime/tests/test_execute_stream.py`

- [ ] **Step 1: 写 worker NDJSON 失败测试**

用 fake Claude adapter 驱动完整 worker：验证已有 Workspace → SDK init/fork → Profile policy → normalized assistant/tool events → Manifest validator → `artifact.candidate` → 唯一 `agent.completed`。失败分支唯一 terminal 为 `agent.failed`；stdout 每行一个 V1 event、sequence 递增，stderr 不混入协议。candidate 必须先写 control FD 并获 parent ack，才可输出 Artifact/terminal event。

Run: `cd services/agent-runtime && uv run pytest tests/test_runner.py -v`

Expected: FAIL，runner orchestration 尚不存在。

- [ ] **Step 2: 实现 runner 编排并确认绿色**

Runner 串接 Workspace validation、Claude adapter、candidate created/durable control handshake、event normalization 和 Manifest validation。只有 transcript 与 execution record 均 durable 后才输出 `artifact.candidate` 与唯一 agent terminal。

Run: `cd services/agent-runtime && uv run pytest tests/test_runner.py -v`

Expected: PASS。确认绿色后进入 process/API 下一轮红测。

- [ ] **Step 3: 写取消和 restart 失败测试**

覆盖 active duplicate 同 Run 409 `already_running`、不同 Run 409 `runtime_busy`、awaiting-finalize duplicate 409、committed/aborted duplicate 200 JSON disposition且不启动 worker。Cancel 对 starting 必须设置取消意图并与 spawn lock 串行：Popen 前取消则永不 spawn，Popen 后则关闭 barrier、终止并 wait，任何路径都不得放行 child；running 先 SIGTERM、超时后 SIGKILL，重复 cancel 204；awaiting/terminal cancel 204，未知 Run 404。客户端断流后父进程继续 drain 到 event log并更新 awaiting_finalize。

启动采用 barrier pipe：child 新 process group 启动后先阻塞；parent 取得 PID/PGID、原子持久化 running record并 fsync 后才放行。测试注入 reserve 后/Popen 前崩溃，restart 将无 PGID 的 starting record 转 awaiting_finalize；注入“Popen 后、record 前”父进程崩溃，pipe EOF 使 child 自退且 restart 转 awaiting_finalize；注入 record 后崩溃，restart kill 整个 PGID、等待 inactive并转 awaiting_finalize。全部 startup cleanup 完成后 health 才 ready。

Run: `cd services/agent-runtime && uv run pytest tests/test_processes.py tests/test_execute_stream.py -v`

Expected: FAIL，process manager/execute endpoint 尚不存在。

- [ ] **Step 4: 实现 worker process 与 `application/x-ndjson` endpoint**

API server 将 ExecuteRequest 原子写入 execution directory，记录 baseline Session IDs，建立 start/candidate-control pipes，以 `python -m harness_forge_runtime.runner <request-file>` 启动新 process group。ProcessManager 的 per-run async lock 串行 spawn/cancel；父进程持久化 PGID并再次检查 cancel intent 后才放行。candidate control message 持久化并 ack。stdout 同时写 execution event log与 HTTP stream；客户端断开不取消 worker，server 继续消费并记录唯一终态，worker exit 后转 awaiting_finalize。

只有 execution store reserve 成功才创建 worker。Worker 非零退出、stdout 非法、缺 terminal 映射为父进程合成的 `agent.failed`；协议错误保留受大小限制的 stderr 和 correlation ID，但 redaction 后才落盘，禁止 credential/env 写入 event。GET executions 返回 active/unfinalized state；server readiness 要等 startup cleanup 完成。

Run: `cd services/agent-runtime && uv run pytest tests/test_processes.py tests/test_execute_stream.py -v`

Expected: PASS。确认绿色后执行本 Task 全量验证。

- [ ] **Step 5: 验证并提交**

```bash
cd services/agent-runtime
uv run pytest tests/test_runner.py tests/test_processes.py tests/test_execute_stream.py -v
uv run ruff check .
cd ../..
git add services/agent-runtime
git commit -m "feat: execute isolated runtime workers"
```

### Task 16：实现 Geo Profile、固定镜像与真实 smoke

**Files:**
- Modify: `profiles/geo-analysis/profile.yaml`
- Modify: `profiles/geo-analysis/system-prompt.md`
- Modify: `profiles/geo-analysis/workspace-template/README.md`
- Create: `profiles/geo-analysis/workspace-template/assets/echarts.min.js`
- Create: `profiles/geo-analysis/workspace-template/assets/LICENSE.echarts.txt`
- Modify: `services/agent-runtime/Dockerfile`
- Modify: `services/agent-runtime/pyproject.toml`
- Modify: `services/agent-runtime/uv.lock`
- Create: `services/agent-runtime/tests/test_geo_profile.py`
- Create: `tests/fixtures/geo/locations.csv`
- Create: `scripts/vendor-echarts.sh`
- Create: `apps/web/scripts/smoke-claude.mjs`
- Create: `apps/web/scripts/smoke-claude.test.mjs`
- Modify: `Makefile`

- [ ] **Step 1: 写 vendored asset 失败测试**

在 `test_geo_profile.py` 先写 `test_vendored_echarts_assets`，验证 ECharts asset/license 存在、checksum 正确且 HTML 目标路径固定。

Run: `cd services/agent-runtime && uv run pytest tests/test_geo_profile.py -k vendored_echarts_assets -v`

Expected: FAIL，asset 尚不存在。

- [ ] **Step 2: 实现可重复 vendor asset**

`scripts/vendor-echarts.sh` 只允许 `echarts@5.6.0`，用 `npm pack` 在临时目录解包并复制 asset/LICENSE，然后硬校验 SHA-256：`echarts.min.js=bf4a223524e40b77c304bec67e1222cf551f14880cf42c69dc046558e11c07b1`、`LICENSE=634293835b43a6dd2094fa39182a3d9a6b9ca43b7fdb9ac354e8037af2a3093a`。checksum 不符非零退出；运行两次 git diff 必须为空。报告查看时不访问 CDN。

Run: `bash scripts/vendor-echarts.sh && bash scripts/vendor-echarts.sh && git diff --exit-code -- profiles/geo-analysis/workspace-template/assets && cd services/agent-runtime && uv run pytest tests/test_geo_profile.py -k vendored_echarts_assets -v`

Expected: PASS 且第二次无 diff。确认绿色后进入 Profile policy 下一轮红测。

- [ ] **Step 3: 写并实现精确 Profile policy**

新增 `test_profile_policy_and_prompt`，检查以下精确值。

Run: `cd services/agent-runtime && uv run pytest tests/test_geo_profile.py -k profile_policy_and_prompt -v`

Expected: FAIL，Profile 仍是 Task 5 最小配置。

然后将 `profile.yaml` 固定为：ID `geo-analysis`、version `1`、accepted inputs `text/csv|application/geo+json`、Manifest version 1、Artifact types `html|markdown|image|data`、`max_file_bytes: 10485760`、`max_total_bytes: 52428800`、`permission_mode: default`、allowed tools `Read|Write|Edit|Bash|Glob|Grep`、disallowed tools `WebFetch|WebSearch|NotebookEdit`、`max_turns: 8`、`max_budget_usd: 2.0`；Claude adapter 的 callback 对 enum 外新工具仍默认拒绝。

系统提示词要求先用 Python 检查字段、类型和缺失值，再分析；只从 Project inputs 得出结论；中间文件在 workspace，发布内容只放 outputs；必须生成 Manifest；不得声称未经计算的结论。固定复制 `workspace/assets/echarts.min.js` 和 license 到 `outputs/report/vendor/`，primary 为 `outputs/report/index.html` 且引用 `./vendor/echarts.min.js`；另由 Python 写 `outputs/data/analysis-evidence.json` data Artifact，含 input digest、row count 和实际计算字段。

Run: `cd services/agent-runtime && uv run pytest tests/test_geo_profile.py -k profile_policy_and_prompt -v`

Expected: PASS。确认绿色后进入依赖/镜像下一轮红测。

- [ ] **Step 4: 写并实现锁定依赖与非 root 镜像**

先新增 `test_dependency_lock_and_runtime_user`，检查六个精确依赖版本、基础镜像和 UID/GID；运行确认 FAIL：

Run: `cd services/agent-runtime && uv run pytest tests/test_geo_profile.py -k dependency_lock_and_runtime_user -v`

Expected: FAIL，geospatial dependencies 尚未进入 lockfile。

先锁定依赖：

```bash
cd services/agent-runtime
uv add 'pandas==2.3.1' 'geopandas==1.1.1' 'shapely==2.1.1' 'pyproj==3.7.1' 'duckdb==1.3.2' 'pyarrow==20.0.0'
uv lock --check
cd ../..
```

沿用精确 `python:3.12.11-slim-bookworm` 和 Task 10 的固定 UID/GID 10001:10001，安装 `uv.lock` production dependencies 和所需 OS runtime libs；不得复制 `.env`、宿主 Claude 配置或缓存。Container test import 六个地理依赖并验证进程非 root。

Run: `cd services/agent-runtime && uv run pytest tests/test_geo_profile.py -k dependency_lock_and_runtime_user -v && cd ../.. && docker compose -f docker-compose.yaml build --no-cache agent-runtime && docker compose -f docker-compose.yaml run --rm agent-runtime python -c 'import os,pandas,geopandas,shapely,pyproj,duckdb,pyarrow; assert os.getuid() == 10001 and os.getgid() == 10001'`

Expected: PASS。

- [ ] **Step 5: 测试并实现 opt-in smoke target**

先用 Node built-in test + mocked fetch/browser 测试成功、Run failed、120 秒 timeout、cancel/finally cleanup、pageerror 和 failed request；文件尚不存在时运行：

Run: `cd apps/web && node --test scripts/smoke-claude.test.mjs`

Expected: FAIL，smoke module 尚不存在。

`apps/web/scripts/smoke-claude.mjs` 使用 Node fetch + `@playwright/test`：创建 Geo Project、上传固定 CSV、创建 Conversation、提交固定 prompt，每秒轮询且 120 秒超时；失败时打印 Run 与 Events。断言至少一个真实 assistant/tool Event（SDK connectivity）、成功的 Python tool execution和 `analysis-evidence.json` data Artifact、Run succeeded、Artifact list 有且仅一个 primary。随后用 Chromium 打开 primary Gateway URL，断言无 pageerror/failed request、body 非空、`window.echarts` 已加载；不比较自然语言或精确图表数据。`finally` 若 Run 未终态先 cancel 并限时等待 finalized，再逻辑删除 Project；删除冲突也打印但不掩盖原始错误。

Make target 先检查 credential，缺失立即非零；再 build/up、安装 Chromium、执行脚本，失败时输出 Go/Runtime logs，最后 purge：

```make
smoke-claude:
	@test -n "$$ANTHROPIC_API_KEY" || (echo 'ANTHROPIC_API_KEY is required' && exit 1)
	docker compose -f docker-compose.yaml up -d --build --wait
	@status=0; \
	  (cd apps/web && pnpm exec playwright install chromium && node scripts/smoke-claude.mjs) || status=$$?; \
	  if [ $$status -ne 0 ]; then docker compose -f docker-compose.yaml logs --tail=200 control-plane agent-runtime; fi; \
	  $(MAKE) purge-deleted; \
	  exit $$status
```

Run: `cd apps/web && node --test scripts/smoke-claude.test.mjs`

Expected: PASS。确认绿色后执行最终验证。

- [ ] **Step 6: 验证并提交**

```bash
cd services/agent-runtime && uv run pytest tests/test_geo_profile.py -v
cd ../..
docker compose -f docker-compose.yaml build agent-runtime
ANTHROPIC_API_KEY= make smoke-claude
git add profiles services/agent-runtime tests/fixtures/geo scripts apps/web/scripts/smoke-claude.mjs apps/web/scripts/smoke-claude.test.mjs Makefile
git commit -m "feat: add the Geo Analyst runtime profile"
```

Expected: Profile/image tests PASS；空 credential smoke 立即 FAIL（不发网络请求）。真实 credential smoke 是人工 opt-in，不属于默认 CI。

## Chunk 4：Vue 产品体验与端到端交付

### Task 17：实现三栏应用骨架与路由状态

**Files:**
- Create: `apps/web/src/app/router.ts`
- Create: `apps/web/src/app/store.ts`
- Create: `apps/web/src/styles/tokens.css`
- Create: `apps/web/src/styles/base.css`
- Create: `apps/web/src/components/ResizablePane.vue`
- Create: `apps/web/src/components/ResizablePane.test.ts`
- Create: `apps/web/src/components/WorkbenchLayout.vue`
- Create: `apps/web/src/components/WorkbenchLayout.test.ts`
- Modify: `apps/web/src/App.vue`
- Modify: `apps/web/src/main.ts`

- [ ] **Step 1: 使用 `@frontend-design` 固定视觉方向**

采用克制的地图制图工作台视觉语言，保持已批准的三栏信息架构；不得加入终端、代码编辑器或地图编辑器。

- [ ] **Step 2: 写布局失败测试**

验证会话栏默认 240px/可折叠、Chat 默认 440px/可拖动、Artifact 占剩余宽度；窄屏使用 tabs；splitter 可键盘操作并具有 ARIA 属性。路由覆盖 `/`（Project onboarding）、`/projects/:projectId`（尚无 Conversation 时可上传/新建会话）、`/projects/:projectId/conversations/:conversationId`（完整工作台）及不存在参数回退。

Run: `cd apps/web && pnpm test -- --run src/components/ResizablePane.test.ts src/components/WorkbenchLayout.test.ts`

Expected: FAIL，布局/router 尚不存在。

- [ ] **Step 3: 实现布局与路由**

Pane 宽度只存 `localStorage`。`App.vue` 挂载 router view，`WorkbenchLayout` 提供 project/sidebar/chat/artifact slots；三种 route 使用同一 layout 按上下文显示 onboarding/空态/完整面板。Artifact selection 使用 query parameter；刷新深链不丢 project/conversation ID。

- [ ] **Step 4: 验证并提交**

```bash
cd apps/web
pnpm test -- --run src/components/ResizablePane.test.ts src/components/WorkbenchLayout.test.ts
pnpm build
cd ../..
git add apps/web/src
git diff --cached --check
git commit -m "feat: add the three-pane application shell"
```

### Task 18：实现 Project、Conversation 与上传体验

**Files:**
- Create: `apps/web/src/lib/api/client.ts`
- Create: `apps/web/src/lib/api/types.ts`
- Create: `apps/web/src/lib/api/upload.ts`
- Create: `apps/web/src/lib/api/upload.test.ts`
- Create: `apps/web/src/features/projects/projectStore.ts`
- Create: `apps/web/src/features/projects/ProjectSwitcher.vue`
- Create: `apps/web/src/features/projects/CreateProjectDialog.vue`
- Create: `apps/web/src/features/projects/CreateProjectDialog.test.ts`
- Create: `apps/web/src/features/projects/InputFiles.vue`
- Create: `apps/web/src/features/projects/InputFiles.test.ts`
- Create: `apps/web/src/features/conversations/conversationStore.ts`
- Create: `apps/web/src/features/conversations/ConversationSidebar.vue`
- Create: `apps/web/src/features/conversations/ConversationSidebar.test.ts`
- Create: `apps/web/src/app/onboarding.test.ts`
- Modify: `apps/web/src/App.vue`
- Modify: `apps/web/src/app/router.ts`
- Modify: `apps/web/src/components/WorkbenchLayout.vue`

- [ ] **Step 1: 写失败的 store/component 测试**

覆盖从空系统创建 `geo-analysis` Project 并导航 `/projects/{id}`、Project 切换、上传进度/取消/失败、Conversation 创建后导航完整 route、切换/重命名/逻辑删除、按 updated_at 分组、本地关键词过滤、409 active Run 提示。组合测试 mount `App.vue`，证明 ProjectSwitcher、InputFiles、ConversationSidebar 实际出现在 Workbench slots。

Run: `cd apps/web && pnpm test -- --run src/lib/api/upload.test.ts src/features/projects src/features/conversations src/app/onboarding.test.ts`

Expected: FAIL，stores/components/upload adapter 尚不存在。

- [ ] **Step 2: 实现 typed fetch client**

普通 JSON/204 请求使用 typed fetch client，统一 Error envelope 与 AbortSignal；上传单独使用可注入 `XMLHttpRequest` 的 typed adapter，监听 `xhr.upload.progress`、映射后端 Error、支持 abort。不得伪造 fetch 上传进度，组件不得重复拼 URL 或解析错误。

- [ ] **Step 3: 实现侧边栏和上传**

V0 create dialog 的 Profile catalog 只有 `geo-analysis`，提交后以服务端 Project 为准。Project response 的 `accepted_input_media_types` 驱动 file picker `accept` 与前端早期提示，后端仍作权威校验；上传成功显示 digest/size。删除需要二次确认；409 提示先取消/等待 Run。

`App.vue`/Workbench 将 ProjectSwitcher 和 InputFiles 挂到 Project 区，将 ConversationSidebar 挂到左栏；Project route 无会话时仍显示上传和“新建会话”。

- [ ] **Step 4: 验证并提交**

```bash
cd apps/web
pnpm test -- --run src/lib/api/upload.test.ts src/features/projects src/features/conversations src/app/onboarding.test.ts
pnpm build
cd ../..
git add apps/web/src
git diff --cached --check
git commit -m "feat: manage projects conversations and inputs"
```

### Task 19：实现 Chat、SSE 与 Run 时间线

**Files:**
- Create: `apps/web/src/lib/api/sse.ts`
- Create: `apps/web/src/lib/api/sse.test.ts`
- Create: `apps/web/src/features/chat/chatStore.ts`
- Create: `apps/web/src/features/chat/ChatPanel.vue`
- Create: `apps/web/src/features/chat/Composer.vue`
- Create: `apps/web/src/features/runs/runStore.ts`
- Create: `apps/web/src/features/runs/RunTimeline.vue`
- Create: `apps/web/src/features/runs/RunTimeline.test.ts`
- Create: `apps/web/src/app/chat-workbench.test.ts`
- Modify: `apps/web/src/App.vue`
- Modify: `apps/web/src/components/WorkbenchLayout.vue`

- [ ] **Step 1: 写 SSE 断线重连失败测试**

用 fetch-stream SSE（不使用不能自定义 header 的原生 EventSource）模拟 sequence 1–3 后断开，再连接发送 `Last-Event-ID: 3`；last sequence 按 `run_id` 隔离，重复 event 幂等，未知非终态 event 忽略。只有 `run.succeeded|run.failed|run.cancelled|run.interrupted` 这些 Go product terminal 才停止重连；`agent.completed/agent.failed` 仍继续等待 publication/finalize Event。组件卸载只 abort 浏览器 stream，不取消 Run。

- [ ] **Step 2: 写 Chat/Timeline 失败测试**

提交后立即显示 user Message 和 queued Run；assistant delta 合并；tool step 折叠；错误显示安全 message 和 correlation ID；取消仅在 queued，或 running 且 phase 为 preparing/agent 时可见，publishing 时隐藏/禁用并解释“正在发布”。

刷新组合测试先 `GET messages` + `GET /conversations/{id}/runs`，为每个 Run 读取 events，恢复所有历史、当前 timeline 和每个非终态 Run 的 SSE；不得从 Message 推测 Run ID。mount `App.vue` 断言 ChatPanel/Composer/RunTimeline 已进入中栏。

Run: `cd apps/web && pnpm test -- --run src/lib/api/sse.test.ts src/features/runs src/features/chat src/app/chat-workbench.test.ts`

Expected: FAIL，SSE/stores/components 尚不存在。

- [ ] **Step 3: 实现 SSE client 与 stores**

Pinia 以 `Map<run_id,lastDurableSequence>` 保存游标；刷新时先拉 Conversation Runs 与各 Run 历史，再对非终态 Run 订阅增量。SSE client 接受 AbortSignal，以有上限的指数退避重连并发送对应 Run 的 Last-Event-ID；abort 只停止浏览器订阅。最终 `assistant.message` 替换/收口 delta preview，避免刷新后重复两条 Agent 回复。

`App.vue`/Workbench 将 ChatPanel 与 RunTimeline 挂到中栏，route 变化时取消旧 Conversation streams 并加载新数据。

- [ ] **Step 4: 验证并提交**

```bash
cd apps/web
pnpm test -- --run src/lib/api/sse.test.ts src/features/runs src/features/chat src/app/chat-workbench.test.ts
pnpm build
cd ../..
git add apps/web/src
git diff --cached --check
git commit -m "feat: stream conversations and run progress"
```

### Task 20：实现 Artifact 列表、历史和 iframe

**Files:**
- Modify: `.env.example`
- Modify: `docker-compose.yaml`
- Create: `apps/web/src/app/config.ts`
- Create: `apps/web/src/app/config.test.ts`
- Create: `apps/web/src/features/artifacts/artifactStore.ts`
- Create: `apps/web/src/features/artifacts/ArtifactPanel.vue`
- Create: `apps/web/src/features/artifacts/ArtifactTabs.vue`
- Create: `apps/web/src/lib/artifact-viewer/ArtifactFrame.vue`
- Create: `apps/web/src/lib/artifact-viewer/ArtifactFrame.test.ts`
- Create: `apps/web/src/features/artifacts/ArtifactPanel.test.ts`
- Create: `apps/web/src/app/artifact-workbench.test.ts`
- Modify: `apps/web/src/App.vue`
- Modify: `apps/web/src/components/WorkbenchLayout.vue`

- [ ] **Step 1: 写 iframe 隔离失败测试**

HTML iframe 只有 `sandbox="allow-scripts"`，没有 `allow-same-origin`；Markdown/data iframe 使用空 `sandbox`，image 用 `<img>`，都直接导航/加载独立 Gateway URL而不做跨 origin fetch。URL 只能来自后端 Artifact resource且 origin 必须等于 `VITE_ARTIFACT_ORIGIN`；切换 Conversation 清空不属于它的 selection。`.env.example` 增加 `ARTIFACT_PUBLIC_ORIGIN=http://localhost:8081`，Compose 同时传给 Go 生成 resource URL，并作为 `VITE_ARTIFACT_ORIGIN` 传给 Web。

- [ ] **Step 2: 写版本体验失败测试**

默认选择最新成功 Run 的 primary；可切换其他 Artifact/旧 Run；当前失败时保留上一成功制品；无制品 Run 显示空状态。

组合测试以 `GET Conversation Runs` → 每个 succeeded Run `GET artifacts` 重建版本历史，mount `App.vue` 断言 ArtifactPanel 已进入右栏。

Run: `cd apps/web && pnpm test -- --run src/app/config.test.ts src/features/artifacts src/lib/artifact-viewer src/app/artifact-workbench.test.ts`

Expected: FAIL，Artifact stores/viewers 尚不存在。

- [ ] **Step 3: 实现 Artifact UI**

HTML 用 `sandbox="allow-scripts"` iframe；Markdown 和 data 因 Gateway 明确无 CORS，使用 `sandbox=""` iframe 让浏览器以 Gateway Content-Type 安全内联显示，并总是提供下载/新标签链接；image 使用 `<img>`。不得 `fetch` 后 `v-html`，V0 不在线编辑。`App.vue`/Workbench 将 ArtifactPanel 挂到右栏。

- [ ] **Step 4: 验证并提交**

```bash
cd apps/web
pnpm test -- --run src/app/config.test.ts src/features/artifacts src/lib/artifact-viewer src/app/artifact-workbench.test.ts
pnpm build
cd ../..
git add .env.example docker-compose.yaml apps/web/src
git diff --cached --check
git commit -m "feat: browse immutable run artifacts"
```

### Task 21：完成 Fake Runtime Playwright E2E

**Files:**
- Modify: `services/control-plane/internal/agentexec/fake.go`
- Modify: `services/control-plane/internal/agentexec/http_test.go`
- Create: `services/control-plane/internal/agentexec/fake_test.go`
- Modify: `tests/fixtures/fake-runtime/geo-report/events.ndjson`
- Modify: `tests/fixtures/fake-runtime/geo-report/outputs/artifact-manifest.json`
- Modify: `tests/fixtures/fake-runtime/geo-report/outputs/report/index.html`
- Create: `tests/fixtures/fake-runtime/geo-report/outputs/report/vendor/echarts.min.js`
- Create: `tests/fixtures/fake-runtime/success-v2/scenario.json`
- Create: `tests/fixtures/fake-runtime/success-v2/events.ndjson`
- Create: `tests/fixtures/fake-runtime/success-v2/outputs/artifact-manifest.json`
- Create: `tests/fixtures/fake-runtime/success-v2/outputs/report/index.html`
- Create: `tests/fixtures/fake-runtime/success-v2/outputs/report/vendor/echarts.min.js`
- Create: `tests/fixtures/fake-runtime/agent-failure/scenario.json`
- Create: `tests/fixtures/fake-runtime/agent-failure/events.ndjson`
- Create: `tests/fixtures/fake-runtime/invalid-manifest/scenario.json`
- Create: `tests/fixtures/fake-runtime/invalid-manifest/events.ndjson`
- Create: `tests/fixtures/fake-runtime/invalid-manifest/outputs/artifact-manifest.json`
- Create: `tests/fixtures/fake-runtime/invalid-manifest/outputs/report/index.html`
- Create: `tests/fixtures/fake-runtime/delayed-success/scenario.json`
- Create: `tests/fixtures/fake-runtime/blocking/scenario.json`
- Create: `tests/e2e/package.json`
- Create: `tests/e2e/pnpm-lock.yaml`
- Create: `tests/e2e/playwright.config.ts`
- Create: `tests/e2e/specs/health.spec.ts`
- Create: `tests/e2e/specs/geo-report.spec.ts`
- Create: `tests/e2e/specs/reconnect-and-failure.spec.ts`
- Create: `tests/e2e/fixtures/locations.csv`
- Modify: `docker-compose.yaml`
- Modify: `Makefile`

- [ ] **Step 1: 先建立隔离 E2E harness 并确认基础绿色**

在 `tests/e2e` 执行并提交 lockfile：

```bash
pnpm add -D '@playwright/test@1.54.1' 'typescript@5.8.3'
pnpm exec playwright install chromium
```

先只写 `health.spec.ts` 断言首页和 `/health`。Compose 所有 host ports 改为带默认值变量。Make target 使用独立 project/端口并无论成功失败清理：

```make
test-e2e:
	@set -eu; \
	  project=harness-forge-e2e; \
	  cleanup() { docker compose -f docker-compose.yaml -p $$project down -v --remove-orphans; }; \
	  trap cleanup EXIT INT TERM; \
	  export RUNTIME_MODE=fake WEB_PORT=15173 CONTROL_PLANE_PORT=18080 ARTIFACT_PORT=18081 RUNTIME_PORT=18090 POSTGRES_PORT=15432 MINIO_PORT=19000 MINIO_CONSOLE_PORT=19001; \
	  export POSTGRES_DB=harness_forge POSTGRES_USER=harness_forge POSTGRES_PASSWORD=local-dev-only MINIO_ROOT_USER=harness_forge MINIO_ROOT_PASSWORD=local-dev-only MINIO_BUCKET=harness-forge; \
	  export WEB_ORIGIN=http://localhost:15173 ARTIFACT_PUBLIC_ORIGIN=http://localhost:18081; \
	  docker compose -f docker-compose.yaml -p $$project up -d --build --wait; \
	  cd tests/e2e; \
	  pnpm install --frozen-lockfile; \
	  pnpm exec playwright install chromium; \
	  BASE_URL=http://localhost:15173 pnpm exec playwright test
```

`playwright.config.ts` 只连接已由 Make 启动的服务，不另起 webServer。Run: `make test-e2e`

Expected: health spec PASS，trap 清理 volumes。先证明 harness 可用，再增加行为红测。

- [ ] **Step 2: 写 Fake scenario 和完整 E2E 失败测试**

Fake 模式保留普通 prompt 默认 `geo-report`；测试前缀 `[fixture:<scenario>]` 选择 `geo-report|success-v2|agent-failure|invalid-manifest|delayed-success|blocking`。`scenario.json` schema 固定：`{version:1, base_fixture:string, event_delay_ms:uint, block_before_type:string|null, release:"none"|"context_cancel"}`。Delayed 固定 `{base_fixture:"geo-report",event_delay_ms:1000,block_before_type:null,release:"none"}`；blocking 固定 `{base_fixture:"geo-report",event_delay_ms:50,block_before_type:"agent.completed",release:"context_cancel"}`。Cancel context 只解除 blocking 且不得再发 terminal；其他业务 event 仍来自 V1 NDJSON。Go 测试覆盖 selector、outputs、第二版本、失败、无效 Manifest、精确 delay hook 和 cancel release；HTTP mode 不解析前缀。

Golden path 创建 Geo Project、上传 CSV、创建两个 Conversation、发送 `[fixture:geo-report]`、观察 tool steps、打开 primary HTML并断言 `window.echarts`、继续 `[fixture:success-v2]` 产生新版本、切回旧版、刷新后通过 Conversation Runs API恢复。

第二 Conversation 也实际提交首个 `[fixture:geo-report]` Run，完成后从 Run API 断言 `source_sdk_session_id=null`，再验证两个 Conversation timeline互不串线。恢复/失败 specs 在 delayed-success 的 `tool.started` 出现后、下一 event 的 1000ms 窗口内 reload，断言 replay sequence 无 gap/duplicate并最终成功；用 blocking Run 占全局 worker、创建第二 queued Run并取消；active 时删除 409；agent-failure、invalid-manifest、当前失败保留上一 Artifact。所有请求经浏览器→Go，禁止 route.fulfill/mock API。

```bash
go -C services/control-plane test ./internal/agentexec -run 'FakeScenario' -v
make test-e2e
```

Expected: 两条命令分别 FAIL 于 selector 与 unknown scenario/第二版本/失败时序断言；E2E health spec 仍 PASS，不能是 connection refused。

- [ ] **Step 3: 实现 Fake scenarios 并确认 adapter 绿色**

实现上述 fixtures。成功 fixture HTML 引用同 prefix `report/vendor/echarts.min.js` 小型 stub并设置 `window.echarts`，用于证明 JS Content-Type + nosniff；真实 ECharts 由 Task 16 smoke 验证。

Run: `go -C services/control-plane test ./internal/agentexec -run 'FakeScenario' -v`

Expected: PASS。

- [ ] **Step 4: 运行完整 E2E 并确认绿色**

Run: `make test-e2e`

Expected: 全部行为 specs PASS；parent 与 `frame-ancestors` 都使用 `localhost:15173`，iframe 不被 CSP 阻止；失败保留 trace/screenshot，trap 始终清理。

- [ ] **Step 5: 运行并提交 E2E**

```bash
make test-e2e
git add services/control-plane/internal/agentexec tests/fixtures/fake-runtime tests/e2e docker-compose.yaml Makefile
git diff --cached --check
git commit -m "test: cover the Harness Forge user journey"
```

Expected: Playwright 全部 PASS，命令结束后 Compose 资源被清理。

### Task 22：文档、完整验证与交付检查

**Files:**
- Modify: `README.md`
- Create: `docs/development/local-setup.md`
- Create: `docs/development/troubleshooting.md`
- Create: `docs/architecture/runtime-protocol.md`
- Create: `docs/decisions/0001-postgres-and-s3-for-local-v0.md`
- Modify: `Makefile`

- [ ] **Step 1: 编写从零启动文档**

README 和本 Task 新增文档全部使用中文，并链接中英文设计规格。README 包含前置条件、复制 `.env.example`、启动、创建 Geo Project、上传 fixture、测试、停止和清理；读者无需先读设计规格。

- [ ] **Step 2: 编写排障、协议与 ADR**

排障覆盖端口、MinIO bucket、migration、Runtime unavailable、unfinalized Run、Session missing、孤儿对象、SSE 和 Claude credential。ADR 只记录个人项目选择 PostgreSQL+S3 的不可直觉权衡，以及重新评估条件。

- [ ] **Step 3: 运行完整验证**

先把 `test-integration` 从 Task 4 的单 package target 扩为隔离全量 target：

```make
test-integration:
	@set -eu; \
	  project=harness-forge-integration; \
	  cleanup() { docker compose -f docker-compose.yaml -p $$project down -v --remove-orphans; }; \
	  trap cleanup EXIT INT TERM; \
	  export RUNTIME_MODE=fake CONTROL_PLANE_PORT=28080 ARTIFACT_PORT=28081 RUNTIME_PORT=28090 POSTGRES_PORT=25432 MINIO_PORT=29000 MINIO_CONSOLE_PORT=29001; \
	  export POSTGRES_DB=harness_forge POSTGRES_USER=harness_forge POSTGRES_PASSWORD=local-dev-only MINIO_ROOT_USER=harness_forge MINIO_ROOT_PASSWORD=local-dev-only MINIO_BUCKET=harness-forge; \
	  export WEB_ORIGIN=http://localhost:25173 ARTIFACT_PUBLIC_ORIGIN=http://localhost:28081; \
	  docker compose -f docker-compose.yaml -p $$project up -d --build --wait postgres minio minio-init runtime-volume-init control-plane agent-runtime; \
	  TEST_DATABASE_URL='postgres://harness_forge:local-dev-only@localhost:25432/harness_forge?sslmode=disable' \
	  TEST_MINIO_ENDPOINT='localhost:29000' TEST_MINIO_ACCESS_KEY='harness_forge' TEST_MINIO_SECRET_KEY='local-dev-only' \
	  COMPOSE_PROJECT_NAME=$$project go -C services/control-plane test -tags=integration ./internal/... -v
```

该命令运行 postgres、projects、runs、cleanup、workspace permissions 及任何后续 `//go:build integration` tests，并由 trap 始终清除隔离 volumes。

```bash
make test
make test-integration
make test-e2e
docker compose -f docker-compose.yaml config --quiet
git diff --check
```

Expected: 所有测试 0 failure，Compose config 与 diff check exit 0。

- [ ] **Step 4: 在干净环境验证启动**

```bash
set -eu
project=harness-forge-clean-verify
cleanup() { docker compose -f docker-compose.yaml -p "$project" down -v --remove-orphans; }
trap cleanup EXIT INT TERM
export RUNTIME_MODE=fake WEB_PORT=35173 CONTROL_PLANE_PORT=38080 ARTIFACT_PORT=38081 RUNTIME_PORT=38090 POSTGRES_PORT=35432 MINIO_PORT=39000 MINIO_CONSOLE_PORT=39001
export POSTGRES_DB=harness_forge POSTGRES_USER=harness_forge POSTGRES_PASSWORD=local-dev-only MINIO_ROOT_USER=harness_forge MINIO_ROOT_PASSWORD=local-dev-only MINIO_BUCKET=harness-forge
export WEB_ORIGIN=http://localhost:35173 ARTIFACT_PUBLIC_ORIGIN=http://localhost:38081
docker compose -f docker-compose.yaml -p "$project" up -d --build --wait
curl -fsS http://localhost:35173/
curl -fsS http://localhost:35173/health
docker compose -f docker-compose.yaml -p "$project" ps
```

Expected: 必需服务全部 healthy；只清理 `harness-forge-clean-verify`，不删除开发者默认项目数据。README 人工 golden path 与 Task 21 自动路径字段一致。

- [ ] **Step 5: 提交交付文档**

```bash
git add README.md docs Makefile
git commit -m "docs: document local V0 operation"
test -z "$(git status --porcelain)"
```

## 最终验收

- [ ] `make dev` 能启动 PostgreSQL、MinIO、Go、Python Runtime 和 Vue。
- [ ] 默认 `make test` 不访问 Claude API。
- [ ] `make test-integration` 使用真实 PostgreSQL/MinIO 并通过。
- [ ] `make test-e2e` 使用 Fake Runtime 跑通完整用户链路。
- [ ] `make smoke-claude` 仅在显式配置 credential 时运行。
- [ ] 同一 Project 可创建多个独立 Conversation，并共享 Input File。
- [ ] 成功 Run 使用 SDK Session fork + commit；失败 Run abort，不污染 active context。
- [ ] Control Plane/Runtime 崩溃后先协调 execution，再启动 scheduler。
- [ ] Artifact 只有已提交元数据时可见，HTML 在隔离 iframe 中展示。
- [ ] 逻辑删除和 `purge-deleted` 满足幂等顺序。
- [ ] Git 工作区干净，提交粒度与 Task 对齐。
