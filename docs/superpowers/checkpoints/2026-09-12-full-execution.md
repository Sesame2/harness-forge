# Harness Forge 连续执行记录

## 当前授权与执行方式

用户最新要求：直接完整执行全部任务。此前“每完成一个任务即停下来”的节奏已取消；从 Task 7 连续执行至 Task 22，测试、双审查、checkpoint 与主分支同步继续保留。不要因为到达单个任务检查点而结束执行。

工作树：`/Users/mei/Desktop/Project/harness-forge/.worktrees/v0-implementation`，分支 `feat/v0-implementation`。起始提交 `ac341b5`；Task 1–6 已完成。以[已批准计划](../plans/2026-07-19-harness-forge-v0.md)为范围，不提前加入 E2B adapter、通用 shell API 或新产品功能。

## 进度

- Task 1–6：完成（历史证据见 [Task 6 checkpoint](2026-09-12-task-6.md)）。
- Task 7：完成；`88bda8d` 主体、`673323d` 删除幂等修复，规格/质量审查通过。
- Task 8：完成；`1d7ee5e`，独立规格/质量审查通过，本机全量测试与容器验收通过。
- Task 9：完成；`ef49ddb`，独立规格/质量审查、本机测试和容器联调通过。
- Task 10：完成；主体 `13016e5`，审查修复 `dd3b046` / `ca2b9d3` / `1c76254`；独立规格与质量审查均通过，最新固定提交本机全量测试/容器联调通过。
- Task 11：完成；`bdca98b` 与测试补充 `86c744c`，规格/质量审查与真实 CLI 验收通过。
- Task 12：完成；`5c38e84`，规格/质量审查、77 项 Python 测试和真实容器持久化验证通过。
- Task 13：完成；`fcf6642` 与权限修复 `5e50900`，规格/质量审查及真实 Go→Runtime 校验通过。
- Task 14–16：待实施；SDK Session、worker、Geo Profile/smoke。
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
- Task 14 还需读 [fork 文件依赖补充研究](../../research/claude-session-fork-storage.md)：Python 离线 fork 不能证明固定 CLI query fork 的文件依赖；成功 commit 保留 owned source staging 至会话整体显式 purge，abort 只删本 Run candidate/staging、不动原始 source。实际同名关联目录（例如大工具输出 tool-results）不能无依据丢弃；删除 terminal execution record 不应丢失仍存活 staging 的所有权证据。无需为省副本空间提前 GC，也不能把未运行的真实 SDK 恢复说成已验证。

## Task 8 中间接口约定

- Runtime `GET /v1/executions` 返回 JSON 数组，字段 `run_id`、`lifecycle=starting|running|awaiting_finalize`，可带 nullable `candidate_sdk_session_id`；Go 忽略其他 record 字段。文中 active 指 running，不引入 status/active 别名。
- 已 finalized 的重复 execute 返回 200 JSON `{"decision":"commit"|"abort"}`；Go 识别为 typed finalized result，绝不再次执行。`POST finalize` 使用同 decision body，成功 204。
- Task 8 ActiveCancel 使用窄 callback seam 的临时 unavailable 状态已在 Task 10 关闭：main 现已接入实际 Coordinator；取消确认/worker stop/收尾遵守 durable 状态机。
- Task 10 已补入 Snapshot 的 DisallowedTools 以及 resolver/clone，ExecuteRequest 包含工具禁用策略，Task 16 YAML 接入时不再丢失该字段。
- Task 10 已约定具体 payload：`profile.config.system_prompt`；`tools={allowed,disallowed,permission_mode}`；`artifacts={manifest_schema_version,allowed_types,max_file_bytes,max_total_bytes}`；`inputs={accepted_media_types}`。Agent 限制只放外层 `limits`。Task 13/14 按此读取策略，不另造扁平 alias；通用 Runtime request schema 的 config 仍为 object，worker policy validation 才要求这些字段。

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

## Task 10 中间验证（尚未提交/审查完成）

真实权限 RED→GREEN 已完成：原 Task 9 两镜像均 UID/GID 0，`TestComposeWorkspacePermissions` 的身份检查失败；改造后重建原始 Compose，五个常驻服务 healthy、MinIO init 与 runtime-volume-init 均退出 0，`HF_COMPOSE_PROJECT=hf-full-20260912 HF_PERMISSIONS_INTEGRATION=1 ... test -tags=integration ./internal/workspaces -run TestComposeWorkspacePermissions -v -count=1` 通过。两镜像统一 `10001:10001`；两容器共享 Workspace，只有 Runtime 挂载 SDK/execution Session 目录；inputs 写入失败，workspace/outputs/Runtime Session 目录可写。初始化仅调整约定顶层目录，不递归改变已有 inputs 只读模式。

父级已在中间镜像上跑通首次真实 HTTP+PG+MinIO+Fake Provider 后端完整链路，无直接 SQL seed：CSV 上传→同 Conversation 连续两轮+另一 Conversation 首轮→读取 SSE 产品终态→校验 source/candidate 衔接、assistant Message 持久化、Artifact Gateway、finalized 与 terminal Events 顺序→逻辑删除。Project `f52855d5-a498-4ee4-9090-2e7146432204`，Conversations `75e29329-0884-4025-9ce2-b96d3bde771f` / `7eca6920-6422-49a2-8450-73a63efcbe3d`，Runs `ace3cf03-52bd-45db-9a65-94ddeae61541` / `9849b2ca-3f73-420d-8405-08ebd17cb6ca` / `023fa415-0786-44e6-86fc-e6f7c563064f`；全部 succeeded/finalized，父级已逻辑删除，无 queued 遗留。对象与 Workspace 按设计保留，供后续显式 purge；固定提交上的最终复验仍待 Task 10 完成后进行，不能用中间成功代替最终验收。

Task 11 准备提示：当前所有测试容器/数据均属 `hf-full-20260912`，跑 Make maintenance 时显式 `COMPOSE_PROJECT_NAME=hf-full-20260912 SANDBOX_PROVIDER=fake`，不误用默认 Compose project。非 root purge 删除 `inputs` 前需安全恢复该受控目录的删除权限（0550 目录不能直接 RemoveAll）；不追随外部 symlink。Orphan scanner 检查任何仍存在的 Artifact metadata，不能复用隐藏逻辑删除父级的公共 Read，否则可能把待 purge 的正式 Artifact 当 orphan；Provider mismatch 必须在删除该根任何数据之前检查。

Task 11 还需覆盖：migration 003 引入 Message→Run FK，与 Run→trigger Message 形成删除顺序约束，hard-delete transaction 需先清理 assistant 的 Run 引用再删 Runs/Messages；真实 MinIO scanner 测试应使用独立测试 bucket，不能仅隔离 PG schema 却扫描共享验收 bucket。当前 `MinIO.DeletePrefix` 将 ListObjects channel 直接传给 RemoveObjects；固定 minio-go v7.2.1 的 batch loop 不检查输入 `ObjectInfo.Err`，不能把 listing failure 当作清理成功，接入 purge 时须补显式错误传播回归（同时避免提前 return 遗留 producer goroutine）。

## Task 10 固定提交验证（审查进行中）

实现冻结于 `13016e5`。父级独立运行全量 Go `test -race ./... -count=1`、`vet ./...`、真实 PostgreSQL `test -tags=integration -race ./... -count=1 -timeout=120s`，全部通过。关键事务失败/取消/恢复用例位于 integration-tag tests，不能用未带 tag 的测试结果代替。统一 `make test` 通过 Go、Python 55 项、Web 2 项；`git diff --check` 通过。

以显式 Fake/空 Claude credentials 重建原始 Compose，五个常驻服务 healthy，两个 init 正常退出 0；固定提交的真实权限测试再次通过。后端黄金链路再次通过：上传 CSV、同会话两轮与另一个会话首轮、执行时刷新 source、assistant Message 持久化、真实 Artifact 发布与 Gateway、finalized 后产品终态 Event、SSE 重放。验收 Project `5cbf38dc-4dbd-46ef-b808-3caa37fee553`，Conversations `5c3199ef-247e-4867-b506-5677b286aa78` / `247ebd43-0f88-4cf5-8327-02404942d58f`，Runs `8e148eff-b12f-4bb6-b1ed-ca15750b3a25` / `ed018b1c-979b-476c-9421-68afc9a36553` / `54f9882a-8ec4-4a7b-9e33-dd1706eb1211`；均 succeeded/finalized，父级已逻辑删除，无 queued 遗留。这里的会话衔接由 Fake 验证，不等价于真实 SDK fork 验收。

实现加入独立于 Agent cancellation 的 30 秒清理 attempt 和 5 秒诊断 SyncBack 子预算；已观察到的相反 tombstone disposition 写入既有 `Run.error.cleanup_consistency`，保留原错误 code，防止后续 List 省略 tombstone 时错误释放。hard consistency 需人工检查，scheduler 不自动清除；不添加新产品状态或协议字段。

规格审查 `13016e5` 发现三项待修，当前由 Task 10 实现者处理，尚未进入质量审查/合并：Claim transaction commit 已成功但 ACK 丢失时需要恢复全量协调，避免未执行的 running 永久占槽；promotion-first 的 Conversation 删除竞态测试误调用 Project 删除；补成功流 SyncBack 失败及 Manifest 失败的无部分产品提交/abort/release 回归。审查者独立 runs/workspaces/profiles race、真实 PG runs integration race 通过，但现有测试通过不免除这些缺口。

三项修复已冻结于 `dd3b046`，等待规格复审与后续质量审查。仅生产改动为 claim error 后重新全量协调；通过 pgx tracer 让真实 COMMIT 成功而外层收到 error，验证第一 Run 未执行且 interrupted/finalized、第二 Run 正常成功。删除竞态移到 conversations test package，避免 imports cycle 并直接调用真实 Conversation service。父级再次全量 Go race/vet、全真实 PG integration race 通过；原始 Compose 重建、非 root 权限和后端黄金链路再次通过。最新验收 Project `2544fc7e-1895-4325-8430-540343ebcc61`，Conversations `03ed44d8-7c1b-4998-8fa9-93164e77af34` / `3f4270f6-0fa8-40c2-a4ce-faa8cdbd2770`，Runs `75a34277-ffff-4759-893c-a6fe700816a0` / `fdcc1ce1-758a-464f-a9df-5b8249314aac` / `402fbc44-dff2-4cd9-87ce-07443cbe53eb`，全部 finalized，父级已逻辑删除，无 queued 遗留。

`dd3b046` 规格复审通过，三项 findings 全关闭。质量审查另外通过真实 PG overlay 复现取消 ACK 丢失：`Coordinator.Cancel` 事务已提交 cancelled 却在 error 分支直接返回，未 stop worker；`Canceller.Cancel` 重试见 cancelled 也直接返回。当前交回原实现者修复，需独立有界 context 权威回读确认后 stop，并让 cancelled/unfinalized HTTP 重试再次触发 active callback；finalized cancelled 保持幂等无副作用。修复后须质量复审、固定 SHA 最终测试，再合并主分支。overlay 复现文件仅在 `/tmp/hf-task10-quality-cancel-overlay.json`，不属于仓库实现。

取消修复冻结 `ca2b9d3`；质量复审确认原问题关闭，新旧真实 PG 回归与原 overlay 均通过。父级全 Go race/vet、全 PG integration race、原始 Compose 重建、权限测试和后端黄金链路也全部通过。最新黄金链路 Project `3c2e640d-0769-40f1-91c9-96a924734556`，Runs `82b5d9f1-c0b6-40a5-985f-0f0c28acbd26` / `0f92ce49-abeb-4573-bc55-fc37a6540e9b` / `2ad979a8-dee4-4103-90ab-19720f0539f9`；父级逻辑删除、全部 finalized。

当前最后一项质量修复进行中：finalized transaction 已成功但 COMMIT ACK 丢失会跳过 broker Notify，SSE 长连接可能永久漏终态（真实 PG overlay 已复现）。固定 pgx 5.4.3 的 asyncClose 不保证 error 返回时 server transaction 已结束，单次过早 Notify 也不够。已安排对 `change`/`AppendEvent`/`CancelQueued` 三处 owning transaction 在 COMMIT attempt 后做 coalesced Notify，并给 SSE 固定 5 秒低频 DB 补读兜底，仍只从数据库按游标读取、不增加协议/依赖/配置。修复后需质量复审及最终验证，Task 10 尚未合并；当前 main/origin/main 仍 `094ad49`。

## Task 10 最终验收与恢复位置

以上中间待修状态已全部关闭：最终实现 `1c76254` 通过独立质量复审，无 Critical/Important/Minor；之前规格复审已通过。三处 owning transaction 在 COMMIT attempt 返回后通知，caller-owned `AppendEventTx` 不越权；SSE 固定 5 秒补读保障提交后漏通知仍能送达，游标去重、断流停止 ticker。审查者独立重跑原取消/终态丢通知复现、三事务 ACKlost、取消重试和真实 PG→HTTP 静默提交，全部通过。

父级在 `1c76254` 独立运行全量 Go `test -race ./... -count=1`、`vet -tags=integration ./...`、真实 PG `test -tags=integration -race ./... -count=1 -timeout=120s`，全部通过；原始 Compose 显式 Fake/空凭证构建、五服务 healthy、两个 init 退出 0；权限测试再次通过。最新完整后端验收 Project `96be1c72-9710-4375-ac2a-c2a2f38197ba`，Conversations `a8fb38dc-3e19-493b-ad7d-56ca3b37ec2a` / `33602c04-1d2f-4fe7-a115-f570961bea7a`，Runs `8a56d975-61cb-478d-8697-43d247a14219` / `865a4065-dab7-4a27-a8cc-9edbc0aaea3f` / `d23c6ffb-cbdf-4f7c-ab70-42d9d10ab48f`；全部 succeeded/finalized，父级已逻辑删除，没有 queued 遗留。

下一任务直接执行 Task 11：先读本文件 Task 11 准备提示及计划，按批准范围实现 dry-run/apply purge 和 metadata orphan scanner，再进入 Python Tasks 12–16。Task 10 不再重复实现；真实 SDK fork/worker、业务前端和第二环境验收仍未完成。

主分支同步已实际完成：`main` 与 `origin/main` 均为 `4054d46`（2026-09-13），main 上全量 Go 测试通过后 push 成功。Task 11 新实现者已从同一提交开始；父级持有本 checkpoint 文档更新，实现者只提交 Task 11 代码。

## Task 11 验收准备

父级已通过现有真实 API 在隔离栈创建 Project `db549db7-68bc-498f-b1c2-9102152b55a4`，共享 Input `9f472889-7e63-452c-8e82-7bcf413f4c1c`，两个已成功收尾的 Run `f8879632-b86b-439e-abf5-5718ab7484e6` / `5d538ae2-b50b-474b-bb8e-76345c13c58d`。Conversation `863d2460-b05e-4836-a1dd-4f9e2eee6083` 已逻辑删除，`3cc7af5c-82c8-4e5f-8917-76514649f833` 保留活跃，用于真实 CLI 验证“清一个会话不删共享输入/另一个会话制品”。初始 PG/MinIO/Workspace 与 Gateway 状态检查通过，无 queued/unfinalized。

本机临时验收脚本 `/Users/mei/Documents/Codex/2026-07-19/new-chat/work/full-execution/task11-smoke.py`，fixture JSON 同目录 `task11-fixture.json`。CLI 完成后先 dry-run+脚本 `before`，再 apply+`conversation`，随后脚本 `delete-project`、apply+`project`，最后重复 apply 验证幂等。脚本第二参数传 fixture JSON 内容。只使用 `hf-full-20260912`，不操作默认项目数据；仓库内自动化回归仍由 Task 11 实现者交付。

同一验收 Project 另有人工上传、无 Artifact metadata 的真实 MinIO orphan `2aa4eabd-d5d2-47ef-8ee6-6de5258fee1d/index.html`（完整 key 见 fixture JSON），当前对象 200、Gateway 404。脚本也验证 dry-run 保留此对象、apply 删除 orphan，同时另一 Conversation 的正式 Artifact 仍可读。

Task 11 当前实施约定：Runtime 清理任何一步失败都不提前 Release；queued-cancel 的 source Session 只是引用，不是该未 Acquire Run 的资源，按有 ref 的 Run candidate 归属清理。Provider mismatch preflight 必须早于 orphan scanner 删除。Make 使用常驻 `up --wait postgres minio agent-runtime`，再显式 `run --rm --no-deps minio-init`，最后 one-shot CLI；不额外启动 HTTP/Scheduler，也不增加 Runtime→MinIO 依赖。此 one-shot 调整已同步计划，等待父级真实 Compose 验证。

## Task 11 固定提交验收（2026-09-13）

实现 `bdca98b`：父级全量 Go race、integration-tag vet、真实 PostgreSQL/隔离 MinIO bucket 全量 integration race 均通过。原始 Dockerfile 成功构建两个 binary；真实 Make dry-run 保留全部 fixture，apply 清理 8 个逻辑删除根及 1 个 orphan，重复 apply 为 0。会话级清理后另一个会话、其制品和共享 CSV 均保留（PG/MinIO/Gateway/Workspace 独立检查通过）；随后逻辑删除整个 Project 并执行 apply。

使用同一 Compose one-shot 镜像，在新建空数据库 `hf_task11_empty_20260913` 与专属空 bucket 上直接运行 CLI apply，退出 0、候选根为 0；查询确认自动应用 3 项 migration、projects 可读且为空。没有在宿主机运行维护命令，也没有启动额外 HTTP/Scheduler。空验收数据库随后清理，bucket 留在本次隔离栈内等待统一销毁。

规格审查仅发现 typed NotFound 幂等分支缺少 Purger 层测试，已交回原实现者补充；生产逻辑未发现偏差。仍待规格复审和质量审查，不能将 Task 11 勾选完成。后续按 Task 12 开始 execution store，继续全部已批准任务。

以上待审状态已关闭：`86c744c` 补充 typed NotFound 表驱动回归，规格复审和独立质量审查均通过，无 Critical/Important/Minor。父级该提交 cleanup race 与统一 `make test` 通过（Go、Python 55、Web 2）。整 Project 清理后的 PG/MinIO/Workspace/Gateway 检查通过，重复 apply 为 0。下一任务为 Task 12；本机真实 Claude 验收仍未运行。

主分支 `main` 与 `origin/main` 已同步至 `1fdf73e`；main 独立 Go 全量测试通过后 push 成功。Task 12 新实现者已开始，父级继续拥有计划/checkpoint 文档修改。

## Task 12 固定提交验证

实现冻结 `5c38e84`：父级独立全量 Python 77 项通过，Ruff/mypy 通过。执行记录不可变、baseline 使用 tuple，candidate 一经记录不可替换；任何写盘不确定会关闭 Store，必须重新扫描后才能继续操作。规格审查通过，质量审查进行中。

原始 Compose 显式 Fake/空 Claude 凭证重建 Runtime，UID/GID 10001:10001，服务 healthy。父级在该持久卷独立测试目录 reserve，重启容器后重新实例化 Store 读到 starting，再 awaiting_finalize→abort→删除两次；真实 HTTP GET executions 返回空数组、缺失 DELETE 返回 204、health 正常。测试目录已清理，默认 execution root 未注入记录。此验证不代表 Task 15 的 worker 崩溃恢复已交付。

质量审查通过，无 Critical/Important/Minor；Task 12 完成。接下来直接执行 Task 13，复用现有 Pydantic Manifest 模型，Runtime 只校验 Go 已准备的 Workspace，不重复创建、下载或复制。

主分支与远端已同步 `a630837`，main 的 77 项 Python 测试通过后 push 成功。Task 13 实施中，另补共享 `ArtifactManifest` 对 bool schema_version 的严格拒绝，避免 Pydantic Literal 将 true 等同于 1。当前正在检查不可读目录、有效权限和 Manifest 有界读取，尚未冻结/审查。

## Task 13 最终验证

最终实现 `5e50900`：父级独立 Python 116 项、Ruff、mypy 全通过；规格审查通过，质量审查发现目录写入需要 W_OK|X_OK，补两项 0600 目录红→绿回归后复审通过，无剩余问题。输入仍单独检查 W_OK；Manifest 允许 Go 同样支持的安全内部文件链接，拒绝逃逸、特殊节点和不可读子树，读取有界且计入所有输出文件大小。

父级重建原始非 root Runtime，在真实 Go API 生成的 Workspace 上验证路径/权限及 Fake 输出 Manifest，通过。Project `63ca49e3-6f1f-4e6e-9842-fce95c9d662b`，Run `7051bbc7-d9ce-422b-8c0f-cd38f9fe4194`；输入文件真实命名为 `{input_id}-shared.csv`，不是裸文件名（首次父级脚本断言已据此修正，未改产品命名）。验收两个 Run 全部 finalized，Project 已逻辑删除并 purge，PG/MinIO/Workspace 清理检查通过。下一步 Task 14，先读两份已提交 SDK/fork 研究，保持固定版本、opaque staging 和明确所有权。
