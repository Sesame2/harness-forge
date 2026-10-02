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

## 原 V0 第二独立环境：已通过（保留历史失败）

已添加 `.github/workflows/verify-v0.yaml`：Ubuntu24.04，read-only/manual，初次固定 checkout `c5962a5299264a5dee158f255606138b3a7d3b12`（与九月本机 fresh clone 相同），空 Claude 凭证、无 secrets / 真实模型调用。Action SHA 和工具版本固定，actionlint/脚本语法/策略检查及两轮审查通过；当前成功验收已重新冻结到下述 `7db5334`。

首次真实运行：[36991884881](https://github.com/Sesame2/harness-forge/actions/runs/36991884881)，workflow commit `f7cfa91`，**FAIL**：

- 环境初始化、locked dependency 安装、layout、全部 unit、Web build 成功。
- `make test-integration` 在镜像拉取阶段即失败：`quay.io/minio/minio:RELEASE.2025-04-22T22-12-26Z` 返回 `unauthorized`；尚未启动集成用例。
- Fake 浏览器 E2E 未运行；always 资源清理检查通过。
- 官方同版 Quay/Docker Hub 镜像已不可公开获取，历史 amd64/arm64 二进制与校验附件均为 410。已在 `4ec613721a00a89c5ef145dc61a7915644c6c23d` 改为同 release 固定官方 source SHA 构建 MinIO/mc，基础镜像 digest 固定，保留版本和许可证；不使用第三方镜像，也不声称与原 OCI 镜像逐字节一致。
- 新构建在 arm64 完成，容器 `--version` 确认 release/source SHA；工作区 Fake E2E 5/5（19.9s）。发现 integration 可复用旧 Go test 结果缓存，已加入 `-count=1` 后重新实际运行全部集成用例成功，权限测试 0.46s，无 skip。
- `4ec6137` 的本机 fresh clone 完整 PASS（Python253、Vitest108、Node17；真实 integration 权限0.48s；Fake E2E5/5，21.5s；build/layout/config/Git clean/资源清理）；另在独立新卷完成真实 UI golden path，父级已查看截图。临时 clone 为 `/private/tmp/harness-forge-accept-4ec6137-20261002`，未复制真实配置或依赖目录；仍可复用本机下载/镜像缓存，不计作独立机器。
- 第二次 CI [36994212247](https://github.com/Sesame2/harness-forge/actions/runs/36994212247) 对 `4ec6137` 的真实 PostgreSQL/MinIO integration **PASS**，包括 amd64 源码镜像构建；随后 E2E 在 Web 镜像构建阶段失败，浏览器尚未启动：未声明 packageManager 使 Corepack 选择滚动 pnpm12.8.1，固定 Node 镜像的旧 Corepack 找不到其 `bin/pnpm.cjs`。
- `7db5334948a2f377bbf2d2dc9eed9a02a80d8d35` 只为两个 JS package 固定已验证的 `pnpm@9.15.4`，不改依赖/lock。规格、质量审查通过；本机 Web `docker build --no-cache` 成功，`--network none` 确认容器 pnpm9.15.4，E2E frozen install 成功。
- 对 `7db5334` 的全新本机 clone `/private/tmp/harness-forge-accept-7db5334-20261002` 完整 PASS：Go 全包、Python253（7.38s）、Vitest108/15、Node17、真实 integration 权限0.43s无skip/cache、E2E5/5（23.5s）、build/layout/空env config/Git clean。独立新卷 golden path、Run finalized 与主 HTML 实际展示通过，父级已查看新截图。三个本次 owned 项目的容器/卷/网络标签复查全部为空；未复制真实.env或既有依赖目录。
- 第三次独立 CI [36995155512](https://github.com/Sesame2/harness-forge/actions/runs/36995155512) **全部 PASS**：workflow commit `64fc200c415edde6bdbf75061a2f1b0dddbd26fc`，实际 checkout 同一 `7db5334948a2f377bbf2d2dc9eed9a02a80d8d35`；UTC10:23:02–10:29:40，6分38秒。Ubuntu24.04.5/Linux6.17.0-1022-azure x86_64，Docker28.0.4、Compose2.38.2；Go1.25.4、Node24.11.1、pnpm9.15.4、Python3.12.11、uv0.6.6。Go全包、Python253（7.97s）、Vitest108/15、Node17、真实integration权限0.65s无skip、E2E5/5（23.0s）、build/layout/config/Git clean/cleanup均通过。仅已有非阻断告警。

原 V0 独立环境闸门完成，Task22 和冻结 V0 验收已勾选，本记录随独立文档提交同步主分支；实施工作树中的 Task23 后续变更另行审查。完整同 SHA 双环境表、golden IDs、截图和首次临时端口冲突后的原样重试证据见 [verification.md](../../development/verification.md)。Task1–5 的历史 checkbox 已在 `64fc200` 按九月 checkpoint 和实现提交补齐，标明历史证据，不是本轮重做 RED。原生真实 Claude smoke 仍未运行，以上结果不覆盖新网关或真实模型调用。

## 新网关当前进度

- 成熟方案研究完成：LiteLLM/Bifrost/CCR/CLIProxyAPI/CC Switch/RelayKit；[中文研究记录](../../research/2026-10-02-anthropic-openai-compatibility.md) 含固定源码、实际丢字段/重试风险和验收闸门。
- 采用 LiteLLM v1.103.2，每部署明确选一种协议，窄 raw-request guard；不自写转换器，不修改 SandboxProvider。官方镜像 digest 为 `sha256:f63fb81b831b170ec16851e23c36ac5bf52ef106b271406429524a2ed730bbfd`。
- 真实固定转换器→mock 证实未知 Chat finish 被当成功、损坏 Responses 工具 JSON 被替换成 `{}`。用户明确批准固定版本最小错误处理补丁，保留成熟映射；原始源码 hash 和 patch 上下文漂移即构建失败。该批准已同步规格/计划，两个原始反例已 GREEN，其他流式/错误合同仍在补齐。
- raw-request 必须从正式 custom_auth 的 FastAPI Request 读取；pre-call 的 proxy_server_request.body 已经被清洗，不能作为原始白名单信任边界。
- 规格与计划独立审查通过。Task23 实施/审查中：先真实固定转换器→本地 mock 合同、能力拒绝与错误语义，尚未宣称完成。
- Task24 Runtime/部署配置、Task25 两协议真实 UI/Agent/工具/制品闭环尚待后续执行。真实 key 已准备，不需用户再次提供。
- 固定 SDK 为 0.2.120，bundled CLI 2.1.211。无凭证、禁止外网的本地 mock 探针证实：显式 `model=harness-openai`、`thinking={type: disabled}` 配合 `DISABLE_PROMPT_CACHING=1`、`CLAUDE_CODE_EFFORT_LEVEL=unset`、`DISABLE_INTERLEAVED_THINKING=1` 可发出不含 thinking/output_config/cache_control 的真实请求；仅关闭 thinking 仍会携带默认 effort，不能省略对应 env。Task24 尚待接入。
- 已完成固定 CLI 只读静态审计：兼容模式还需显式 `CLAUDE_CODE_MAX_OUTPUT_TOKENS=8192`、`CLAUDE_CODE_MAX_RETRIES=0`、`CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK=1`、`CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`，加既有 cache/effort/interleaved thinking/实验 beta 关闭配置；Haiku/Sonnet/Opus alias 统一为 `harness-openai`，不设置会改变能力判断的 Fable alias。兼容 spawn 先清除继承的 `ANTHROPIC_*`、`CLAUDE_CODE_*`、`OPENAI_*`，再注入明确环境。普通 retry 关闭不等于总 HTTP 只调用一次：独立 stream/watchdog 仍可能重试，必须继续依靠 120s、max_turns、输出 token 界限。以上是 Task24 接入要求，尚未实现。
- 已完成 Runtime 只读审计并实际复现：旧 A 路由 transcript 在切到 B 后，DELETE 返回 204 但旧文件遗留。父级已将必要 purge 回归加入新规格/计划的 Task24：仅 DELETE 枚举原生与受管指纹根并复用安全 SessionStore；HEAD、续聊、finalize 仍只访问当前 active 根，busy 或 fsync 失败继续 fail closed。此修复尚未实现，不得将新增回归要求记为通过。

恢复时先检查 Git 与运行中测试资源，读取上述计划/规格和最新 checkpoint。原 V0 镜像分发及 pnpm 漂移已修复并通过同 SHA 两环境验收；继续完成 Task23 合同审查，再执行 Task24 Runtime/部署接入及 Task25 两协议真实全流程。不得将已有直连连通性、mock 合同或 Fake E2E 记成真实网关/Agent 成功。
