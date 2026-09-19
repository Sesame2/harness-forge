# SandboxProvider：当前实现与 E2B 接入边界

设计依据：[中文](../superpowers/specs/2026-07-19-harness-forge-design.zh-CN.md) / [English](../superpowers/specs/2026-07-19-harness-forge-design.md)。接口源文件为 [`provider.go`](../../services/control-plane/internal/sandbox/provider.go)，执行契约见 [Runtime V1](runtime-protocol.md)。

Provider 只管理 Run 执行资源与文件同步，业务状态、Message、Artifact 发布和 Conversation Session 提升仍归 Go 控制平面。V0 没有 E2B 依赖，没有通用 shell/filesystem API，也不支持跨 Provider Session/历史迁移。

## 接口与不变量

| 接口 | 职责与约束 |
| --- | --- |
| `Acquire(ctx, AcquireRequest{RunID, Paths})` | 获取本次 Run 的 Lease；Go 先持久化 acquire intent，获得后再写 Provider/ref。若资源可能已创建但确认丢失，不能误报确定不存在。 |
| `Recover(ctx, RecoverRequest{RunID, Ref, Paths})` | 恢复已拥有的资源，不创建替代资源；不存在与暂时不可达必须区分。 |
| `List(ctx) []LeaseInfo{RunID, Ref}` | 发现本 Provider 管理的待对账资源，尤其是 Acquire 确认丢失时；Run ID 与 ref 必须稳定、非空且不能重复归属。 |
| `Lease.Ref()` | 可持久保存并用于恢复的 opaque 标识；业务代码不解析云厂商内部格式。 |
| `Lease.Runtime()` | 提供 Executor，而非暴露容器、SSH 或通用命令执行句柄。 |
| `Lease.Paths()` | Runtime 视角的 inputs/workspace/outputs 路径；不假定与 Go 本地路径相同。 |
| `Lease.SyncBack(ctx)` | 将产物/诊断带回 Go 的本地 Run 路径；成功路径须在 manifest 验证和发布之前完成。失败/取消只尽力同步诊断。 |
| `Lease.Release(ctx)` | 幂等释放该 Lease 资源；不代替 Runtime commit/abort，也不能删除仍保留的 committed Session。 |

错误区分 unavailable、not found、conflict、outcome unknown；不要把网络超时当资源不存在。持久 Run 上的 Provider ID 不能因当前配置改变而重解释，即使 Run 已 finalized。恢复前先核对全部保留 Run 的 Provider 归属。

Run 顺序是：准备并封存 inputs → 保存 acquire intent → Acquire → 保存 ref → Execute → 等待执行收尾 → SyncBack → 验证/发布/产品事务 → Finalize → 成功时 HEAD Session → Release → finalized。取消/失败走停止执行、尽力 SyncBack、abort、Release；不会提升 Session 或发布制品。产品提交后的失败只重试 commit 收尾。退出浏览器不打断此流程。

## Docker 与 Fake

Docker Provider 连接已由 Compose 启动的共享 Python Runtime；不是每 Run 一个容器。固定 ref 为 `docker:agent-runtime`，Acquire/Recover 检查健康和路径归属，List 从 Runtime 未 finalized executions 映射。Go 与 Runtime 共享 `run-workspaces` 卷，Runtime 路径为 `/workspaces/<run_id>/{inputs,workspace,outputs}`；当前 SyncBack/Release 是空操作。Runtime Session 使用独立 `runtime-sessions` 卷，应用容器 UID/GID 都为 `10001:10001`。这不是面向恶意代码的强租户隔离。

Fake Provider 完全在 Go 进程内回放 V1 fixture，ref 为 `fake:<run_id>`，完成事件前复制输出文件。它仍执行会话/Finalize 语义，但 Lease、execution 和 Session 记录在内存中；重启不提供远程持久恢复保证，不应作为长期会话存储。普通 prompt 默认 `geo-report`；开头的 `[fixture:<name>]` 仅在 Fake 有意义。

内置场景为 `geo-report`、`success-v2`、`agent-failure`、`invalid-manifest`、`delayed-success` 和 `blocking`。延迟场景用于刷新重连，阻塞场景在 `agent.completed` 前等待取消；未知/格式错误的选择器拒绝执行。Docker HTTP adapter 不解释选择器。HTML 本地 stub 暴露 `window.echarts`，只证明实际脚本响应可执行，不能当作真实模型或 ECharts 功能测试。

## 未来 E2B adapter 接入清单

1. 实现同一 Provider/Lease 接口，再加入显式 Provider 配置；不改业务 API，也不提前扩展通用远程 shell/filesystem 能力。
2. 明确远程沙箱创建、稳定 ref/Run 标签、List 发现、Recover 的不存在/不可达语义；验证 Acquire 成功但回包丢失的恢复。
3. 定义本地输入上传、Runtime 路径映射、只读输入、输出/诊断 SyncBack、安全路径及大小校验；不能在同步前发布。
4. 提供同一 Runtime V1 Executor，保证取消确实停止执行、candidate Session 持久化、commit/abort 幂等、HEAD 与删除契约；Release 后仍能恢复 Conversation 所需 Session。
5. 处理远程 TTL、断网、重启和泄漏资源；保持 Provider/Run 所有权检查、收尾确认和 orphan 对账，不通过新建替代资源掩盖丢失。
6. 运行 Provider 合同测试、生命周期/取消/恢复/发布失败集成测试及完整浏览器验收，再另行评估是否需要并发执行。跨 Provider 的旧历史仍先用旧 Provider purge/reset，不承诺迁移。
