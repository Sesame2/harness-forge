# OpenAI 双协议网关 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在不替换 Claude Agent SDK、Go SandboxProvider 或现有 UI 的前提下，增加明确失败而非静默降级的 OpenAI Chat/Responses 模式，并完成两种模式的真实应用闭环。

**Architecture:** 可选 LiteLLM v1.103.2 服务负责成熟协议转换，正式 custom_auth 入口仅检查真实原始请求能力。用户已批准对实证的吞错路径维护最小固定版本补丁，不重写映射逻辑。每次部署只选择一种出站协议；Runtime 的模型配置和会话目录跟随固定路由身份，原生 Claude 默认不变。测试先使用真实转换器和本地 mock 上游，再使用用户已授权的真实配置。

**Tech Stack:** 既有 Go / Python 3.12 / Vue / Docker Compose / Node；固定 LiteLLM，Python stdlib unittest 与已有 pytest/Playwright。不上新数据库、管理 UI 或路由框架。

**Approved spec:** [中文规格](../specs/2026-10-02-openai-gateway-design.md)，用户已于本轮明确确认。调研：[成熟方案及固定源码](../../research/2026-10-02-anthropic-openai-compatibility.md)。

---

## 执行约束

- 复用 `.worktrees/v0-implementation`；主目录 `.env` 已有真实 key，禁止读取后打印、提交、复制进镜像或测试快照。只有显式 live 命令可加载该文件。
- 每个实现任务先 RED→GREEN，再做规格和质量审查。一次只有一个代码实施者，其他 agent 可并行只读审查；父级独立复验。
- 若固定转换器合同不成立，先给出最小反例；可以拒绝不支持的请求形状，不可维护另一套转换器、静默改参数或换协议/模型来过测试。
- 不改变旧 `make test` / integration / Fake E2E 的无付费请求边界。原 Task22 CI 固定应用 SHA（MinIO 分发修复后重新冻结），不能冒充新网关 CI 验收。

## Chunk 1: 成熟转换器合同与严格入口

### Task 23：锁定 LiteLLM 并证明两种真实出站协议

**Files:**
- Create: `services/model-gateway/Dockerfile`（只扩展固定官方镜像，复制配置/窄 hook）。
- Create: `services/model-gateway/config-chat.yaml`、`config-responses.yaml`（每部署固定一种模式）。
- Create: `services/model-gateway/capabilities.py`（纯能力校验）、`callback.py`（正式 custom_auth 接口）、`strict-errors.patch`（固定版最小错误处理补丁）。
- Create: `services/model-gateway/tests/test_capabilities.py`、`test_contract.py`（标准库测试，真实网关对本地 HTTP mock）。
- Modify: `Makefile`（`test-gateway` 显式离线合同目标；默认 test 加无网络纯校验即可）。

- [ ] **Step 1: 写最小合同反例并确认 RED。**
  使用临时、可观测请求的 mock 上游；Chat fixture 仅接受 `/v1/chat/completions`，Responses fixture 仅接受 `/v1/responses`。不替换 LiteLLM converter。至少先验证普通文本、两工具及下一轮 tool result、stream 分片参数；尚无镜像/配置时测试必须失败。
  `python3 -m unittest discover -s services/model-gateway/tests -p 'test_capabilities.py' -v`；随后 `make test-gateway`（预期缺目标/服务失败）。
- [ ] **Step 2: 使用固定官方发布并记录可复现镜像身份。**
  核验 `ghcr.io/berriai/litellm:v1.103.2` manifest/digest/目标架构；不使用 latest/main。配置只含用户指定模型、`openai/` provider、API base/key 的环境引用。Chat 配置启用全局强制 Chat；Responses 不启用。重试为 0，不启 affinity/fallback/polyfill/drop_params，Responses 使用 `store=false`。
- [ ] **Step 3: 先写能力白名单失败测试，再实现窄 hook。**
  纯函数接口 `validate_request(body: dict, mode: str) -> None`，非法输入抛安全 `ValueError`；正式 custom_auth 入口从 Request 取未经清洗的 JSON，常量时间校验网关 key 并检查允许路径，再返回 UserAPIKeyAuth；失败明确 4xx。不能信任 pre-call 内已经清洗的 proxy_server_request.body，不能让用户伪造内部字段绕过校验。
  允许明确关闭的 thinking、普通文本/成功工具和 schema；未知语义字段/块、原生 thinking/signature/cache、image/document/server tool、is_error=true、无法等价参数及会重排的混合形状拒绝，且 mock 请求计数仍为 0。错误不包含值、key、原始请求。
- [ ] **Step 4: 运行并补齐真实转换器合同。**
  验证空白文本/顺序、多个 call ID/嵌套 JSON、成功结果回填、usage 来源、finish reason、SSE 顺序；拒绝坏 JSON/未知终态/半截流，401/429/5xx/超时/断开不可成功。已实证的未知 finish→end_turn、坏 JSON→{} 允许以最小固定版本补丁改为明确异常，不改变正常映射；patch 上下文或源版本漂移即构建失败，两个反例必须GREEN。Responses opaque reasoning 的工具轮次回放逐字段检查，`invalid_encrypted_content` 不得删字段后重试；不可支持则明确失败，不能让生产返回丢内容的成功。
  测试网关仅收到内网访问 key，上游只收到 fake OpenAI key；保留 `count_tokens` 不可用错误。`make test-gateway` 必须退出 0 并清理自有临时容器/网络/端口。
- [ ] **Step 5: 审查与提交。**
  审查固定配置、未知字段信任边界及合同是否真经过网关。`git diff --check`，提交 `feat: add fail-closed OpenAI protocol gateway`。不含真实凭证。

## Chunk 2: Runtime 与部署接线

### Task 24：最小模型配置及会话隔离

**Files:**
- Modify: `services/agent-runtime/src/harness_forge_runtime/{settings,claude,runner,api,processes}.py`，必要时窄 `backend.py` 集中身份计算/校验；复用原 SessionStore，不改 transcript 格式。
- Test: 既有 `services/agent-runtime/tests/test_claude_adapter.py`、`test_runner.py`、`test_processes.py`、`test_session_api.py`/`test_settings.py`。
- Create: `docker-compose.openai.yaml`（可选覆盖文件，不改变默认服务拓扑）。
- Create: `scripts/openai.mjs`、`scripts/openai.test.mjs`（Node 原生 parseEnv / fetch / child_process，配置加载和显式 live 操作）。
- Modify: `.env.example`、`Makefile`、`README.md`、`docs/development/local-setup.md`。

- [ ] **Step 1: 写 RED 测试，确认默认原生模式参数不变。**
  测试两种模式显式模型/网关 env；纯配置缺 key/model、坏 URL、未知模式失败，根 URL 正确补 `/v1`，既有 prefix 不被重写。不用真实 env 文件。父级和 worker 必须用相同 session 路径，使用 processes.py 的真实 spawn 环境路径证明不会二次附加指纹。
- [ ] **Step 2: 实现最小配置与会话归属。**
  兼容模式关闭 native thinking/prompt caching/非必要网络调用，显式设置 SDK 模型别名。以模式/上游 URL/实际模型生成不包含 secret 的身份，隔离 Claude 配置目录。旧路由有未 finalize execution 时拒绝切换；旧 source 不存在时拒绝恢复。默认原生目录及权限策略不变；不触碰 Go Provider 接口或 Runtime wire contract。
- [ ] **Step 3: 接线并测试安全配置加载。**
  launcher 从显式路径（默认仓库 `.env`）以 Node `parseEnv` 读取；不执行 dotenv 为 shell、不在命令行参数或 stdout 传 key。向 Compose child 传所需 env；OpenAI key 仅在 gateway service，SDK 为临时/本地 gateway key。覆盖 Compose 网关不暴露 host port；Runtime 仍使用 Docker Provider。先实现 `check`（双接口最小调用）、`dev` 和 `smoke` 明确子命令，`--api chat|responses` 控制部署模式；启动 smoke 前拒绝已有测试 project 资源，finally 只清理自己创建的资源。
- [ ] **Step 4: GREEN 回归。**
  `node --test scripts/openai.test.mjs`、`make test-python`、`make test-gateway`、`make test`；空 env 模板的 Compose config `--quiet` 成功，绝不打印真实渲染配置。验证空 key 时没有 Docker/HTTP 调用。
- [ ] **Step 5: 规格/质量审查并提交。**
  核对 parent/worker 一致性、启动恢复和取消不被破坏、未知模式 fail closed；提交 `feat: connect runtime to selectable OpenAI gateway`。

## Chunk 3: 真实全链路与交付

### Task 25：真实 Chat / Responses 验收及 checkpoint

**Files:**
- Modify: `apps/web/scripts/smoke-claude.mjs` / `.test.mjs`（优先复用已有 runSmoke，保持原有入口与替身测试）。
- Create only if needed: `apps/web/scripts/smoke-openai.mjs` / `.test.mjs`（两轮/两会话及UI验证，不复制已有上传/清理循环）。
- Modify: `docs/development/verification.md`、本计划。
- Create: `docs/superpowers/checkpoints/2026-10-02-openai-gateway.md`。

- [ ] **Step 1: 先写并跑失败的 smoke 编排测试。**
  假 HTTP/进程测试证明每模式使用指定模型、完整第二轮/独立会话、异常不发布、超时取消/finalize/清理；空凭证及非 live 默认绝无真实调用。
- [ ] **Step 2: 实现最小真实测试编排。**
  复用合成 CSV、现有 profile、分析证据/manifest 检查及 Playwright。真实 Web 页面创建/上传/发送，核对真实 Python Bash 成功、两轮各一个 HTML 版本、第二 Conversation 不带前者 source、刷新/replay与制品切换；至少确认 iframe ECharts 实例实际绘制且无 pageerror，不只检查 stub。取消和可控失败/重启使用已有确定性基础设施证明，不靠无限重复付费请求触发。
- [ ] **Step 3: 顺序执行两种真实模式。**
  用主目录 `.env`，明确 `chat` 然后 `responses`，每模式独立 project/端口/卷，不复用默认开发卷。单轮有超时/输出/turn 上限，每失败最多三次定位修复后重测；仍失败时记录安全错误及反例，不跨协议 fallback、不隐藏失败。只向用户指定上游发送合成资料。
- [ ] **Step 4: 全量离线和应用回归。**
  `make test`、`make test-gateway`、`make test-integration`、`make test-e2e`、`pnpm --dir apps/web build`、`make verify-layout`、空 env Compose config 和 `git diff --check`。父级验证自有测试资源全部清理。
- [ ] **Step 5: 记录、最终审查、同步主分支。**
  记录真实/替身证据来源、代码 SHA、固定镜像、协议、脱敏模型、Run/Artifact IDs、结果与限制，不记 key/原始敏感报文。审查通过后提交，按既有授权快进 main 并 push；核对远端及两个工作区状态。原生真实 Claude 未测试须单列，不能以 OpenAI smoke 替代声明。仅当所有已批准硬闸门满足才勾选完成。
