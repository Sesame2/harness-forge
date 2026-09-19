# V0 交付验证记录

设计依据：[中文](../superpowers/specs/2026-07-19-harness-forge-design.zh-CN.md) / [English](../superpowers/specs/2026-07-19-harness-forge-design.md)。操作步骤见 [README](../../README.md) 与 [fresh clone 验收](local-setup.md#可复现的-fresh-clone-验收)。

本页明确区分工作区验证、同机 fresh clone 和第二独立环境。Task 22 **尚未完成**；未执行的验收不能用既有单元测试或 health 代替。所有自动验证使用 Fake/测试替身，不调用 Claude；真实 Claude smoke 仍未运行。

## 本地实现检查（2026-09-19）

- 起始 commit：`68724ff`；此处结果针对随后 Task 22 的工作区变更，不冒充冻结 commit 的重验。
- 环境：macOS 27.0（26A428）、arm64；Docker 29.4.0（arm64）、Compose 5.1.2。
- 宿主工具：Go 1.25.4、Node 25.6.1、pnpm 9.15.4、uv 0.6.6、Python 3.12.7。
- `test-integration` 编排检查先 RED（原目标仅 postgres package、没有隔离项目），更新后 GREEN。
- `GOTOOLCHAIN=local env -u GOROOT make test-integration`：PASS，真实 PostgreSQL/MinIO；`TestComposeWorkspacePermissions` 实际运行并 PASS，不是 skip。退出自动删除本次 `harness-forge-integration` 容器、卷与网络。
- `GOTOOLCHAIN=local env -u GOROOT make test`：PASS（Go 全包、Python 253、Vitest 108 / 15 suites、smoke 脚本替身测试 17）。既有 Starlette/httpx deprecation 与 Node localstorage 警告仍在，无测试失败。
- `make verify-layout`、Compose 空 env-file `config --quiet`、`git diff --check`：PASS；integration 项目 container/volume/network 标签复查均为空。

## 冻结 commit 验收

提交文档后填写完整 SHA，使用该 commit 在 fresh clone 和第二环境验证，最后另提交本页结果。最终记录提交可以不同于被验证 commit，但必须保留明确对应关系，不把未测试的新代码视为已验证。

| 记录项 | 同机 fresh clone | 第二独立 Docker 机器 / 干净 CI |
| --- | --- | --- |
| 冻结 commit（完整 SHA） | 待填写 | 待提供环境；必须同一 commit |
| 日期、OS / architecture | 待填写 | 待填写 |
| Docker / Compose 版本 | 待填写 | 待填写 |
| `make verify-layout` | 待运行 | 待运行 |
| `make test` | 待运行 | 待运行 |
| `make test-integration` | 待运行 | 待运行 |
| `make test-e2e` | 待运行 | 待运行 |
| `pnpm --dir apps/web build` | 待运行 | 待运行 |
| `docker compose -f docker-compose.yaml config --quiet` | 待运行 | 待运行 |
| `git diff --check` | 待运行 | 待运行 |
| README 浏览器 golden path | 待运行 | 待运行 |
| 测试资源清理与标签复查 | 待运行 | 待运行 |

Golden path 记录：Project ID、Conversation ID、Run ID、primary Artifact ID 均待填写；需确认 CSV 上传、`[fixture:geo-report]`、Run `succeeded` 且 `finalized_at` 非空、HTML `Geographic report` 与工具步骤完成。只记录测试对象 ID，不记录 credential、环境变量转储或用户数据。

## 自动测试隔离说明

Integration 项目 `harness-forge-integration` 使用宿主端口 28080/28081/28090/25432/29000/29001；E2E 项目 `harness-forge-e2e` 使用 15173/18080/18081/18090/15432/19000/19001。它们忽略 `.env`，显式固定内部数据库/对象存储/Runtime 连接、共享路径与空 Claude credential。命名项目已有资源时立即拒绝，不自动接管；正常退出和测试失败都只删除本次拥有的项目卷。

Integration 使用 `go test -p 1 -tags=integration ./internal/... -v`：跨 package 共享数据库级维护锁，所以按 package 串行，不削弱测试内部并发断言。`TEST_MINIO_ENDPOINT` 含 `http://`；`HF_PERMISSIONS_INTEGRATION=1` 与 `HF_COMPOSE_PROJECT` 确保两容器真实权限测试启用。

Fake E2E 的本地图表 stub 只验证脚本传输及执行。Python SDK 替身测试、Runtime 镜像启动与 Fake 浏览器 PASS 均不代表真实 Claude API 已验收。
