# 本地排障

设计依据：[中文](../superpowers/specs/2026-07-19-harness-forge-design.zh-CN.md) / [English](../superpowers/specs/2026-07-19-harness-forge-design.md)。启动步骤见 [README](../../README.md)，接口边界见 [Runtime](../architecture/runtime-protocol.md) 和 [SandboxProvider](../architecture/sandbox-provider.md)。

先确认当前 Compose 项目、Provider 和端口。对自定义项目给所有 Compose 命令加相同的 `-p <项目名>`；别让排障命令误用默认开发栈。安全的基础检查是 `docker compose -f docker-compose.yaml ps`、`docker compose -f docker-compose.yaml logs --tail=100 control-plane agent-runtime postgres minio minio-init` 与 `docker compose -f docker-compose.yaml config --quiet`。日志可能含业务输入；分享前脱敏，不输出完整环境或 credential。

## 端口、bucket 与 migration

| 现象 | 检查与处理 |
| --- | --- |
| `address already in use` | 找出占用端口的进程/容器，不直接停止未知服务。按[本地配置](local-setup.md)修改宿主机端口，并同步 Web/Artifact origin。不要修改容器内部服务端口。 |
| MinIO bucket 不存在、上传失败 | 检查 MinIO healthy、`minio-init` 正常退出、bucket 与服务配置一致。初始化幂等；在确认同一配置后执行 `docker compose -f docker-compose.yaml run --rm --no-deps minio-init`。旧 MinIO 卷的 credential 不会因随意改 `.env` 自动匹配。 |
| PostgreSQL 连不上或 migration 失败 | 检查 healthy、数据库名/用户/密码、Go 的 `DATABASE_URL`。Go 启动时执行内嵌 migrations；保留原错误，修复连接或迁移问题，不手改 migration 版本表，也不靠删卷“修好”需要保留的数据。 |
| 测试报告错误的 advisory lock 泄漏 | 全量 integration 使用 `make test-integration` 的 `-p 1`：不同测试 schema 仍共享数据库级锁。不要删断言或放松生产锁；不要并行向同一个测试数据库运行其他集成测试。 |

## Provider 配置不匹配

数据库保存 Run 的 `sandbox_provider` 和 `sandbox_ref`；即使 Run 已 finalized，保留历史仍绑定原 Provider。配置改变不能改变资源所有权。若调度器报告 retained Run requires original provider：

1. 恢复旧 Provider 和原存储/Runtime，先修复未收尾 Run。
2. 确认不再需要历史后，在界面删除相关 Project/Conversation，再用**旧 Provider**执行 `make purge-deleted-dry-run`，核对范围后 `make purge-deleted`；仍保留的历史不会被 purge。
3. 所有旧历史与资源均清理后才能切换。另一种选择是创建全新独立 Compose 项目和卷；若要重置原项目，先明确接受全部数据丢失，核对项目名再手工 `down -v`。

没有跨 Provider Session 或历史迁移。不要改数据库 Provider 字段绕过保护，也不要用新 Provider 删除旧 Provider 的资源。

## Sandbox 与 Runtime

| 现象 | 检查与处理 |
| --- | --- |
| `acquire_failed` | 检查 `RUNTIME_URL`、Runtime `/health`、Run ID 对应路径和共享卷。Docker Acquire 只连接已有 Compose Runtime，不会为每个 Run 新建容器。结果不确定时保留 acquire intent，由恢复流程对账。 |
| `sync_failed` | 检查共享卷、UID/GID `10001:10001`、只读 inputs 和可写 outputs。成功路径必须 SyncBack 后才验证 manifest；失败路径 diagnostic sync 失败不能覆盖原失败原因。Docker/Fake 当前 SyncBack 是本地共享路径上的空操作；远程 adapter 才需要传回文件。 |
| Release 失败或 `finalized_at` 为空 | 不手工补 finalized 时间；检查 Runtime finalize/session HEAD 与 Provider 日志，恢复可用性，让 reconciler 重试。资源确认收尾前不会继续下一 Run。 |
| Runtime unavailable / stream 断开 | 检查 Runtime 健康与网络，保持同一 Run ID。缺少终止事件属于结果不确定，不自动当作成功，也不另起一个执行来“补回”。 |
| succeeded 但 unfinalized | 产品事务可能已提交，Runtime commit/Session 确认或 Release 尚未完成；此后只能重试 commit 收尾，不得改成 abort 或重复发布。 |
| Session missing | 确认原 Runtime Session 卷仍在、Provider 未换、路径正确。保留证据并恢复备份；不能凭空生成相同 Session 或把旧 Conversation 静默重置。Fake Session 在 Go 内存里，重启不保证保留，不应用来长期保存会话。 |
| 删除 Project/Conversation 返回 409 | 仍有 queued/running 或 unfinalized Run；先取消并等待收尾，无法收尾时排查上面的 Runtime/Provider 问题，不能强删依赖数据。 |

Runtime 重启的恢复与 Go 重启的对账见[协议](../architecture/runtime-protocol.md)。单个全局 worker 会有意串行执行；取消排队任务不会取消正在执行的另一个任务。

## 孤儿对象与删除

UI 软删除不等于物理清除。`make purge-deleted-dry-run` 查看 purge 与 orphan 扫描范围；确认后再 `make purge-deleted`。扫描和发布共用数据库维护锁，避免删除正在发布的对象。保留未知 key、忙碌/不可确定资源；遇到冲突应先修复来源，不在 MinIO 控制台批量清空 bucket，也不使用 `docker system prune` 代替业务清理。

自动测试只拥有 `harness-forge-integration` 或 `harness-forge-e2e` 的本次新建资源。若 preflight 发现同名遗留容器/卷/网络，先核对标签、创建来源和是否有人仍在使用；确认全部属于废弃测试后才按项目执行清理。测试退出通常自动 `down -v --remove-orphans`，但强制杀进程或 Docker 不可用可能留下资源。

## SSE、制品和 Claude credential

对话使用 HTTP 提交 + SSE，不使用 WebSocket。反向代理需允许 `text/event-stream`、禁用缓冲并给长连接合理超时；先检查 `GET /api/v1/runs/{id}/events` 是否已有持久事件，再检查 `/events/stream` 的 `Last-Event-ID`。刷新通过持久历史与游标补回事件；关闭页面或断开 SSE 不会取消 Run，取消必须点按钮调用 API。

制品空白时检查 Run 是否成功、Artifact 是否 committed、独立网关 URL/origin、入口与相对资源 HTTP 状态及 Content-Type。HTML iframe 只允许 scripts，不允许 same-origin；不要为“修好图表”移除 sandbox/CSP/nosniff。Fake 的本地 ECharts stub 仅证明脚本加载，不代表真实 ECharts 图表或模型分析。

Fake 模式不需要任何 Claude credential。真实调用失败时由调用者检查主动配置的 key、base URL、网络、模型额度；只记录错误类别，不打印秘密。`make test`、integration 和 E2E 不可偷偷回退真实模型。`make smoke-claude` 未运行就记录“未运行”，不能用 Python SDK 单元测试或 Fake 报告替代。
