# Task 16–22 连续执行 checkpoint

## 当前授权

用户在 Task 15 完成后明确要求“直接持续开始完成后续的所有任务”。本轮从 `b8e5fdc` 接着执行 Task 16–22，每个 Task 保留测试、规格审查、质量审查和提交；**不再在 Task 16 完成后自动停止**。下次恢复以本文件最新进度为准，Task 15 checkpoint 的单任务停止边界已被新指令替换。

主目录 `/Users/mei/Desktop/Project/harness-forge`（main），保留工作树 `.worktrees/v0-implementation`（feat/v0-implementation）。起始 main/origin/main/feature 一致且干净，worktree 被 Git 忽略。恢复基线 `make test` 通过：Go 全量、Python 250 项、Web 2 项；已有一个 Starlette/httpx deprecation warning。

## 进度

- Task 1–15：已完成，历史见 [Task 15 checkpoint](2026-09-14-task-15.md)。
- Task 16：完成；`bf7c029` / `2abdba1`，Geo Profile、固定地理依赖、ECharts vendor、opt-in smoke；测试和两阶段审查通过。
- Task 17：完成；`e92246d` 三栏布局与路由，测试、真实浏览器及两阶段审查通过。
- Task 18：完成；`7c1a90e` / `3236c04` / `7640e18`，Project、Conversation、上传及竞态修复，测试、真实浏览器与两阶段审查通过。
- Task 19：下一任务；Chat、SSE、Run timeline，持续执行。
- Task 20：待实施；Artifact 版本与安全预览。
- Task 21：待实施；Fake scenarios 与真实浏览器 E2E。
- Task 22：待实施；中文文档、完整测试、fresh clone 与第二 Docker 环境。

## 验证与环境边界

默认测试只使用 Fake/fixture，明确空 Claude 凭证；不读取宿主 Claude 配置，不运行真实 query/CLI/API。真实 smoke 保持人工 opt-in。已向用户询问是否允许添加并运行 GitHub Actions 作为第二 Docker 环境，尚待答复；未授权前不添加/触发外部 CI。缺少第二环境时，不将 Task 22 全部勾选通过。

Docker 当前正常：29.4.0 / aarch64，Compose v5.1.2。上一轮 `hf-full-20260912` 测试容器/卷已清理；后续仅操作明确命名的本项目隔离测试资源，不碰默认开发数据。保留缓存镜像不等于已验证新代码。

按[已批准计划](../plans/2026-07-19-harness-forge-v0.md)推进，不升级固定 Claude SDK，不增加 E2B adapter、通用 shell API 或在线制品编辑器。

## Task 16 完成验证记录

实现提交 `bf7c0294bc0ee76d1f705743eb347b02dbb455bb`。三个 Profile/asset/dependency 测试和 Node smoke 的 RED→GREEN 已完成；额外修复 smoke 清理继承错误 Provider、`echo python` 被误当执行证据。`apps/web/vite.config.ts` 仅增加 scripts 测试排除，防止 Vitest 扫描 Node 内置测试；Make 单独运行 Node，属于必要测试接入，不增加前端功能。

父级独立：`make test` Go 全量/Python 253/Vitest 2/Node 14 通过，Web build、Ruff、mypy 12 个源文件、uv lock 通过；空 `ANTHROPIC_API_KEY` 的 `make smoke-claude` 立即失败且不执行 Docker。vendor 两次固定 checksum 均通过，Go profiles/workspaces 针对测试通过。

不使用构建缓存的 Runtime 镜像构建成功；`--network none`、空凭证、UID/GID 10001 实测六库精确版本、Pandas 求和、DuckDB SQL、Arrow 表转换及 GeoPandas 坐标转换通过。提交后重建临时非 root Linux 测试镜像，全量 Python 253 项再次通过；真实 TCP worker/断流/cancel/SIGKILL restart probe 五项通过。辅助 Dockerfile 位于本机 `work/full-execution/task16-tests.Dockerfile`，不是仓库交付依赖。

`hf-full-20260912` 已显式 Fake/空凭证重建，五个常驻服务 healthy，Runtime executions 空数组。未调用真实 Claude，也未进行跨 Provider 数据切换。

实际 vendored ECharts 5.6.0 在 Chromium（所有网络请求阻断）中成功渲染 SVG 柱图、无 pageerror。真实 Go API 使用当前 Geo Profile 跑通 Project→上传 CSV→Conversation→Fake Run→成功/已 finalized→读取主 HTML；测试 Project `9f6c00cc-2d5e-4b7e-9840-c3f2906f78da` 已逻辑删除并由隔离栈 purge 成功，无 orphan。此项不是 Claude smoke，也不替代后续完整三栏 E2E。

规格审查 `bf7c029` PASS；质量审查发现一项 P2：succeeded 但尚未 finalized 时误 cancel 导致 409 假失败。`2abdba1` 修复为仅取消非终态、独立等待 finalized，并在 cancel 409 时刷新确认是否已终态；新增真实语义 mock 回归，Node 共 17 项。最终质量复审 PASS，父级独立 Node 17 项通过；镜像/Profile 输入未变。

Task 16 checkpoint `1f442ce` 已快进合并并推送 main，main/origin/main/feature 一致；主目录独立重跑 `make test` 全通过。继续 Task 17 时，父级在隔离 PostgreSQL/MinIO 上执行 `TEST_DATABASE_URL=... GOTOOLCHAIN=local env -u GOROOT go -C services/control-plane test -tags=integration -race ./... -count=1 -timeout=120s` 全包通过。该集成测试使用原有隔离 schema/bucket，不使用 Claude。

## Task 17 验证记录

实现提交 `e92246da623ff212d896c66a0562df87e9abcada`。最小三栏壳层：240/440 默认宽度、折叠/拖动/键盘 splitter、<1000px 窄屏 tabs、四个业务 slots、三种路由/回退、query 制品选择、localStorage 仅存容错宽度。移除 HelloWorld/旧 starter 样式；额外修改 index.html 的中文 lang/产品标题是本 Task 的必要页面入口修正。未提前实现 Task 18–20 业务。

父级指定组件测试 20 项和 build 通过；全量 Vitest 口径为新增 20 + 原有 health 2 = 22。测试修复包含窄屏 tab aria-controls 与 tabpanel 同节点关联及焦点。当前 Node 有实验性 localStorage warning，测试中隔离 Storage，不影响真实浏览器持久化。

重建隔离栈 Web 后，用真实 Chromium 验证拖动 +40px、键盘 +16px、刷新恢复宽度、侧栏折叠/展开、深链刷新、1280/1024/800/390px 无横向溢出、窄屏标签箭头与路径 fallback，全部通过，无 pageerror。父级已查看桌面/窄屏截图，符合克制制图工作台方向。辅助 probe 与 PNG 在本机 `work/full-execution/task17-browser-probe.mjs`、`task17-desktop.png`、`task17-narrow.png`；仓库自动组件回归不依赖这些辅助文件。

独立规格审查和质量审查均 PASS，无待修 findings。父级完整 `make test`：Go/Python 253/Vitest 22/Node 17 全通过，生产 build 和 diff check 通过。同步 checkpoint/main 后接着 Task 18，不按旧单任务边界停止。

## Task 18 验证记录

实现 `7c1a90eca70ebb4f835297f2fd4cb64e9e343144`：typed JSON/204 与原生 XHR 上传、Project 创建/切换、Conversation 创建/切换/筛选/重命名/确认逻辑删除、Project 资料区及真实 App slots。复用现有 named routes，未为 router.ts 制造无意义修改，未增加依赖。

父级独立完整 `make test`：Go/Python 253/Vitest 45/Node 17 通过。真实 Chromium→Go 验证创建/切换两个 Project、上传 CSV/digest、两个 Conversation 的重命名/筛选/删除/刷新、删除确认与 1280/1024/800/390px 无横向溢出。

检查实际截图时发现新建 Conversation 的空 title 导致无名链接；Go 允许空 title，并在首条 Message 时自动命名，不能通过提交默认 title 修复。`3236c044dde4c8f937832e6f0ef0cbe9a75b4cf6` 仅为展示/操作标签/确认/筛选增加“新会话”兜底，保留原始字段与 POST `{}`。单测及真实浏览器都先 RED 再 GREEN；父级最终 Vitest 47、build、diff check 通过，规格复审 PASS。

质量审查发现两项 Important，均由 `7640e187a9478208c9bc3a91e66213aeda283142` 修复并复审关闭：

1. 同 Project 的 Conversation 导航误清空 Project context，导致上传组件卸载并 abort；现在保留同 Project 上传，仅真正切换 Project/卸载时清理。父级真实 Chromium 限速 XHR 从 RED 复验到 GREEN，上传在创建会话/导航后正常完成。
2. Conversation 成功删除/重命名/创建可能被并发旧 list/get 快照覆盖；现在只为当前读取临时合并期间成功的 mutation，读取结束或切换 context 即释放，无永久 tombstone 或重试循环。旧 list/selected GET 不再覆盖成功修改。

所有验收 Project 已逻辑删除并由隔离 `hf-full-20260912` purge 清理，无 orphan。真实容器共享目录权限验证显式使用 `HF_PERMISSIONS_INTEGRATION=1 HF_COMPOSE_PROJECT=hf-full-20260912`，通过 UID/GID 10001、inputs 不可写与 workspace/outputs 双向访问检查。Task 22 的全量 integration target 必须设置这两个实际开关，只有 `COMPOSE_PROJECT_NAME` 不会执行该权限用例。

父级最终完整 `make test`：Go 全量/Python 253/Vitest 53/Node 17 通过，生产 build 与 diff check 通过。两套真实浏览器 probe 在最终代码重建后均通过，最终 Project `2471bfbe-ecc9-45be-a9ac-16e09c8b42fa`、`d4c0eba5-4a70-492c-bfe9-0042720f8aaa` 与限速上传 Project `732c1f0b-de79-4e0e-a4fd-60a48dcf240b` 已 purge。规格复审 PASS；质量复审独立 31 项及原始两项注入复现 PASS，无剩余 findings。

本机验收辅助文件位于 `work/full-execution/task18-browser-probe.mjs`、`task18-upload-navigation-probe.mjs`、`task18-desktop.png`、`task18-narrow.png`；交付自动测试不依赖这些机器本地文件。同步本 checkpoint 与 main 后继续 Task 19。
