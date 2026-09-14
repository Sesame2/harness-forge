"""One isolated worker; execution records are exclusively owned by the parent."""

from __future__ import annotations

import os
import sys
import asyncio
import json
from collections.abc import Awaitable, Callable
from contextlib import redirect_stdout
from datetime import datetime, timezone
from typing import Any, TextIO
from uuid import UUID
from pathlib import Path

from harness_forge_runtime.artifacts import validate_artifacts
from harness_forge_runtime.claude import ClaudeAdapter
from harness_forge_runtime.models import RunRequest, RuntimeEvent
from harness_forge_runtime.sessions import SessionStore
from harness_forge_runtime.settings import RuntimeSettings
from harness_forge_runtime.workspaces import validate_workspace


def event_line(run_id: UUID, sequence: int, kind: str, payload: dict[str, Any]) -> str:
    return (
        RuntimeEvent(
            version="1",
            run_id=run_id,
            sequence=sequence,
            type=kind,
            occurred_at=datetime.now(timezone.utc).isoformat(),  # type: ignore[arg-type]
            payload=payload,
        ).model_dump_json(exclude_none=True)
        + "\n"
    )


async def run_worker(
    request: RunRequest,
    settings: RuntimeSettings,
    baseline: tuple[str, ...],
    control: Callable[[str, str], Awaitable[None]],
    *,
    stdout: TextIO | None = None,
    stderr: TextIO | None = None,
    sessions: SessionStore | None = None,
) -> int:
    output, diagnostic = stdout or sys.stdout, stderr or sys.stderr
    sequence = 0
    candidate: str | None = None

    def emit(kind: str, payload: dict[str, Any]) -> None:
        nonlocal sequence
        sequence += 1
        output.write(event_line(request.run_id, sequence, kind, payload))
        output.flush()

    async def on_candidate(value: str) -> None:
        nonlocal candidate
        await control("candidate.created", value)
        candidate = value

    try:
        # SDK/library diagnostics must never share the public NDJSON stream.
        with redirect_stdout(diagnostic):
            paths = validate_workspace(request, settings.run_workspace_root)
            session_store = sessions or SessionStore(settings.claude_config_dir)
            adapter = ClaudeAdapter(
                claude_config_dir=settings.claude_config_dir,
                baseline_session_ids=baseline,
                anthropic_base_url=os.environ.get("ANTHROPIC_BASE_URL"),
            )
            async for event in adapter.stream_turn(request, on_candidate):
                emit(event.type, event.payload)
            if candidate is None:
                raise ValueError("candidate is required")
            session_store.sync_transcript(candidate)
            await control("candidate.durable", candidate)
            artifacts = [
                item.model_dump()
                for item in validate_artifacts(paths.outputs, request.profile.config)
            ]
            emit("artifact.candidate", {"artifacts": artifacts})
            emit(
                "agent.completed",
                {"candidate_sdk_session_id": candidate, "artifacts": artifacts},
            )
        return 0
    except Exception:
        # Raw SDK exceptions may contain credentials or environment configuration.
        diagnostic.write(f"run={request.run_id} worker_failed\n")
        diagnostic.flush()
        emit(
            "agent.failed",
            {
                "code": "worker_failed",
                "message": "Worker execution failed",
                "retryable": False,
            },
        )
        return 1


def main() -> int:
    # EOF means the parent died before its durable PID/PGID record. No SDK work.
    start_fd = int(os.environ["HARNESS_START_FD"])
    try:
        if os.read(start_fd, 1) != b"1":
            return 0
    finally:
        os.close(start_fd)
    request = RunRequest.model_validate_json(Path(sys.argv[1]).read_bytes())
    baseline = tuple(json.loads(os.environ["HARNESS_BASELINE_SESSION_IDS"]))
    with (
        os.fdopen(int(os.environ["HARNESS_CONTROL_FD"]), "w") as writer,
        os.fdopen(int(os.environ["HARNESS_ACK_FD"]), "r") as reader,
    ):

        async def control(kind: str, candidate: str) -> None:
            writer.write(
                json.dumps({"type": kind, "candidate_sdk_session_id": candidate}) + "\n"
            )
            writer.flush()
            if await asyncio.to_thread(reader.readline, 16) != "ack\n":
                raise ValueError("parent did not acknowledge candidate")

        # Reserve a dedicated protocol FD before redirecting native/SDK stdout.
        with os.fdopen(os.dup(sys.stdout.fileno()), "w") as output:
            os.dup2(sys.stderr.fileno(), sys.stdout.fileno())
            return asyncio.run(
                run_worker(request, RuntimeSettings(), baseline, control, stdout=output)
            )


if __name__ == "__main__":
    raise SystemExit(main())
