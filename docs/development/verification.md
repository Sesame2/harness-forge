# V0 交付验证记录

设计依据：[中文](../superpowers/specs/2026-07-19-harness-forge-design.zh-CN.md) / [English](../superpowers/specs/2026-07-19-harness-forge-design.md)。操作步骤见 [README](../../README.md) 与 [fresh clone 验收](local-setup.md#可复现的-fresh-clone-验收)。

本页明确区分工作区验证、同机 fresh clone 和第二独立环境。2026-10-02，原 V0 已通过同一冻结 commit 的本机与独立 CI 完整验收，Task 22 的第二环境与记录交付闸门完成；本记录随独立文档提交同步主分支。所有下列自动验证使用 Fake/测试替身，不调用真实模型；原生 Claude smoke 仍未运行。新增 OpenAI 网关 Task 23–25 不包含在本次 V0 验收中，实施工作树仍有该后续任务的变更，两协议经网关的真实全流程尚未执行。

## 当前冻结版本：两环境验收通过（2026-10-02）

两个环境实际检出的源码均为 `7db5334948a2f377bbf2d2dc9eed9a02a80d8d35`。它包含固定官方源码构建 MinIO/mc、integration 禁用测试结果缓存，以及两个 JS package 的 `pnpm@9.15.4` 声明。后续仅记录证据的提交不替代这个已验收源码 SHA。

独立 CI：[36995155512，全部通过](https://github.com/Sesame2/harness-forge/actions/runs/36995155512)。Workflow 提交为 `64fc200c415edde6bdbf75061a2f1b0dddbd26fc`，其 checkout 明确固定到上述源码 SHA；作业运行于 UTC 10:23:02–10:29:40，耗时 6 分 38 秒。主执行代理核对了实际 checkout、完整日志及清理结果，API 状态亦为 `completed/success`。

| 记录项 | 本机 fresh clone | 第二独立环境：干净 GitHub runner |
| --- | --- | --- |
| 源码 SHA | `7db5334948a2f377bbf2d2dc9eed9a02a80d8d35` | `7db5334948a2f377bbf2d2dc9eed9a02a80d8d35` |
| OS / architecture | macOS 27.0（26A428）/ arm64 | Ubuntu 24.04.5 LTS，Linux 6.17.0-1022-azure / x86_64 |
| Docker client/server；Compose | 29.4.0 / 29.4.0；5.1.2 | 28.0.4 / 28.0.4；2.38.2 |
| Go；Node；pnpm | 1.25.4；25.6.1；9.15.4 | 1.25.4；24.11.1；9.15.4 |
| Python；uv | 3.12.7；0.6.6 | 3.12.11；0.6.6 |
| `make verify-layout` | PASS | PASS |
| `make test` | PASS：Go 全包；Python 253（7.38s）；Vitest 108 / 15 文件；Node 17 | PASS：Go 全包；Python 253（7.97s）；Vitest 108 / 15 文件；Node 17 |
| `make test-integration` | PASS；真实权限用例 0.43s，无 skip / cached | PASS；真实权限用例 0.65s，无 skip / cached |
| `make test-e2e` | PASS：5/5（23.5s） | PASS：5/5（23.0s） |
| `pnpm --dir apps/web build` | PASS | PASS |
| Compose `--env-file /dev/null config --quiet` | PASS | PASS |
| `git diff --check` / checkout Git clean | PASS | PASS |
| README 浏览器路径 | 独立新卷真实 UI，记录下列 IDs，执行代理与父级均查看截图 | 真实浏览器 E2E 覆盖同一路径；未另记独立 UI IDs 或人工截图 |
| 清理和标签复查 | integration、e2e、clean-verify 均无容器、卷、网络残留 | integration、e2e 均无容器、卷、网络残留 |

本机克隆为 `/private/tmp/harness-forge-accept-7db5334-20261002`，通过 `git clone --no-local` 创建并 detached checkout；按冻结锁文件安装依赖，没有复制原工作区 `.env`、node_modules、`.venv`、构建产物或数据卷。此轮不创建 `.env`，所有 Compose 验证显式使用 `/dev/null` 和 Fake/空凭证环境；末尾再次确认 `.env` 不存在。安装可以复用宿主下载与 Docker 构建缓存，所以同机 clone 本身不冒充第二独立环境。Go/Node 明确优先使用 `/opt/homebrew/bin`，Go 命令使用 `GOTOOLCHAIN=local env -u GOROOT`；新 Web 镜像在 `--network none` 下执行 `pnpm --version` 确认为 9.15.4。

### 当前版本的本机 golden path

在独立 `harness-forge-clean-verify` 新卷上，通过真实 UI 创建 Geo Project、上传 `locations.csv`、创建 Conversation、发送 `[fixture:geo-report]`，确认工具步骤完成、Run `succeeded` 且已 finalized、主 HTML 显示、iframe 内 `window.echarts` 就绪，pageerror 为 0。

- Project：`c71d7480-223b-4d5d-a20b-c63459590171`
- Conversation：`9dd59bf5-c8b0-4825-a354-1b7bff9e3322`
- Run：`280253da-6eed-4cc4-987a-1854656e818d`
- Primary Artifact：`9ca391b9-a445-4c01-8346-75492790f3e5`
- `finalized_at`：`2026-10-02T10:27:01.43134Z`
- 本机截图：`/private/tmp/harness-forge-accept-7db5334-20261002-golden.png`；已由执行代理和父级目视检查，不代表用户亲自验收。临时截图可能被系统清理，不是项目运行依赖。

以上测试卷已销毁，IDs 仅作为历史证据。首次 golden 栈启动遇到宿主 39000 瞬时占用，trap 完整清理；只读检查时占用已消失，实际 bind preflight 通过后原命令重试成功。没有停止其他进程，没有修改源码、端口或测试断言。

### 历史失败与验证边界

- [首次 CI 36991884881](https://github.com/Sesame2/harness-forge/actions/runs/36991884881) 检出九月 `c5962a5`，unit/build 通过，integration 在旧 MinIO 镜像拉取阶段失败，E2E 未运行。官方镜像停止公开分发，历史二进制及校验附件返回 410，不能用本机旧缓存声称 fresh clone 可用。
- `4ec6137` 改为同 release 固定官方 source SHA 构建 MinIO/mc，并给 integration 加 `-count=1`。该版本本机 fresh clone 完整通过；[第二次 CI 36994212247](https://github.com/Sesame2/harness-forge/actions/runs/36994212247) 的 amd64 源码镜像和真实 integration 通过，但 E2E 在 Web 镜像构建阶段失败，浏览器未启动：缺少 `packageManager` 使 Corepack 选择滚动 pnpm 12.8.1，固定 Node 镜像的旧 Corepack 无法找到其入口。
- `7db5334` 为两个 JS package 固定已验证的 pnpm 9.15.4，不修改 dependency/lock；no-cache Web build、离线版本检查和上述完整两环境验证通过。新 MinIO 镜像不宣称与旧官方 OCI 镜像逐字节一致。
- 成功 CI 留有 setup-go Action 声明 Node 20 已弃用、runner 强制使用 Node 24 的非阻断告警；宿主测试已有 Starlette/httpx deprecation 与 Node localstorage 告警，无失败。
- Fake E2E/golden path 使用本地图表 stub；SDK fork/commit/abort、故障恢复等由模块/合同测试验证，不冒充真实 Claude 或 OpenAI 服务成功。最新网关执行进度见 [十月 checkpoint](../superpowers/checkpoints/2026-10-02-openai-gateway.md)。

## 本地实现检查（2026-09-19）

- 起始 commit：`68724ff`；此处结果针对随后 Task 22 的工作区变更，不冒充冻结 commit 的重验。
- 环境：macOS 27.0（26A428）、arm64；Docker 29.4.0（arm64）、Compose 5.1.2。
- 宿主工具：Go 1.25.4、Node 25.6.1、pnpm 9.15.4、uv 0.6.6、Python 3.12.7。
- `test-integration` 编排检查先 RED（原目标仅 postgres package、没有隔离项目），更新后 GREEN。
- `GOTOOLCHAIN=local env -u GOROOT make test-integration`：PASS，真实 PostgreSQL/MinIO；`TestComposeWorkspacePermissions` 实际运行并 PASS，不是 skip。退出自动删除本次 `harness-forge-integration` 容器、卷与网络。
- `GOTOOLCHAIN=local env -u GOROOT make test`：PASS（Go 全包、Python 253、Vitest 108 / 15 suites、smoke 脚本替身测试 17）。既有 Starlette/httpx deprecation 与 Node localstorage 警告仍在，无测试失败。
- `make verify-layout`、Compose 空 env-file `config --quiet`、`git diff --check`：PASS；integration 项目 container/volume/network 标签复查均为空。

## 九月冻结 commit 验收（历史）

冻结 commit：`c5962a5299264a5dee158f255606138b3a7d3b12`。父级通过 `git clone --no-local` 新克隆并 detached checkout 此 commit，复制 `.env.example` 新建配置，按 README 安装依赖并执行验证。没有从原工作区复制 `.env`、node_modules、`.venv`、构建产物或卷；安装工具与 Docker 可复用本机下载/镜像缓存，因此这仍然只是同机验证。

当时后续 `e81d060` 仅校正 README 的 Node 最低版本，记录提交不改变已验收代码。下表保留九月时点的“待运行”状态，不表示当前仍待第二环境；十月因基础设施分发与工具漂移修复已重新冻结，结果见页首同 SHA 两环境证据。

| 记录项 | 同机 fresh clone | 第二独立 Docker 机器 / 干净 CI |
| --- | --- | --- |
| 冻结 commit（完整 SHA） | `c5962a5299264a5dee158f255606138b3a7d3b12` | 待提供环境；必须同一 commit |
| 日期、OS / architecture | 2026-09-19；macOS 27.0（26A428）/ arm64 | 待填写 |
| Docker / Compose 版本 | 29.4.0（arm64）/ 5.1.2 | 待填写 |
| `make verify-layout` | PASS | 待运行 |
| `make test` | PASS：Go 全包、Python 253、Vitest 108、Node 17 | 待运行 |
| `make test-integration` | PASS；真实权限测试 0.49s，无 skip | 待运行 |
| `make test-e2e` | PASS：5/5（23.2s） | 待运行 |
| `pnpm --dir apps/web build` | PASS | 待运行 |
| `docker compose -f docker-compose.yaml config --quiet` | PASS | 待运行 |
| `git diff --check` | PASS；clone Git 状态干净 | 待运行 |
| README 浏览器 golden path | PASS；下列 IDs，已检查页面截图 | 待运行 |
| 测试资源清理与标签复查 | PASS；三个拥有的测试项目均无容器、卷、网络残留 | 待运行 |

宿主 Go 命令使用 `GOTOOLCHAIN=local env -u GOROOT` 绕过本机旧 GOROOT；其他工具版本同上。浏览器连接新建的 `harness-forge-clean-verify`（35173/38080/38081 等独立端口），真实操作创建项目、上传 CSV、新建会话、发送 `[fixture:geo-report]`，断言 Run `succeeded` 且 `finalized_at` 非空、工具步骤完成、主 HTML 显示、隔离 iframe 内 `window.echarts` 就绪，无 pageerror。

- Project：`07201d91-cf43-4986-a722-f00a51886f86`
- Conversation：`d083172f-bfcd-475c-8822-d67d6dd5070b`
- Run：`cad4308a-59fd-430f-b424-9bfc798b458d`
- Primary Artifact：`d87702a0-5796-4253-a192-cc06a0f47096`

这些是已销毁测试卷中的历史证据，不是当前仍可访问的数据。浏览器验收由 Playwright 驱动真实 UI，执行代理另行查看截图确认显示结果，没有模拟业务 API，也不代表用户已亲自验收。

额外隔离回归：将 `COMPOSE_ENV_FILES` 指向仅含测试内容的损坏 dotenv，普通 Compose config 如预期拒绝解析；在同一环境变量下完整 `make test-integration` 仍通过，包含真实权限测试和清理。验证了顶层与权限测试子进程均显式忽略外部 dotenv，而不是只检查 Make 文本。

规格审查和最终质量审查已通过；修正了权限子进程 dotenv 隔离、Runtime 终态说明、容器 UID 范围及 Node 版本要求。截至九月记录时尚未添加或触发 CI，不能将以上同机结果计作第二环境；十月新进度见页首说明。

## 自动测试隔离说明

Integration 项目 `harness-forge-integration` 使用宿主端口 28080/28081/28090/25432/29000/29001；E2E 项目 `harness-forge-e2e` 使用 15173/18080/18081/18090/15432/19000/19001。它们忽略 `.env`，显式固定内部数据库/对象存储/Runtime 连接、共享路径与空 Claude credential。命名项目已有资源时立即拒绝，不自动接管；正常退出和测试失败都只删除本次拥有的项目卷。

Integration 使用 `go test -count=1 -p 1 -tags=integration ./internal/... -v`：禁用 Go 测试结果缓存，确保外部服务改变后实际重跑；仍可复用编译和镜像缓存。跨 package 共享数据库级维护锁，所以按 package 串行，不削弱测试内部并发断言。`TEST_MINIO_ENDPOINT` 含 `http://`；`HF_PERMISSIONS_INTEGRATION=1` 与 `HF_COMPOSE_PROJECT` 确保两容器真实权限测试启用。

Fake E2E 的本地图表 stub 只验证脚本传输及执行。Python SDK 替身测试、Runtime 镜像启动与 Fake 浏览器 PASS 均不代表真实 Claude API 已验收。
