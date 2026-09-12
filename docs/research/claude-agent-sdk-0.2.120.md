# Claude Agent SDK 0.2.120：Task 14 集成边界核验

研究日期：2026-09-12。范围仅为官方资料、Anthropic 发布的固定版本 Python 源码和 PyPI 元数据；未运行 Claude/API、未读取真实凭证、未启动 Docker、未修改依赖或实现。文中的“建议”不是已批准的计划变更，也不是实机验证结果。

## 结论先行

1. `claude-agent-sdk==0.2.120` 的 bundled CLI 是 **2.1.211**。当前官方文档把任意工作目录按 UUID 恢复的支持起点标为 CLI **2.1.223**；旧版本局限于当前项目及 Git worktrees。故本项目每 Run 不同 `cwd`，不能直接依赖 `resume=source_id` 自动跨目录找历史。[固定版本 CLI 声明](https://raw.githubusercontent.com/anthropics/claude-agent-sdk-python/v0.2.120/src/claude_agent_sdk/_cli_version.py)、[会话文档：Resume by ID](https://code.claude.com/docs/en/agent-sdk/sessions#resume-by-id)
2. 实际主 transcript 是 `$CLAUDE_CONFIG_DIR/projects/<encoded-cwd>/<session-id>.jsonl`，不是 `$CLAUDE_CONFIG_DIR/<session-id>/`。同名 `<session-id>/` 目录可存子代理 transcript；不能删除整个 project 目录。[固定版本 sessions.py](https://raw.githubusercontent.com/anthropics/claude-agent-sdk-python/v0.2.120/src/claude_agent_sdk/_internal/sessions.py)、[固定版本 delete_session](https://raw.githubusercontent.com/anthropics/claude-agent-sdk-python/v0.2.120/src/claude_agent_sdk/_internal/session_mutations.py)
3. `allowed_tools` 是自动批准规则，不是可用工具全集；`can_use_tool` 只处理最终需要询问的调用。固定源码专门发出 `CanUseToolShadowedWarning`。仅测回调对 unknown 返回 deny，不能证明真实 unknown 工具一定经过回调。[固定版本权限字段及 warning](https://raw.githubusercontent.com/anthropics/claude-agent-sdk-python/v0.2.120/src/claude_agent_sdk/types.py)
4. SDK 有官方 list/get/delete/fork 和外部 `SessionStore` API，但它们不等同于 Harness Forge 的窄文件系统 SessionStore，也没有公开 `sync_transcript/fsync` API。后者仍需本项目实现并测试；收到 Result 不等于已证明本项目要求的断电持久化屏障。[固定版本公开导出](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/src/claude_agent_sdk/__init__.py)、[镜像处理](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/src/claude_agent_sdk/_internal/query.py)

## 证据版本与可重复性

已下载并只读检查 PyPI 官方 sdist `claude_agent_sdk-0.2.120.tar.gz`，SHA-256 为 `e428552f79a76e0d85789369eeb58249b33f350200124e5fc86b24168bd00805`，与发布元数据一致。源码解压在 `/tmp/claude_agent_sdk-0.2.120`；仓库文档不依赖此临时目录。下面源码链接均固定到 `v0.2.120`，普通文档是滚动版本，冲突时以固定代码为准。[PyPI 0.2.120 元数据](https://pypi.org/pypi/claude-agent-sdk/0.2.120/json)、[官方发布源码包（已核验）](https://files.pythonhosted.org/packages/eb/7f/7b69aed292a4edecae132e4dbe6b6decb4e88ec142fc91d117b19058c9e0/claude_agent_sdk-0.2.120.tar.gz)

## Python API：已核验

### 请求参数

`query(*, prompt: str | AsyncIterable[dict[str, Any]], options: ClaudeAgentOptions | None = None, transport=None) -> AsyncIterator[Message]`。以下都是该版本的真实字段，不使用 TypeScript 驼峰名。[query.py](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/src/claude_agent_sdk/query.py)、[ClaudeAgentOptions](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/src/claude_agent_sdk/types.py)

| 本项目需求 | 固定版本字段和语义 |
| --- | --- |
| 首轮 | `resume=None, fork_session=False, continue_conversation=False` |
| 后续轮 | `resume=source_sdk_session_id, fork_session=True`；不能改为恢复 candidate |
| 工作目录 | `cwd: str | Path | None`；实际传给子进程 `cwd`，同时设置 `PWD` |
| Profile 系统提示词 | `system_prompt=str` 为自定义提示词；不是默认提示词追加。追加需 preset 字典 |
| 工具 | `allowed_tools: list[str]` 自动批准；`disallowed_tools: list[str]` 禁止；`tools: list[str]` 才控制内建工具可用集合 |
| 权限 | 显式 `permission_mode="default"`；dataclass 默认值实际为 `None`，不可把两者混为一谈 |
| 限额 | `max_turns: int | None`、`max_budget_usd: float | None` |
| 输出增量 | `include_partial_messages=True` |
| 环境 | `env: dict[str, str]`，覆盖继承的同名环境变量，不是清空父环境 |
| 本地设置隔离 | `setting_sources=[]`；**此版本 `None` 会载入所有来源**，不能沿用早期版本“默认不读设置”的说法 |

环境合并以及 CLI 参数映射可见固定 [`SubprocessCLITransport`](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/src/claude_agent_sdk/_internal/transport/subprocess_cli.py)。`env={"ANTHROPIC_BASE_URL": configured_url, "CLAUDE_CONFIG_DIR": "/sessions/claude"}` 可以传递本项目配置；Base URL 是 API 端点覆盖，Config Dir 则同时影响设置、会话等，不只 transcript。[官方环境变量](https://code.claude.com/docs/en/env-vars)

**建议：**父进程配置固定 Config Dir，SDK 子进程再显式传相同值。SDK 的独立 list/get/delete 函数读的是当前 Python `os.environ["CLAUDE_CONFIG_DIR"]`，没有 `options.env` 参数；只给子进程设置会导致两边查不同位置。不要在每次 HTTP 请求中临时修改全局环境。CLI 还继承其他环境，需检查运行镜像的配置设计，不能把 `env` 当作环境白名单。[路径解析](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/src/claude_agent_sdk/_internal/sessions.py)、[子进程环境合并](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/src/claude_agent_sdk/_internal/transport/subprocess_cli.py)

### 权限回调与输入模式

准确类型为 `async callback(tool_name: str, input_data: dict, context: ToolPermissionContext) -> PermissionResultAllow | PermissionResultDeny`。允许返回 `PermissionResultAllow(updated_input=input_data)`；拒绝返回 `PermissionResultDeny(message="…", interrupt=False)`。返回裸字典或 bool 会被 SDK 拒绝。`updated_permissions` 不必设置，否则可能扩大后续权限。[回调类型](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/src/claude_agent_sdk/types.py)、[结果转换与 TypeError](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/src/claude_agent_sdk/_internal/query.py)

`can_use_tool` 配合独立 `query(prompt="文字")` 会直接抛 `ValueError`，必须使用异步输入迭代器。最小输入形状如下；示例仅说明接口，未执行：

```python
async def one_prompt(text):
    yield {
        "type": "user",
        "message": {"role": "user", "content": text},
        "parent_tool_use_id": None,
        "session_id": "",
    }

# async for message in query(prompt=one_prompt(text), options=options): ...
```

另一条合法路线为 `async with ClaudeSDKClient(options=options) as client: await client.query(text)`，再迭代 `client.receive_response()`；上下文先无 prompt 连接，所以不会触发上述 string-connect 检查。不要使用带权限回调的 `client.connect(prompt=text)`。[内部 query 检查](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/src/claude_agent_sdk/_internal/client.py)、[客户端 connect/query/receive_response](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/src/claude_agent_sdk/client.py)

**计划风险：**保留 Profile `allowed_tools` 与 deny-by-default 回调是可行配置，但允许列表中的 Read/Bash 通常已经自动批准，不会回调。若要求所有新增工具一律 fail-closed，建议将 `tools` 明确限制为 Profile 允许的内建工具，并评估 `PreToolUse` deny hook 作为统一策略入口；MCP 配置另需显式控制（该版本有 `strict_mcp_config`）。是否增设这些防线由 Task 14 决定，不能仅凭 fake 回调单测宣布工具全集隔离成立。文档也指出 hook 能覆盖每次调用。[固定 options 注释](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/src/claude_agent_sdk/types.py)、[官方权限说明](https://code.claude.com/docs/en/agent-sdk/permissions)

### 消息映射与 candidate

| SDK 消息 | 正确读取位置 / 本项目建议 |
| --- | --- |
| `SystemMessage(subtype="init")` | `message.data["session_id"]`；不是 `message.session_id` |
| `StreamEvent` | `message.event["type"] == "content_block_delta"` 且 `delta.type == "text_delta"` 时读取 `delta.text` |
| `AssistantMessage` | `content` 中 `TextBlock.text`、`ToolUseBlock.id/name/input`；检查 `error` |
| `UserMessage` | `content` 中 `ToolResultBlock.tool_use_id/content/is_error`；不能作为新的用户 prompt 公开转发 |
| `ResultMessage` | `session_id`、`subtype`、`is_error`、`result`、`errors`；Result 是 SDK 结果，不是产品成功提交 |

类型与原始数据解析见固定 [types.py](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/src/claude_agent_sdk/types.py)、[message_parser.py](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/src/claude_agent_sdk/_internal/message_parser.py)。增量判断得到[官方 streaming 示例](https://code.claude.com/docs/en/agent-sdk/streaming-output)支持。SDK 同时产生完整 assistant 和增量事件；不要把同一段文字在完整消息与增量间重复拼接。

**本项目协议要求，非 SDK 自带保证：**首次 init ID 必须合法、不同于 source、不在启动前 baseline。先 `await on_candidate(id)` 等父进程持久化并 ack，再公开 agent events。缺 init、重复且不一致 init、Result ID 不一致、assistant error、`is_error=True` 或非成功 subtype，均不得 completed。`subtype="success"` 也可能伴随 `is_error=True`，必须同时判断。直到 SDK 流正确收尾且 transcript 屏障完成前，Result 不可直接转换为 `agent.completed`。[Result 类型注释](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/src/claude_agent_sdk/types.py)、[结果与异常处理](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/src/claude_agent_sdk/_internal/query.py)

## 真实文件布局与官方管理 API

给定 `cwd=/workspaces/<run-uuid>/workspace`、`CLAUDE_CONFIG_DIR=/sessions/claude`，短 ASCII 路径对应布局为：

```text
/sessions/claude/
└── projects/
    └── -workspaces-<run-uuid>-workspace/
        ├── <session-uuid>.jsonl          # 主 transcript
        └── <session-uuid>/              # 可不存在
            └── subagents/agent-*.jsonl   # 附属 transcript
```

固定 `_sanitize_path` 将非 ASCII 字母数字换成 `-`；超过 200 字符时截断附 hash，SDK 还为 CLI hash 差异提供前缀搜索。不要任意推导长路径 hash，也不要把 `projects` 外的全局 settings/history 当成某 Session 的私有文件。[路径实现](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/src/claude_agent_sdk/_internal/sessions.py)、[附属目录删除语义](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/src/claude_agent_sdk/_internal/session_mutations.py)

固定版本公开函数如下。`directory` 指**工作项目路径**，不是已经编码的 transcript 存储目录：

```python
list_sessions(directory=None, limit=None, offset=0, include_worktrees=True)
get_session_info(session_id, directory=None)
get_session_messages(session_id, directory=None, limit=None, offset=0)
delete_session(session_id, directory=None)
fork_session(session_id, directory=None, up_to_message_id=None, title=None)
```

其中 list 返回按修改时间排序的 `SDKSessionInfo`；get messages 只返回沿 parent 链得到的可见 user/assistant 消息，**不是完整可无损恢复的 JSONL**；info 缺记录或无法提取摘要时可为 None；delete 无记录抛 `FileNotFoundError`，成功删除主 JSONL 和同名附属目录，未做本项目的目录 fsync。fork 是离线复制并重映射 UUID 的独立 API，不应与本计划的 `resume=source/fork_session=True` 混用。准确签名和实现见 [sessions.py](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/src/claude_agent_sdk/_internal/sessions.py)、[session_mutations.py](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/src/claude_agent_sdk/_internal/session_mutations.py)。

**重要区别：**`list_sessions()` 会过滤 sidechain、空文件和没有摘要的记录。crash-before-init 时文件可能已经出现但还没可见消息，因此 baseline/new-ID 清理不能直接用这个 UI 列表。固定函数内部也使用普通路径访问，不提供本项目的 root-only、拒绝 symlink、并发安全删除保证。[列表过滤实现](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/src/claude_agent_sdk/_internal/sessions.py)

## 跨 cwd 恢复：已确认与未确认

### 已确认的官方桥接策略

固定 `_internal/session_resume.py::materialize_resume_session` 在 `resume + options.session_store` 时：从 store 加载完整 opaque JSONL entries，写入临时 `config/projects/<project_key_for_directory(options.cwd)>/<source_uuid>.jsonl`，必要时 materialize subagent 文件，再通过 `apply_materialized_options` 改写 `CLAUDE_CONFIG_DIR`、保留 `resume=source_uuid`；`cwd` 与 `fork_session` 不被更改。对应测试验证建树和 cleanup。[固定 materialization 源码](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/src/claude_agent_sdk/_internal/session_resume.py)、[固定测试](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/tests/test_session_resume.py)

这说明“把完整 source 的受控副本放入当前 cwd 对应项目目录”是官方采用的桥接方式。**但**直接启用官方 `session_store` 不符合本项目当前最小方案：它会创建并最终删除临时 config 树；store key 默认依赖 cwd；append/mirror 错误不会自动终止会话。它是镜像后端协议，不是固定 `/sessions/claude` 的透明路径开关。[固定 SessionStore 协议](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/src/claude_agent_sdk/types.py)、[materialization 收尾](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/src/claude_agent_sdk/_internal/client.py)

### 建议的最窄 seam，尚未实机验证

在唯一 `sessions.py` 布局 adapter 内定位 source 主 JSONL 及所需附属文件，将其 **byte-preserving、非硬链接**复制到本 Run cwd 对应 project 的 source UUID 路径；保持真实 `CLAUDE_CONFIG_DIR=/sessions/claude`、`resume=source_uuid`、`fork_session=True`。不要编辑 baseline source，不改 UUID、不先离线 fork、不升级 SDK/CLI。此建议依据上述官方桥接机制，具体 CLI 行为仍须 opt-in smoke 验证。

复制会引入两个路径具有同一 source UUID 的情况。因此需在启动前验证目标不存在，用原子独占创建/发布，并将“本 Run 创建了哪个 staging 路径”纳入 ownership marker 或 durable execution record；可由 run ID 与 source UUID 确定**确切路径**，但仅凭可推导路径不能证明所有权。收尾/重启只清理由本 Run 明确创建的 staging，不调用“按 UUID 全局删除 source”。baseline ID 集合不能识别这个 staging 副本，不能解决其泄漏；这一点需要 Task 14 单独测试和记录设计。

**未确认：**`--resume /absolute/source.jsonl` 是否被 bundled CLI 2.1.211 接受。Python transport 仅证明原样透传字符串，类型注释仍称 session ID，不能据此推断 CLI 支持路径。当前研究没有运行 CLI 或获得固定 CLI 路径解析代码，因此不把绝对路径作为可用契约。[resume 透传](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/src/claude_agent_sdk/_internal/transport/subprocess_cli.py)

## 本项目窄 SessionStore：建议的责任界面

以下是落实现有计划的设计建议，不是 SDK 承诺。所有操作只在 runtime-owned `/sessions/claude/projects`，不能访问开发者真实 `~/.claude`，不能影响 `/sessions/executions`。

- `list_ids()`：仅扫描预期 project 层中的合法 UUID 主文件名及合法 UUID 附属目录，包含空/部分写入记录；不递归把子代理 `agent-*` 当主 ID，不套用 SDK 摘要过滤。若出现未知布局、重复 ID 或非法节点，显式拒绝/报告，不能静默删掉。受控 staging 的重复 source ID 要由精确路径所有权区分。
- `exists(id)`：先验证规范 UUID，再定位实际 regular transcript；HEAD 的“存在”不等于“可恢复/已 durable”，commit 必须另查 durable marker。不要用 `get_session_info()!=None` 代替文件存在性。
- `delete(id)`：只删授权目标的主文件和同名附属树；目录必须全部受控，拒绝 symlink、特殊文件和逃逸。缺失可转换成幂等 204；其他 I/O 失败应上抛。绝不删 project 整棵树。source/baseline 的保护属于执行归属验证，不能只靠 UUID 合法性。
- `sync_transcript(id)`：打开真实 regular 主 transcript 和实际存在的附属 regular 文件并 fsync，再 fsync 附属目录、project 目录及受影响的祖先目录。不存在 UUID 附属目录不是错误；“Session directory fsync”在此布局主要指实际 project 容器和已存在附属目录。
- 路径防护：仅 `resolve().is_relative_to(root)` 无法消除检查到使用之间的 symlink 置换。Linux 实现宜围绕预先打开的受控 dir FD、no-follow 打开、`fstat` 和相对访问；拒绝节点类型变化并避免跟随整个 symlink 子树。准确策略由 Task 14 实现测试决定。

此界面刻意比官方通用 API 更窄；其布局依据为 [sessions.py](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/src/claude_agent_sdk/_internal/sessions.py) 和 [delete_session](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/src/claude_agent_sdk/_internal/session_mutations.py)，上述安全和 durable 要求来自本项目计划。

### fsync 与 Result 的证据限度

固定 `SessionStore.append` 注释声称本地写入成功后才镜像且已 locally durable；固定 `_internal/query.py` 在 Result 上先 flush 镜像再交付 Result。但这证明的是 SDK 镜像排序，不是本项目所要求的主文件和父目录均调用 `os.fsync` 的明确契约。所检查 Python 源码没有 transcript fsync 实现或公开 flush-to-disk 方法；实际 CLI 写盘逻辑未在本次研究中验证。[SessionStore 注释](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/src/claude_agent_sdk/types.py)、[Result flush](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/src/claude_agent_sdk/_internal/query.py)

**建议：**不能省掉计划中的显式 fsync；还需定义“Result 后 CLI 是否还在追加”的时序边界，确保 SDK 正确收尾，不在清理/取消过程中竞态同步。fsync 只能保证已写入文件的内容，不能替 CLI flush 尚在用户态的内容。fake SDK 测试能验证本项目顺序，不能证明真实 CLI 完整 transcript 已写毕。失败/未确认时不设置 durable marker。之后仍按现有链路：worker sync → parent 验证 candidate/落盘 marker → ack → `artifact.candidate/agent.completed`。

`current_ids - baseline_ids` 的 crash-before-init 清理仅在全局单执行、会话根纯 Runtime 所有、所有写入者已停止后才成立。它能找新增 UUID，不能找同 UUID 的 source staging，也不能保护被有权限进程主动篡改的 baseline。若 agent 与父进程同 UID 且可写会话 volume，“runtime-owned”不自动形成 OS 隔离；需部署权限/挂载策略保证该前提，而不是仅靠路径校验。

## Linux / UID 10001 / 安装

- Python 要求 `>=3.10`；运行依赖为 `anyio>=4.0.0`、`sniffio>=1.0.0`、`mcp>=1.23.0,<2.0.0`，Python `<3.11` 另需 `typing_extensions>=4.0.0`。普通安装不需要 examples/otel/dev extras。[固定 pyproject.toml](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/pyproject.toml)
- PyPI 有 `manylinux_2_17_x86_64` 和 `manylinux_2_17_aarch64` 平台 wheel；Linux x86_64 wheel SHA-256 为 `888070c246c92e102c52001d26532cd3646a700656d7c368f2f91a1d3c16b534`。官方 wheel 包含 CLI，SDK 默认优先 `_bundled/claude`，缺失后才搜索系统 CLI；不需要另行 `npm install -g`。sdist 小且本次解压未含 bundled binary，不能把 sdist 安装等同 wheel 的固定二进制供应。[发布文件](https://pypi.org/pypi/claude-agent-sdk/0.2.120/json)、[CLI 查找](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/src/claude_agent_sdk/_internal/transport/subprocess_cli.py)
- `0.2.120` 本身不是全依赖锁：上游依赖仍是范围。建议依计划锁定依赖和平台 wheel/hash，安装时不静默退回系统 CLI，镜像 smoke 核验实际 SDK/CLI 版本。这里不建议/不执行 SDK 或 CLI 升级。
- SDK 通过 `anyio.open_process(..., cwd=..., env=..., user=options.user)` 启动子进程，不自行提权。容器以 UID 10001 启动并让 options.user 保持 None 可沿用进程用户；但“UID 10001 已实测兼容”没有证据。本项目还应确保 CLI 可执行、HOME/临时目录/会话根/工作区可写，选择与 wheel 架构和 libc 匹配的 Linux 镜像；不能由 manylinux 标签推断 Alpine musl 支持。[子进程创建](https://github.com/anthropics/claude-agent-sdk-python/blob/v0.2.120/src/claude_agent_sdk/_internal/transport/subprocess_cli.py)

## Task 14 验收补充清单（建议）

默认测试使用 fake SDK / 临时路径，无网络及凭证。保留批准计划的 init-candidate、source/baseline 保护、Result 一致性、fsync/parent ack、重复 finalize 和 cancellation 测试，另覆盖：

1. 带回调的异步输入模式；明确 `permission_mode="default"`、`setting_sources=[]`，不把 allowed list 回调被跳过误判为 SDK bug。
2. 真实形状的 `projects/<cwd>/<uuid>.jsonl` fixture：空文件、init 前半成品、附属树、重复 ID、symlink/FIFO、缺失、删除错误、fsync 错误。
3. 不同 Run cwd 的受控 source staging：byte-preserving、source 内容未变、不是硬链接、目标已存在则拒绝；创建前/后 crash 的精确所有权恢复和清理。
4. Result 成功但 transcript 不存在、不完整、无法 fsync 或 SDK 收尾报错，均不发送 completed。
5. **仅显式 opt-in 的真实 smoke**：固定 0.2.120 / CLI 2.1.211、两轮不同 cwd、保留源、fork ID 改变、init/Result 同 ID、终止后重建 Store 并恢复。它是验证上游语义的必要证据，不能拿 fake restart test 替代；本次未运行。

尚待 Task 14 决定/验证：staging ownership 落点、未知工具的整体 fail-closed 机制、CLI 收尾与最终 transcript 完整性的同步边界、Linux UID 10001 镜像实际兼容性。无需为这份研究引入外部 SessionStore 或变更固定 Config Dir。
