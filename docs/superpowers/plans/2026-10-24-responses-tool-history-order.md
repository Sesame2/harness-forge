# Responses 混合工具历史顺序修复与验收

日期：2026-10-24。承接 `c6fba08`；用户明确要求解决剩余顺序限制、优先成熟方案、真实全流程后同步main和checkpoint。旧Task1–25不重做。

## 范围

保留固定LiteLLM、现有严格校验、模型/协议选择及会话归属。不换模型、不fallback、不删说明/参数/工具结果，不新增转换框架。最新版和未合并上游PR的核查记录见[研究](../../research/2026-10-24-responses-tool-history-order.md)。仅在单条assistant消息内采用同版本成熟Chat→Responses已有的text-first策略，保留同类块相对顺序，不跨Message移动内容。

## Task26：确定性反例和最小修复

- [x] 以真实固定网关→严格本地HTTP上游复现single mixed、parallel/reversed results、多轮及budget边界；父级复验3个HTTP400，8.959s，原样保留失败证据。
- [x] 固定源hash和zero-fuzz的独立顺序补丁；不改变既有错误补丁的拒绝策略，完整出站shape合同GREEN（完整5项106.969s）。纯空白独立块改为早拒绝，不静默丢失。
- [x] 移除smoke禁止混合的规避提示；实际网关安全标记证明真实请求包含混合assistant历史，缺证据不能算通过；marker真/假/缺失/日志失败及清理均有回归。

## Task27：真实与回归验证

- [x] 最多64输出token的合成混合历史实际网关诊断；随后Chat/Responses各三Run/两Conversation完整真实UI/SDK/工具/制品闭环，并核实Responses实际混合历史证据。Chat第3次、Responses第2次均完整PASS。
- [x] 默认unit、真实网关mock合同、PostgreSQL/MinIO integration、Fake浏览器E2E、build、类型/静态检查、空env Compose及资源清理复验。最终提示字符串回归继续重验，不放宽闸门。
- [x] 记录实际Run/Artifact IDs、脱敏模型和失败/成功；保留8轮/8192单次输出/每Run120s边界，每协议至多三次针对失败修复后重测，不无限收费重试。详见验证记录与checkpoint。

## Task28：审查与交付

- [x] 审查规格、相关调用者、秘密与状态边界；中文研究/规格/验证记录/checkpoint更新。独立规格审查及最终复核无阻断问题，作者自查不冒充独立审查；真实值扫描无泄漏，.env仍600/忽略/未跟踪。
- [x] 提交、快进main、push，核对远端SHA和两工作区干净；真实.env保持本机600且不提交。实现冻结 `7ab65052253600f8345810d8df2e7771e80530af`，后续仅checkpoint完成记录，不改变实现。

原生真实Claude需要独立凭证，不用OpenAI测试冒充；E2B实现不在本轮范围。新网关独立CI若未执行须如实单列，不能沿用旧冻结V0 CI冒充。
