# Harness Forge 连续执行记录

## 当前授权与执行方式

用户最新要求：直接完整执行全部任务。此前“每完成一个任务即停下来”的节奏已取消；从 Task 7 连续执行至 Task 22，测试、双审查、checkpoint 与主分支同步继续保留。不要因为到达单个任务检查点而结束执行。

工作树：`/Users/mei/Desktop/Project/harness-forge/.worktrees/v0-implementation`，分支 `feat/v0-implementation`。起始提交 `ac341b5`；Task 1–6 已完成。以[已批准计划](../plans/2026-07-19-harness-forge-v0.md)为范围，不提前加入 E2B adapter、通用 shell API 或新产品功能。

## 进度

- Task 1–6：完成（历史证据见 [Task 6 checkpoint](2026-09-12-task-6.md)）。
- Task 7：完成；`88bda8d` 主体、`673323d` 删除幂等修复，规格/质量审查通过。
- Task 8：完成；`1d7ee5e`，独立规格/质量审查通过，本机全量测试与容器验收通过。
- Task 9：完成；`ef49ddb`，独立规格/质量审查、本机测试和容器联调通过。
- Task 10：实施中；Workspace materializer、Coordinator、Scheduler 与 reconciliation。
- Task 11：待实施；purge。
- Task 12–16：待实施；Python execution store、Workspace、SDK Session、worker、Geo Profile/smoke。
- Task 17–21：待实施；三栏前端、产品操作、SSE、Artifact 展示、Fake E2E。
- Task 22：待实施；中文交付文档、本机干净检出与第二环境验收。

## 环境与待确认事项

- 本轮隔离验证环境为 Compose project `hf-full-20260912`，新 PostgreSQL/MinIO 卷，不操作默认项目数据。
- Go 命令使用 `GOTOOLCHAIN=local env -u GOROOT`；数据库测试使用独立 schema。
- 默认测试不得使用真实 Claude 凭证；`make smoke-claude` 保持人工 opt-in。不会读取宿主 Claude 配置来绕过此限制。
- 已异步询问用户是否允许添加/运行 GitHub Actions 作为第二独立 Docker 验证环境；未收到授权前不擅自运行外部 CI。此事项不阻塞本机实现。
- Task 5 的 Docker Hub 网络问题本轮已恢复：三个基础镜像正常拉取，当前原始 Dockerfile 全栈构建与启动通过。最终 fresh-clone/第二环境验证仍须单独执行。
- Task 7 smoke 结束后已清理其隔离容器/卷并重建 postgres/minio，避免后续 scheduler 消费遗留 queued Run。后续应用验证明确使用 Fake Provider 与空 Claude credential。
- Task 14 前必读 [固定 SDK 版本研究](../../research/claude-agent-sdk-0.2.120.md)：实际 Session 为 `projects/<encoded-cwd>/<uuid>.jsonl`；bundled CLI 2.1.211 的跨 cwd 恢复不可直接套用新文档。研究提出保留固定 Config Dir/source UUID/fork 的受控 staging 路径，需要明确 durable ownership 与 source 保护，不擅自升级 SDK。另需异步 prompt 输入支持权限回调、`setting_sources=[]` 和精确工具集限制；真实上游语义仍只在 opt-in smoke 验证。

## Task 8 中间接口约定

- Runtime `GET /v1/executions` 返回 JSON 数组，字段 `run_id`、`lifecycle=starting|running|awaiting_finalize`，可带 nullable `candidate_sdk_session_id`；Go 忽略其他 record 字段。文中 active 指 running，不引入 status/active 别名。
- 已 finalized 的重复 execute 返回 200 JSON `{"decision":"commit"|"abort"}`；Go 识别为 typed finalized result，绝不再次执行。`POST finalize` 使用同 decision body，成功 204。
- Task 8 ActiveCancel 使用窄 callback seam；Task 10 Coordinator 尚不存在时，main 对 active 取消明确报 unavailable，不能伪造成功或 finalized。Task 10 必须完成实际 wiring，最终交付不得保留此中间状态。
- Task 10 组装 ExecuteRequest 时需明确 `profile.config` 的 system prompt、工具策略和 Artifact policy 跨语言字段；当前 Task 5 Snapshot 尚无 DisallowedTools，必须在后续策略接入时补入 resolver/clone，不能让 Task 16 YAML 中的 disallowed 配置被静默丢弃。

## Task 7 验证

固定提交 `673323d` 上，全量 Go `test -race ./... -count=1`、`vet ./...`、真实 PostgreSQL `test -tags=integration -race ./... -count=1 -timeout=90s` 均通过。集成测试证明 Message/Run insert 任一失败均整体回滚、Project→Conversation 锁顺序下两种 Submit/Delete 竞态均正确，并通过明确 backend PID 观察锁等待。

原始 `docker compose -p hf-full-20260912 up -d --build --wait control-plane agent-runtime web` 成功，五个常驻服务 healthy；真实 HTTP 验证多会话隔离、自动/手动标题、Message 与 queued Run 一致、待执行 Run 阻止删除、重复删除幂等。额外复现并修复“会话和父 Project 删除后再 DELETE 会话仍须 204”。验证 Project `511c6a73-5c2c-4de2-8016-b92939415e84`、Conversation `27a74e03-c319-463a-aeaa-6ee5c315ef92`、Run `3cd6b238-cffe-41bf-a37e-88780942ad50`，均属隔离测试数据。

非阻断兼容性备注：Go 标准 JSON decoder 会宽容接受 `Title`/`Content` 等大小写变体，较 OpenAPI 更宽；无权限或数据归属歧义，规格复议降为 Minor，未为此新增自制解码器。

## Task 9 中间接口约定

`artifacts.NewPublisher(pool, objects).Prepare(ctx, projectID, runID, outputsRoot, snapshot)` 返回 `PreparedPublication{Records}` 和幂等 `Release() error`。`artifacts.NewStore(pool).InsertTx(ctx, tx, records)` 只参与调用方事务；`Read`/`ListByRun` 按 committed metadata 和逻辑删除状态过滤。`AcquirePublicationLock(ctx, pool)` 返回幂等 release function，供 Task 11 scanner 复用同一专用连接上的 session advisory lock。Task 10 必须在 metadata commit/rollback 之后释放 handle，在任何 object upload 之前持久化 publishing phase/Event。

`ValidateManifest(outputsRoot, snapshot) (ValidatedManifest, error)` 的 `.Manifest` 为现有 `contracts.ArtifactManifest`，供 Task 10 在 publishing phase 之前校验并比对 Runtime summary；`.Files` 为受 Profile 文件/总大小限制的内存快照。V0 单并发保留这个简单实现，不另建流处理框架。Gateway 未指定相对路径时，在 metadata gate 后 302 跳转至存储 entry，保证相对资源目录正确。

Go/Python 统一大小口径：所有常规输出文件（包括 Manifest）均计入总大小，Manifest 不作为 published file 上传。Manifest 没有文件归属列表，因此每个 Artifact prefix 复制同一完整已校验输出树，保留相对资源及 shared sibling data；不额外创造未批准的 file-membership schema。

依赖方向保持 `runs(Coordinator) → artifacts/workspaces`；artifacts 不反向导入 runs，使用本域错误并由 HTTP 边界映射。Conversations 已依赖 runs 创建队列，Task 10 不可再从 runs 反向导入 conversations；读取执行上下文可在 Run store 做带归属校验的定向查询。

Task 10 接入时还需注意：当前 SubmitMessage 会在排队时记录 active Session，但实际 ExecuteRequest 必须按计划读取执行时 Conversation 的当前 active pointer，并保持 Run source 记录与实际请求一致，覆盖连续排队两轮的情况。启动 reconciliation 会处理崩溃残留 running；运行期协调不能把本进程正在正常执行的 Run 当作崩溃任务中断。默认 Docker Runtime 的业务 API 在 Task 12–15 才补齐，Task 10 的真实执行验收应明确使用 Fake，不伪称 Docker Agent 链路已可用。

## Task 8 验证

固定提交 `1d7ee5e`，父级独立运行全量 Go `test -race ./... -count=1`、`vet ./...` 与真实 PostgreSQL `test -tags=integration -race ./... -count=1 -timeout=120s`，全部通过。实现者记录各功能 RED→GREEN，并补充 terminal 后出现已知事件、权威 List 非法尾部、Fake Release 后 List 收敛、SSE frame 注入和 uint64 cursor 边界回归测试。

独立规格及质量审查均通过，无 Critical/Important/Minor 待修问题。统一 `make test` 通过 Go、Python 55 项和 Web 2 项；Python 保留已有 Starlette/httpx deprecation warning，不影响当前结果。调用 Execute 的 Coordinator 必须完整消费事件及错误 channel 至 EOF 后才进行发布/finalize，不以收到单个 terminal frame 代替流完整性校验。

以 `SANDBOX_PROVIDER=fake ANTHROPIC_API_KEY= ANTHROPIC_BASE_URL=` 显式启动原始 Compose/Dockerfile，`hf-full-20260912` 五个常驻服务全部 healthy，bucket init 正常退出 0。真实 HTTP/SSE 验收通过：Run 按会话隔离、queued 取消后终态与事件同时可读、连接存续期间实时收到取消事件、重新连接重放相同 durable ID、游标后无重复事件、重复取消不增加事件也不改变 finalized_at；取消完成后可逻辑删除父级。测试 Project `b1850b48-6276-40f5-ba73-9954435a7a7b`、Conversation `6714602d-010b-4222-9024-0b679ad8896e`、Run `e65595bc-8e49-4a35-aae6-2d8e686bc131`，无待调度测试 Run 遗留。

## Task 9 验证（2026-09-13）

固定提交 `ef49ddb`，父级独立运行全量 Go `test -race ./... -count=1`、`vet ./...`、真实 PostgreSQL `test -tags=integration -race ./... -count=1 -timeout=120s`，全部通过。数据库测试使用真实 deferred FK commit failure 验证 metadata 不可见，并验证发布期间/事务期间持有 session lock、context cancellation 与 backend termination 后无连接锁泄漏。

独立规格/质量审查均通过，无 Critical/Important/Minor 待修问题；审查者分别重新运行相关 race 和真实 PG 集成测试。统一 `make test` 通过 Go、Python 55 项、Web 2 项。

以同样显式 Fake/空 Claude credentials 重建原始 Compose，五服务 healthy。真实 MinIO+PostgreSQL+双端口 HTTP 验证通过：只上传 objects 时 Gateway 404/API 空列表；测试专用 metadata transaction 提交后 HTML、JS、CSS、JSON、PNG body/Content-Type 正确；CSP/nosniff/no-referrer 生效；无 cookie/CORS；原始和编码路径穿越均 400；伪造 Host/X-Forwarded-Host 不改变 configured gateway_url；默认入口 metadata-gated 302；移除 metadata 后对象再次不可见。

此 Task 尚无 Coordinator，metadata transaction 是隔离验收 fixture 的定向 SQL，不代表端到端 Agent 发布已实现。验收 Project `d51271df-7605-440d-a2b5-9047d5206024`、Conversation `7b874b42-3dd0-4c48-a4ee-f45d98bc517e`、Run `43439417-5d03-41de-9f64-5614bcb2435f`、Artifact `c49eee70-a091-4836-916a-24bd1d26ec41`；测试对象及 Artifact metadata 已清理，Project/Conversation 已逻辑删除，无 queued Run 遗留。
