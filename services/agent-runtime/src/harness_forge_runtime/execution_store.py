from __future__ import annotations

import asyncio
import os
import tempfile
from datetime import datetime, timezone
from pathlib import Path
from typing import Literal, Self
from uuid import UUID

from pydantic import BaseModel, ConfigDict, Field, ValidationError, model_validator

from harness_forge_runtime.errors import ExecutionConflict, InvalidExecutionState


Lifecycle = Literal[
    "starting", "running", "awaiting_finalize", "committed", "aborted"
]
Decision = Literal["commit", "abort"]
UNFINALIZED = frozenset({"starting", "running", "awaiting_finalize"})


def _now() -> datetime:
    return datetime.now(timezone.utc)


class ExecutionRecord(BaseModel):
    model_config = ConfigDict(extra="forbid", frozen=True)

    run_id: UUID
    source_sdk_session_id: str | None
    candidate_sdk_session_id: str | None
    candidate_durable_at: datetime | None
    baseline_session_ids: tuple[str, ...]
    worker_pid: int | None = Field(ge=1)
    worker_pgid: int | None = Field(ge=1)
    lifecycle: Lifecycle
    created_at: datetime
    updated_at: datetime
    disposition: Decision | None

    @model_validator(mode="after")
    def validate_state(self) -> Self:
        if self.candidate_durable_at is not None and self.candidate_sdk_session_id is None:
            raise ValueError("candidate_durable_at requires a candidate session")
        if self.candidate_sdk_session_id is not None and (
            self.candidate_sdk_session_id == self.source_sdk_session_id
            or self.candidate_sdk_session_id in self.baseline_session_ids
        ):
            raise ValueError("candidate session must belong to this execution")
        expected = {"committed": "commit", "aborted": "abort"}.get(self.lifecycle)
        if expected is None and self.disposition is not None:
            raise ValueError("unfinalized execution cannot have a disposition")
        if expected is not None and self.disposition != expected:
            raise ValueError("terminal lifecycle and disposition must agree")
        if self.updated_at < self.created_at:
            raise ValueError("updated_at cannot precede created_at")
        return self


class ExecutionStore:
    def __init__(self, root: Path) -> None:
        self.root = root
        self._records: dict[UUID, ExecutionRecord] = {}
        self._lock = asyncio.Lock()
        self._initialized = False

    async def initialize(self) -> None:
        async with self._lock:
            self._initialized = False
            self.root.mkdir(parents=True, exist_ok=True)
            records: dict[UUID, ExecutionRecord] = {}
            try:
                for path in self.root.glob("*.json"):
                    run_id = UUID(path.stem)
                    record = ExecutionRecord.model_validate_json(path.read_bytes())
                    if record.run_id != run_id:
                        raise ValueError("record run_id does not match filename")
                    records[run_id] = record
            except (OSError, ValueError) as error:
                raise InvalidExecutionState("corrupt execution record") from error
            if sum(record.lifecycle in UNFINALIZED for record in records.values()) > 1:
                raise InvalidExecutionState("multiple unfinalized execution records")
            self._records = records
            self._initialized = True

    async def reserve(
        self,
        run_id: UUID | str,
        source_sdk_session_id: str | None,
        baseline_session_ids: list[str],
    ) -> ExecutionRecord:
        parsed = UUID(str(run_id))
        async with self._lock:
            self._require_initialized()
            existing = self._records.get(parsed)
            if existing is not None:
                if existing.lifecycle == "awaiting_finalize":
                    raise ExecutionConflict("awaiting_finalize")
                if existing.lifecycle in {"starting", "running"}:
                    raise ExecutionConflict("already_running")
                raise ExecutionConflict(existing.disposition or "conflict")
            if any(record.lifecycle in UNFINALIZED for record in self._records.values()):
                raise ExecutionConflict("runtime_busy")
            now = _now()
            record = ExecutionRecord(
                run_id=parsed,
                source_sdk_session_id=source_sdk_session_id,
                candidate_sdk_session_id=None,
                candidate_durable_at=None,
                baseline_session_ids=tuple(baseline_session_ids),
                worker_pid=None,
                worker_pgid=None,
                lifecycle="starting",
                created_at=now,
                updated_at=now,
                disposition=None,
            )
            self._write(record)
            self._records[parsed] = record
            return record

    async def get(self, run_id: UUID | str) -> ExecutionRecord | None:
        parsed = UUID(str(run_id))
        async with self._lock:
            self._require_initialized()
            return self._records.get(parsed)

    async def list_unfinalized(self) -> list[ExecutionRecord]:
        async with self._lock:
            self._require_initialized()
            return [
                record
                for record in self._records.values()
                if record.lifecycle in UNFINALIZED
            ]

    async def mark_running(
        self, run_id: UUID | str, worker_pid: int, worker_pgid: int
    ) -> ExecutionRecord:
        return await self._update(
            run_id,
            {"starting"},
            lifecycle="running",
            worker_pid=worker_pid,
            worker_pgid=worker_pgid,
        )

    async def record_candidate(
        self, run_id: UUID | str, candidate_sdk_session_id: str
    ) -> ExecutionRecord:
        parsed = UUID(str(run_id))
        async with self._lock:
            record = self._record_for_update(parsed, {"running"})
            if record.candidate_sdk_session_id == candidate_sdk_session_id:
                return record
            if record.candidate_sdk_session_id is not None:
                raise InvalidExecutionState("candidate session is already recorded")
            return self._replace(
                record, candidate_sdk_session_id=candidate_sdk_session_id
            )

    async def mark_candidate_durable(self, run_id: UUID | str) -> ExecutionRecord:
        parsed = UUID(str(run_id))
        async with self._lock:
            record = self._record_for_update(parsed, {"running"})
            if record.candidate_sdk_session_id is None:
                raise InvalidExecutionState("candidate session is not recorded")
            return self._replace(record, candidate_durable_at=_now())

    async def mark_awaiting_finalize(self, run_id: UUID | str) -> ExecutionRecord:
        return await self._update(
            run_id, {"starting", "running"}, lifecycle="awaiting_finalize"
        )

    async def finalize(
        self, run_id: UUID | str, decision: Decision
    ) -> ExecutionRecord:
        parsed = UUID(str(run_id))
        if decision not in {"commit", "abort"}:
            raise ValueError("decision must be commit or abort")
        async with self._lock:
            self._require_initialized()
            record = self._records.get(parsed)
            if record is None:
                raise InvalidExecutionState("execution record not found")
            if record.disposition is not None:
                if record.disposition != decision:
                    raise ExecutionConflict("conflicting_decision")
                return record
            if record.lifecycle != "awaiting_finalize":
                raise ExecutionConflict("execution is not awaiting finalize")
            lifecycle: Lifecycle = "committed" if decision == "commit" else "aborted"
            return self._replace(record, lifecycle=lifecycle, disposition=decision)

    async def delete(self, run_id: UUID | str) -> bool:
        parsed = UUID(str(run_id))
        async with self._lock:
            self._require_initialized()
            record = self._records.get(parsed)
            if record is None:
                return False
            if record.lifecycle in UNFINALIZED:
                raise ExecutionConflict("unfinalized execution cannot be deleted")
            try:
                self._path(parsed).unlink()
                self._fsync_root()
            except OSError:
                self._initialized = False
                raise
            del self._records[parsed]
            return True

    async def _update(
        self,
        run_id: UUID | str,
        allowed: set[str],
        **changes: object,
    ) -> ExecutionRecord:
        parsed = UUID(str(run_id))
        async with self._lock:
            record = self._record_for_update(parsed, allowed)
            return self._replace(record, **changes)

    def _record_for_update(
        self, run_id: UUID, allowed: set[str]
    ) -> ExecutionRecord:
        self._require_initialized()
        record = self._records.get(run_id)
        if record is None:
            raise InvalidExecutionState("execution record not found")
        if record.lifecycle not in allowed:
            raise InvalidExecutionState(
                f"cannot update execution in {record.lifecycle} lifecycle"
            )
        return record

    def _replace(self, record: ExecutionRecord, **changes: object) -> ExecutionRecord:
        try:
            updated = ExecutionRecord.model_validate(
                {**record.model_dump(), **changes, "updated_at": _now()}
            )
        except ValidationError as error:
            raise InvalidExecutionState(str(error)) from error
        self._write(updated)
        self._records[record.run_id] = updated
        return updated

    def _write(self, record: ExecutionRecord) -> None:
        payload = record.model_dump_json().encode()
        temporary_name: str | None = None
        try:
            descriptor, temporary_name = tempfile.mkstemp(
                dir=self.root, prefix=f".{record.run_id}.", suffix=".tmp"
            )
            with os.fdopen(descriptor, "wb") as temporary:
                temporary.write(payload)
                temporary.flush()
                os.fsync(temporary.fileno())
            os.replace(temporary_name, self._path(record.run_id))
            self._fsync_root()
        except OSError:
            self._initialized = False
            raise
        finally:
            if temporary_name is not None:
                try:
                    os.unlink(temporary_name)
                except FileNotFoundError:
                    pass

    def _path(self, run_id: UUID) -> Path:
        return self.root / f"{run_id}.json"

    def _fsync_root(self) -> None:
        descriptor = os.open(self.root, os.O_RDONLY | getattr(os, "O_DIRECTORY", 0))
        try:
            os.fsync(descriptor)
        finally:
            os.close(descriptor)

    def _require_initialized(self) -> None:
        if not self._initialized:
            raise InvalidExecutionState("execution store is not initialized")
