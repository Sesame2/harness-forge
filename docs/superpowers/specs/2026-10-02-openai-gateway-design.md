# OpenAI 双协议兼容网关设计

日期：2026-10-02。基于 [方案调研](../../research/2026-10-02-anthropic-openai-compatibility.md)，补充现有 V0，不替换 Claude Agent SDK 或 Go SandboxProvider。

## 已确认的需求

- 保留原生 Claude 调用，同时支持 OpenAI-compatible Chat Completions 和 Responses 上游。
- 用户已接受：文本、流式、工具调用、多轮上下文在声明支持的集合内保真；不能等价转换的能力明确报错，不静默丢弃。
- 用户已在主目录 `.env` 填写真实凭证，授权先测连通性，再实现并执行两种协议的完整真实流程。
- 父级执行代理分别向指定 HTTPS 上游发送一个最小文本请求：两接口均 HTTP 200、响应结构有效且有文本输出。这不是网关或 Agent 全流程通过。

## 方案选择

选用固定版本 LiteLLM v1.103.2 的 Anthropic Messages 转换器，使用其正式 `custom_auth(request, api_key)` 入口读取未经清洗的请求并检查能力，不信任 pre-call 中已清洗的 body 快照，不自行实现协议转换。

真实固定镜像合同测试发现：未知 Chat finish_reason 被映射为 end_turn，损坏的 Responses 工具 JSON 被替换为 `{}`。用户已明确批准维护固定版本的最小错误处理补丁，将这类吞错改为显式失败。补丁保留成熟转换器的映射逻辑，严格匹配源版本/上下文，构建时不匹配即失败；真实网关回归必须从 RED 转 GREEN，不维护另一套转换器。

比较：Bifrost 支持显式双路由，但其 reasoning strip-and-retry 尚无已核实的关闭方式；Claude Code Router / CLIProxyAPI 等也有静默去字段及额外部署行为。LiteLLM 同样不是无损保证，必须通过本项目合同测试后才可接真实工作流。

```text
浏览器 → Go → Docker Runtime → Claude Agent SDK
                                ↓ Anthropic Messages
                    LiteLLM + 转换前能力检查
                                ↓ 本次部署指定的一种协议
                  Chat Completions 或 Responses 上游
```

只增加一个可选网关服务。每次部署显式选择 `openai-chat` 或 `openai-responses`，两类测试使用独立 Compose project 顺序执行。不做同一进程内动态修改 LiteLLM 全局开关，不增加路由数据库、管理 UI、自动选模型、fallback 或前端选择器。原生 Claude 部署不依赖网关。

Chat 模式打开 `use_chat_completions_url_for_anthropic_messages`，拒绝可能触发 Responses 重路由的 thinking；Responses 模式使用原生 Messages→Responses 路径，不串联 Chat→Responses 二次转换。上游模型来自用户配置，不猜测或替换模型。

## 配置、凭证及会话

- 项目根 `.env` 已被 Git 忽略且权限为 600；只提交无密钥的 `.env.example`。
- 用户配置 `OPENAI_API_KEY`、`OPENAI_BASE_URL`、`OPENAI_CHAT_MODEL`、`OPENAI_RESPONSES_MODEL`。根地址自动补 `/v1`，已有路径不重复附加；拒绝带用户名、密码、query、fragment 的地址。真实外部请求使用 HTTPS。
- OpenAI key 只进入网关，Agent Runtime 只得到网关访问凭证、地址和路由模型别名。默认测试不读取真实 `.env`，不接真实模型。
- SDK 显式设置模型，并在兼容模式关闭 Anthropic prompt caching、native thinking 和无关网络流量；保留原有工具权限、隔离配置、fork/commit/abort。
- 兼容模式的 Claude session 目录按协议、上游地址、模型的非秘密指纹隔离；父进程 SessionStore 与 runner/SDK 必须使用同一路径。恢复时找不到对应来源必须失败，不能跨路由静默续聊；原生 Claude 路径保持不变。切换部署模式不迁移原生或其他模型的 transcript；旧路由仍有未 finalize execution 时拒绝切换，不能在新目录接管旧候选的 commit/abort。
- 网关只在 Compose 内网开放；不得暴露到公网或把用户 key 写进生成的配置文件、镜像、日志、测试快照或 Git。

## 保真集合与失败策略

首批范围是地理分析所需的文本与本地 function 工具，CSV/GeoJSON 是沙箱输入文件，不是模型多模态输入。

允许 system/user/assistant 文本、成功的 tool result、tool use 的 name/ID/JSON 参数、工具 schema、流式或非流式、多工具及多轮历史。必须保留文本空白、内容次序、调用关联和参数值。若固定转换器会重排某种混合文本/工具 block 且无法保真，该结构必须在转换前拒绝，不能编写补偿转换器。`is_error=true` 没有等价字段时明确失败，不假装普通成功结果。

拒绝原生 Anthropic thinking/signature、显式缓存控制、image/document/未知内容块、服务器工具、无法支持的 stop/top_k/上下文管理及未知语义字段。`thinking: disabled` 等明确关闭状态允许；不把“字段不认识”处理成忽略。实现前以固定 SDK 真实请求确认所需字段白名单，不能以放宽校验掩盖差异。

Responses 自身返回的 opaque reasoning 只能在相同路由中完整往返，不能冒充 Anthropic 签名。若固定版本不能保持该往返则明确失败；不启用 encrypted-content affinity 清理，也不在错误后删除推理内容重试。关闭自动 fallback/重试；需要显式重新运行时由用户或应用已有机制决定。

usage 仅映射上游实际返回的计数，不承诺不同 tokenizer/计费等价。token count 接口若上游不支持，返回明确不可用，不伪造精确计数。未知 finish reason、坏 JSON 参数、流中错误或截断不得转成成功终态。

原有 Run 取消、失败保留旧制品、恢复协调、发布事务、iframe 隔离规则继续有效。网关错误日志只输出安全错误码；不输出原始请求、响应或凭证。

## 验收与费用边界

1. 先离线：真实固定版转换器连接本地 mock 上游，逐项断言两模式的实际 HTTP path/body/鉴权、文本/工具/历史及 SSE；mock 明确拒绝另一协议，防止路由假通过。
2. 反例必须在上游调用前拒绝：未知字段/块、cache_control、native thinking、工具错误状态、不可映射参数。额外验证 reasoning 不被 strip-and-retry、错误/截断/取消不成功。
3. 再真实：每协议使用独立临时项目和合成 CSV，经过浏览器/Go/真实 Docker Runtime/SDK/网关/用户模型，产生真实 Python 工具执行、分析证据 JSON、合法 manifest 和离线 HTML/ECharts 制品。
4. 两协议各验证首轮、续聊产生第二版本、另一个会话隔离、刷新重放、制品版本切换、取消与失败保留旧制品。可控失败/崩溃场景仍以确定性合同和既有集成/E2E 为主要证据，不要求付费模型碰运气触发。
5. 真实执行设置有界输出、最大轮数、单次超时和有限重试次数；先每协议一个最小工作流，再仅针对失败修复复测，不进行无界收费循环。SDK 的 Anthropic budget 不能当作第三方上游精确费用上限。
6. 原有 unit/integration/Fake E2E/build 全量复验。记录代码 SHA、网关版本、实际模式、脱敏模型标识、Run/Artifact IDs、通过/失败和清理结果；每项证据注明 mock 或真实来源。

只有真实转换器合同测试及两协议真实工作流通过才称功能完成。若成熟转换器无法满足上述边界，记录具体反例并停止该不兼容路径，不静默降级，也不借机重写一套协议桥接框架。
