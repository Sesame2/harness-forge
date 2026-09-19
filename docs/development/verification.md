# V0 交付验证记录

设计依据：[中文](../superpowers/specs/2026-07-19-harness-forge-design.zh-CN.md) / [English](../superpowers/specs/2026-07-19-harness-forge-design.md)。操作步骤见 [README](../../README.md) 与 [fresh clone 验收](local-setup.md#可复现的-fresh-clone-验收)。

本页明确区分工作区验证、同机 fresh clone 和第二独立环境。代码、中文交付文档及本机完整验收已完成；Task 22 **仅剩第二独立环境验收，尚未全部完成**。所有自动验证使用 Fake/测试替身，不调用 Claude；真实 Claude smoke 仍未运行。

## 本地实现检查（2026-09-19）

- 起始 commit：`68724ff`；此处结果针对随后 Task 22 的工作区变更，不冒充冻结 commit 的重验。
- 环境：macOS 27.0（26A428）、arm64；Docker 29.4.0（arm64）、Compose 5.1.2。
- 宿主工具：Go 1.25.4、Node 25.6.1、pnpm 9.15.4、uv 0.6.6、Python 3.12.7。
- `test-integration` 编排检查先 RED（原目标仅 postgres package、没有隔离项目），更新后 GREEN。
- `GOTOOLCHAIN=local env -u GOROOT make test-integration`：PASS，真实 PostgreSQL/MinIO；`TestComposeWorkspacePermissions` 实际运行并 PASS，不是 skip。退出自动删除本次 `harness-forge-integration` 容器、卷与网络。
- `GOTOOLCHAIN=local env -u GOROOT make test`：PASS（Go 全包、Python 253、Vitest 108 / 15 suites、smoke 脚本替身测试 17）。既有 Starlette/httpx deprecation 与 Node localstorage 警告仍在，无测试失败。
- `make verify-layout`、Compose 空 env-file `config --quiet`、`git diff --check`：PASS；integration 项目 container/volume/network 标签复查均为空。

## 冻结 commit 验收

冻结 commit：`c5962a5299264a5dee158f255606138b3a7d3b12`。父级通过 `git clone --no-local` 新克隆并 detached checkout 此 commit，复制 `.env.example` 新建配置，按 README 安装依赖并执行验证。没有从原工作区复制 `.env`、node_modules、`.venv`、构建产物或卷；安装工具与 Docker 可复用本机下载/镜像缓存，因此这仍然只是同机验证。

后续 `e81d060` 仅校正 README 的 Node 最低版本，本页与 checkpoint 后续提交只记录结果，不改变冻结版本的代码或测试。第二环境应检出上述完整 SHA，不用最新记录提交冒充已验收版本。

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

规格审查和最终质量审查已通过；修正了权限子进程 dotenv 隔离、Runtime 终态说明、容器 UID 范围及 Node 版本要求。尚未添加或触发 CI：第二环境/相关授权待用户提供，不能将以上同机结果计作第二环境。

## 自动测试隔离说明

Integration 项目 `harness-forge-integration` 使用宿主端口 28080/28081/28090/25432/29000/29001；E2E 项目 `harness-forge-e2e` 使用 15173/18080/18081/18090/15432/19000/19001。它们忽略 `.env`，显式固定内部数据库/对象存储/Runtime 连接、共享路径与空 Claude credential。命名项目已有资源时立即拒绝，不自动接管；正常退出和测试失败都只删除本次拥有的项目卷。

Integration 使用 `go test -p 1 -tags=integration ./internal/... -v`：跨 package 共享数据库级维护锁，所以按 package 串行，不削弱测试内部并发断言。`TEST_MINIO_ENDPOINT` 含 `http://`；`HF_PERMISSIONS_INTEGRATION=1` 与 `HF_COMPOSE_PROJECT` 确保两容器真实权限测试启用。

Fake E2E 的本地图表 stub 只验证脚本传输及执行。Python SDK 替身测试、Runtime 镜像启动与 Fake 浏览器 PASS 均不代表真实 Claude API 已验收。
