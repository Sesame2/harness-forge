"""The only runtime module coupled to the pinned Claude Agent SDK."""

from __future__ import annotations

from collections.abc import AsyncIterator, Awaitable, Callable
from dataclasses import dataclass
from pathlib import Path
from typing import Any
from uuid import UUID

from claude_agent_sdk import (
    AssistantMessage,
    ClaudeAgentOptions,
    PermissionResultAllow,
    PermissionResultDeny,
    ResultMessage,
    SystemMessage,
    TextBlock,
    ToolResultBlock,
    ToolUseBlock,
    UserMessage,
    query,
)
from claude_agent_sdk.types import StreamEvent, ToolPermissionContext

from harness_forge_runtime.models import PAYLOAD_MODELS, RunRequest


class ClaudeAdapterError(Exception):
    """Safe failure boundary: never expose raw SDK errors or private config."""

    code = "claude_stream_failed"


@dataclass(frozen=True)
class NormalizedEvent:
    type: str
    payload: dict[str, Any]

    def __post_init__(self) -> None:
        PAYLOAD_MODELS[self.type].model_validate(self.payload)


class ClaudeAdapter:
    def __init__(
        self,
        *,
        claude_config_dir: Path,
        baseline_session_ids: tuple[str, ...],
        anthropic_base_url: str | None = None,
    ) -> None:
        if not claude_config_dir.is_absolute():
            raise ValueError("Claude config directory must be absolute")
        self.config_dir = claude_config_dir
        self.baseline = frozenset(baseline_session_ids)
        self.base_url = anthropic_base_url

    async def stream_turn(
        self, request: RunRequest, on_candidate: Callable[[str], Awaitable[None]]
    ) -> AsyncIterator[NormalizedEvent]:
        candidate: str | None = None
        got_result = False
        tool_names: dict[str, str] = {}
        try:
            source = request.source_sdk_session_id
            if source is not None and str(UUID(source)) != source:
                raise ClaudeAdapterError("invalid source session")
            profile = request.profile.config
            policy = profile["tools"]
            if policy.get("permission_mode") != "default":
                raise ClaudeAdapterError("unsupported permission policy")
            allowed, disallowed = policy["allowed"], policy["disallowed"]
            if not all(
                isinstance(names, list) and all(isinstance(n, str) for n in names)
                for names in (allowed, disallowed)
            ) or not isinstance(profile["system_prompt"], str):
                raise ClaudeAdapterError("invalid profile policy")
            available = [name for name in allowed if name not in disallowed]

            async def can_use_tool(
                name: str, input_data: dict[str, Any], context: ToolPermissionContext
            ) -> PermissionResultAllow | PermissionResultDeny:
                if name in available:
                    return PermissionResultAllow(updated_input=input_data)
                return PermissionResultDeny(message="Tool is not allowed by profile")

            async def prompt() -> AsyncIterator[dict[str, Any]]:
                yield {
                    "type": "user",
                    "message": {"role": "user", "content": request.prompt},
                    "parent_tool_use_id": None,
                    "session_id": "",
                }

            env = {"CLAUDE_CONFIG_DIR": str(self.config_dir)}
            if self.base_url and self.base_url.strip():
                env["ANTHROPIC_BASE_URL"] = self.base_url
            options = ClaudeAgentOptions(
                cwd=request.paths.workspace,
                resume=request.source_sdk_session_id,
                fork_session=request.source_sdk_session_id is not None,
                system_prompt=profile["system_prompt"],
                allowed_tools=allowed,
                disallowed_tools=disallowed,
                tools=available,
                permission_mode="default",
                can_use_tool=can_use_tool,
                setting_sources=[],
                mcp_servers={},
                strict_mcp_config=True,
                max_turns=request.limits.max_turns,
                max_budget_usd=request.limits.max_budget_usd,
                include_partial_messages=True,
                env=env,
            )
            async for message in query(prompt=prompt(), options=options):
                if isinstance(message, SystemMessage) and message.subtype == "init":
                    value = message.data.get("session_id")
                    if (
                        not isinstance(value, str)
                        or str(UUID(value)) != value
                        or value == request.source_sdk_session_id
                        or value in self.baseline
                        or candidate is not None
                    ):
                        raise ClaudeAdapterError("invalid candidate session")
                    await on_candidate(value)
                    candidate = value
                    continue
                if candidate is None:
                    raise ClaudeAdapterError("SDK init is required before events")
                if got_result:
                    raise ClaudeAdapterError("unexpected SDK event after result")
                if isinstance(message, StreamEvent):
                    delta = message.event.get("delta", {})
                    if (
                        message.event.get("type") == "content_block_delta"
                        and delta.get("type") == "text_delta"
                    ):
                        yield NormalizedEvent(
                            "assistant.delta", {"text": delta["text"]}
                        )
                elif isinstance(message, AssistantMessage):
                    if message.error is not None:
                        raise ClaudeAdapterError("SDK assistant failed")
                    for block in message.content:
                        if isinstance(block, TextBlock):
                            yield NormalizedEvent(
                                "assistant.message", {"text": block.text}
                            )
                        elif isinstance(block, ToolUseBlock):
                            if block.name not in available or block.id in tool_names:
                                raise ClaudeAdapterError("invalid SDK tool call")
                            tool_names[block.id] = block.name
                            yield NormalizedEvent(
                                "tool.started",
                                {
                                    "tool_call_id": block.id,
                                    "name": block.name,
                                    "input": block.input,
                                },
                            )
                elif isinstance(message, UserMessage) and isinstance(
                    message.content, list
                ):
                    for block in message.content:
                        if isinstance(block, ToolResultBlock):
                            name = tool_names.pop(block.tool_use_id, None)
                            if name is None:
                                raise ClaudeAdapterError("unmatched SDK tool result")
                            output = (
                                block.content if isinstance(block.content, str) else ""
                            )
                            if isinstance(block.content, list):
                                output = "\n".join(
                                    item["text"]
                                    for item in block.content
                                    if item.get("type") == "text"
                                    and isinstance(item.get("text"), str)
                                )
                            payload = {
                                "tool_call_id": block.tool_use_id,
                                "name": name,
                                "outcome": "failed" if block.is_error else "succeeded",
                            }
                            payload["error" if block.is_error else "output"] = (
                                "Tool execution failed" if block.is_error else output
                            )
                            yield NormalizedEvent("tool.completed", payload)
                elif isinstance(message, ResultMessage):
                    if (
                        message.session_id != candidate
                        or message.is_error
                        or message.subtype != "success"
                        or message.errors
                    ):
                        raise ClaudeAdapterError("SDK result failed validation")
                    got_result = True
            if not got_result:
                raise ClaudeAdapterError("SDK stream ended without a successful result")
            # Result is not completion: the runner still owes fsync and parent ACK.
        except ClaudeAdapterError:
            raise
        except Exception:
            raise ClaudeAdapterError("Claude stream failed") from None
