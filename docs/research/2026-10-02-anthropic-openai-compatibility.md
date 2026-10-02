# Claude Messages → OpenAI 双协议网关调研

研究日期：2026-10-02（Asia/Shanghai）。范围：Claude Code / Python Agent SDK 仍使用 Anthropic Messages 协议，网关将出站转换为 OpenAI Chat Completions 或 Responses；不是让前端改用 OpenAI SDK，也不是替换 Claude Agent SDK。

本文件是方案研究，不表示已实现、部署或通过真实模型验证。研究 agent 使用官方文档、固定发布源码及 GitHub Releases API，没有读取 `.env`、主机 Claude 配置或任何密钥，没有运行模型请求。在线文档可能晚于或落后于发布版本，下文遇到差异以固定源码为准。

## 结论

成熟网关能提供这一方向的兼容，但本次没有发现能对任意 Anthropic 请求、任意 OpenAI 兼容上游保证“全字段、全语义无损”的方案。工具错误标记、显式缓存控制、签名与加密推理、推理预算、服务器工具和上下文管理都存在不能直接等价映射的内容。不能把 HTTP 200、客户端可启动或一次聊天成功当成无损证明。

用户已确认首批边界：**文本、流式、本地 function 工具和多轮上下文保真；无法等价转换的内容显式报错，不静默丢弃；保留原生 Claude 模式。**图像、document、服务器工具、原生 Anthropic thinking/signature/cache_control 不进入首批允许集合。上传 CSV 是沙箱文件输入，不要求模型 API 接收 image/document block。

在这一边界下最终推荐 **LiteLLM v1.103.2 + 原始请求白名单 callback**，不自写协议转换。使用同一固定镜像，**每次部署显式选择 Chat 或 Responses 模式**，顺序验证两种模式，不要求同一网关实例同时提供两种协议。Chat 模式才打开全局 `use_chat_completions_url_for_anthropic_messages`，并在入站拒绝 thinking，避免其隐藏重路由；Responses 模式保留默认 Responses 分支。两者均使用普通 `openai/<model>` 配置，避免 `openai/responses/...` 经 Chat 再转 Responses 的双重桥接。[LiteLLM 路由选择][l-handler]、[thinking 重路由实现][l-chat-handler]

Bifrost OSS v2.2.4 仍是有双协议能力的成熟候选，但本次未核实如何关闭它自动剥离 reasoning 后重试等行为；为了这个个人项目的 fail-closed 子集，不优先引入 Go 插件或维护网关 fork。Bifrost 的显式 custom-provider 双路设计详见下文，保留为后续比较证据。[Bifrost OpenAI 路由实现][b-openai]、[custom provider 配置][b-custom]、[加密推理重试逻辑][b-encrypted]

**这不是“Bifrost 已满足严格无损”的结论。**该版本有主动剥离不支持字段、不可验证推理信息后重试等兼容策略；若要求“无法保留时必须失败”，这些必须先通过反例测试并确认可关闭或拦截，不能假设 `max_retries=0` 就能关闭所有特殊重试。[加密推理重试逻辑][b-encrypted]、[发布说明][b-release]

### LiteLLM fail-closed 最小接入点

- 固定版 `proxy/anthropic_endpoints/endpoints.py` 先 `_read_request_body`，把原始 JSON 交给 `ProxyBaseLLMRequestProcessing`；`common_request_processing.py` 在实际路由前以 `call_type="anthropic_messages"` 调用 `pre_call_hook`，此时仍是 Anthropic 消息结构。`CustomLogger.async_pre_call_hook(data, call_type, ...)` 可检查原字段并抛出 HTTP 异常；不要返回一个“拒绝提示字符串”冒充成功响应。[入口源码][l-endpoint]、[请求处理源码][l-processing]、[CustomLogger 接口][l-logger]
- 白名单覆盖请求顶层、message、content block、tool definition、tool choice、tool result；未知字段/类型、`is_error=true`、不可表示的混合结构等明确 4xx。不是先删字段再送入 LiteLLM。网关自身补入的内部 metadata 不等于用户输入字段，需要用原请求快照与显式内部字段区分，实际 hook 行为须测试。
- Chat 模式拒绝 thinking 参数和历史中的 thinking/redacted_thinking。Runtime 明确关闭 prompt caching 和主动 thinking，以减少客户端生成超出合同的请求；这不是授权网关接受后丢弃这些字段。
- Responses 模式只允许**该固定网关生成、同一路由身份返回**的 opaque reasoning carrier 回放，不能接受任意原生 Claude 签名。v1.103.2 会用 `litellm_encrypted_reasoning:` 包装 encrypted content，并转换回 reasoning item；源码不构成真实上游验证，因此两轮 replay 必须作为硬闸门。若不能证明保真，必须明确失败，不得删除 reasoning 后重试。[固定 carrier/replay 实现][l-common]、[Responses adapter][l-responses]
- 不启用 `encrypted_content_affinity`，不配置 `model_group_affinity_config` 中该检查，不启用 fallback、上下文压缩/polyfill、内容改写或静默 `drop_params`，设置普通 retry 为 0。固定 Router 仅在显式全局/per-group 开启时注册 encrypted-content affinity；本次发现的 strip 调用位于该可选检查，关闭后仍须用 `invalid_encrypted_content` mock 证明不产生删内容重试。[Router 注册条件][l-router]、[可选 affinity 源码][l-affinity]

用户已提供真实上游配置并授权后续真实测试。**父任务反馈**其已分别发送一个最小 Chat 和 Responses 请求，均返回 HTTP 200 且有非空文本；该结果不是研究 agent 的验证，也不证明经网关的 streaming/tool/session/artifact 流程通过，后续仍须执行本文件验收闸门。

## 固定版本与成熟度证据

| 项目 | 本次核验版本 | 许可与部署 | 有效成熟度证据及限制 |
| --- | --- | --- | --- |
| LiteLLM | `v1.103.2`，2026-10-01 发布，commit `f69b2103dfc0f7a41f65555fd66df05274584e5a` | 非 `enterprise/` 部分 MIT；enterprise 另有许可。发布说明提供 `ghcr.io/berriai/litellm:v1.103.2` 及 cosign 签名校验步骤。 | 有固定版本的 Chat/Responses 适配、流中错误/工具参数/首 chunk/停止原因等回归测试；Agent SDK E2E 文件当前主要覆盖 Bedrock，不能推断已覆盖本项目 OpenAI 两协议闭环。 |
| Bifrost OSS | `transports/v2.2.4`，2026-09-29 发布，commit `ed8371a9779bfbc8aa689d4d77964cf8ce9308bf` | 根许可 Apache-2.0；官方 Docker 镜像 `maximhq/bifrost:v2.2.4`。不要把 GitHub `/releases/latest` 返回的 enterprise tag 当成 OSS 版本。 | 有 Anthropic、OpenAI、流截断、reasoning replay、tool result/error 等单测，以及 HTTP/SDK/CLI E2E 基础设施；官方 Claude Code 文档列出测试客户端版本，不能替代本项目固定 SDK/CLI 实测。 |

版本、镜像与许可依据：[LiteLLM release][l-release]、[LiteLLM LICENSE][l-license]、[Bifrost release][b-release]、[Bifrost LICENSE][b-license]。测试依据：[LiteLLM adapter 测试目录][l-tests]、[Agent SDK E2E][l-agent-tests]、[Bifrost Anthropic 测试目录][b-anthropic-dir]、[OpenAI 测试目录][b-openai-dir]、[Bifrost CI workflows][b-workflows]。这里只确认测试存在和其检查对象，没有运行上游全部测试，没有根据 stars 宣称可靠性，也没有核验镜像 digest 或实际体积。

## 方向和协议选择：已由源码确认

### LiteLLM

- 官方入口为 `/v1/messages`，支持将 Anthropic 请求转换到 OpenAI；Claude Code 可通过 `ANTHROPIC_BASE_URL` 和网关认证令牌连接。[Messages 文档](https://docs.litellm.ai/docs/anthropic_unified)、[Claude Code 配置](https://docs.litellm.ai/docs/proxy/client_setup/claude_code)
- `messages/handler.py::_should_use_responses_api` 默认将 OpenAI/Azure 等 provider 送到 Responses；`litellm.use_chat_completions_url_for_anthropic_messages=true` 能退出该分支。该选项是全局设置，不能直接等同于两个同时存在的逐模型协议开关。[固定路由源码][l-handler]
- 后续 Chat adapter 的 `_route_openai_thinking_to_responses_api_if_needed` 对 `custom_llm_provider == "openai"`、`thinking.type == "enabled"` 且模型未声明不支持 reasoning 的请求，会改成 `openai/responses/<model>`。因此严格 Chat-only 上游必须专门测试带 thinking 的请求，不能只用普通文本测试。[固定 Chat handler][l-chat-handler]
- 在线参数映射文档声称 thinking 变成普通 output text、仅 enabled 支持、summary 总是 detailed；但固定 v1.103.2 源码已支持 adaptive 和 encrypted reasoning carrier/replay。应以源码和合同测试为准，不能照搬在线表格当作当前事实。[在线映射说明](https://docs.litellm.ai/docs/anthropic_unified/messages_to_responses_mapping)、[固定 Responses adapter][l-responses]

### Bifrost

- Claude Code 对接 base URL 形如 `http://gateway:8080/anthropic`，实际 `/anthropic/v1/messages` 进入 `ToBifrostResponsesRequest`；响应再变回 Anthropic 结构，含流事件转换。[官方 Claude Code 文档][b-claude]、[Anthropic HTTP 路由][b-ingress]
- `OpenAIProvider.Responses/ResponsesStream` 在 custom provider **禁用 Responses、允许 Chat** 时，调用 `ToChatRequest()` 后送 Chat Completions；否则走 `/v1/responses`。这提供显式协议控制，不必猜模型名称。[固定 OpenAI provider][b-openai]
- 最小配置方向：`openai-chat` 的 `allowed_requests` 只打开 `chat_completion`、`chat_completion_stream`；`openai-responses` 只打开 `responses`、`responses_stream`，均以 `base_provider_type: openai` 接相应上游。调用模型分别使用 `openai-chat/<chat-model>` 和 `openai-responses/<responses-model>`。**这只是配置设计，尚未在镜像中验证。**`allowed_requests` 部分指定时其余操作默认 false；若需模型列表、token count，要单独定义和验证，不能意外全开。[custom providers 官方配置][b-custom]

## 能力边界与已发现的信息损失

下表的“有转换”只代表有代码路径，不代表任意模型能力相同或本项目已验收。

| 能力 | LiteLLM v1.103.2 | Bifrost v2.2.4 | Harness Forge 验收含义 |
| --- | --- | --- | --- |
| 文本、system、历史 | 两类 adapter 均转换；部分 system/text block 被合并，结构并非字节保真。 | Anthropic → Responses → 可选 Chat；有角色、内容和 block 转换。 | 比较内容、顺序、角色和轮次；不能只比最终回答。 |
| `tool_use` / `tool_result` | 映射 function call、call ID、JSON arguments / output；Responses 路径会重排部分 user/tool result 内容。 | 保留 call ID 关联并转换 tool input；流和非流分别处理。 | 多工具并发、分片 JSON、多轮 tool result 必测，不能把坏 JSON 当空对象成功。 |
| `tool_result.is_error` | 本次检查的 Chat/Responses tool result 转换未映射该字段。 | 有专门测试要求 OpenAI wire **移除** IsError，因无对应字段。 | 即使输出文本还在，错误状态也不是无损；必须明确失败或使用经批准的有标记降级策略。 |
| image / document | 用户 base64/URL 图像、部分 document 有转换；工具返回图像在 Responses adapter 中移到额外 user message 并添加占位/边界说明。 | 有图像/file block 转换及相关回归测试；可用性依赖上游模型。 | 校验 bytes/MIME/URL 和与 tool result 的关联；服务器可能下载 URL，测试应只使用受控地址。 |
| thinking / reasoning | budget 映射为 effort 档位；已有 encrypted content → signature/data → reasoning replay。 | budget 同样离散化；已有 reasoning ID、signature/data carrier 和 replay。 | 能保存上游自己的 opaque reasoning，不等于能生成或验证 Anthropic 真签名，更不保证跨模型/账号可用。Chat-only 不得宣称能保留 Responses 私有加密 reasoning。 |
| `cache_control`、usage | Chat 对目标模型判断是否传 cache_control；Responses 主要映射其支持的缓存信息；usage 对 cache-read/write 做字段换算。 | OpenAI Chat marshal 明确删除 cache_control；Responses 亦有目标能力归一化。 | Anthropic 显式缓存 TTL/断点和 OpenAI 缓存策略不是同一语义；usage 以实际上游结果为准，不能伪装 Anthropic 计费或精确等价。 |
| sampling、stop | Responses adapter 未映射 `stop_sequences`、`top_k`；响应 stop_sequence 为 null；一般 `drop_params=false` 不能补救在 adapter 内没进入上游参数的字段。 | ingress 暂存 top_k/stop，OpenAI 侧会按能力移除若干 sampling 参数；不支持的工具类型也会被过滤。 | 声称严格模式前必须证明不支持参数会失败，而不是“被接受但没效果”。 |
| token count | 有 `/v1/messages/count_tokens`；官方文档将 OpenAI 路径列为 Responses `/input_tokens`。 | OpenAI CountTokens 明确调用 `/v1/responses/input_tokens`。 | Chat-only 服务可能没有此接口；不能假设一个兼容 Chat 的地址同时支持 count_tokens，也不能把本地估算标成上游精确计数。 |
| 上下文 / session | 有 context_management 转换与 polyfill，语义与原生 Anthropic 不完全相同。 | 有 context_management 字段传递；Anthropic thread continue 被显式拒绝，要求完整历史重放。 | SDK 本地 transcript/fork 保持原有机制；网关不等于会话存储。禁止会话中静默换协议/模型然后宣称上下文无损。 |
| 流、错误、取消 | 有 SSE 适配及 mid-stream error 测试。 | OpenAI provider 使用 context cancellation 关闭 raw network stream，并转换错误。 | 必测断流、429/5xx、超时和用户取消；取消浏览器订阅不等于取消 Run。关闭连接也不保证上游从未计费。 |

表格依据：[LiteLLM Responses adapter][l-responses]、[Chat adapter][l-chat]、[reasoning 共用代码][l-common]、[流错误回归][l-stream-errors]、[count 文档](https://docs.litellm.ai/docs/anthropic_count_tokens)；[Bifrost Anthropic converter][b-responses]、[OpenAI request marshal][b-types]、[OpenAI Responses 归一化][b-oai-responses]、[is_error 回归][b-toolerror]、[HTTP thread 拒绝逻辑][b-ingress]、[OpenAI count/cancel 源码][b-openai]。

### 尤其不能忽略的默认“修复”

Bifrost `core/encryptedreasoning.go` 将部分上游 400 判为 reasoning replay 问题，剥离不可验证的推理 carrier 后再试。v2.2.4 发布说明也明确列出此行为。即使最终返回成功，原推理上下文可能已减少；本次未找到已核实的“严格不剥离”配置开关。LiteLLM 共用 reasoning 代码同样有删除不可验证 encrypted blocks 的逻辑，不能因为有 `signature` 字段就默认端到端保真。[Bifrost 固定实现][b-encrypted]、[LiteLLM 固定实现][l-common]

因此推荐首先把需求写成：**对声明支持的能力保真；不支持或需要修改语义时显式失败；任何可接受降级由用户单独批准且可观察。**如果固定网关无法执行该策略，应报告能力缺口、研究其正式 hook/配置或上游修复；不要通过静默删字段、切换模型或换回 Anthropic 来冒充验证成功。

## 最小接入位置（设计建议，待批准）

```text
Python Agent SDK / Claude Code
  └─ Anthropic Messages HTTP
      └─ 可选成熟网关（一个服务）
          ├─ 明确的 Chat 路由 → 上游 /chat/completions
          └─ 明确的 Responses 路由 → 上游 /responses
```

- 现有 `services/agent-runtime/src/harness_forge_runtime/claude.py` 已向 SDK 传 `ANTHROPIC_BASE_URL`，这是接入点；网关连接、令牌、模型选择为 Runtime/部署配置，不修改 Go `SandboxProvider`。SandboxProvider 继续负责沙箱生命周期与文件/执行能力，未来 E2B seam 保持独立。
- 默认仍走现有 Anthropic 直连；新增可选 LiteLLM 网关及每部署固定的 Chat/Responses 模式，而非把网关内核嵌入控制平面。只用基础代理功能，暂不增加多租户、路由数据库、管理后台或智能 fallback。
- 上游 OpenAI key 只进入网关，SDK 只拿网关访问凭证。禁止通过打印环境变量、Compose 完整渲染配置、HTTP debug body 等泄露凭证。保留 SDK 的隔离 `CLAUDE_CONFIG_DIR` 与 `setting_sources=[]`，不读取开发者个人登录状态。
- 配置区分 gateway URL 与 upstream base URL；不要机械叠加 `/v1`。选定 provider/model 后一个 Conversation 固定其路由身份；不得未经用户允许自动在 Chat/Responses/不同模型之间迁移加密历史。
- 当前 Geo Profile 仅允许 Read/Write/Edit/Bash/Glob/Grep，禁用 WebFetch/WebSearch/NotebookEdit；这些本地工具比 Anthropic 专有服务器工具更适合首批兼容范围，但不能因此省略 tool error、取消、历史恢复和 artifact 验证。

以上本项目现状可在 `services/agent-runtime/src/harness_forge_runtime/claude.py`、`profiles/geo-analysis/profile.yaml` 及既有 Runtime/Sandbox 文档核对；它们不是上游保证。

## 专用路由方案交叉核查

并行研究以官方仓库固定源码检查了以下候选；没有执行第三方代码或读取凭证。

| 候选 | 已核实的限制 | 结论 |
| --- | --- | --- |
| Claude Code Router v3.1.1 / next-ai gateway | [Responses adapter](https://github.com/The-NeXT-AI/ai-gateway/blob/14a0c09c4bc8872554af0b42420a84cc5d2c0f01/src/adapters/builtins/target/openai-responses.ts) 不传不支持的 stop；[Anthropic parser](https://github.com/The-NeXT-AI/ai-gateway/blob/14a0c09c4bc8872554af0b42420a84cc5d2c0f01/src/adapters/builtins/source/parsers.ts) 存在 trim/system 合并。 | 有明确 Chat/Responses 双路，但没有严格拒绝所有损失的证据；本次不优先。 |
| CLIProxyAPI v8.0.10 | [Chat converter](https://github.com/router-for-me/CLIProxyAPI/blob/6fecc6e5567912661654a4eaf9b8f5436facd1c2/internal/translator/openai/claude/openai_claude_request.go) 忽略 redacted_thinking；[Responses 执行器](https://github.com/router-for-me/CLIProxyAPI/blob/6fecc6e5567912661654a4eaf9b8f5436facd1c2/internal/runtime/executor/codex_executor_execute.go) 有 Codex 专用字段处理。 | MIT/无头部署适合服务，但不能直接视为通用 Responses 保真桥。 |
| CC Switch v3.20.4 | [官方说明](https://github.com/farion1231/cc-switch/blob/b9e9620265a76b2c074a83644ae1ad5ec57c3c98/README.md) 定位 GUI，无头 CLI 是另一个社区项目；[测试](https://github.com/farion1231/cc-switch/blob/b9e9620265a76b2c074a83644ae1ad5ec57c3c98/src-tauri/src/proxy/providers/transform_responses.rs) 明确允许丢弃原生 thinking。 | 不适合作为本项目最小 sidecar。 |
| New API / RelayKit（v1.0.0-rc.41） | [官方模块说明](https://github.com/QuantumNous/new-api/blob/1a4166d8e8ba9802d2ca56fe8ecf0ed5404e80d5/relaykit/README.md) 将 Claude→Chat/Responses 评级 Fair；strict/safe 只在请求阶段拒绝损失，响应/流式仅诊断。AGPLv3，运输层需自行集成。 | 不比复用 LiteLLM proxy 加窄 guard 更小，也不满足天然全链路 fail-closed。 |

## 验证闸门

1. **先不花模型额度的合同测试**：真实固定版网关连本地可记录请求的 mock OpenAI 上游。分别以 Chat/Responses 配置启动并校验真实出站 path、body、模型名、鉴权隔离；要求 mock 不支持另一协议，防止隐式重路由测试假通过。
2. **共同保真集合**：多轮 system/text、单/并行本地工具、分片 arguments、空内容、usage、length/refusal/正常结束、SSE 事件顺序、重复/丢失/半截流、429/5xx、timeout、cancel。服务端返回错误不得合成为成功 terminal；`tool_result.is_error=true` 必须明确拒绝。
3. **必测反例**：未知字段/内容块、image/document/服务器工具、stop/top_k、cache_control、is_error、thinking budget、原生签名、上游 `invalid_encrypted_content`、不存在 count_tokens。逐项确认失败，不允许仅在文档写“不支持”却实际静默吞掉。
4. **Responses 专项**：function call `call_id` 与 reasoning item ID/opaque encrypted content 原样关联回放，`store=false` 路径、工具调用后的继续请求、断流后不能发布完整成功制品。Chat 路径不得偷偷调用 Responses。
5. **真实验证（用户已授权，尚待执行）**：以独立 Compose 项目顺序运行 Chat/Responses 两轮，各跑完整浏览器 → Go → Docker Runtime → SDK → 网关 → 模型 → 本地工具 → manifest → artifact 的地理分析；上传 CSV、创建两会话、两轮不同 cwd fork、刷新重放、失败保留旧制品、取消、重启恢复、版本切换。只用用户允许的合成资料和限额；报告固定代码与镜像版本、协议及脱敏的模型标识、成功/失败证据，不记录密钥或原始敏感报文。
6. **证据分层**：Mock 合同测试通过 ≠ 真实模型能力通过；真实直连成功 ≠ 双协议都成功；网关健康检查成功 ≠ Agent SDK 工具循环成功。本研究没有完成上述测试，不应提前勾选验收。

## 待决策 / 未核实

- 范围已确定为上述受限集合；仍待实现与测试证明原请求/流式响应 guard 不会漏过静默丢字段路径。
- Bifrost v2.2.4 能否通过正式配置/hook 禁止 reasoning strip-and-retry 以及其他静默修改；需要进一步验证，不在研究中臆造开关。
- 两类具体上游真实支持哪些核心参数、token count、usage、reasoning；最小直连 200 不等于这些都支持。本研究没有使用任何真实凭证。
- 本项目固定 Claude SDK/CLI 与候选网关的实测组合、两种 CPU 架构镜像与 digest、资源占用。发布文档列举的其他客户端测试版本不能代替本项目组合验收。
- 专用路由候选已补充交叉核查；上述限制是具体固定源码观察，不等于整个项目质量判断。

[l-release]: https://github.com/BerriAI/litellm/releases/tag/v1.103.2
[l-license]: https://github.com/BerriAI/litellm/blob/v1.103.2/LICENSE
[l-handler]: https://github.com/BerriAI/litellm/blob/v1.103.2/litellm/llms/anthropic/experimental_pass_through/messages/handler.py
[l-chat-handler]: https://github.com/BerriAI/litellm/blob/v1.103.2/litellm/llms/anthropic/experimental_pass_through/adapters/handler.py
[l-chat]: https://github.com/BerriAI/litellm/blob/v1.103.2/litellm/llms/anthropic/experimental_pass_through/adapters/transformation.py
[l-responses]: https://github.com/BerriAI/litellm/blob/v1.103.2/litellm/llms/anthropic/experimental_pass_through/responses_adapters/transformation.py
[l-common]: https://github.com/BerriAI/litellm/blob/v1.103.2/litellm/litellm_core_utils/prompt_templates/common_utils.py
[l-tests]: https://github.com/BerriAI/litellm/tree/v1.103.2/tests/test_litellm/llms/anthropic/experimental_pass_through
[l-stream-errors]: https://github.com/BerriAI/litellm/blob/v1.103.2/tests/test_litellm/llms/anthropic/experimental_pass_through/adapters/test_streaming_iterator_mid_stream_error.py
[l-agent-tests]: https://github.com/BerriAI/litellm/blob/v1.103.2/tests/proxy_e2e_anthropic_messages_tests/test_claude_agent_sdk.py
[l-endpoint]: https://github.com/BerriAI/litellm/blob/v1.103.2/litellm/proxy/anthropic_endpoints/endpoints.py
[l-processing]: https://github.com/BerriAI/litellm/blob/v1.103.2/litellm/proxy/common_request_processing.py
[l-logger]: https://github.com/BerriAI/litellm/blob/v1.103.2/litellm/integrations/custom_logger.py
[l-router]: https://github.com/BerriAI/litellm/blob/v1.103.2/litellm/router.py
[l-affinity]: https://github.com/BerriAI/litellm/blob/v1.103.2/litellm/router_utils/pre_call_checks/encrypted_content_affinity_check.py
[b-release]: https://github.com/maximhq/bifrost/releases/tag/transports%2Fv2.2.4
[b-license]: https://github.com/maximhq/bifrost/blob/ed8371a9779bfbc8aa689d4d77964cf8ce9308bf/LICENSE
[b-claude]: https://github.com/maximhq/bifrost/blob/ed8371a9779bfbc8aa689d4d77964cf8ce9308bf/docs/cli-agents/claude-code.mdx
[b-custom]: https://github.com/maximhq/bifrost/blob/ed8371a9779bfbc8aa689d4d77964cf8ce9308bf/docs/providers/custom-providers.mdx
[b-ingress]: https://github.com/maximhq/bifrost/blob/ed8371a9779bfbc8aa689d4d77964cf8ce9308bf/transports/bifrost-http/integrations/anthropic.go
[b-openai]: https://github.com/maximhq/bifrost/blob/ed8371a9779bfbc8aa689d4d77964cf8ce9308bf/core/providers/openai/openai.go
[b-responses]: https://github.com/maximhq/bifrost/blob/ed8371a9779bfbc8aa689d4d77964cf8ce9308bf/core/providers/anthropic/responses.go
[b-oai-responses]: https://github.com/maximhq/bifrost/blob/ed8371a9779bfbc8aa689d4d77964cf8ce9308bf/core/providers/openai/responses.go
[b-types]: https://github.com/maximhq/bifrost/blob/ed8371a9779bfbc8aa689d4d77964cf8ce9308bf/core/providers/openai/types.go
[b-toolerror]: https://github.com/maximhq/bifrost/blob/ed8371a9779bfbc8aa689d4d77964cf8ce9308bf/core/providers/openai/toolresultiserror_test.go
[b-encrypted]: https://github.com/maximhq/bifrost/blob/ed8371a9779bfbc8aa689d4d77964cf8ce9308bf/core/encryptedreasoning.go
[b-anthropic-dir]: https://github.com/maximhq/bifrost/tree/ed8371a9779bfbc8aa689d4d77964cf8ce9308bf/core/providers/anthropic
[b-openai-dir]: https://github.com/maximhq/bifrost/tree/ed8371a9779bfbc8aa689d4d77964cf8ce9308bf/core/providers/openai
[b-workflows]: https://github.com/maximhq/bifrost/tree/ed8371a9779bfbc8aa689d4d77964cf8ce9308bf/.github/workflows
