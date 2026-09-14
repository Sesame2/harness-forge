# harness-forge

个人 Harness 能力底座：Go 控制平面、Vue Web、Python Agent Runtime；V0 使用 Docker，按已批准设计预留 E2B SandboxProvider 扩展点。

## 当前进度与恢复入口

Task 1–20 已完成并推送，当前暂停在 Task 21 的场景红测阶段，另有 Task 22 待完成。先阅读[最新恢复 checkpoint](docs/superpowers/checkpoints/2026-09-14-task-21-wip.md)，不要重复实现已完成任务。未完成代码保存在 `feat/v0-implementation`，尚未合入 main。完整历史见[连续执行记录](docs/superpowers/checkpoints/2026-09-12-full-execution.md)。

- [中文设计规格](docs/superpowers/specs/2026-07-19-harness-forge-design.zh-CN.md)
- [实施计划](docs/superpowers/plans/2026-07-19-harness-forge-v0.md)

三栏业务前端、Runtime 与 Geo Profile 已完成；完整 Fake E2E 和交付文档仍在后续任务中验证，尚不能宣称完整交付。真实 Claude smoke 保持人工 opt-in 且尚未运行。
