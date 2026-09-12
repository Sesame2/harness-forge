from __future__ import annotations

import asyncio
from pathlib import Path
from uuid import uuid4

import pytest
from fastapi.testclient import TestClient

from harness_forge_runtime.api import create_app
from harness_forge_runtime.execution_store import ExecutionStore
from test_sessions import sessions, transcript


def test_head_delete_and_invalid_session_ids(tmp_path):
    root = tmp_path / "claude"
    session, _ = transcript(root)
    store = ExecutionStore(tmp_path / "executions")
    with TestClient(create_app(store=store, sessions=sessions(root))) as client:
        assert client.head(f"/v1/sessions/{session}").status_code == 200
        assert client.delete(f"/v1/sessions/{session}").status_code == 204
        assert client.head(f"/v1/sessions/{session}").status_code == 404
        assert client.delete(f"/v1/sessions/{session}").status_code == 204
        assert client.delete("/v1/sessions/not-a-uuid").status_code == 422


@pytest.mark.parametrize(
    "state",
    [
        "starting",
        "running",
        "missing_candidate",
        "missing_file",
        "not_durable",
        "invalid_candidate",
    ],
)
def test_commit_rejects_incomplete_barriers(tmp_path, state):
    root = tmp_path / "claude"
    sdk = sessions(root)
    store = ExecutionStore(tmp_path / "executions")
    run, candidate = uuid4(), str(uuid4())
    if state == "invalid_candidate":
        candidate = "not-a-uuid"
    with TestClient(create_app(store=store, sessions=sdk)) as client:
        client.portal.call(store.reserve, run, None, [])
        bucket = sdk.prepare_run(run, Path(f"/workspaces/{run}/workspace"), None)
        if state != "starting":
            client.portal.call(store.mark_running, run, 10, 10)
        if state not in {"starting", "running", "missing_candidate"}:
            client.portal.call(store.record_candidate, run, candidate)
            if state == "not_durable":
                transcript(root, bucket.name, candidate)
            else:
                client.portal.call(store.mark_candidate_durable, run)
        if state not in {"starting", "running"}:
            client.portal.call(store.mark_awaiting_finalize, run)
        assert (
            client.post(
                f"/v1/runs/{run}/finalize", json={"decision": "commit"}
            ).status_code
            == 409
        )
        assert client.portal.call(store.get, run).disposition is None


def test_commit_retains_ownership_after_tombstone_delete_and_abort_retry(tmp_path):
    root = tmp_path / "claude"
    source, original = transcript(root)
    sdk = sessions(root)
    store = ExecutionStore(tmp_path / "executions")
    run = uuid4()
    with TestClient(create_app(store=store, sessions=sdk)) as client:
        client.portal.call(store.reserve, run, source, [source])
        bucket = sdk.prepare_run(run, Path(f"/workspaces/{run}/workspace"), source)
        candidate, _ = transcript(root, bucket.name)
        client.portal.call(store.mark_running, run, 10, 10)
        client.portal.call(store.record_candidate, run, candidate)
        sdk.sync_transcript(candidate)
        client.portal.call(store.mark_candidate_durable, run)
        client.portal.call(store.mark_awaiting_finalize, run)
        for _ in range(2):
            assert (
                client.post(
                    f"/v1/runs/{run}/finalize", json={"decision": "commit"}
                ).status_code
                == 204
            )
        assert (
            client.post(
                f"/v1/runs/{run}/finalize", json={"decision": "abort"}
            ).status_code
            == 409
        )
        assert client.delete(f"/v1/executions/{run}").status_code == 204
    assert sessions(root).list_ids() == {source, candidate}
    assert (bucket / original.name).exists()
    run2 = uuid4()
    store2 = ExecutionStore(tmp_path / "executions")
    with TestClient(create_app(store=store2, sessions=sessions(root))) as client:
        baseline = sorted(sdk.list_ids())
        client.portal.call(store2.reserve, run2, candidate, baseline)
        bucket2 = sdk.prepare_run(
            run2, Path(f"/workspaces/{run2}/workspace"), candidate
        )
        transcript(root, bucket2.name)
        client.portal.call(store2.mark_awaiting_finalize, run2)
        sdk.abort(
            run2, tuple(baseline), candidate
        )  # crash after deletes, before tombstone
        for _ in range(2):
            assert (
                client.post(
                    f"/v1/runs/{run2}/finalize", json={"decision": "abort"}
                ).status_code
                == 204
            )
        assert (
            client.post(
                f"/v1/runs/{uuid4()}/finalize", json={"decision": "abort"}
            ).status_code
            == 404
        )
    assert original.exists() and (bucket / original.name).exists()


def test_session_delete_is_blocked_by_any_unfinalized_execution(tmp_path):
    root = tmp_path / "claude"
    source, original = transcript(root)
    store = ExecutionStore(tmp_path / "executions")
    with TestClient(create_app(store=store, sessions=sessions(root))) as client:
        run = uuid4()
        client.portal.call(store.reserve, run, None, [source])
        sdk = sessions(root)
        active_bucket = sdk.prepare_run(run, Path(f"/workspaces/{run}/workspace"), None)
        assert client.delete(f"/v1/sessions/{source}").status_code == 409
        assert client.delete(f"/v1/sessions/{uuid4()}").status_code == 204
        assert active_bucket.exists(), "absent deletion cannot prune active ownership"
        client.portal.call(store.mark_awaiting_finalize, run)
        assert client.delete(f"/v1/sessions/{source}").status_code == 409
    assert original.exists()


def test_abort_delete_error_never_writes_success_tombstone(tmp_path, monkeypatch):
    root = tmp_path / "claude"
    sdk = sessions(root)
    store = ExecutionStore(tmp_path / "executions")
    run = uuid4()
    with TestClient(create_app(store=store, sessions=sdk)) as client:
        client.portal.call(store.reserve, run, None, [])
        transcript(root)
        client.portal.call(store.mark_awaiting_finalize, run)
        monkeypatch.setattr(
            sdk, "abort", lambda *a: (_ for _ in ()).throw(OSError("secret path"))
        )
        response = client.post(f"/v1/runs/{run}/finalize", json={"decision": "abort"})
        assert response.status_code == 500
        assert response.json() == {"code": "session_operation_failed"}
        assert client.portal.call(store.get, run).disposition is None


@pytest.mark.asyncio
async def test_reservation_obeys_shared_lifecycle_gate(tmp_path):
    store = ExecutionStore(tmp_path / "executions")
    await store.initialize()
    assert hasattr(store, "lifecycle_lock"), "reservation needs shared lifecycle gate"
    async with store.lifecycle_lock:
        reservation = asyncio.create_task(store.reserve(uuid4(), None, []))
        await asyncio.sleep(0)
        assert not reservation.done()
    await reservation


@pytest.mark.asyncio
@pytest.mark.parametrize("operation", ["abort", "delete"])
async def test_api_mutations_and_reservation_share_gate(tmp_path, operation):
    import httpx

    root = tmp_path / "claude"
    source, _ = transcript(root)
    store = ExecutionStore(tmp_path / "executions")
    app = create_app(store=store, sessions=sessions(root))
    async with app.router.lifespan_context(app):
        run = uuid4()
        if operation == "abort":
            await store.reserve(run, source, [source])
            await store.mark_awaiting_finalize(run)
        async with httpx.AsyncClient(
            transport=httpx.ASGITransport(app=app), base_url="http://runtime"
        ) as client:
            async with store.lifecycle_lock:
                mutation = asyncio.create_task(
                    client.post(f"/v1/runs/{run}/finalize", json={"decision": "abort"})
                    if operation == "abort"
                    else client.delete(f"/v1/sessions/{source}")
                )
                await asyncio.sleep(0)
                reservation = asyncio.create_task(store.reserve(uuid4(), None, []))
                await asyncio.sleep(0)
                assert not mutation.done() and not reservation.done()
            assert (await mutation).status_code == 204
            await reservation
