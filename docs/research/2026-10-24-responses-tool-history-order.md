# Responses 混合工具历史顺序：成熟方案核查

日期：2026-10-24（Asia/Shanghai）。本次只研究固定源码、官方文档及上游 issue/PR；未读取 `.env`、个人 Claude 配置或凭证，未调用收费模型。真实上游与完整 UI 验收由实施任务另行记录，本文不能替代验收证据。

## 结论

保留固定 LiteLLM，不为本问题升级整个网关或切换协议。推荐在现有 Messages → Responses 的助手消息分支中，将既有文本 message 的追加放在既有 function-call 分组追加之前：**每条助手消息内，全部文本 → 全部工具调用**；两类各自顺序、原文及空白、参数、call ID 和结果关联保持不变，不跨消息搬运内容。

这不是自建转换器，也不是已合并上游修复的 backport，而是本地窄范围映射修补。它沿用同一固定版本 LiteLLM 已用于 Chat → Responses 的成熟 text-first 策略：`convert_chat_completion_messages_to_responses_api` 的助手 `content + tool_calls` 分支先追加助手文字，再逐项追加 `function_call`。[固定实现](https://github.com/BerriAI/litellm/blob/v1.103.2/litellm/completion_extras/litellm_responses_transformation/transformation.py#L438-L460)

原先只有错误处理补丁；新增映射修补应与其分开命名、记录来源及精确源 hash。用户已批准混合块顺序规范化，但这仍不等于所有 Claude 语义无损。thinking、tool error、未知字段等既有 fail-closed 边界不因顺序修补而放宽。

## 固定源码确认

| 对象 | 固定版本/提交 | 核查结果 |
| --- | --- | --- |
| 当前网关 | LiteLLM `v1.103.2`；adapter SHA256 `2104f838068fda3610dfb74cbcd8ea95867a3da54fefcb7458de748afeeaadd5` | 与项目 Dockerfile 的原始源 hash 一致；先输出所有工具调用，再集中输出助手文字。 |
| 最新正式版本 | `v1.106.2`，2026-10-21 发布；adapter SHA256 `31f530eb4ec102c40f95f31723e1df394462cbeb6b5aa72ff34fd2048a46e102` | 包路径已改名为 `anthropic/pass_through`，顺序缺陷仍在。 |
| 检查时 main | `f2118fc7670ba3fa736d9b8f4deef9077a45a720`，2026-10-22 | 同一助手顺序逻辑仍未修复；不能把 main 或版本升级当解决方案。 |
| 同版本 Chat → Responses | `v1.103.2`，文件 blob `1b976f5a48b11b1f5c00bd87e3c0a69586bd6fdf` | 已采用同消息 text-first，提供最小策略依据。 |

版本依据：[发布元数据](https://api.github.com/repos/BerriAI/litellm/releases/latest)、[v1.106.2 发布页](https://github.com/BerriAI/litellm/releases/tag/v1.106.2)。实现依据：[v1.103.2 adapter](https://github.com/BerriAI/litellm/blob/v1.103.2/litellm/llms/anthropic/experimental_pass_through/responses_adapters/transformation.py#L194-L391)、[v1.106.2 adapter](https://github.com/BerriAI/litellm/blob/v1.106.2/litellm/llms/anthropic/pass_through/responses_adapters/transformation.py)、[固定 main adapter](https://github.com/BerriAI/litellm/blob/f2118fc7670ba3fa736d9b8f4deef9077a45a720/litellm/llms/anthropic/pass_through/responses_adapters/transformation.py)。主分支会移动，表中提交才是本次证据边界。

当前直接 adapter 的 `_assistant_group_to_input_items` 不输出 text；调用方先展开 reasoning/tool 分组，最后再聚合 `asst_parts`。正式路由开关 `use_chat_completions_url_for_anthropic_messages` 改的是目的协议，不是该分支的排序策略。本次核查的配置与源码中未找到针对该顺序的正式开关。[固定路由](https://github.com/BerriAI/litellm/blob/v1.103.2/litellm/llms/anthropic/experimental_pass_through/messages/handler.py)、[官方映射说明](https://docs.litellm.ai/docs/anthropic_unified/messages_to_responses_mapping)

## 可复现输入与差异

以下合成历史不含用户数据或模型凭证：

```json
[
  {"role":"user","content":"start"},
  {"role":"assistant","content":[
    {"type":"text","text":" before\n"},
    {"type":"tool_use","id":"call_a","name":"lookup","input":{"n":1,"flag":false}},
    {"type":"text","text":" after\t"}
  ]},
  {"role":"user","content":[
    {"type":"tool_result","tool_use_id":"call_a","content":" result\n"}
  ]}
]
```

为排除猜测，研究中下载固定原始文件，在内存中仅提取相关 adapter 方法执行合成输入；没有导入整个网关或访问模型。`v1.103.2` 与 `v1.106.2` 均实际得到：

```text
user("start")
function_call(call_a, lookup, {"n": 1, "flag": false})
assistant([" before\n", " after\t"])
function_call_output(call_a, " result\n")
```

同时断言原输入未修改，两个文本块及空白未丢失。问题是跨类型顺序，不是文本缺失。项目原 checkpoint 已记录当前上游拒绝 call 与 output 之间的文字；上游官方 issue 也报告相同的严格批次校验问题，但 issue 的其他服务实测不是本项目实测。[项目 checkpoint](../superpowers/checkpoints/2026-10-02-openai-gateway.md)、[LiteLLM issue #47169](https://github.com/BerriAI/litellm/issues/47169)

推荐规范化的目标是：

```text
user("start")
assistant([" before\n", " after\t"])
function_call(call_a, lookup, {"n": 1, "flag": false})
function_call_output(call_a, " result\n")
```

它显式改变同一助手消息内 text 与 tool 的相对位置，不声称逐块原序保真；工具之间、文本之间以及消息之间的顺序不变。这正是已批准的规范化，而非通过提示要求模型避免混合块。

## 官方 PR 为什么不是完整答案

截至本次 API 核查，以下两个 PR 均为 **open、merged=false**，不能标成已发布、已合并或独立验证完成：

- [PR #45478](https://github.com/BerriAI/litellm/pull/45478)，head `f237a9fac4ad12b58a659bedc1b5bf69628339ce`。对应 [issue #45470](https://github.com/BerriAI/litellm/issues/45470)。仅在分组函数增加 text 映射、删除尾部聚合，修复原始块顺序；作者报告的 72 项测试不是本项目运行结果。
- [PR #47177](https://github.com/BerriAI/litellm/pull/47177)，head `443a353a7205f294cab3ca6070f8d92fc6b1820e`。除按序映射外还改 reasoning carrier、类型和流处理；作者明确表示未做 reporter 上游的 live 测试。与当前不支持 thinking 的合同相比，整份引入过宽。

研究中对 #45478 固定 head 的相同方法实际运行上述合成输入，得到：

```text
user → assistant(" before\n") → function_call → assistant(" after\t") → function_call_output
```

这对恢复原始顺序是正确修复，但工具后原本就有文本时，仍违反当前上游的严格邻接约束。只 backport 该 PR 不能诚实地宣称解除全部混合历史限制。对只有 `[text, tool_use]` 的输入，它才同时修复排序和邻接。

## 候选比较

| 方案 | 成熟依据 | 对当前需求的结论 |
| --- | --- | --- |
| 只升级 LiteLLM 到正式最新版 | 固定 v1.106.2 源码 | 缺陷仍在；增加原有八文件严格补丁的迁移面，却不能解决问题。 |
| backport #45478 的按序修复 | 上游精确问题及小 PR | 能解决常见 text-before-call；无法覆盖 `[tool, text]` 或交错文本，不能作为完整答案。 |
| 整份引入 #47177 | 同类正式 repo PR，含测试 | 尚未合并，reasoning 改动过大；仍以原序为目标，不解决任意 tool 后 text 的邻接限制。 |
| Messages → Chat → Responses 双桥 | 同版本现成 text-first 路径 | 请求侧有可借鉴策略，但增加一次完整转换；其 Responses → Chat 输出又分离文字 choices 并聚合工具，需重新验全部响应/流边界。为排序问题换整条路径不划算。[实现](https://github.com/BerriAI/litellm/blob/v1.103.2/litellm/completion_extras/litellm_responses_transformation/transformation.py#L659-L800) |
| 换 Bifrost | 固定 OSS v2.2.4 对交错块按原序 flush | 同样不会把 tool 后 text 全搬到前面；还有既有未解决的 reasoning strip/retry fail-closed 核查，迁移成本大。[固定转换](https://github.com/maximhq/bifrost/blob/ed8371a9779bfbc8aa689d4d77964cf8ce9308bf/core/providers/anthropic/responses.go#L6108-L6158) |
| 当前 adapter 内 text-first 窄修补 | 同一固定 LiteLLM 现成策略 | 推荐。移动既有输出步骤，不新增参数解析、协议转换器或上游重试；精确 hash、零 fuzz 构建与真实 HTTP 回归继续保护版本边界。 |

## 必须保留的验证闸门

1. 严格本地 HTTP 上游先拒绝旧映射，再接受新映射，不能只测内部 Python 函数。
2. 覆盖 `[text, tool]`、`[tool, text]`、多工具交错文本、纯工具、纯文本、两轮历史及 inline system budget。断言文本字节和空白、同类顺序、JSON 参数、ID 与结果关联均不变。
3. 并行结果逆序时依据 call ID 对应，不按位置换结果；不能跨消息找工具并全局重排。thinking、工具错误、坏 JSON、未知终态、usage、超时、取消和脱敏仍执行原合同。
4. 真实 Responses 全流程必须故意经过混合文字/工具历史，并验证确实发生，而不是只把 smoke 提示改成工具期间不说话。继续检查真实 Bash/Python、报告、ECharts、版本切换、刷新恢复和 Conversation 隔离。
5. Chat 不应受新增 Responses 补丁影响；原生 Claude 模式不变。真实上游结果、Run/Artifact ID、回归与 CI 状态写入 checkpoint 后才可交付。

OpenAI 官方手动状态管理示例要求回放完整 output items；function call 与 output 通过 call ID 关联。本项目 text-first 是针对已批准非思考子集和当前严格兼容上游的规范化，不是 OpenAI 对任意文本重排的保证，也不是通用 Responses 标准强制“所有 call/output 必须邻接”。[官方 function calling](https://developers.openai.com/api/docs/guides/function-calling)、[官方迁移与 item 映射](https://developers.openai.com/api/docs/guides/migrate-to-responses)

## HTTP合同补充：独立纯空白块

完整HTTP链补测发现，固定版 `messages/handler.py` 在分派后端前调用 `strip_empty_content_blocks_from_anthropic_messages`；`common_utils.py` 中的 `_is_empty_text_block` 以 `not text.strip()` 删除独立空/纯空白text块。这发生在顺序adapter之前，不是本地顺序补丁造成；非空正文首尾空白及嵌套tool_result.content不被此清洗删除。[固定handler](https://github.com/BerriAI/litellm/blob/v1.103.2/litellm/llms/anthropic/experimental_pass_through/messages/handler.py#L265)、[固定清洗函数](https://github.com/BerriAI/litellm/blob/v1.103.2/litellm/llms/anthropic/common_utils.py#L1296-L1345)

处理方式是在已有原始请求能力检查中加一条明确拒绝，错误码 `HF_GATEWAY_EMPTY_TEXT_UNREPRESENTABLE`；不扩展映射补丁或把丢失改成成功。两协议×user/assistant的纯校验先RED再GREEN，HTTP合同进一步要求stream/nonstream均在访问上游之前拒绝，且不产生mixed-history成功证据。含正文首尾空白的完整出站合同保持不变。

未执行完整 LiteLLM/Bifrost 上游测试，未测任何真实模型；未确认其他厂商接受此规范化。真实验收通过只能证明指定配置、固定版本和已声明子集，不能扩大为所有上游或所有 Claude 字段的兼容承诺。
