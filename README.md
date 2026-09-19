# harness-forge

个人 Harness 能力底座：Go 控制平面、Vue Web、Python Claude Agent SDK Runtime。界面包含会话管理侧栏、对话时间线和制品预览；首个应用是地理分析。V0 使用 Docker，Go 保留 SandboxProvider 边界，尚未接入 E2B。

这是本地单用户原型，没有认证或多租户隔离。Compose 发布的端口不限定回环地址；请在可信开发环境运行，不要直接暴露到公网。

## 从零运行（无需 Claude 凭证）

准备 Git、Make、Docker Engine 或 Docker Desktop/OrbStack 及 Docker Compose v2+。运行宿主机测试还需 Go 1.25+、Node.js 22.13+（22.x）或 24+、pnpm 9、Python 3.12 和 uv。锁文件与容器固定具体依赖；首次安装需要下载镜像、包和 Chromium。

```sh
git clone git@github.com:Sesame2/harness-forge.git
cd harness-forge
cp .env.example .env
pnpm --dir apps/web install --frozen-lockfile
(cd services/agent-runtime && uv sync --frozen)
go -C services/control-plane mod download

# .env.example 默认是 docker；必须显式选择 Fake，并覆盖继承的 Claude 凭证。
export SANDBOX_PROVIDER=fake ANTHROPIC_API_KEY='' ANTHROPIC_BASE_URL=''
docker compose -f docker-compose.yaml up -d --build --wait
docker compose -f docker-compose.yaml exec -T control-plane sh -ec 'test "$SANDBOX_PROVIDER" = fake'
```

浏览器打开 <http://localhost:5173>。Go API 为 8080，独立制品网关为 8081，Python Runtime 为 8090，PostgreSQL 为 5432，MinIO 为 9000/9001。端口冲突和自定义配置见[本地开发](docs/development/local-setup.md)。Fake 在 Go 进程内回放固定事件，不调用 Claude；Compose 中 Python 服务仍会启动，供健康检查与权限验证使用。

1. 在首页创建项目，填写名称；当前默认 Profile 为 `geo-analysis`。
2. 展开“项目资料”，上传仓库里的 `tests/e2e/fixtures/locations.csv`，确认文件出现在列表中。
3. 在左侧新建会话，发送 `[fixture:geo-report]`。
4. 等待 Run 显示“已完成”（API status 为 `succeeded`），检查工具步骤完成，右侧主 HTML 制品显示 `Geographic report`。Fake 内容是固定测试报告，不代表真的分析了 CSV。
5. 同会话发送 `[fixture:success-v2]`，可切换新旧制品版本；新建另一会话可验证独立历史和共享项目资料。

普通提示词也会走 Fake 默认报告。需要验证真实 Claude 时，另行阅读[人工 smoke 注意事项](docs/development/local-setup.md#真实-claude人工选择)，不要将 Fake 成功当作真实 SDK/API 验收。

## 测试

```sh
make verify-layout
make test
make test-integration
make test-e2e
pnpm --dir apps/web build
docker compose -f docker-compose.yaml config --quiet
git diff --check
```

`make test` 不访问 Claude API。Integration 使用真实 PostgreSQL/MinIO 和两容器共享卷权限验证；E2E 使用真实浏览器 → Go → Fake → 制品网关。两者使用独立 Compose 项目、固定独立端口、空 Claude 凭证，不读取 `.env`，结束时删除各自新建的测试卷。若发现同名容器、卷或网络，会拒绝启动而不是自动删除；先核对资源归属。详见[验证记录](docs/development/verification.md)。

## 停止和清理

```sh
# 停止默认开发栈，保留 PostgreSQL、MinIO、工作目录和 Session 卷。
make down
```

界面删除 Project/Conversation 是软删除。确认不再需要这些数据后，用与原 Run **相同的 Provider** 查看并执行物理清理；例如上述 Fake 开发栈：

```sh
SANDBOX_PROVIDER=fake ANTHROPIC_API_KEY='' ANTHROPIC_BASE_URL='' make purge-deleted-dry-run
SANDBOX_PROVIDER=fake ANTHROPIC_API_KEY='' ANTHROPIC_BASE_URL='' make purge-deleted
```

`purge-deleted` 不可逆，会删除软删除对象关联的数据库记录、对象存储内容及 Runtime/工作目录，并扫描孤儿对象。它不会把仍保留的项目迁移到其他 Provider。切换 Provider 前必须用旧 Provider 清理所有保留历史，或在确认数据可丢弃后重置该环境。**`docker compose down -v` 是删除整个所选 Compose 项目的卷，不是只清理软删除项目**；不要对未知项目或需要保留的数据执行。详见[排障](docs/development/troubleshooting.md)。

## 仓库与文档

```text
services/control-plane/   Go API、调度、持久化、SandboxProvider、制品网关
services/agent-runtime/   Python Claude Agent SDK 与 Runtime V1
apps/web/                Vue 三栏工作台
profiles/                应用 Profile（当前 geo-analysis）
contracts/               OpenAPI 与版本化 JSON Schema
tests/                   Fake fixtures、浏览器 E2E
infra/                   本地基础设施初始化
docs/                    设计、实施、协议、开发与验收记录
docker-compose.yaml      本地服务编排
```

- [中文设计规格](docs/superpowers/specs/2026-07-19-harness-forge-design.zh-CN.md) / [English design specification](docs/superpowers/specs/2026-07-19-harness-forge-design.md)
- [本地开发](docs/development/local-setup.md)、[排障](docs/development/troubleshooting.md)、[验证记录](docs/development/verification.md)
- [Runtime 协议](docs/architecture/runtime-protocol.md)、[SandboxProvider 与 E2B 接口边界](docs/architecture/sandbox-provider.md)、[存储选择 ADR](docs/decisions/0001-postgres-and-s3-for-local-v0.md)

## 当前进度与恢复入口

Task 1–21 已完成；Task 22 的中文交付文档和干净环境验收正在继续。先阅读[最新恢复 checkpoint](docs/superpowers/checkpoints/2026-09-19-final-tasks.md)，不要重复实现已完成任务。完整历史见[连续执行记录](docs/superpowers/checkpoints/2026-09-12-full-execution.md)。

- [实施计划](docs/superpowers/plans/2026-07-19-harness-forge-v0.md)

三栏业务前端、Runtime、Geo Profile 与完整 Fake E2E 已完成；第二独立 Docker 环境验收仍待落实，尚不能宣称完整交付。真实 Claude smoke 保持人工 opt-in 且尚未运行。
