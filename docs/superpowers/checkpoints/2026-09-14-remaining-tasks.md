# Task 16–22 连续执行 checkpoint

## 当前授权

用户在 Task 15 完成后明确要求“直接持续开始完成后续的所有任务”。本轮从 `b8e5fdc` 接着执行 Task 16–22，每个 Task 保留测试、规格审查、质量审查和提交；**不再在 Task 16 完成后自动停止**。下次恢复以本文件最新进度为准，Task 15 checkpoint 的单任务停止边界已被新指令替换。

主目录 `/Users/mei/Desktop/Project/harness-forge`（main），保留工作树 `.worktrees/v0-implementation`（feat/v0-implementation）。起始 main/origin/main/feature 一致且干净，worktree 被 Git 忽略。恢复基线 `make test` 通过：Go 全量、Python 250 项、Web 2 项；已有一个 Starlette/httpx deprecation warning。

## 进度

- Task 1–15：已完成，历史见 [Task 15 checkpoint](2026-09-14-task-15.md)。
- Task 16：完成；`bf7c029` / `2abdba1`，Geo Profile、固定地理依赖、ECharts vendor、opt-in smoke；测试和两阶段审查通过。
- Task 17：下一任务；三栏布局与路由，继续执行，不停止。
- Task 18：待实施；Project、Conversation、上传。
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
