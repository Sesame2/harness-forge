from __future__ import annotations

import asyncio
import importlib
import importlib.util
import json
import os
import signal
import stat
import subprocess
import sys
import threading
import time
from uuid import uuid4

import pytest

from harness_forge_runtime.execution_store import ExecutionStore
from harness_forge_runtime.errors import InvalidExecutionState
from harness_forge_runtime.sessions import SessionStore
from test_runner import fixture_run


def processes_module():
    name = "harness_forge_runtime.processes"
    assert importlib.util.find_spec(name) is not None, (
        "process manager is not implemented"
    )
    return importlib.import_module(name)


# Synthetic subprocess: never invokes the real SDK/CLI, and uses only fixture paths.
FIXTURE = r"""
import json, os, pathlib, re, signal, sys, time
request = json.loads(pathlib.Path(sys.argv[1]).read_text())
barrier = int(os.environ["HARNESS_START_FD"])
if os.read(barrier, 1) != b"1":
    sys.exit(0)
os.close(barrier)
mode = sys.argv[2]
if mode == "hang":
    signal.signal(signal.SIGTERM, signal.SIG_IGN)
    child = os.fork()
    if child == 0:
        while True: time.sleep(1)
    print("READY " + str(child), file=sys.stderr, flush=True)
    while True: time.sleep(1)
if mode == "orphan":
    child = os.fork()
    if child == 0:
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        while True: time.sleep(1)
    sys.exit(0)
run = request["run_id"]
candidate = sys.argv[3]
control = os.fdopen(int(os.environ["HARNESS_CONTROL_FD"]), "w")
ack = os.fdopen(int(os.environ["HARNESS_ACK_FD"]), "r")
def exchange(kind):
    control.write(json.dumps({"type": kind, "candidate_sdk_session_id": candidate}) + "\n")
    control.flush()
    if ack.readline() != "ack\n": sys.exit(2)
def emit(seq, kind, payload):
    print(json.dumps({"version":"1", "run_id":run, "sequence":seq, "type":kind,
       "occurred_at":"2026-09-14T01:02:03Z", "payload":payload}), flush=True)
if mode == "invalid":
    print("not-json secret token", flush=True)
    print("ANTHROPIC_API_KEY=sk-ant-super-secret", file=sys.stderr, flush=True)
    sys.exit(3)
if mode == "missing": sys.exit(0)
if mode == "control_bad_id": candidate = "not-a-uuid"
if mode == "control_unknown": exchange("candidate.unknown")
if mode == "early_event": emit(1, "assistant.message", {"text":"before ACK"})
if mode == "oversized": print("x" * (4 * 1024 * 1024 + 1), flush=True)
exchange("candidate.created")
bucket = pathlib.Path(os.environ["CLAUDE_CONFIG_DIR"]) / "projects" / re.sub(r"[^A-Za-z0-9]", "-", request["paths"]["workspace"])
if mode != "durable_missing": (bucket / (candidate + ".jsonl")).write_text("synthetic transcript")
if mode == "early_artifact": emit(1, "artifact.candidate", {"artifacts":[]})
if mode == "durable_mismatch": candidate = "f446076d-2b72-43d8-8bcb-d150498e507d"
exchange("candidate.durable")
if mode == "wrong_run": run = "f446076d-2b72-43d8-8bcb-d150498e507d"
if mode == "missing_artifact": emit(1, "agent.completed", {"candidate_sdk_session_id":candidate, "artifacts":[]})
emit(1, "assistant.message", {"text": "hello"})
if mode == "bad_sequence": emit(1, "assistant.message", {"text":"duplicate sequence"})
if mode == "redact":
    emit(2, "tool.started", {"tool_call_id":"read", "name":"Read", "input":{"nested":{"text":"TOKEN=fake-value", "value":os.environ["ANTHROPIC_API_KEY"], "token":"fixture-sensitive-token", "access_token":"fixture-sensitive-access"}}})
    print("Authorization: Bearer fake-bearer", file=sys.stderr, flush=True)
    print("credential " + os.environ["ANTHROPIC_API_KEY"], file=sys.stderr, flush=True)
    print(json.dumps({"password":"fixture-sensitive-password"}), file=sys.stderr, flush=True)
    print("x" * 70000, file=sys.stderr, flush=True)
    emit(3, "artifact.candidate", {"artifacts": []})
    emit(4, "agent.completed", {"candidate_sdk_session_id":candidate, "artifacts":[]})
    sys.exit(0)
if mode == "delay": time.sleep(0.2)
emit(2, "artifact.candidate", {"artifacts": []})
emit(3, "agent.completed", {"candidate_sdk_session_id":candidate, "artifacts":[]})
if mode == "trailer": print("invalid trailer", flush=True)
if mode == "duplicate": emit(4, "agent.completed", {"candidate_sdk_session_id":candidate, "artifacts":[]})
if mode == "nonzero": sys.exit(3)
"""


def fixture_popen(monkeypatch, module, mode="success", *, entered=None, release=None):
    real = subprocess.Popen
    children = []
    candidate = str(uuid4())

    def popen(command, **kwargs):
        child = real(
            [sys.executable, "-c", FIXTURE, command[-1], mode, candidate], **kwargs
        )
        children.append(child)
        if entered is not None:
            entered.set()
            assert release.wait(5)
        return child

    monkeypatch.setattr(module.subprocess, "Popen", popen)
    return children, candidate


async def manager_fixture(tmp_path):
    module = processes_module()
    turn, settings = fixture_run(tmp_path)
    store = ExecutionStore(settings.runtime_state_root)
    await store.initialize()
    sessions = SessionStore(settings.claude_config_dir)
    manager = module.ProcessManager(store, sessions, settings)
    return module, turn, store, manager


@pytest.mark.asyncio
@pytest.mark.parametrize(
    "mode",
    [
        "success",
        "invalid",
        "missing",
        "trailer",
        "duplicate",
        "nonzero",
        "control_bad_id",
        "control_unknown",
        "early_event",
        "oversized",
        "durable_missing",
        "early_artifact",
        "durable_mismatch",
        "wrong_run",
        "missing_artifact",
        "bad_sequence",
    ],
)
async def test_parent_drains_persists_and_exposes_unique_validated_terminal(
    tmp_path, monkeypatch, mode
):
    module, turn, store, manager = await manager_fixture(tmp_path)
    children, candidate = fixture_popen(monkeypatch, module, mode)
    handle = await manager.start(turn)
    try:
        lines = [line async for line in manager.stream(handle)]
        await handle.task
        events = [json.loads(line) for line in lines]
        assert [event["sequence"] for event in events] == list(
            range(1, len(events) + 1)
        )
        terminal = [event for event in events if event["type"].startswith("agent.")]
        assert len(terminal) == 1
        assert terminal[0]["type"] == (
            "agent.completed" if mode == "success" else "agent.failed"
        )
        assert handle.log_path.read_text() == "".join(lines)
        record = await store.get(turn.run_id)
        assert record.lifecycle == "awaiting_finalize"
        assert children[0].poll() is not None
        assert not module.group_active(children[0].pid)
        if mode == "success":
            assert record.candidate_sdk_session_id == candidate
            assert record.candidate_durable_at is not None
        persisted = "".join(path.read_text() for path in store.root.rglob("*.log"))
        assert "sk-ant-super-secret" not in persisted
        assert "ANTHROPIC_API_KEY" not in persisted
        assert str(turn.run_id) in handle.stderr_path.read_text()
    finally:
        await manager.close()


@pytest.mark.asyncio
async def test_cancel_before_spawn_never_creates_child(tmp_path, monkeypatch):
    module, turn, store, manager = await manager_fixture(tmp_path)
    children, _ = fixture_popen(monkeypatch, module)
    handle = await manager.start(turn)
    await manager.cancel(turn.run_id)
    await handle.task
    assert children == []
    assert (await store.get(turn.run_id)).lifecycle == "awaiting_finalize"


@pytest.mark.asyncio
async def test_cancel_during_popen_closes_barrier_without_releasing_child(
    tmp_path, monkeypatch
):
    module, turn, store, manager = await manager_fixture(tmp_path)
    entered, release = threading.Event(), threading.Event()
    children, _ = fixture_popen(monkeypatch, module, entered=entered, release=release)
    handle = await manager.start(turn)
    try:
        assert await asyncio.to_thread(entered.wait, 5)
        cancel = asyncio.create_task(manager.cancel(turn.run_id))
        await asyncio.sleep(0)
        release.set()
        await asyncio.wait_for(cancel, 5)
        await handle.task
        record = await store.get(turn.run_id)
        assert record.lifecycle == "awaiting_finalize"
        assert record.candidate_sdk_session_id is None
        assert children[0].poll() is not None
        assert not module.group_active(children[0].pid)
    finally:
        release.set()
        await manager.close()


@pytest.mark.asyncio
async def test_running_cancel_kills_ignoring_term_entire_group_and_allows_abort(
    tmp_path, monkeypatch
):
    module, turn, store, manager = await manager_fixture(tmp_path)
    children, _ = fixture_popen(monkeypatch, module, "hang")
    handle = await manager.start(turn)
    try:
        for _ in range(200):
            if (
                handle.stderr_path.exists()
                and "READY" in handle.stderr_path.read_text()
            ):
                break
            await asyncio.sleep(0.01)
        else:
            pytest.fail("synthetic grandchild did not start")
        await asyncio.wait_for(manager.cancel(turn.run_id), 6)
        await manager.cancel(turn.run_id)
        assert not module.group_active(children[0].pid)
        assert (await store.get(turn.run_id)).lifecycle == "awaiting_finalize"
        await store.finalize(turn.run_id, "abort")
    finally:
        await manager.close()


@pytest.mark.asyncio
async def test_disconnect_does_not_cancel_parent_drain(tmp_path, monkeypatch):
    module, turn, store, manager = await manager_fixture(tmp_path)
    fixture_popen(monkeypatch, module, "delay")
    handle = await manager.start(turn)
    stream = manager.stream(handle)
    assert json.loads(await anext(stream))["type"] == "assistant.message"
    await stream.aclose()
    await asyncio.wait_for(handle.task, 5)
    assert (await store.get(turn.run_id)).lifecycle == "awaiting_finalize"
    assert (
        json.loads(handle.log_path.read_text().splitlines()[-1])["type"]
        == "agent.completed"
    )


@pytest.mark.asyncio
async def test_recovery_of_reserved_no_pid_and_owned_running_group(tmp_path):
    module, turn, store, manager = await manager_fixture(tmp_path)
    await store.reserve(turn.run_id, None, [])
    recovered = ExecutionStore(store.root)
    await recovered.initialize()
    manager = module.ProcessManager(recovered, manager.sessions, manager.settings)
    await manager.recover()
    store = recovered
    assert (await store.get(turn.run_id)).lifecycle == "awaiting_finalize"
    await store.finalize(turn.run_id, "abort")
    other = uuid4()
    child = subprocess.Popen(
        [sys.executable, "-c", "import time; time.sleep(30)"], start_new_session=True
    )
    try:
        await store.reserve(other, None, [])
        await store.mark_running(other, child.pid, child.pid)
        recovered = ExecutionStore(store.root)
        await recovered.initialize()
        manager = module.ProcessManager(recovered, manager.sessions, manager.settings)
        await manager.recover()
        store = recovered
        child.wait(timeout=3)
        assert not module.group_active(child.pid)
        assert (await store.get(other)).lifecycle == "awaiting_finalize"
    finally:
        if child.poll() is None:
            os.killpg(child.pid, signal.SIGKILL)
            child.wait(timeout=3)


@pytest.mark.asyncio
async def test_redaction_preserves_nested_json_and_bounds_stderr(tmp_path, monkeypatch):
    module, turn, store, manager = await manager_fixture(tmp_path)
    # Explicit fake value, never a developer credential.
    monkeypatch.setenv("ANTHROPIC_API_KEY", "fixture-credential-12345")
    fixture_popen(monkeypatch, module, "redact")
    handle = await manager.start(turn)
    lines = [line async for line in manager.stream(handle)]
    events = [json.loads(line) for line in lines]
    assert events[-1]["type"] == "agent.completed"
    tool = next(event for event in events if event["type"] == "tool.started")
    assert tool["payload"]["input"]["nested"]["text"] == "[redacted]"
    assert tool["payload"]["input"]["nested"]["token"] == "[redacted]"
    assert tool["payload"]["input"]["nested"]["access_token"] == "[redacted]"
    persisted = handle.log_path.read_text() + handle.stderr_path.read_text()
    assert "fixture-sensitive-" not in persisted
    assert all(
        value not in persisted
        for value in ("fake-value", "fake-bearer", "fixture-credential-12345")
    )
    assert handle.stderr_path.stat().st_size <= module.MAX_STDERR_BYTES + 100


@pytest.mark.asyncio
async def test_exited_leader_cannot_leave_live_descendant_and_open_pipes(
    tmp_path, monkeypatch
):
    module, turn, store, manager = await manager_fixture(tmp_path)
    children, _ = fixture_popen(monkeypatch, module, "orphan")
    handle = await manager.start(turn)
    try:
        await asyncio.wait_for(asyncio.shield(handle.task), 5)
        assert not module.group_active(children[0].pid)
        assert (await store.get(turn.run_id)).lifecycle == "awaiting_finalize"
    finally:
        await manager.close()


@pytest.mark.asyncio
async def test_terminal_cancel_never_signals_stale_pgid(tmp_path, monkeypatch):
    module, turn, store, manager = await manager_fixture(tmp_path)
    fixture_popen(monkeypatch, module)
    handle = await manager.start(turn)
    await handle.task

    def forbidden(*args):
        pytest.fail("terminal cancellation must not inspect or signal stale PGID")

    monkeypatch.setattr(module, "group_active", forbidden)
    await manager.cancel(turn.run_id)
    await store.finalize(turn.run_id, "commit")
    await manager.cancel(turn.run_id)
    await manager.close()


@pytest.mark.asyncio
async def test_cancel_rechecks_lifecycle_after_waiting_for_spawn_lock(
    tmp_path, monkeypatch
):
    module, turn, store, manager = await manager_fixture(tmp_path)
    fixture_popen(monkeypatch, module)
    handle = await manager.start(turn)
    await handle.task
    original = store.get
    record = await original(turn.run_id)
    snapshots = [record.model_copy(update={"lifecycle": "running"})]

    async def get(run_id):
        # Snapshot taken before cancellation waited on the lock; run stopped meanwhile.
        return snapshots.pop() if snapshots else await original(run_id)

    async def forbidden(*args):
        pytest.fail("cancel must recheck stopped state inside serialized lock")

    monkeypatch.setattr(store, "get", get)
    monkeypatch.setattr(module, "stop_group", forbidden)
    await manager.cancel(turn.run_id)


@pytest.mark.parametrize(
    "key", ["token", "access_token", "refresh-token", "authToken", "PASSWORD"]
)
def test_sensitive_dictionary_keys_are_redacted_recursively(key):
    module = processes_module()
    result = module.redact_payload(
        {"input": [{key: "fixture-sensitive-value", "safe": "kept"}]}
    )
    assert result == {"input": [{key: "[redacted]", "safe": "kept"}]}


@pytest.mark.parametrize(
    "diagnostic",
    [
        '{"password": "fixture-sensitive-value"}',
        '{"nested": {"access_token": "fixture-sensitive-value"}, "safe": "kept"}',
        'SDK diagnostic: {"password": "fixture-sensitive-value with spaces"}',
        "SDK diagnostic: {'token': 'fixture-sensitive-value with spaces'}",
        'SDK diagnostic: {"token": "fixture-sensitive-value\\" escaped"}',
    ],
)
def test_quoted_diagnostic_secrets_are_redacted(diagnostic):
    result = processes_module().redact(diagnostic)
    assert "fixture-sensitive-value" not in result
    assert "with spaces" not in result and "escaped" not in result
    if diagnostic.startswith("{"):
        json.loads(result)


def test_orphan_before_pid_record_exits_on_barrier_eof(tmp_path):
    # The real runner may be started here, but cannot cross its closed barrier or query SDK.
    processes_module()
    read_fd, write_fd = os.pipe()
    env = {"PATH": os.environ["PATH"], "HARNESS_START_FD": str(read_fd)}
    child = subprocess.Popen(
        [
            sys.executable,
            "-m",
            "harness_forge_runtime.runner",
            str(tmp_path / "not-read.json"),
        ],
        env=env,
        pass_fds=(read_fd,),
        start_new_session=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    os.close(read_fd)
    os.close(write_fd)
    try:
        stdout, stderr = child.communicate(timeout=5)
        assert child.returncode == 0
        assert stdout == stderr == b""
    finally:
        if child.poll() is None:
            os.killpg(child.pid, signal.SIGKILL)
            child.wait(timeout=3)


def test_actual_parent_crash_does_not_leave_barrier_writer_in_child(tmp_path):
    module = processes_module()
    parent_code = r"""
import os, subprocess, sys
read_fd, write_fd = os.pipe()
child = subprocess.Popen([sys.executable, "-m", "harness_forge_runtime.runner", sys.argv[1]],
    env={"PATH": os.environ["PATH"], "HARNESS_START_FD": str(read_fd)},
    pass_fds=(read_fd,), start_new_session=True)
print(child.pid, flush=True)
os._exit(0)
"""
    parent = subprocess.Popen(
        [sys.executable, "-c", parent_code, str(tmp_path / "not-read.json")],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    child_pid = None
    try:
        assert parent.stdout is not None
        child_pid = int(parent.stdout.readline())
        stdout, stderr = parent.communicate(timeout=5)
        assert parent.returncode == 0 and stdout == stderr == b""
        deadline = time.monotonic() + 3
        while module.group_active(child_pid) and time.monotonic() < deadline:
            time.sleep(0.02)
        assert not module.group_active(child_pid)
    finally:
        if parent.poll() is None:
            parent.kill()
            parent.wait(timeout=3)
        if child_pid is not None and module.group_active(child_pid):
            os.killpg(child_pid, signal.SIGKILL)


@pytest.mark.asyncio
async def test_pid_record_is_persisted_before_barrier_release(tmp_path, monkeypatch):
    module, turn, store, manager = await manager_fixture(tmp_path)
    children, _ = fixture_popen(monkeypatch, module)
    original = store.mark_running
    observed = []

    async def mark_running(run_id, pid, pgid):
        assert children[0].poll() is None
        assert not list(manager.sessions.projects.glob("*/*.jsonl"))
        before = json.loads((store.root / f"{run_id}.json").read_text())
        assert before["lifecycle"] == "starting" and before["worker_pgid"] is None
        result = await original(run_id, pid, pgid)
        persisted = json.loads((store.root / f"{run_id}.json").read_text())
        assert persisted["worker_pid"] == persisted["worker_pgid"] == pid
        observed.append(pid)
        return result

    monkeypatch.setattr(store, "mark_running", mark_running)
    handle = await manager.start(turn)
    await handle.task
    assert observed == [children[0].pid]
    assert (await store.get(turn.run_id)).candidate_durable_at is not None


@pytest.mark.asyncio
async def test_record_fsync_failure_never_releases_worker_and_recovers_fail_closed(
    tmp_path, monkeypatch
):
    module, turn, store, manager = await manager_fixture(tmp_path)
    children, _ = fixture_popen(monkeypatch, module)
    original = store._fsync_root
    calls = 0

    def fsync():
        nonlocal calls
        calls += 1
        if calls == 2:
            raise OSError("fixture private failure")
        original()

    monkeypatch.setattr(store, "_fsync_root", fsync)
    handle = await manager.start(turn)
    with pytest.raises(InvalidExecutionState):
        await handle.task
    assert len(children) == 1 and children[0].poll() is not None
    assert children[0].stdout.closed and children[0].stderr.closed
    assert not module.group_active(children[0].pid)
    assert not list(manager.sessions.projects.glob("*/*.jsonl"))
    with pytest.raises(InvalidExecutionState):
        await store.list_unfinalized()
    recovered = ExecutionStore(store.root)
    await recovered.initialize()
    fresh = module.ProcessManager(recovered, manager.sessions, manager.settings)
    await fresh.recover()
    assert (await recovered.get(turn.run_id)).lifecycle == "awaiting_finalize"


@pytest.mark.asyncio
async def test_recovery_cannot_be_ready_if_process_group_stop_is_unconfirmed(
    tmp_path, monkeypatch
):
    module, turn, store, manager = await manager_fixture(tmp_path)
    await store.reserve(turn.run_id, None, [])
    await store.mark_running(turn.run_id, 99999999, 99999999)

    async def unconfirmed(*args):
        raise InvalidExecutionState("still active")

    monkeypatch.setattr(module, "stop_group", unconfirmed)
    with pytest.raises(InvalidExecutionState):
        await manager.recover()
    assert (await store.get(turn.run_id)).lifecycle == "running"
    for unsafe in (0, 1, os.getpgrp()):
        with pytest.raises(InvalidExecutionState):
            module.group_active(unsafe)


@pytest.mark.asyncio
async def test_event_journal_failure_does_not_block_abort_of_stopped_worker(
    tmp_path, monkeypatch
):
    module, turn, store, manager = await manager_fixture(tmp_path)
    children, _ = fixture_popen(monkeypatch, module)

    def unavailable_log(*args):
        raise OSError("event journal unavailable")

    monkeypatch.setattr(manager, "_publish", unavailable_log)
    handle = await manager.start(turn)
    with pytest.raises(OSError):
        await handle.task
    assert not module.group_active(children[0].pid)
    assert (await store.get(turn.run_id)).lifecycle == "awaiting_finalize"
    await manager.cancel(turn.run_id)
    await store.finalize(turn.run_id, "abort")


@pytest.mark.asyncio
async def test_failed_ack_flush_and_close_still_closes_all_streams_and_finishes(
    tmp_path, monkeypatch
):
    module, turn, store, manager = await manager_fixture(tmp_path)
    fixture_popen(monkeypatch, module)
    original_popen = module.subprocess.Popen
    original_fdopen = os.fdopen
    streams = []
    failed_flush = []

    class FailingClose:
        def __init__(self, stream, *, ack=False):
            self.stream, self.ack = stream, ack
            streams.append(self)

        def __getattr__(self, name):
            return getattr(self.stream, name)

        def flush(self):
            if self.ack:
                failed_flush.append(True)
                raise BrokenPipeError("synthetic ACK flush failure")
            return self.stream.flush()

        def close(self):
            try:
                self.stream.close()
            finally:
                raise BrokenPipeError("synthetic close failure")

    def popen(*args, **kwargs):
        child = original_popen(*args, **kwargs)
        child.stdout, child.stderr = (
            FailingClose(child.stdout),
            FailingClose(child.stderr),
        )
        return child

    def fdopen(fd, mode, *args, **kwargs):
        stream = original_fdopen(fd, mode, *args, **kwargs)
        return (
            FailingClose(stream, ack=mode == "wb")
            if mode in {"rb", "wb"} and stat.S_ISFIFO(os.fstat(fd).st_mode)
            else stream
        )

    monkeypatch.setattr(module.subprocess, "Popen", popen)
    monkeypatch.setattr(os, "fdopen", fdopen)
    handle = await manager.start(turn)
    await asyncio.wait_for(handle.task, 5)
    assert failed_flush == [True]
    assert len(streams) == 4 and all(stream.closed for stream in streams)
    assert handle.changed.is_set()
    assert (await store.get(turn.run_id)).lifecycle == "awaiting_finalize"
    assert [
        json.loads(line)["type"] for line in handle.log_path.read_text().splitlines()
    ] == ["agent.failed"]


@pytest.mark.asyncio
async def test_unknown_old_event_log_is_never_appended_or_replayed(
    tmp_path, monkeypatch
):
    module, turn, store, manager = await manager_fixture(tmp_path)
    children, _ = fixture_popen(monkeypatch, module)
    log = store.root / "logs" / f"{turn.run_id}.events.log"
    log.parent.mkdir()
    log.write_text("old private bytes\n")
    handle = await manager.start(turn)
    lines = []
    with pytest.raises(OSError):
        async for line in manager.stream(handle):
            lines.append(line)
    assert lines == [] and children == []
    assert log.read_text() == "old private bytes\n"
    assert (await store.get(turn.run_id)).lifecycle == "awaiting_finalize"
