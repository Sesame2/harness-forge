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

### 基础设施镜像来源

2026-10-02 干净 GitHub runner 无法拉取原先固定的 Quay MinIO 镜像；官方 Docker Hub 同版也不可公开获取，`dl.min.io` 对这两个版本的 amd64/arm64 二进制及校验附件均返回 410。MinIO 已转为 [source-only 分发](https://github.com/minio/minio#source-only-distribution)。本机旧镜像缓存仍可用不能证明 fresh clone 可运行。

Compose 因此通过 [`infra/minio/Dockerfile`](../../infra/minio/Dockerfile) 的两个 target 构建同源码版本：

| 组件 | 源码 release | 固定官方 commit |
| --- | --- | --- |
| MinIO | `RELEASE.2025-04-22T22-12-26Z` | [`0d7408fc9969caf07de6a8c3a84f9fbb10a6739e`](https://github.com/minio/minio/tree/0d7408fc9969caf07de6a8c3a84f9fbb10a6739e) |
| mc | `RELEASE.2025-04-16T18-13-26Z` | [`b00526b153a31b36767991a4f5ce2cced435ee8e`](https://github.com/minio/mc/tree/b00526b153a31b36767991a4f5ce2cced435ee8e) |

构建校验 source SHA，使用上游 ldflags 生成器、固定 release 时间和 Go module 校验和，base image 固定 digest，保留上游 LICENSE/CREDITS。分别为当前目标架构编译静态二进制；不使用失效的下载地址或第三方镜像。首次需要访问 GitHub、Go module 服务和基础镜像仓库，耗时比原来直接拉镜像更长；后续可复用本机构建缓存。

这些是同源码版本的新镜像，不宣称与旧官方 OCI 镜像逐字节相同；此改动必须形成新冻结 SHA，并在本机 fresh clone 与独立 CI 重新验收。

### 验收步骤

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

## OpenAI 双协议兼容模式

默认栈不启用网关。可选 [`docker-compose.openai.yaml`](../../docker-compose.openai.yaml) 增加一个固定 LiteLLM 网关，保留 Go Docker Provider 和 Python Claude Agent SDK。仅支持声明过的文本、流式、本地 function 工具与多轮上下文；原生 thinking、显式缓存、多模态、服务器工具、未知语义等明确失败，不能认为所有 Claude 能力都可无损转换。详细边界见[中文规格](../superpowers/specs/2026-10-02-openai-gateway-design.md)。

编辑仓库根 `.env`（已存在时不要覆盖），只需这四个用户字段，未使用协议的模型可留空：

```dotenv
OPENAI_API_KEY=
OPENAI_BASE_URL=
OPENAI_CHAT_MODEL=
OPENAI_RESPONSES_MODEL=
```

上游地址必须为 HTTPS，不允许用户名、密码、query、fragment、反斜杠或空白。根地址自动补 `/v1`，已有路径只去末尾斜杠，不重复追加 `/v1`。填写实际模型名，不填 LiteLLM 的 provider 前缀；不会自动猜测、替换模型或切换协议。`chmod 600 .env`，不要把真实值放进命令行、截图或 Git。

```sh
# 下列命令显式访问真实上游，可能收费；不会被 make test 调用。
node scripts/openai.mjs check --api chat
node scripts/openai.mjs check --api responses
node scripts/openai.mjs dev --api chat
# dev 改用 Responses：先等待所有旧 Run finalize，再执行
# node scripts/openai.mjs dev --api responses

# 读取其他位置的凭证，而不是当前 shell 的同名变量
node scripts/openai.mjs check --api chat --env-file /absolute/path/to/.env
```

`check` 每次只向选中接口发送一次最多 64 output tokens 的短文本请求，30 秒超时，无重试；只打印模式、HTTP 状态和模型哈希，不输出响应或凭证。启动器使用 Node 原生 dotenv 解析而非 `source`，仅读取上述四个字段，并覆盖内部数据库、MinIO、Runtime 路径和 Provider，避免继承其他项目的连接配置。网关 key 每次启动随机生成，OpenAI key 只传入 gateway，Runtime 只取得网关 key；网关不发布宿主机端口。不要直接打印带真实配置的 `docker compose config`。

`dev` 使用独立 Compose 项目 `harness-forge-openai`，端口与默认开发栈相同，所以启动前先停止占用端口的默认栈。重复 `dev` 保留卷；失败也不自动销毁开发数据。停止并保留卷：

```sh
docker compose --env-file /dev/null -p harness-forge-openai \
  -f docker-compose.yaml -f docker-compose.openai.yaml down
```

兼容模式按协议、规范上游 URL、实际模型隔离 Session 目录；密钥轮换不改变归属。旧路由仍有未 finalize Run 时拒绝切换；切换后旧会话不能在另一模型上续聊，切回原路由才能继续，不迁移 transcript。已软删除会话的物理清理仍覆盖受管旧路由，失败不会假报成功。

显式完整 smoke 入口如下，按顺序运行，不把连通性检查当作应用验收。完整浏览器扩展及两种真实流程的最终结果以[最新 checkpoint](../superpowers/checkpoints/2026-10-02-openai-gateway.md) 为准，当前仍在实施验收中：

```sh
node scripts/openai.mjs smoke --api chat
node scripts/openai.mjs smoke --api responses
# 等价 Make 入口：make openai-check|dev-openai|smoke-openai API=chat ENV_FILE=.env
```

smoke 每次生成独立项目 `harness-forge-openai-smoke-chat-<随机后缀>` 或 `harness-forge-openai-smoke-responses-<随机后缀>`，启动日志给出本次完整项目名；各自 Web/API/制品/Runtime/Postgres/MinIO/Console 端口依次为 `45173/48080/48081/48090/45432/49000/49001`、`55173/58080/58081/58090/55432/59000/59001`。两模式按顺序运行；同协议并发可能端口冲突而失败，但不会共用项目或清理另一调用的卷。如已有本次项目名的容器、卷或网络，立即拒绝，不自动接管。启动成功、失败或 SIGINT/SIGTERM 都只清理本次拥有的项目及卷；若报告 `HF_OPENAI_CLEANUP_FAILED`，按启动日志中的完整项目名核对归属再手工清理，不删除其他项目。需要已安装 `apps/web` 依赖，入口会显式安装其固定 Playwright Chromium。

真实 smoke 仅使用合成 CSV，单 Run 120 秒、Profile 最大 8 轮、兼容模式单次输出上限 8192 tokens；SDK 的 Anthropic `max_budget_usd` 不是 OpenAI 上游的精确费用上限。网关禁用自动重试/fallback，但固定 Claude CLI 部分连接/流中恢复分支并不受单一 retry 参数控制，因此不承诺整个工作流只有一个 HTTP 请求。失败不会由 launcher 自动重跑，修复后显式复测，禁止无界付费循环。

无付费验证：`make test-openai` 只用假 HTTP/子进程；`make test-gateway` 才显式启动固定真实网关接本地 mock 上游。无凭证的覆盖文件渲染检查可用下面命令；占位值仅为配置校验服务，不表示可以无凭证调用：

```sh
env -i PATH="$PATH" HOME="$HOME" docker compose --env-file /dev/null \
  -f docker-compose.yaml -f docker-compose.openai.yaml config --quiet
```
