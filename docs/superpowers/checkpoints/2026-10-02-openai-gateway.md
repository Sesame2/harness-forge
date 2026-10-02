# 恢复记录：V0 独立 CI 与 OpenAI 兼容网关

日期：2026-10-02。本文件是执行中 checkpoint，**不是全部完成声明**。

## 用户确认与安全边界

- 用户要求完成原有全部任务及完整验证，并新增 Claude Messages → OpenAI Chat Completions / Responses 双协议支持，先研究成熟方案。
- 用户已确认受限核心能力保真、不可等价能力明确报错、不静默降级、保留原生 Claude；已确认 [中文规格](../specs/2026-10-02-openai-gateway-design.md)，继续实施及真实全流程验证。
- 用户在主目录 `/Users/mei/Desktop/Project/harness-forge/.env` 填写上游配置，授权实际调用。文件权限 600、Git 忽略；不得打印/提交/复制进镜像或自动 CI。研究和代码审查 agent 均不读该文件。
- 父级使用 Node 原生 parseEnv 加载本地文件，只向用户指定 HTTPS 地址请求。两次直连使用合成短提示、各限制 64 输出 token：`/v1/chat/completions` HTTP 200、有效 choices 和文本，`/v1/responses` HTTP 200、有效 output 和文本。根 Base URL 自动补 `/v1`。
- 上述只证明连通性，不证明网关转换、工具循环、会话或制品通过；目前尚未运行经网关的真实全流程。

## Git 与恢复入口

- 主目录 `/Users/mei/Desktop/Project/harness-forge`，已有且忽略的 `.worktrees/v0-implementation`，分支 `feat/v0-implementation`；继续复用，不重新创建或重做 Task1–21。
- 本轮起点 `34899f91099558ca4295846972ffb17b0e9c12cc`，两工作区及远端一致、干净。
- `f7cfa91`：新增经规格与质量双审查的仅手动 Fake CI，已同步 main/远端。
- `c85695f`：成熟方案调研及已审查中文规格；`317921d`：已审查实施计划。
- 新任务执行入口：[OpenAI 网关计划 Task23–25](../plans/2026-10-02-openai-gateway.md)。用户已批准，不重复询问两个协议或兼容范围。

## 本轮 V0 新鲜本机验证

基于 `34899f9`，未改变应用代码；全部默认测试使用 Fake/替身，不读真实 key。

- `GOTOOLCHAIN=local env -u GOROOT make test`：PASS，Go 全包、Python 253、Vitest 108 / 15 suites、Node smoke 替身 17；仅已有告警。
- `make test-integration`：PASS，真实 PostgreSQL/MinIO，双容器权限测试实际运行 0.49s，无 skip。
- `make test-e2e`：PASS，5/5，25.4s。
- 前端 production build、layout、空 env-file Compose config、`git diff --check`：PASS。
- `harness-forge-integration` 与 `harness-forge-e2e` 测试容器、卷、网络按标签复查全部为空；默认/其他开发资源未清理。
- 首次默认权限下 Go cache / Git / gh cache 被系统 sandbox 拒绝，使用正常审批后执行；不是代码测试失败。

## 第二独立环境：尚未通过

已添加 `.github/workflows/verify-v0.yaml`：Ubuntu24.04，read-only/manual，固定 checkout `c5962a5299264a5dee158f255606138b3a7d3b12`（与九月本机 fresh clone 相同），空 Claude 凭证、无 secrets / 真实模型调用。Action SHA 和工具版本固定，actionlint/脚本语法/策略检查及两轮审查通过。

首次真实运行：[36991884881](https://github.com/Sesame2/harness-forge/actions/runs/36991884881)，workflow commit `f7cfa91`，**FAIL**：

- 环境初始化、locked dependency 安装、layout、全部 unit、Web build 成功。
- `make test-integration` 在镜像拉取阶段即失败：`quay.io/minio/minio:RELEASE.2025-04-22T22-12-26Z` 返回 `unauthorized`；尚未启动集成用例。
- Fake 浏览器 E2E 未运行；always 资源清理检查通过。
- 正在核查官方固定版本镜像是否仍有可验证的公开分发。不允许换另一版本后仍称冻结版本全部验收通过，也不把本机镜像缓存成功冒充干净 CI 成功。

Task22 Step6/7 及最终独立环境验收暂不勾选。原有计划 Task1–5 的历史 checkbox 尚未同步，但九月 checkpoint 和本轮只读审计均确认实现已完成，不应重新实现。

## 新网关当前进度

- 成熟方案研究完成：LiteLLM/Bifrost/CCR/CLIProxyAPI/CC Switch/RelayKit；[中文研究记录](../../research/2026-10-02-anthropic-openai-compatibility.md) 含固定源码、实际丢字段/重试风险和验收闸门。
- 采用 LiteLLM v1.103.2，每部署明确选一种协议，窄 raw-request guard；不自写转换器，不修改 SandboxProvider。
- 规格与计划独立审查通过。Task23 实施中：先真实固定转换器→本地 mock 合同、能力拒绝与错误语义，尚未宣称完成。
- Task24 Runtime/部署配置、Task25 两协议真实 UI/Agent/工具/制品闭环尚待后续执行。真实 key 已准备，不需用户再次提供。
- 固定 SDK 为 0.2.120，bundled CLI 2.1.211；需要验证实际请求 shape、关闭 prompt cache/thinking 的有效配置。不要直接假设滚动官网的新 CLI 开关在固定版本可用。

恢复时先检查 Git 与运行中测试资源，读取上述计划/规格和最新 checkpoint。继续解决既有 CI 镜像分发问题与 Task23 合同闸门；不把尚未执行的测试记录为 PASS。
