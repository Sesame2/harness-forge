# Task 16–22 连续执行 checkpoint

## 当前授权

用户在 Task 15 完成后明确要求“直接持续开始完成后续的所有任务”。本轮从 `b8e5fdc` 接着执行 Task 16–22，每个 Task 保留测试、规格审查、质量审查和提交；**不再在 Task 16 完成后自动停止**。下次恢复以本文件最新进度为准，Task 15 checkpoint 的单任务停止边界已被新指令替换。

主目录 `/Users/mei/Desktop/Project/harness-forge`（main），保留工作树 `.worktrees/v0-implementation`（feat/v0-implementation）。起始 main/origin/main/feature 一致且干净，worktree 被 Git 忽略。恢复基线 `make test` 通过：Go 全量、Python 250 项、Web 2 项；已有一个 Starlette/httpx deprecation warning。

## 进度

- Task 1–15：已完成，历史见 [Task 15 checkpoint](2026-09-14-task-15.md)。
- Task 16：完成；`bf7c029` / `2abdba1`，Geo Profile、固定地理依赖、ECharts vendor、opt-in smoke；测试和两阶段审查通过。
- Task 17：完成；`e92246d` 三栏布局与路由，测试、真实浏览器及两阶段审查通过。
- Task 18：下一任务；Project、Conversation、上传，持续执行。
- Task 19：待实施；Chat、SSE、Run timeline。
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
