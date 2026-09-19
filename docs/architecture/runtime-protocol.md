# Runtime V1 协议与提交边界

设计依据：[中文](../superpowers/specs/2026-07-19-harness-forge-design.zh-CN.md) / [English](../superpowers/specs/2026-07-19-harness-forge-design.md)。可执行契约是 [`run-request.schema.json`](../../contracts/runtime/v1/run-request.schema.json)、[`runtime-event.schema.json`](../../contracts/runtime/v1/runtime-event.schema.json) 与 [`control-plane.openapi.yaml`](../../contracts/control-plane.openapi.yaml)。本页描述现有实现，不增加新协议。

## 两条通信链路

浏览器通过 Go HTTP API 提交消息、查询 Run 与制品、取消 Run，通过 SSE 接收持久事件；不直连 Python。Go 通过 Sandbox Lease 的 `Runtime()` 调用执行接口：Docker 使用 Python HTTP + NDJSON，Fake 在 Go 进程内实现同一 Executor。浏览器断线不拥有执行生命周期。

| Python HTTP 接口 | 语义 |
| --- | --- |
| `GET /health` | 健康状态与当前 starting/running 的 `active_run_id`；不是业务 Run 查询。 |
| `POST /v1/runs/{run_id}/execute` | 请求 `version: "1"`，body Run ID 必须与 URL 一致。正常响应 `application/x-ndjson`，逐行输出 V1 事件。已 finalized 的相同 ID 返回 JSON `decision`，不重新执行。已有/忙碌冲突返回 409。 |
| `POST /v1/runs/{run_id}/cancel` | 停止执行并等待进程收尾；成功 204，不存在 404。取消不等于 Finalize。 |
| `POST /v1/runs/{run_id}/finalize` | body 为 `{"decision":"commit"}` 或 `{"decision":"abort"}`。仅 awaiting_finalize 可处理；同决策重试 204，冲突决策/未就绪 409，不存在 404。 |
| `GET /v1/executions` | 仅列出未 finalized 的执行，用于对账；字段包含 Run ID、lifecycle、candidate SDK Session ID。 |
| `HEAD /v1/sessions/{session_id}` | 持久 Session 存在 200，不存在 404。 |
| `DELETE /v1/executions/{run_id}` | 删除已 finalized 的执行记录及辅助文件；不存在亦 204，尚未 finalized 返回 409。 |
| `DELETE /v1/sessions/{session_id}` | 幂等清理 Session；Runtime 存在未 finalized 执行时，删除仍存在的 Session 返回 409。 |

请求包含 Project/Conversation/Run ID、prompt、可空 `source_sdk_session_id`、已解析的不可变 Profile snapshot（ID/version/digest/config）、inputs/workspace/outputs 路径以及 turns/budget 限制。Runtime 不重新解析 Profile，也不接受用户自由指定的宿主机文件系统路径。

每条事件包含 `version`、`run_id`、`sequence`、`type`、`occurred_at`、`payload`。已知类型为 `phase.changed`、`assistant.delta`、`assistant.message`、`tool.started`、`tool.completed`、`artifact.candidate`、`agent.completed`、`agent.failed`。顺序严格递增，终止事件后不得再发事件；未知合法非终止类型允许解析并由 Go 忽略，不推测业务成功。已知 payload 按 schema 严格验证；`tool.completed` 不是 `tool.finished`。

Go HTTP adapter 对网络不可用、结果不确定、冲突、已 finalized 等情况分类；错误信息不回显原始响应、URL 或凭证。NDJSON EOF、网络断开与 Runtime `agent.completed` 都**不直接等于业务 Run succeeded**。

## Session、制品与两阶段收尾

Runtime 未 finalized 的 lifecycle 为 `starting → running → awaiting_finalize`，Finalize 后持久化为 `committed` 或 `aborted`；`GET /v1/executions` 不返回这两个终态。它与 Go Run 的 `queued/running/succeeded/failed/cancelled/interrupted` 是两个状态模型。Python 持久化执行元数据及 Session，并在重启时将未收尾执行恢复到可对账状态；不在后台静默重跑模型。

首次 Conversation 执行的 source 为 null。后续执行从该 Conversation 最近一次成功的 SDK Session fork；SDK adapter 使用 `resume=source`、`fork_session=true`，新 candidate 不得等于 source 或已有基线 Session。不同 Conversation 不共享 SDK Session；它们只共享 Project 的 Input Files。

成功顺序：Runtime 完成并证明 candidate 持久化 → Go 等事件/错误通道收尾 → Lease SyncBack → 本地验证 manifest 和事件声明一致 → 上传对象 → 在 PostgreSQL 同一产品事务内提交 Artifact metadata、assistant Message、Run succeeded 并提升 Conversation Session → Runtime Finalize(commit) → Session HEAD 确认 → Lease Release → Go 写 `finalized_at`。

失败/取消时，不发布候选制品、不提升 Conversation Session；先停止活动执行，尽力同步诊断，再 Finalize(abort) 清除本次候选、保留 source，Release 后确认 finalized。产品事务一旦已提交，收尾只能重试 commit，不能改 abort。上一成功制品因此不会被失败 Run 覆盖。

Go 的 reconciler 按数据库 Provider/ref 和 Runtime 未收尾列表恢复；错误决策、资源所有权不一致等会阻止调度，而不是猜测成功。只有收尾确认后全局单 worker 才能继续下一 Run；更多 Provider 不变量见 [SandboxProvider](sandbox-provider.md)。

## SSE 与可重放历史

业务查询 `GET /api/v1/runs/{run_id}` 是 Run 状态权威；`GET .../events?after_sequence=N` 返回 Go 持久事件，`GET .../events/stream` 输出 `id/event/data`。SSE 的 `Last-Event-ID` 优先于查询参数。Go 先订阅通知再读数据库，通知只是提示，定时重读兜底，事件序号由 Go 持久层维护，不等同 Runtime 内部 sequence。

前端先加载持久历史，再从已处理游标接流；刷新/断线重连按 Run 隔离游标并去重。只有 Go 业务终止事件触发终态查询及结束订阅，不能被 Runtime `agent.completed` 提前关闭。关闭页面只断开订阅；取消需 `POST /api/v1/runs/{run_id}/cancel`。完整 assistant 内容使用 canonical Message，delta 仅作进行中展示。
