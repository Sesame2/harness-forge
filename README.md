# harness-forge

个人 Harness 能力底座：Go 控制平面、Vue Web、Python Agent Runtime；V0 使用 Docker，按已批准设计预留 E2B SandboxProvider 扩展点。

## 当前进度与恢复入口

Task 1–15 已完成，按用户要求暂停；下次从 Task 16 恢复。先阅读[最新恢复 checkpoint](docs/superpowers/checkpoints/2026-09-14-task-15.md)，不要重复实现已完成任务。完整历史见[连续执行记录](docs/superpowers/checkpoints/2026-09-12-full-execution.md)。

- [中文设计规格](docs/superpowers/specs/2026-07-19-harness-forge-design.zh-CN.md)
- [实施计划](docs/superpowers/plans/2026-07-19-harness-forge-v0.md)

当前并非完整可用的地理分析应用；Runtime worker、流式执行与取消恢复已完成 fixture 验证，Geo Profile、真实 Claude opt-in smoke 与业务前端仍在后续任务中。
