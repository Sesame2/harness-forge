from __future__ import annotations

import importlib
import importlib.util
import io
import json
from pathlib import Path
from uuid import uuid4

import pytest
from claude_agent_sdk import AssistantMessage, SystemMessage, TextBlock

from harness_forge_runtime.models import RunPaths, RuntimeEvent
from harness_forge_runtime.sessions import SessionStore
from harness_forge_runtime.settings import RuntimeSettings
from test_claude_adapter import request, result
from test_backend import configured


def runner_module():
    name = "harness_forge_runtime.runner"
    assert importlib.util.find_spec(name) is not None, (
        "worker runner is not implemented"
    )
    return importlib.import_module(name)


def fixture_run(tmp_path: Path, source=None):
    turn = request(source)
    root = tmp_path / "workspaces"
    paths = {
        name: root / str(turn.run_id) / name
        for name in ("inputs", "workspace", "outputs")
    }
    for path in paths.values():
        path.mkdir(parents=True)
    paths["inputs"].chmod(0o555)
    turn.paths = RunPaths(**{name: str(path) for name, path in paths.items()})
    settings = RuntimeSettings(
        run_workspace_root=root,
        session_mount_root=tmp_path / "sessions",
        runtime_state_root=tmp_path / "sessions" / "executions",
        claude_config_dir=tmp_path / "sessions" / "claude",
    )
    return turn, settings


@pytest.mark.asyncio
@pytest.mark.parametrize(
    "failure",
    [
        None,
        "sdk",
        "tail",
        "sync",
        "created_ack",
        "durable_ack",
        "manifest",
        "workspace",
    ],
)
async def test_worker_ack_order_and_one_safe_terminal(tmp_path, monkeypatch, failure):
    module = runner_module()
    import harness_forge_runtime.claude as claude

    turn, settings = fixture_run(tmp_path)
    sessions = SessionStore(settings.claude_config_dir)
    bucket = sessions.prepare_run(turn.run_id, Path(turn.paths.workspace), None)
    candidate = str(uuid4())
    stdout, stderr = io.StringIO(), io.StringIO()
    acknowledged = []
    closed = False
    sync = sessions.sync_transcript

    def sync_transcript(value):
        assert closed
        assert "artifact.candidate" not in stdout.getvalue()
        if failure == "sync":
            raise OSError("secret token in private path")
        sync(value)

    async def control(kind, value):
        assert value == candidate
        if kind == "candidate.created":
            assert stdout.getvalue() == ""
        else:
            assert kind == "candidate.durable" and closed
            assert "artifact.candidate" not in stdout.getvalue()
        if failure == ("created_ack" if kind == "candidate.created" else "durable_ack"):
            raise OSError("secret token in private path")
        acknowledged.append(kind)

    async def query(*, prompt, options):
        nonlocal closed
        assert options.cwd == turn.paths.workspace
        assert options.system_prompt == turn.profile.config["system_prompt"]
        if failure == "sdk":
            raise RuntimeError("secret token in private path")
        yield SystemMessage(subtype="init", data={"session_id": candidate})
        assert acknowledged == ["candidate.created"]
        (bucket / f"{candidate}.jsonl").write_bytes(b"opaque transcript\n")
        print("SDK diagnostic", file=__import__("sys").stdout)
        yield AssistantMessage(content=[TextBlock(text="Hello\nworld")], model="fake")
        yield result(candidate)
        if failure == "tail":
            raise RuntimeError("secret token in private path")
        closed = True

    monkeypatch.setattr(claude, "query", query)
    monkeypatch.setattr(sessions, "sync_transcript", sync_transcript)
    if failure == "manifest":
        (Path(turn.paths.outputs) / "unlisted.txt").write_text("missing manifest")
    if failure == "workspace":
        Path(turn.paths.inputs).chmod(0o755)

    code = await module.run_worker(
        turn, settings, (), control, stdout=stdout, stderr=stderr, sessions=sessions
    )
    events = [
        RuntimeEvent.model_validate_json(line)
        for line in stdout.getvalue().splitlines()
    ]
    assert [event.sequence for event in events] == list(range(1, len(events) + 1))
    assert all(event.run_id == turn.run_id for event in events)
    terminals = [event for event in events if event.type.startswith("agent.")]
    assert len(terminals) == 1
    assert "secret token" not in stdout.getvalue() + stderr.getvalue()
    assert "SDK diagnostic" not in stdout.getvalue()
    if failure is None:
        assert code == 0
        assert acknowledged == ["candidate.created", "candidate.durable"]
        assert [event.type for event in events] == [
            "assistant.message",
            "artifact.candidate",
            "agent.completed",
        ]
        assert "SDK diagnostic" in stderr.getvalue()
    else:
        assert code == 1
        assert terminals[0].type == "agent.failed"
        assert all(event.type != "artifact.candidate" for event in events)


@pytest.mark.asyncio
async def test_worker_manifest_and_fork_use_real_normalization(tmp_path, monkeypatch):
    module = runner_module()
    import harness_forge_runtime.claude as claude
    from claude_agent_sdk import ToolResultBlock, ToolUseBlock, UserMessage

    source, candidate = str(uuid4()), str(uuid4())
    turn, settings = fixture_run(tmp_path, source)
    sessions = SessionStore(settings.claude_config_dir)
    old = sessions.projects / "-old"
    old.mkdir(parents=True)
    (old / f"{source}.jsonl").write_bytes(b"source unchanged")
    bucket = sessions.prepare_run(turn.run_id, Path(turn.paths.workspace), source)
    artifact = {
        "name": "map",
        "title": "Map",
        "type": "html",
        "entry": "map.html",
        "primary": True,
    }
    outputs = Path(turn.paths.outputs)
    (outputs / "map.html").write_text("<h1>Map</h1>")
    (outputs / "artifact-manifest.json").write_text(
        json.dumps({"schema_version": 1, "artifacts": [artifact]})
    )

    async def query(*, prompt, options):
        assert options.resume == source and options.fork_session
        assert (bucket / f"{source}.jsonl").read_bytes() == b"source unchanged"
        yield SystemMessage(subtype="init", data={"session_id": candidate})
        yield AssistantMessage(
            content=[
                ToolUseBlock(id="read", name="Read", input={"file_path": "input.txt"})
            ],
            model="fake",
        )
        yield UserMessage(
            content=[ToolResultBlock(tool_use_id="read", content="contents")]
        )
        (bucket / f"{candidate}.jsonl").write_bytes(b"fork")
        yield result(candidate)

    async def control(kind, value):
        pass

    monkeypatch.setattr(claude, "query", query)
    stdout = io.StringIO()
    assert (
        await module.run_worker(
            turn, settings, (source,), control, stdout=stdout, stderr=io.StringIO()
        )
        == 0
    )
    events = [json.loads(line) for line in stdout.getvalue().splitlines()]
    assert [event["type"] for event in events] == [
        "tool.started",
        "tool.completed",
        "artifact.candidate",
        "agent.completed",
    ]
    assert events[-1]["payload"] == {
        "candidate_sdk_session_id": candidate,
        "artifacts": [artifact],
    }
    assert (old / f"{source}.jsonl").read_bytes() == b"source unchanged"


@pytest.mark.asyncio
@pytest.mark.parametrize("mode", ["openai-chat", "openai-responses"])
async def test_gateway_worker_uses_effective_sessions_and_explicit_sdk_options(
    tmp_path, monkeypatch, mode
):
    import harness_forge_runtime.claude as claude

    turn, _ = fixture_run(tmp_path)
    settings = configured(tmp_path, mode)
    sdk = SessionStore(settings.effective_claude_config_dir)
    bucket = sdk.prepare_run(turn.run_id, Path(turn.paths.workspace), None)
    candidate = str(uuid4())
    acknowledged = []
    captured = []

    async def query(*, prompt, options):
        captured.append(options)
        assert options.env["CLAUDE_CONFIG_DIR"] == str(
            settings.effective_claude_config_dir
        )
        assert options.env["ANTHROPIC_API_KEY"] == "fixture-gateway-key"
        assert options.env["ANTHROPIC_BASE_URL"] == "http://model-gateway:4000"
        assert options.model == "harness-openai"
        assert options.thinking == {"type": "disabled"}
        assert options.env["DISABLE_PROMPT_CACHING"] == "1"
        assert options.env["CLAUDE_CODE_EFFORT_LEVEL"] == "unset"
        assert options.env["DISABLE_INTERLEAVED_THINKING"] == "1"
        assert options.env["CLAUDE_CODE_MAX_OUTPUT_TOKENS"] == "8192"
        assert options.env["CLAUDE_CODE_MAX_RETRIES"] == "0"
        for name in (
            "NONSTREAMING_FALLBACK",
            "NONESSENTIAL_TRAFFIC",
            "EXPERIMENTAL_BETAS",
        ):
            assert options.env[f"CLAUDE_CODE_DISABLE_{name}"] == "1"
        for name in ("HAIKU", "SONNET", "OPUS"):
            assert options.env[f"ANTHROPIC_DEFAULT_{name}_MODEL"] == "harness-openai"
        assert "ANTHROPIC_DEFAULT_FABLE_MODEL" not in options.env
        assert not any(key.startswith("OPENAI_") for key in options.env)
        assert options.permission_mode == "default" and options.setting_sources == []
        assert options.max_turns == 8 and options.max_budget_usd == 2.0
        yield SystemMessage(subtype="init", data={"session_id": candidate})
        (bucket / f"{candidate}.jsonl").write_text("opaque")
        yield result(candidate)

    async def control(kind, value):
        acknowledged.append(kind)

    monkeypatch.setattr(claude, "query", query)
    output = io.StringIO()
    assert (
        await runner_module().run_worker(
            turn, settings, (), control, stdout=output, stderr=io.StringIO()
        )
        == 0
    )
    assert captured and acknowledged == ["candidate.created", "candidate.durable"]
    assert '"type":"agent.completed"' in output.getvalue()
