# 本地开发与干净启动

首次运行直接按 [README](../../README.md) 安装依赖、复制 `.env.example`、启动 Fake 并完成 Geo golden path。本页补充配置、隔离验收和真实 Claude 的边界，不要求先阅读设计规格。

设计依据：[中文](../superpowers/specs/2026-07-19-harness-forge-design.zh-CN.md) / [English](../superpowers/specs/2026-07-19-harness-forge-design.md)。

## 配置与日常命令

Compose 读取当前仓库 `.env`，但同名 shell 环境变量优先；不要在带有别的项目数据库、MinIO、Runtime 配置的终端直接启动。容器连接使用服务名 `postgres`、`minio`、`agent-runtime`，不是宿主机 `localhost`。共享路径保持 `/workspaces`、`/sessions/executions`、`/sessions/claude`。不挂载宿主机 Claude 配置或凭证目录。

`make dev` 在前台启动；`docker compose -f docker-compose.yaml up -d --build --wait` 在后台启动。两者均需显式 `SANDBOX_PROVIDER=fake ANTHROPIC_API_KEY='' ANTHROPIC_BASE_URL=''` 才是无模型调用的开发模式。`make down` 保留卷。

宿主机测试依赖安装命令见 README；E2E 自行在 `tests/e2e` 执行冻结锁安装及 Chromium 安装。Linux 上如 Chromium 提示缺少系统库，先按 Playwright 错误提示安装所需系统依赖，不能把浏览器未启动视为用例通过。当前 Web 代理面向 Compose 内部服务名，本文采用 Compose 启动 Web，不将它当作无需额外配置的宿主机独立服务。

若机器残留旧 Go 的 `GOROOT`，使用 `GOTOOLCHAIN=local env -u GOROOT make test`，integration 同理。正常 Go 安装不必设置 `GOROOT`；`GOTOOLCHAIN=local` 要求本机已安装 Go 1.25+。

端口通过 `WEB_PORT`、`CONTROL_PLANE_PORT`、`ARTIFACT_PORT`、`RUNTIME_PORT`、`POSTGRES_PORT`、`MINIO_PORT`、`MINIO_CONSOLE_PORT` 修改。Web 端口改变时同步 `WEB_ORIGIN`；制品端口改变时同步 `ARTIFACT_PUBLIC_ORIGIN`，Compose 会将其传给 Web 的 `VITE_ARTIFACT_ORIGIN`。业务页面与制品必须保持独立 origin。

## 可复现的 fresh clone 验收

冻结待验收 commit 后，在空临时目录执行 `git clone --no-local <本地仓库绝对路径> harness-forge-clean`，进入克隆并 `git checkout <完整 commit SHA>`。不要复制原工作区 `.env`、node_modules、`.venv`、构建产物、卷或缓存；用 `cp .env.example .env` 新建配置，再执行 README 的依赖安装命令。记录实际 commit 与工具版本。

下面整段在克隆根目录的**子 shell**运行，使用独立 `harness-forge-clean-verify` 和全新卷。它显式覆盖所有服务连接、路径及空 Claude 凭证；按回车前可以在浏览器完成 README golden path。退出后仅清理本次拥有的验证项目。资源检查拒绝未知同名资源；不要删掉检查以强行继续。

```sh
(
set -eu
project=harness-forge-clean-verify
compose="$PWD/docker-compose.yaml"
for resource in container volume network; do
  flags=-q; if [ "$resource" = container ]; then flags=-aq; fi
  existing=$(docker "$resource" ls "$flags" --filter "label=com.docker.compose.project=$project")
  if [ -n "$existing" ]; then
    echo "已有 $project 的 $resource；请先核对归属，不自动清理" >&2
    exit 1
  fi
done
export SANDBOX_PROVIDER=fake ANTHROPIC_API_KEY='' ANTHROPIC_BASE_URL=''
export WEB_PORT=35173 CONTROL_PLANE_PORT=38080 ARTIFACT_PORT=38081 RUNTIME_PORT=38090 POSTGRES_PORT=35432 MINIO_PORT=39000 MINIO_CONSOLE_PORT=39001
export WEB_ORIGIN=http://localhost:35173 ARTIFACT_PUBLIC_ORIGIN=http://localhost:38081
export POSTGRES_DB=harness_forge POSTGRES_USER=harness_forge POSTGRES_PASSWORD=local-dev-only
export DATABASE_URL='postgres://harness_forge:local-dev-only@postgres:5432/harness_forge?sslmode=disable'
export MINIO_ENDPOINT=http://minio:9000 MINIO_ROOT_USER=harness_forge MINIO_ROOT_PASSWORD=local-dev-only MINIO_ACCESS_KEY=harness_forge MINIO_SECRET_KEY=local-dev-only MINIO_BUCKET=harness-forge
export RUNTIME_URL=http://agent-runtime:8090 WORKSPACE_ROOT=/workspaces RUN_WORKSPACE_ROOT=/workspaces RUNTIME_STATE_ROOT=/sessions/executions CLAUDE_CONFIG_DIR=/sessions/claude
cleanup() {
  status=$?
  trap - EXIT
  docker compose --env-file /dev/null -p "$project" -f "$compose" down -v --remove-orphans || { if [ "$status" -eq 0 ]; then status=1; fi; }
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
docker compose --env-file /dev/null -p "$project" -f "$compose" up -d --build --wait
docker compose --env-file /dev/null -p "$project" -f "$compose" exec -T control-plane sh -ec 'test "$SANDBOX_PROVIDER" = fake'
curl -fsS http://localhost:35173/health
docker compose --env-file /dev/null -p "$project" -f "$compose" ps
printf '\n打开 http://localhost:35173，完成 README golden path 并记录 IDs；回车后销毁本次验证卷。\n'
read -r answer
)
```

记录 Project、Conversation、Run、主 Artifact ID、Run `succeeded` 与 `finalized_at`、HTML 实际显示结果及资源清理结果到[验证记录](verification.md)。只检查 health 不能替代浏览器操作。在**第二台独立 Docker 机器或干净 CI runner** 检出同一冻结 commit，再运行 README 全套验证命令；同一机器换目录或 Compose 项目不算第二环境。未执行就保留“待验证”，不得补写成功。

## 真实 Claude（人工选择）

`make smoke-claude` 是显式 opt-in，会使用真实模型、可能产生费用，并启动 Docker Provider；它要求调用者主动提供 `ANTHROPIC_API_KEY`，可按自己的服务设置 `ANTHROPIC_BASE_URL`。不要把凭证写进 git、命令记录、截图或验证报告，也不要从宿主机 Claude 配置自动复制凭证。自动测试不执行此命令。

使用独立开发环境或先按[Provider 切换规则](troubleshooting.md#provider-配置不匹配)清理旧历史，不能在 Fake 数据卷上直接改为 Docker。人工确认费用、网络和数据范围后才运行；真实 smoke 与 Fake E2E 的结果分别记录。
