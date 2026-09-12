# Harness Forge 连续执行记录

## 当前授权与执行方式

用户最新要求：直接完整执行全部任务。此前“每完成一个任务即停下来”的节奏已取消；从 Task 7 连续执行至 Task 22，测试、双审查、checkpoint 与主分支同步继续保留。不要因为到达单个任务检查点而结束执行。

工作树：`/Users/mei/Desktop/Project/harness-forge/.worktrees/v0-implementation`，分支 `feat/v0-implementation`。起始提交 `ac341b5`；Task 1–6 已完成。以[已批准计划](../plans/2026-07-19-harness-forge-v0.md)为范围，不提前加入 E2B adapter、通用 shell API 或新产品功能。

## 进度

- Task 1–6：完成（历史证据见 [Task 6 checkpoint](2026-09-12-task-6.md)）。
- Task 7：完成；`88bda8d` 主体、`673323d` 删除幂等修复，规格/质量审查通过。
- Task 8：实施中；Runtime/SandboxProvider/SSE。
- Task 9–11：待实施；Artifact Gateway、Coordinator、purge。
- Task 12–16：待实施；Python execution store、Workspace、SDK Session、worker、Geo Profile/smoke。
- Task 17–21：待实施；三栏前端、产品操作、SSE、Artifact 展示、Fake E2E。
- Task 22：待实施；中文交付文档、本机干净检出与第二环境验收。

## 环境与待确认事项

- 本轮隔离验证环境为 Compose project `hf-full-20260912`，新 PostgreSQL/MinIO 卷，不操作默认项目数据。
- Go 命令使用 `GOTOOLCHAIN=local env -u GOROOT`；数据库测试使用独立 schema。
- 默认测试不得使用真实 Claude 凭证；`make smoke-claude` 保持人工 opt-in。不会读取宿主 Claude 配置来绕过此限制。
- 已异步询问用户是否允许添加/运行 GitHub Actions 作为第二独立 Docker 验证环境；未收到授权前不擅自运行外部 CI。此事项不阻塞本机实现。
- Task 5 的 Docker Hub 网络问题本轮已恢复：三个基础镜像正常拉取，当前原始 Dockerfile 全栈构建与启动通过。最终 fresh-clone/第二环境验证仍须单独执行。

## Task 7 验证

固定提交 `673323d` 上，全量 Go `test -race ./... -count=1`、`vet ./...`、真实 PostgreSQL `test -tags=integration -race ./... -count=1 -timeout=90s` 均通过。集成测试证明 Message/Run insert 任一失败均整体回滚、Project→Conversation 锁顺序下两种 Submit/Delete 竞态均正确，并通过明确 backend PID 观察锁等待。

原始 `docker compose -p hf-full-20260912 up -d --build --wait control-plane agent-runtime web` 成功，五个常驻服务 healthy；真实 HTTP 验证多会话隔离、自动/手动标题、Message 与 queued Run 一致、待执行 Run 阻止删除、重复删除幂等。额外复现并修复“会话和父 Project 删除后再 DELETE 会话仍须 204”。验证 Project `511c6a73-5c2c-4de2-8016-b92939415e84`、Conversation `27a74e03-c319-463a-aeaa-6ee5c315ef92`、Run `3cd6b238-cffe-41bf-a37e-88780942ad50`，均属隔离测试数据。

非阻断兼容性备注：Go 标准 JSON decoder 会宽容接受 `Title`/`Content` 等大小写变体，较 OpenAPI 更宽；无权限或数据归属歧义，规格复议降为 Minor，未为此新增自制解码器。
