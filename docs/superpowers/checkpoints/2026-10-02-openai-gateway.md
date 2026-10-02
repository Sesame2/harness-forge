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
- `2acd55be1d59c422d4b7e7fa2250937d202ddd04`：同 SHA 双环境 V0 验收与后续 Runtime 安全要求已同步 main/远端，主目录干净；新增网关及 Runtime 工作继续在既有实施工作树。
- `529a11a94d0693442b6d3edd8ee1ecef0689bbce`：严格双协议网关及真实 mock 合同已提交并同步 main/远端。代码质量审查已通过；system 分块格式边界仍单独记录为待答复。
- `df394b836d1e9c51af5ae99ca46ef2d0ff7e03b7`：模型后端身份及 Runtime 会话隔离已提交并同步 main/远端。
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
- 真实固定转换器→mock 证实未知 Chat finish 被当成功、损坏 Responses 工具 JSON 被替换成 `{}`。用户明确批准固定版本最小错误处理补丁，保留成熟映射；原始源码 hash 和 patch 上下文漂移即构建失败。补丁现覆盖固定 LiteLLM / OpenAI SDK 2.33.0 的 8 个源文件，只改严格校验与错误处理，不改变正常转换。
- raw-request 必须从正式 custom_auth 的 FastAPI Request 读取；pre-call 的 proxy_server_request.body 已经被清洗，不能作为原始白名单信任边界。
- Task23 合同已经实现，父级最新独立 `make test-gateway` PASS：9 个纯校验测试、Chat/Responses 两套真实固定网关→本地 mock 合同（73.947s）；不是模型替身绕开转换器。覆盖出站协议/鉴权、system/text/工具/历史、流式 JSON、真实 usage、状态/坏输出/截断/错误脱敏、30s 超时及实际 socket 取消，合同容器已清理。该次 arm64 镜像 ID 为 `sha256:e43047224f54fb9b20108be64942b353bc9e82a394f3118d6644657378245b5c`；BuildKit attestation 会使重建的本地 index ID 改变，基底固定 digest 与源 hash 不变。
- 质量复审以固定镜像无网络探针另发现 3 个转换后 hook 看不到的丢失：Chat 非流式自动补坏 JSON/空串→对象、`call.1` 与 `call:1` 均归一化为 `call_1`、音频伴随文本时音频被丢。已补真实 HTTP RED（7 项失败）→GREEN，复用 SDK 原始 Chat 解析前检查点统一拒绝；流式 JSON 分片、合法 `{}` 仍通过。
- Responses reasoning 无法保留原 ID，明确拒绝所有 reasoning，不 strip-and-retry；`disable_parallel_tool_use` 亦明确拒绝。实际文本合同确认 Responses 的多个 system 文本块会插入换行连接，块内原字符串和次序不变；Chat assistant 多文本块直接连接。已异步询问用户是否接受这项显式格式规范化，尚未收到答复；不能写成整段字节不变。
- Task24a Runtime 实现已通过独立规格和质量审查，父级完整复验 Python 288/288（5.04s）、Ruff、Mypy 13 源文件和 diff check。配置为 `HF_MODEL_BACKEND`、`HF_GATEWAY_URL/KEY`、`HF_OPENAI_BASE_URL/MODEL`；`CLAUDE_CONFIG_DIR` 始终是原始根，普通 property 派生 `.harness-backends/<sha256>`，密钥轮换不改变目录。
- 已以实际 spawn/子进程 ACK 验证父进程、worker 与 SDK 使用同一路径，并验证兼容模式清理继承凭证、显式 SDK 参数和直接 settings 注入的 gateway key 脱敏。native 默认不变；启动在 recover 前检查路由标记，有未 finalize execution 时禁止切换。跨路由 purge 的忙碌、无目标 fsync、删除后 fsync 重试、失踪已枚举目录和 symlink 均明确失败或按合同完成。
- 质量审查另用真实 `RLIMIT_FSIZE=32` 复现 64 字节身份标记短写却成功替换；已补最小长度检查及真实短写回归，失败保留旧 marker、清理临时文件。没有新增存储抽象或 wire 字段。
- Task24b 部署 launcher、可选 Compose overlay、env 模板和中文操作说明已通过独立规格/质量审查；父级 launcher 41/41（0.917s）、完整默认测试（Python288/Vitest108/原 smoke17/网关纯9）、build/layout/diff check、空 env Compose 及两模式假凭证隔离检查通过。OpenAI key 仅进入 gateway，browser 子进程没有模型凭证，生产 gateway 无 host port。真实 key 已准备，不需用户再次提供。
- 审查复现同时启动同协议 smoke 时固定项目名会互删卷；已使用每次 24 位随机后缀项目名，实际 launcher 边界并发测试 RED→GREEN，启动与 finally 清理绑定同一 own project。端口仍固定，冲突时明确失败但不能清理其他运行。dev 项目保持稳定。
- Task25 正在扩展既有 runSmoke 的 fullUI 模式，尚未运行两协议真实 UI/Agent/工具/制品闭环。launcher 已有三 Run/两会话 UUID 证据门，不接受旧单轮函数的 undefined 结果；下一步先完成离线编排测试和审查，再显式读取主目录 .env 运行 Chat、Responses。
- 固定 SDK 为 0.2.120，bundled CLI 2.1.211。无凭证、禁止外网的本地 mock 探针证实：显式 `model=harness-openai`、`thinking={type: disabled}` 配合 `DISABLE_PROMPT_CACHING=1`、`CLAUDE_CODE_EFFORT_LEVEL=unset`、`DISABLE_INTERLEAVED_THINKING=1` 可发出不含 thinking/output_config/cache_control 的真实请求；仅关闭 thinking 仍会携带默认 effort，不能省略对应 env。上述配置已由 Task24a 接入。
- 固定 CLI 只读审计所需的 `CLAUDE_CODE_MAX_OUTPUT_TOKENS=8192`、`CLAUDE_CODE_MAX_RETRIES=0`、`CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK=1`、`CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1` 及 cache/effort/interleaved thinking/实验 beta 关闭配置均已接入；Haiku/Sonnet/Opus alias 为 `harness-openai`，不设置 Fable alias。兼容 spawn 先清除继承的 `ANTHROPIC_*`、`CLAUDE_CODE_*`、`OPENAI_*` 再注入明确环境。普通 retry 关闭不等于总 HTTP 只调用一次：独立 stream/watchdog 仍可能重试，真实 smoke 仍需 120s/max_turns/输出界限。
- 原审计复现的旧 A 路由 transcript 在切到 B 后 DELETE 虚假返回 204 的问题，已由 Task24a 修复并通过回归；仅 DELETE 跨路由，HEAD、续聊、finalize 仍只访问当前 active 根，busy 或 fsync 失败继续 fail closed。

恢复时先检查 Git 与运行中测试资源，读取上述计划/规格和最新 checkpoint。原 V0 镜像分发及 pnpm 漂移已修复并通过同 SHA 两环境验收；继续收口 Task23 审查和 system block 边界，完成 Task24 Runtime/部署接入及 Task25 两协议真实全流程。120s 是 smoke 的单轮轮询/取消界限，不是现有 Runtime 的自动执行 deadline。不得将已有直连连通性、mock 合同或 Fake E2E 记成真实网关/Agent 成功。
