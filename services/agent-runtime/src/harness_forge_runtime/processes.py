"""Parent-owned subprocess lifecycle, durable control ACKs, and event journal."""

from __future__ import annotations

import asyncio
import json
import os
import re
import signal
import subprocess
import sys
import tempfile
import time
from collections.abc import AsyncIterator
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, BinaryIO, cast
from uuid import UUID

from harness_forge_runtime.errors import InvalidExecutionState
from harness_forge_runtime.execution_store import ExecutionStore
from harness_forge_runtime.models import (
    AgentCompletedPayload,
    ArtifactCandidatePayload,
    PAYLOAD_MODELS,
    RunRequest,
    RuntimeEvent,
    is_terminal_event,
)
from harness_forge_runtime.runner import event_line
from harness_forge_runtime.sessions import SessionStore
from harness_forge_runtime.settings import RuntimeSettings
from harness_forge_runtime.workspaces import validate_workspace


MAX_EVENT_BYTES = 4 * 1024 * 1024
MAX_STDERR_BYTES = 64 * 1024


def group_active(pgid: int) -> bool:
    if pgid <= 1 or pgid == os.getpgrp():
        raise InvalidExecutionState("unsafe worker process group")
    try:
        os.killpg(pgid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        # Darwin reports EPERM for an unreaped zombie; never treat EPERM as dead.
        return True
    if sys.platform == "linux":
        # Container PID 1 may leave orphan zombies: they cannot write or run.
        for entry in Path("/proc").iterdir():
            if not entry.name.isdigit():
                continue
            try:
                fields = (entry / "stat").read_text().rsplit(")", 1)[1].split()
                if int(fields[2]) == pgid and fields[0] not in {"Z", "X"}:
                    return True
            except FileNotFoundError:
                continue
        return False
    return True


async def stop_group(pgid: int, process: subprocess.Popen[bytes] | None = None) -> None:
    for sig, timeout in ((signal.SIGTERM, 1.0), (signal.SIGKILL, 3.0)):
        if process is not None:
            process.poll()
        if group_active(pgid):
            try:
                os.killpg(pgid, sig)
            except (ProcessLookupError, PermissionError):
                pass
        deadline = time.monotonic() + timeout
        while True:
            if process is not None:
                process.poll()  # Reap the worker, not merely its descendants.
            else:
                try:
                    os.waitpid(pgid, os.WNOHANG)
                except ChildProcessError:
                    pass
            if not group_active(pgid):
                if process is not None:
                    await asyncio.to_thread(process.wait)
                return
            if time.monotonic() >= deadline:
                break
            await asyncio.sleep(0.02)
    raise InvalidExecutionState("worker process group is still active")


def redact(value: str) -> str:
    try:
        structured = json.loads(value)
    except (ValueError, RecursionError):
        structured = None
    if isinstance(structured, (dict, list)):
        return json.dumps(redact_payload(structured))
    for name in ("ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"):
        secret = os.environ.get(name)
        if secret:
            value = value.replace(secret, "[redacted]")
    value = re.sub(r"\b[A-Z][A-Z0-9_]*(?:=|:\s*)[^\s]+", "[redacted]", value)
    value = re.sub(
        r"(?i)\b(?:bearer\s+|sk-(?:ant-)?)[A-Za-z0-9_.-]+", "[redacted]", value
    )
    return re.sub(
        r"""(?i)\b(?:api[_-]?key|(?:access[_-]?|refresh[_-]?|auth[_-]?)?token|password|secret)["']?\s*[:=]\s*(?:"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^\s]+)""",
        "[redacted]",
        value,
    )


def redact_payload(value: Any) -> Any:
    if isinstance(value, str):
        return redact(value)
    if isinstance(value, dict):
        return {
            key: "[redacted]"
            if re.search(
                r"(?i)(?:api[_-]?key|token|password|secret|^env(?:ironment)?$)",
                key,
            )
            else redact_payload(item)
            for key, item in value.items()
        }
    if isinstance(value, list):
        return [redact_payload(item) for item in value]
    return value


def write_request(path: Path, request: RunRequest) -> None:
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(dir=path.parent, prefix=".request-")
    try:
        with os.fdopen(fd, "w") as stream:
            stream.write(request.model_dump_json())
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
        for parent in (path.parent, path.parent.parent):
            directory = os.open(parent, os.O_RDONLY | os.O_DIRECTORY)
            try:
                os.fsync(directory)
            finally:
                os.close(directory)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


@dataclass
class WorkerRun:
    request: RunRequest
    request_path: Path
    log_path: Path
    stderr_path: Path
    lock: asyncio.Lock = field(default_factory=asyncio.Lock)
    changed: asyncio.Event = field(default_factory=asyncio.Event)
    cancelled: bool = False
    process: subprocess.Popen[bytes] | None = None
    task: asyncio.Task[None] = field(init=False)
    sequence: int = 0
    journal_ready: bool = False


class ProcessManager:
    def __init__(
        self, store: ExecutionStore, sessions: SessionStore, settings: RuntimeSettings
    ) -> None:
        self.store, self.sessions, self.settings = store, sessions, settings
        self.runs: dict[UUID, WorkerRun] = {}

    async def recover(self) -> None:
        for record in await self.store.list_unfinalized():
            if record.lifecycle in {"starting", "running"}:
                if record.worker_pgid is not None:
                    await stop_group(record.worker_pgid)
                await self.store.mark_awaiting_finalize(record.run_id)

    async def start(self, request: RunRequest) -> WorkerRun:
        # reserve owns lifecycle_lock internally. Do not acquire it recursively.
        await self.store.reserve(
            request.run_id,
            request.source_sdk_session_id,
            sorted(self.sessions.list_ids()),
        )
        root, run = self.store.root, str(request.run_id)
        handle = WorkerRun(
            request,
            root / "requests" / f"{run}.json",
            root / "logs" / f"{run}.events.log",
            root / "logs" / f"{run}.stderr.log",
        )
        self.runs[request.run_id] = handle
        handle.task = asyncio.create_task(self._run(handle))
        return handle

    async def stream(self, handle: WorkerRun) -> AsyncIterator[str]:
        offset = 0
        while True:
            handle.changed.clear()
            if handle.journal_ready:
                with handle.log_path.open() as log:
                    log.seek(offset)
                    for line in log:
                        yield line
                    offset = log.tell()
            if handle.task.done():
                await handle.task
                return
            await handle.changed.wait()

    async def cancel(self, run_id: UUID) -> bool:
        record = await self.store.get(run_id)
        if record is None:
            return False
        if record.lifecycle not in {"starting", "running"}:
            return True
        handle = self.runs.get(run_id)
        if handle is not None:
            # Set intent before waiting on Popen's serialized spawn section.
            handle.cancelled = True
            async with handle.lock:
                current = await self.store.get(run_id)
                if current is None or current.lifecycle not in {"starting", "running"}:
                    return True
                if handle.process is not None:
                    await stop_group(handle.process.pid, handle.process)
            await asyncio.shield(handle.task)
        elif record.lifecycle in {"starting", "running"}:
            if record.worker_pgid is not None:
                await stop_group(record.worker_pgid)
            await self.store.mark_awaiting_finalize(run_id)
        return True

    async def close(self) -> None:
        for run in list(self.runs):
            await self.cancel(run)

    def delete_files(self, run_id: UUID) -> None:
        paths = (
            self.store.root / "requests" / f"{run_id}.json",
            self.store.root / "logs" / f"{run_id}.events.log",
            self.store.root / "logs" / f"{run_id}.stderr.log",
        )
        for path in paths:
            path.unlink(missing_ok=True)
        for parent in dict.fromkeys(path.parent for path in paths):
            if parent.exists():
                directory = os.open(parent, os.O_RDONLY | os.O_DIRECTORY)
                try:
                    os.fsync(directory)
                finally:
                    os.close(directory)
        self.runs.pop(run_id, None)

    def _publish(
        self, handle: WorkerRun, kind: str, payload: dict[str, object]
    ) -> None:
        if not handle.journal_ready:
            raise OSError("event journal is unavailable")
        handle.sequence += 1
        # Redact string leaves, never serialized JSON punctuation.
        line = event_line(
            handle.request.run_id, handle.sequence, kind, redact_payload(payload)
        )
        with handle.log_path.open("a") as log:
            log.write(line)
            log.flush()
            os.fsync(log.fileno())
        handle.changed.set()

    async def _control(
        self, handle: WorkerRun, control: BinaryIO, ack: BinaryIO
    ) -> None:
        while line := await asyncio.to_thread(control.readline, 4097):
            if len(line) > 4096 or not line.endswith(b"\n"):
                raise ValueError("invalid control message")
            message = json.loads(line)
            if not isinstance(message, dict) or set(message) != {
                "type",
                "candidate_sdk_session_id",
            }:
                raise ValueError("invalid control message")
            candidate = message["candidate_sdk_session_id"]
            if not isinstance(candidate, str) or str(UUID(candidate)) != candidate:
                raise ValueError("invalid candidate")
            async with handle.lock:
                record = await self.store.get(handle.request.run_id)
                if (
                    record is None
                    or handle.cancelled
                    or candidate == record.source_sdk_session_id
                    or candidate in record.baseline_session_ids
                ):
                    raise ValueError("candidate is not owned")
                if message["type"] == "candidate.created":
                    await self.store.record_candidate(record.run_id, candidate)
                elif message["type"] == "candidate.durable":
                    if (
                        record.candidate_sdk_session_id != candidate
                        or not self.sessions.validate_candidate(
                            record.run_id,
                            candidate,
                            record.source_sdk_session_id,
                            record.baseline_session_ids,
                        )
                    ):
                        raise ValueError("candidate is not durable")
                    await self.store.mark_candidate_durable(record.run_id)
                else:
                    raise ValueError("unknown control message")
                ack.write(b"ack\n")
                ack.flush()

    async def _stdout(self, handle: WorkerRun, stream: BinaryIO) -> RuntimeEvent | None:
        last = 0
        terminal = None
        artifacts = None
        while line := await asyncio.to_thread(stream.readline, MAX_EVENT_BYTES + 1):
            if len(line) > MAX_EVENT_BYTES or not line.endswith(b"\n"):
                raise ValueError("invalid worker line")
            event = RuntimeEvent.model_validate_json(line)
            if (
                event.run_id != handle.request.run_id
                or event.sequence <= last
                or terminal is not None
                or event.type not in PAYLOAD_MODELS
            ):
                raise ValueError("invalid worker event ordering")
            last = event.sequence
            record = await self.store.get(handle.request.run_id)
            if event.type not in {"agent.failed"} and (
                record is None or record.candidate_sdk_session_id is None
            ):
                raise ValueError("events before candidate ACK")
            if isinstance(
                event.payload, (ArtifactCandidatePayload, AgentCompletedPayload)
            ):
                if record is None or record.candidate_durable_at is None:
                    raise ValueError("artifacts before durable ACK")
                if isinstance(event.payload, ArtifactCandidatePayload):
                    if artifacts is not None:
                        raise ValueError("duplicate artifact candidate")
                    artifacts = event.payload.artifacts
                elif (
                    event.payload.candidate_sdk_session_id
                    != record.candidate_sdk_session_id
                    or artifacts != event.payload.artifacts
                ):
                    raise ValueError("completion does not match candidate")
            if is_terminal_event(event):
                terminal = (
                    event  # Withhold until clean EOF and successful process exit.
                )
            else:
                self._publish(handle, event.type, event.model_dump()["payload"])
        return terminal

    async def _stderr(self, handle: WorkerRun, stream: BinaryIO) -> None:
        remaining = MAX_STDERR_BYTES
        oversized = False
        while chunk := await asyncio.to_thread(stream.readline, MAX_STDERR_BYTES + 1):
            if oversized or len(chunk) > MAX_STDERR_BYTES:
                oversized = not chunk.endswith(b"\n")
                line = "[oversized diagnostic redacted]\n" if not oversized else ""
            else:
                line = redact(chunk.decode("utf-8", errors="replace"))
            if remaining > 0:
                encoded = line.encode()[:remaining]
                with handle.stderr_path.open("ab") as log:
                    log.write(encoded)
                remaining -= len(encoded)

    async def _run(self, handle: WorkerRun) -> None:
        fds: list[int] = []
        readers: list[asyncio.Task[object]] = []
        streams: list[BinaryIO] = []
        terminal = None
        success = False
        try:
            handle.log_path.parent.mkdir(mode=0o700, exist_ok=True)
            with handle.log_path.open("x"):
                pass
            handle.journal_ready = True
            with handle.stderr_path.open("x") as diagnostic:
                diagnostic.write(f"run={handle.request.run_id}\n")
            async with handle.lock:
                if handle.cancelled:
                    return
                validate_workspace(handle.request, self.settings.run_workspace_root)
                write_request(handle.request_path, handle.request)
                record = await self.store.get(handle.request.run_id)
                assert record is not None
                self.sessions.prepare_run(
                    record.run_id,
                    Path(handle.request.paths.workspace),
                    record.source_sdk_session_id,
                )
                start_r, start_w = os.pipe()
                control_r, control_w = os.pipe()
                ack_r, ack_w = os.pipe()
                fds.extend((start_r, start_w, control_r, control_w, ack_r, ack_w))
                env = dict(os.environ)
                env.update(
                    {
                        key.upper(): str(value)
                        for key, value in self.settings.model_dump().items()
                    }
                )
                env.update(
                    HARNESS_START_FD=str(start_r),
                    HARNESS_CONTROL_FD=str(control_w),
                    HARNESS_ACK_FD=str(ack_r),
                    HARNESS_BASELINE_SESSION_IDS=json.dumps(
                        record.baseline_session_ids
                    ),
                )
                handle.process = await asyncio.to_thread(
                    subprocess.Popen,
                    [
                        sys.executable,
                        "-m",
                        "harness_forge_runtime.runner",
                        str(handle.request_path),
                    ],
                    stdin=subprocess.DEVNULL,
                    stdout=subprocess.PIPE,
                    stderr=subprocess.PIPE,
                    start_new_session=True,
                    pass_fds=(start_r, control_w, ack_r),
                    env=env,
                )
                assert (
                    handle.process.stdout is not None
                    and handle.process.stderr is not None
                )
                streams.extend(
                    (
                        cast(BinaryIO, handle.process.stdout),
                        cast(BinaryIO, handle.process.stderr),
                    )
                )
                for fd in (start_r, control_w, ack_r):
                    os.close(fd)
                    fds.remove(fd)
                await self.store.mark_running(
                    record.run_id, handle.process.pid, handle.process.pid
                )
                if not handle.cancelled:
                    os.write(start_w, b"1")
                os.close(start_w)
                fds.remove(start_w)
            process = handle.process
            assert process.stdout is not None and process.stderr is not None
            control, ack = os.fdopen(control_r, "rb"), os.fdopen(ack_w, "wb")
            fds.remove(control_r)
            fds.remove(ack_w)
            stdout, stderr = (
                cast(BinaryIO, process.stdout),
                cast(BinaryIO, process.stderr),
            )
            streams.extend((control, ack))
            stdout_task = asyncio.create_task(self._stdout(handle, stdout))
            control_task = asyncio.create_task(self._control(handle, control, ack))
            stderr_task = asyncio.create_task(self._stderr(handle, stderr))
            exit_task = asyncio.create_task(asyncio.to_thread(process.wait))
            readers.extend((stdout_task, control_task, stderr_task, exit_task))
            if handle.cancelled:
                await stop_group(process.pid, process)
            # Observe process exit independently of pipes inherited by descendants.
            pending = set(readers)
            while pending:
                done, pending = await asyncio.wait(
                    pending, return_when=asyncio.FIRST_COMPLETED
                )
                for task in done:
                    task.result()
                if exit_task in done:
                    await stop_group(process.pid, process)
            terminal = await stdout_task
            status = await exit_task
            success = status == 0 and terminal is not None and not handle.cancelled
        except Exception:
            success = False
        finally:
            for fd in fds:
                os.close(fd)
            try:
                if handle.process is not None:
                    await stop_group(handle.process.pid, handle.process)
                if readers:
                    await asyncio.gather(*readers, return_exceptions=True)
                try:
                    if success and terminal is not None and not handle.cancelled:
                        self._publish(
                            handle, terminal.type, terminal.model_dump()["payload"]
                        )
                    else:
                        self._publish(
                            handle,
                            "agent.failed",
                            {
                                "code": "execution_cancelled"
                                if handle.cancelled
                                else "worker_failed",
                                "message": "Execution cancelled"
                                if handle.cancelled
                                else "Worker execution failed",
                                "retryable": False,
                            },
                        )
                finally:
                    # A journal failure cannot keep an already stopped run active.
                    await self.store.mark_awaiting_finalize(handle.request.run_id)
            finally:
                try:
                    for stream in streams:
                        try:
                            stream.close()
                        except OSError:
                            # A broken buffered ACK can retry its failed flush on close.
                            # Its control-task failure was already handled above.
                            pass
                finally:
                    handle.changed.set()
