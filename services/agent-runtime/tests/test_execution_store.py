import json
from datetime import datetime, timezone
from pathlib import Path
from uuid import UUID, uuid4

import pytest
from pydantic import ValidationError

from harness_forge_runtime.errors import ExecutionConflict, InvalidExecutionState
from harness_forge_runtime.execution_store import ExecutionRecord, ExecutionStore


def store_at(tmp_path: Path) -> ExecutionStore:
    return ExecutionStore(tmp_path / "executions")


@pytest.mark.asyncio
async def test_reserve_is_atomic_and_rejects_same_or_different_active_run(
    tmp_path: Path,
) -> None:
    store = store_at(tmp_path)
    await store.initialize()
    run_id = uuid4()
    record = await store.reserve(run_id, "source", ["source", "older"])

    assert record.lifecycle == "starting"
    assert record.run_id == run_id
    assert json.loads((store.root / f"{run_id}.json").read_text())["run_id"] == str(
        run_id
    )
    with pytest.raises(ExecutionConflict, match="already_running"):
        await store.reserve(run_id, "source", [])
    with pytest.raises(ExecutionConflict, match="runtime_busy"):
        await store.reserve(uuid4(), None, [])


@pytest.mark.asyncio
async def test_execution_moves_through_unfinalized_lifecycle(tmp_path: Path) -> None:
    store = store_at(tmp_path)
    await store.initialize()
    run_id = uuid4()
    await store.reserve(run_id, None, [])

    running = await store.mark_running(run_id, worker_pid=101, worker_pgid=100)
    awaiting = await store.mark_awaiting_finalize(run_id)

    assert (running.lifecycle, running.worker_pid, running.worker_pgid) == (
        "running",
        101,
        100,
    )
    assert awaiting.lifecycle == "awaiting_finalize"


@pytest.mark.asyncio
async def test_candidate_and_durable_marker_are_validated_and_persisted(
    tmp_path: Path,
) -> None:
    store = store_at(tmp_path)
    await store.initialize()
    run_id = uuid4()
    await store.reserve(run_id, "source", ["baseline"])
    await store.mark_running(run_id, worker_pid=101, worker_pgid=100)

    with pytest.raises(InvalidExecutionState):
        await store.record_candidate(run_id, "source")
    with pytest.raises(InvalidExecutionState):
        await store.record_candidate(run_id, "baseline")
    candidate = await store.record_candidate(run_id, "candidate")
    assert await store.record_candidate(run_id, "candidate") == candidate
    with pytest.raises(InvalidExecutionState, match="already recorded"):
        await store.record_candidate(run_id, "different-candidate")
    durable = await store.mark_candidate_durable(run_id)

    assert candidate.candidate_sdk_session_id == "candidate"
    assert durable.candidate_durable_at is not None


@pytest.mark.asyncio
async def test_returned_records_cannot_mutate_the_store(tmp_path: Path) -> None:
    store = store_at(tmp_path)
    await store.initialize()
    record = await store.reserve(uuid4(), None, ["baseline"])

    with pytest.raises(ValidationError):
        record.lifecycle = "committed"  # type: ignore[misc]
    with pytest.raises(AttributeError):
        record.baseline_session_ids.append("injected")  # type: ignore[attr-defined]

    persisted = await store.get(record.run_id)
    assert persisted is not None
    assert persisted.lifecycle == "starting"
    assert persisted.baseline_session_ids == ("baseline",)


@pytest.mark.asyncio
async def test_failed_parent_fsync_requires_restart_scan_before_more_writes(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    store = store_at(tmp_path)
    await store.initialize()
    run_id = uuid4()

    def fail_fsync() -> None:
        raise OSError("fsync failed")

    monkeypatch.setattr(store, "_fsync_root", fail_fsync)
    with pytest.raises(OSError, match="fsync failed"):
        await store.reserve(run_id, None, [])
    with pytest.raises(InvalidExecutionState, match="not initialized"):
        await store.reserve(uuid4(), None, [])

    restarted = store_at(tmp_path)
    await restarted.initialize()
    assert await restarted.get(run_id) is not None


@pytest.mark.asyncio
async def test_finalize_writes_tombstone_and_rejects_conflicting_decision(
    tmp_path: Path,
) -> None:
    store = store_at(tmp_path)
    await store.initialize()
    run_id = uuid4()
    await store.reserve(run_id, None, [])
    await store.mark_awaiting_finalize(run_id)

    committed = await store.finalize(run_id, "commit")
    repeated = await store.finalize(run_id, "commit")

    assert committed.lifecycle == "committed"
    assert committed.disposition == "commit"
    assert repeated == committed
    with pytest.raises(ExecutionConflict, match="conflicting_decision"):
        await store.finalize(run_id, "abort")


@pytest.mark.asyncio
async def test_abort_tombstone_can_be_deleted_but_unfinalized_cannot(
    tmp_path: Path,
) -> None:
    store = store_at(tmp_path)
    await store.initialize()
    active_id, aborted_id = uuid4(), uuid4()
    await store.reserve(active_id, None, [])

    with pytest.raises(ExecutionConflict):
        await store.delete(active_id)
    await store.mark_awaiting_finalize(active_id)
    await store.finalize(active_id, "abort")
    assert await store.delete(active_id)
    assert not await store.delete(active_id)

    await store.reserve(aborted_id, None, [])


@pytest.mark.asyncio
async def test_restart_scan_restores_records_and_rejects_multiple_unfinalized(
    tmp_path: Path,
) -> None:
    first = store_at(tmp_path)
    await first.initialize()
    run_id = uuid4()
    await first.reserve(run_id, None, [])

    restarted = store_at(tmp_path)
    await restarted.initialize()
    assert (await restarted.get(run_id)).lifecycle == "starting"  # type: ignore[union-attr]

    other_id = uuid4()
    raw = (await restarted.get(run_id)).model_dump(mode="json")  # type: ignore[union-attr]
    raw["run_id"] = str(other_id)
    (restarted.root / f"{other_id}.json").write_text(json.dumps(raw))
    with pytest.raises(InvalidExecutionState, match="multiple unfinalized"):
        await store_at(tmp_path).initialize()


@pytest.mark.asyncio
async def test_restart_scan_fails_closed_on_corrupt_or_mismatched_record(
    tmp_path: Path,
) -> None:
    store = store_at(tmp_path)
    await store.initialize()
    (store.root / f"{uuid4()}.json").write_text("not-json")

    with pytest.raises(InvalidExecutionState, match="corrupt execution record"):
        await store.initialize()
    with pytest.raises(InvalidExecutionState, match="not initialized"):
        await store.get(uuid4())


def test_execution_record_rejects_invalid_lifecycle_and_durable_state() -> None:
    values = {
        "run_id": uuid4(),
        "source_sdk_session_id": None,
        "candidate_sdk_session_id": None,
        "candidate_durable_at": datetime.now(timezone.utc),
        "baseline_session_ids": [],
        "worker_pid": None,
        "worker_pgid": None,
        "lifecycle": "invented",
        "created_at": datetime.now(timezone.utc),
        "updated_at": datetime.now(timezone.utc),
        "disposition": None,
    }
    with pytest.raises(ValidationError):
        ExecutionRecord.model_validate(values)

    values["lifecycle"] = "running"
    with pytest.raises(ValidationError, match="candidate_durable_at"):
        ExecutionRecord.model_validate(values)


@pytest.mark.asyncio
async def test_run_id_is_parsed_before_a_record_path_is_formed(tmp_path: Path) -> None:
    store = store_at(tmp_path)
    await store.initialize()

    with pytest.raises(ValueError):
        await store.get("../escape")
    assert not (tmp_path / "escape.json").exists()


def assert_uuid(value: UUID) -> None:
    assert isinstance(value, UUID)
