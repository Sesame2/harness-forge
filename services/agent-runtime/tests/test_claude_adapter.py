from __future__ import annotations

import importlib
import importlib.util
import os
from dataclasses import replace
from pathlib import Path
from uuid import uuid4

import pytest
from claude_agent_sdk import (
    AssistantMessage,
    PermissionResultAllow,
    PermissionResultDeny,
    ResultMessage,
    SystemMessage,
    TextBlock,
    ToolResultBlock,
    ToolUseBlock,
    UserMessage,
)
from claude_agent_sdk.types import StreamEvent, ToolPermissionContext

from harness_forge_runtime.models import RunRequest


def adapter_module():
    name = "harness_forge_runtime.claude"
    assert importlib.util.find_spec(name) is not None, (
        "Claude adapter is not implemented"
    )
    return importlib.import_module(name)


def request(source=None):
    return RunRequest.model_validate(
        {
            "version": "1",
            "run_id": str(uuid4()),
            "project_id": str(uuid4()),
            "conversation_id": str(uuid4()),
            "prompt": "Analyze",
            "source_sdk_session_id": source,
            "profile": {
                "id": "geo",
                "version": "1",
                "digest": "sha256:test",
                "config": {
                    "system_prompt": "Exact system prompt",
                    "tools": {
                        "allowed": ["Read", "Bash", "WebFetch"],
                        "disallowed": ["WebFetch"],
                        "permission_mode": "default",
                    },
                    "artifacts": {
                        "manifest_schema_version": 1,
                        "allowed_types": ["html"],
                        "max_file_bytes": 1000,
                        "max_total_bytes": 2000,
                    },
                    "inputs": {"accepted_media_types": ["text/plain"]},
                },
            },
            "paths": {
                "workspace": "/workspaces/test/workspace",
                "inputs": "/workspaces/test/inputs",
                "outputs": "/workspaces/test/outputs",
            },
            "limits": {"max_turns": 8, "max_budget_usd": 2.0},
        }
    )


def result(candidate):
    return ResultMessage(
        subtype="success",
        duration_ms=1,
        duration_api_ms=1,
        is_error=False,
        num_turns=1,
        session_id=candidate,
    )


@pytest.mark.asyncio
@pytest.mark.parametrize("source", [None, str(uuid4())])
async def test_options_permissions_and_candidate_ack_before_public_events(
    monkeypatch, source
):
    module = adapter_module()
    candidate = str(uuid4())
    captured = {}
    acknowledged = []

    async def query(*, prompt, options):
        captured["options"] = options
        captured["prompt"] = [item async for item in prompt]
        yield SystemMessage(subtype="init", data={"session_id": candidate})
        assert acknowledged == [candidate]
        yield StreamEvent(
            uuid="event",
            session_id=candidate,
            event={
                "type": "content_block_delta",
                "delta": {"type": "text_delta", "text": "Hello"},
            },
        )
        yield AssistantMessage(
            content=[
                TextBlock(text="Hello"),
                ToolUseBlock(id="call", name="Read", input={"file_path": "a.txt"}),
            ],
            model="fake",
        )
        yield UserMessage(
            content=[ToolResultBlock(tool_use_id="call", content="contents")]
        )
        yield result(candidate)

    async def ack(value):
        acknowledged.append(value)

    monkeypatch.setattr(module, "query", query)
    adapter = module.ClaudeAdapter(
        claude_config_dir=Path("/sessions/claude"),
        anthropic_base_url="https://example.test/api",
        baseline_session_ids=(),
    )
    events = [event async for event in adapter.stream_turn(request(source), ack)]
    options = captured["options"]
    assert options.resume == source
    assert options.fork_session is (source is not None)
    assert options.continue_conversation is False
    assert options.cwd == "/workspaces/test/workspace"
    assert options.system_prompt == "Exact system prompt"
    assert options.allowed_tools == ["Read", "Bash", "WebFetch"]
    assert options.disallowed_tools == ["WebFetch"]
    assert options.tools == ["Read", "Bash"]
    assert options.permission_mode == "default"
    assert options.setting_sources == []
    assert options.mcp_servers == {} and options.strict_mcp_config is True
    assert options.max_turns == 8 and options.max_budget_usd == 2.0
    assert options.env == {
        "CLAUDE_CONFIG_DIR": "/sessions/claude",
        "ANTHROPIC_BASE_URL": "https://example.test/api",
    }
    assert captured["prompt"][0]["message"]["content"] == "Analyze"
    for tool in ("Read", "Bash", "WebFetch", "Unknown"):
        data = {"path": "file"}
        permission = await options.can_use_tool(tool, data, ToolPermissionContext())
        if tool in {"Read", "Bash"}:
            assert isinstance(permission, PermissionResultAllow)
            assert permission.updated_input is data
        else:
            assert isinstance(permission, PermissionResultDeny)
    assert [event.type for event in events] == [
        "assistant.delta",
        "assistant.message",
        "tool.started",
        "tool.completed",
    ]
    assert events[-1].payload == {
        "tool_call_id": "call",
        "name": "Read",
        "outcome": "succeeded",
        "output": "contents",
    }


@pytest.mark.asyncio
@pytest.mark.parametrize(
    "failure",
    [
        "missing_init",
        "invalid_id",
        "source",
        "baseline",
        "result_mismatch",
        "assistant_error",
        "result_error",
        "result_subtype",
        "sdk_exception",
        "missing_result",
        "tail_exception",
        "duplicate_init",
        "callback_failure",
    ],
)
async def test_invalid_sdk_streams_fail_without_completed(monkeypatch, failure):
    module = adapter_module()
    candidate, source, baseline = str(uuid4()), str(uuid4()), str(uuid4())
    init_id = {"invalid_id": "../escape", "source": source, "baseline": baseline}.get(
        failure, candidate
    )
    events, acknowledged = [], []

    async def query(*, prompt, options):
        if failure == "sdk_exception":
            raise RuntimeError("secret token")
        if failure != "missing_init":
            yield SystemMessage(subtype="init", data={"session_id": init_id})
        if failure == "duplicate_init":
            yield SystemMessage(subtype="init", data={"session_id": str(uuid4())})
        if failure == "assistant_error":
            yield AssistantMessage(content=[], model="fake", error="server_error")
        yield AssistantMessage(content=[TextBlock(text="text")], model="fake")
        if failure != "missing_result":
            terminal = result(
                candidate if failure != "result_mismatch" else str(uuid4())
            )
            if failure == "result_error":
                terminal = replace(terminal, is_error=True)
            if failure == "result_subtype":
                terminal = replace(terminal, subtype="error_max_turns")
            yield terminal
        if failure == "tail_exception":
            raise RuntimeError("secret token")

    async def ack(value):
        if failure == "callback_failure":
            raise OSError("private path")
        acknowledged.append(value)

    monkeypatch.setattr(module, "query", query)
    adapter = module.ClaudeAdapter(
        claude_config_dir=Path("/sessions/claude"), baseline_session_ids=(baseline,)
    )
    with pytest.raises(module.ClaudeAdapterError) as caught:
        async for event in adapter.stream_turn(request(source), ack):
            events.append(event)
    assert "secret token" not in str(caught.value)
    assert not any(event.type == "agent.completed" for event in events)
    if failure in {
        "missing_init",
        "invalid_id",
        "source",
        "baseline",
        "callback_failure",
    }:
        assert events == []


@pytest.mark.asyncio
@pytest.mark.parametrize("source", ["/tmp/source.jsonl", "../source", "not-a-uuid"])
async def test_invalid_source_never_reaches_sdk(monkeypatch, source):
    module = adapter_module()
    called = False

    async def query(**kwargs):
        nonlocal called
        called = True
        yield result(str(uuid4()))

    async def ack(value):
        pass

    monkeypatch.setattr(module, "query", query)
    adapter = module.ClaudeAdapter(
        claude_config_dir=Path("/sessions/claude"), baseline_session_ids=()
    )
    with pytest.raises(module.ClaudeAdapterError):
        _ = [event async for event in adapter.stream_turn(request(source), ack)]
    assert not called


@pytest.mark.asyncio
async def test_empty_base_url_is_unset(monkeypatch):
    module = adapter_module()
    candidate = str(uuid4())

    async def query(*, prompt, options):
        assert "ANTHROPIC_BASE_URL" not in options.env
        yield SystemMessage(subtype="init", data={"session_id": candidate})
        yield result(candidate)

    async def ack(value):
        pass

    monkeypatch.setattr(module, "query", query)
    adapter = module.ClaudeAdapter(
        claude_config_dir=Path("/sessions/claude"),
        baseline_session_ids=(),
        anthropic_base_url="",
    )
    assert [event async for event in adapter.stream_turn(request(), ack)] == []


@pytest.mark.asyncio
async def test_mock_restart_resume_reads_fsynced_opaque_transcript(
    tmp_path, monkeypatch
):
    from harness_forge_runtime.execution_store import ExecutionStore
    from test_sessions import sessions

    module = adapter_module()
    root = tmp_path / "claude"
    state_root = tmp_path / "executions"
    source = None
    previous_bytes = None
    synced = set()
    real_fsync = os.fsync

    def fsync(fd):
        synced.add(os.fstat(fd).st_ino)
        real_fsync(fd)

    monkeypatch.setattr(os, "fsync", fsync)
    for _ in range(2):
        # A fresh parent/store pair, not a claim about the real bundled CLI.
        execution_store = ExecutionStore(state_root)
        await execution_store.initialize()
        sdk = sessions(root)
        turn = request(source)
        turn.paths.workspace = f"/workspaces/{turn.run_id}/workspace"
        baseline = tuple(sorted(sdk.list_ids()))
        await execution_store.reserve(turn.run_id, source, list(baseline))
        bucket = sdk.prepare_run(turn.run_id, Path(turn.paths.workspace), source)
        await execution_store.mark_running(turn.run_id, 10, 10)
        candidate = str(uuid4())
        main = bucket / f"{candidate}.jsonl"

        async def query(*, prompt, options):
            assert options.resume == source
            assert options.fork_session is (source is not None)
            if source:
                staged = bucket / f"{source}.jsonl"
                assert staged.read_bytes() == previous_bytes
                assert staged.stat().st_ino in synced
            yield SystemMessage(subtype="init", data={"session_id": candidate})
            assert (
                await execution_store.get(turn.run_id)
            ).candidate_sdk_session_id == candidate
            main.write_bytes(b"opaque transcript\x00\xff\n" + candidate.encode())
            yield AssistantMessage(content=[TextBlock(text="done")], model="fake")
            yield result(candidate)

        async def ack(value):
            await execution_store.record_candidate(turn.run_id, value)

        monkeypatch.setattr(module, "query", query)
        adapter = module.ClaudeAdapter(
            claude_config_dir=root, baseline_session_ids=baseline
        )
        events = [event async for event in adapter.stream_turn(turn, ack)]
        assert all(event.type != "agent.completed" for event in events)
        sdk.sync_transcript(candidate)
        assert main.stat().st_ino in synced
        assert sdk.validate_candidate(turn.run_id, candidate, source, baseline)
        await execution_store.mark_candidate_durable(turn.run_id)
        assert (await execution_store.get(turn.run_id)).candidate_durable_at is not None
        await execution_store.mark_awaiting_finalize(turn.run_id)
        await execution_store.finalize(turn.run_id, "commit")
        await execution_store.delete(turn.run_id)
        source, previous_bytes = candidate, main.read_bytes()
