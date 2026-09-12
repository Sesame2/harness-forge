# Claude Session fork：自包含证据与 staging 所有权

研究日期：2026-09-13。续接 [SDK 0.2.120 核验](claude-agent-sdk-0.2.120.md)，仅回答 Task 14 的文件依赖和清理问题。固定 Python SDK `0.2.120` / bundled CLI `2.1.211`；静态检查已下载的官方源码包及第一方文档，未运行 CLI、SDK query、Docker，未读取主机凭证或配置。滚动文档不当作固定 CLI 行为证明。

## 结论

**现有证据不足以保证查询产生的 candidate 在删除 source staging 后仍可恢复。最小保守方案是：commit 保留本 Run 所有的 source staging，直到显式、依赖安全的 purge；abort 在所有写入者停止后删除本 Run staging 和新增 candidate，原始 source/baseline 不动。** 不因清理时机未证而升级 SDK、阻塞功能或增加通用备份系统。保留 staging 消除了“主动删除潜在依赖”的风险，但不是对全部 CLI 跨 cwd 行为的实测证明。

## 1. fork 是否自包含：三层证据不能混用

| 证据 | 能确认什么 | 不能确认什么 |
| --- | --- | --- |
| 固定公开 `fork_session()` / `fork_session_via_store()` 及私有 `_build_fork_lines()` | 离线 fork 复制主 transcript，重映射消息 UUID、`parentUuid` 和 `logicalParentUuid`；写新 session ID；保留 `forkedFrom` 来源标记；过滤 sidechain/progress，保留 content-replacement，不复制文件撤销历史 | 这不是 `query(resume=source, fork_session=True)` 的实现，不能证明 CLI 生成的 candidate 文件组成 |
| 固定 Python 会话读取器 | `get_session_messages()` 在单份已读取 transcript 中沿 `parentUuid` 重建消息链，不加载 `forkedFrom` 指向的原文件 | SDK 查看器不等于 CLI 恢复器；读得出消息不证明下一次 query 可恢复 |
| 当前官方 Sessions 文档 | fork 的产品语义是复制原历史、使用新 ID、原会话不变，两个会话可分别恢复 | 没有给出 CLI 2.1.211 的文件依赖或“删原文件后恢复”的固定版本测试 |

固定来源：[离线 fork 实现](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/src/claude_agent_sdk/_internal/session_mutations.py)、[fork 测试](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/tests/test_session_mutations.py)、[会话读取器](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/src/claude_agent_sdk/_internal/sessions.py)。产品语义：[官方 fork 文档](https://code.claude.com/docs/en/agent-sdk/sessions#fork-to-explore-alternatives)。

查询路径中 Python transport 只把选项转换成 `--resume` / `--fork-session`；所检查的 Python 源码没有 bundled CLI 内部的 fork 写盘、依赖解析逻辑。因此 **`forkedFrom` 存在不证明必须保留原 JSONL；离线 fork 的主链已复制也不证明查询 candidate 无外部文件依赖。** [固定 transport](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/src/claude_agent_sdk/_internal/transport/subprocess_cli.py)

官方 `materialize_resume_session()` 恢复主 JSONL 和可选 subagents，子进程退出后删整个临时 config 树；这条路径的持久副本在外部 SessionStore。它支持“将 source 放入当前 cwd bucket”的桥接思路，却不是“固定 config 内只删 source staging、保留本地 candidate 后再恢复”的测试，不能据此提前删除 staging。[固定 materialization](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/src/claude_agent_sdk/_internal/session_resume.py)、[固定测试](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/tests/test_session_resume.py)、[官方双写说明](https://code.claude.com/docs/en/agent-sdk/session-storage#dual-write-architecture)

## 2. 最小文件集合，不等于整个 config 备份

下表路径相对于固定 `$CLAUDE_CONFIG_DIR`。本项目 staging 继续逐字节复制，不解析或重写 JSONL。

| 文件/状态 | 证据与 Task 14 边界 |
| --- | --- |
| `projects/<cwd-key>/<uuid>.jsonl` | 固定 materialization 必写项；只恢复主文件的单测存在。它是无子代理、无外部结果依赖的主对话最小恢复输入，不应从可见消息 API 重建。 |
| `projects/<cwd-key>/<uuid>/subagents/agent-*.jsonl` 及 `.meta.json` | 固定 `_materialize_subkeys()` 与测试明确恢复两类文件。没有 subagent 的窄文本流程无需虚构它们；实际存在并需要恢复子代理时不能只复制主文件。离线 fork 本身不复制子代理树。 |
| `projects/<cwd-key>/<uuid>/tool-results/` | 第一方目录文档明确说明这里保存溢出的大工具输出。因此仅限制为 Read/Write/Bash 也不能推出“所有结果都在 JSONL”。若实际出现，应作为该 Session 的关联文件保留；固定 Python materialization 未恢复该类文件，不能据此认定不需要。CLI 2.1.211 的落点、fork 后引用是否仍指向旧路径、跨 cwd 如何解析，本次未证。 |
| persistence/listing 索引 | 固定 materialization 没有复制或生成 `sessions-index.json` 等索引便交给 CLI；没有证据要求把它列入最小 staging 集合。这是该官方路径的行为，不是对全部 CLI 布局作“永远不需要”保证。 |
| attachments / session-memory | 固定 fork 认识 JSONL 内的 `attachment` 记录，但不证明所有附件字节内嵌。当前官方目录文档区分图片缓存、Remote Control 上传与跨会话 `projects/<project>/memory/`；本项目纯文字提示与 workspace 文件不等于这些上传功能。未找到固定版本证据证明独立 `session-memory` 文件是此窄流程的恢复必需项，不加入猜测路径，不复制全局 memory；遇到实际未知关联文件应报告而非静默丢弃。 |
| `file-history/<uuid>/`、实际 workspace 文件 | file-history 服务文件回滚；固定 SDK 禁止 checkpointing 与外部 SessionStore 同用，因为备份仅在本地。普通对话恢复不应被扩展成撤销历史迁移。fork 不隔离/恢复 workspace，geo 输入、脚本和产物仍由现有 Workspace 生命周期负责。 |

固定证据：[materialization 与 subkeys](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/src/claude_agent_sdk/_internal/session_resume.py)、[主文件 / subagent fixtures](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/tests/test_session_resume.py)、[checkpointing 校验](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/src/claude_agent_sdk/_internal/session_store_validation.py)。滚动文档仅证明上述数据类别：[官方应用数据目录](https://code.claude.com/docs/en/claude-directory#application-data)、[fork 与文件系统的区别](https://code.claude.com/docs/en/agent-sdk/sessions#fork-to-explore-alternatives)。

**边界：**“主 JSONL + 实际存在且布局已识别的 Session 同名关联树”是窄文件所有权单位，不是已证明完备的跨目录依赖闭包。复制关联树可保护副本字节，但不自动改写 transcript 内旧绝对路径；原始 source 及其关联文件也必须保留。无需因此复制整个 project/config、设置、凭证、缓存或建立通用索引系统。

## 3. Task 14 最小所有权 / 清理合同（设计建议）

保持 `CLAUDE_CONFIG_DIR` 固定、每 Run cwd 不变、`resume=source_uuid` / `fork_session=True`；不离线先 fork，不在当前 Run 恢复 candidate，不用 symlink/hardlink 共享 source 文件。

1. **启动前保留原件并记录归属。** baseline 先持久化。ExecutionRecord 持久记录本 Run 的精确 staging 相对路径、source UUID、原件位置及创建状态；同 UUID 在不同 bucket 的副本是不同文件，不能仅凭 ID 差集或路径可推导就认领。只在 Runtime 独占、目标预先不存在的命名空间创建副本；已有目标、未知重复 UUID、链接/特殊节点或归属不明时拒绝覆盖/删除。
2. **覆盖创建崩溃窗口。** 先 durable 记录创建意图，再独占创建/发布及同步副本、目录，最后记录 ready；ready 前不能启动 SDK。恢复时只有在原记录、独占命名空间和创建协议能证明归属时才清理部分副本；不能把“写了意图”本身当成任意现存路径的所有权证明。
3. **成功 commit 保留 staging。** candidate 按既有协议同步、记录 durable marker、父进程 ack；commit 不删除本 Run source staging，包括实际创建的关联副本。ExecutionRecord/tombstone 保留路径归属供显式 purge 使用。若 terminal execution record 会被删除，不能同时丢掉这些存活 Session 文件唯一的 ownership 证据；需先保留等价的窄 ownership 记录，具体落点由 Task 14 决定。这不是新增通用备份框架。
4. **abort 只清理本 Run。** 确认 SDK/worker 全部停止后，删除已验证新增 candidate 及其关联文件、精确 owned staging；不按 source UUID 全局删除、不删整个 project bucket、不碰原始 source/baseline。删除失败不能写成功 tombstone；删除后再崩溃可幂等重试。此前成功 Run 保留的 staging 也属于受保护 baseline 文件，即使它与本 Run source UUID 相同。
5. **显式 purge 必须依赖安全。** “保留到 purge”不意味着删除旧 execution 就能删 staging。最简单的安全 purge 边界是无活动执行、且明确丢弃所有可能依赖该 staging 的会话数据；不能逐旧 Run 清扫仍可能被 active candidate 引用的副本。无需为暂未确认的依赖新增自动 GC 图。

以上顺序是本项目的安全与持久化合同，不是上游 fsync 承诺；SDK exit、Result 或 finalize 名称本身均不能代替本项目屏障。[项目 Task 14](../superpowers/plans/2026-07-19-harness-forge-v0.md)、[项目 fork / 提交规格](../superpowers/specs/2026-07-19-harness-forge-design.zh-CN.md)

## 4. 验证止点

默认 fake 测试应覆盖 byte-opaque 非链接 staging、同 UUID 多路径的归属、commit 保留、abort 精确删除、ready 前后崩溃、重启仍保有 ownership、删除旧 record 不遗失 ownership。这些只能证明 Harness 合同。

将来显式 opt-in 的固定版本 smoke 才能验证真实行为：A → 不同 cwd 的 B（resume A + fork）→ 重启 → 再不同 cwd 的 C（把 B 作为新 Run source + fork），同时检查 source 不变、历史可用，以及大 Read/Bash 输出的关联文件；若以后要提前清理 staging，另在隔离 fixture 中移走 staging 后验证恢复。**当前不做早清理，因此无需为节省副本空间而阻塞功能；仍不得把未运行的真实恢复验证报告为通过。** 未证的精确 CLI 依赖到此停止研究，不引入 metadata 重写或无边界搜索。
