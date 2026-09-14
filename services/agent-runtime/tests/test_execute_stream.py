import json
import os
from pathlib import Path
from uuid import uuid4

import pytest
from fastapi.testclient import TestClient

from harness_forge_runtime.api import create_app
from harness_forge_runtime.execution_store import ExecutionStore
from harness_forge_runtime.sessions import SessionStore
from test_processes import fixture_popen, processes_module
from test_runner import fixture_run


@pytest.mark.parametrize(
    "state,code,status",
    [
        ("starting", "already_running", 409),
        ("running", "already_running", 409),
        ("awaiting_finalize", "awaiting_finalize", 409),
        ("committed", "commit", 200),
        ("aborted", "abort", 200),
    ],
)
def test_duplicate_execute_never_spawns(tmp_path, monkeypatch, state, code, status):
    module = processes_module()
    turn, settings = fixture_run(tmp_path)
    store = ExecutionStore(settings.runtime_state_root)
    children, _ = fixture_popen(monkeypatch, module)
    with TestClient(create_app(settings=settings, store=store)) as client:
        client.portal.call(store.reserve, turn.run_id, None, [])
        if state == "running":
            client.portal.call(store.mark_running, turn.run_id, 99999999, 99999999)
        elif state != "starting":
            client.portal.call(store.mark_awaiting_finalize, turn.run_id)
            if state in {"committed", "aborted"}:
                client.portal.call(store.finalize, turn.run_id, code)
        response = client.post(
            f"/v1/runs/{turn.run_id}/execute", json=turn.model_dump(mode="json")
        )
        assert response.status_code == status
        assert response.json() == {"decision" if status == 200 else "code": code}
        assert children == []


def test_execute_http_stream_cancel_and_delete_associated_files(tmp_path, monkeypatch):
    module = processes_module()
    turn, settings = fixture_run(tmp_path)
    store = ExecutionStore(settings.runtime_state_root)
    sessions = SessionStore(settings.claude_config_dir)
    children, _ = fixture_popen(monkeypatch, module)
    with TestClient(
        create_app(settings=settings, store=store, sessions=sessions)
    ) as client:
        response = client.post(
            f"/v1/runs/{turn.run_id}/execute", json=turn.model_dump(mode="json")
        )
        assert response.status_code == 200
        assert response.headers["content-type"].startswith("application/x-ndjson")
        events = [json.loads(line) for line in response.text.splitlines()]
        assert events[-1]["type"] == "agent.completed"
        assert len(children) == 1
        assert (
            client.get("/v1/executions").json()[0]["lifecycle"] == "awaiting_finalize"
        )
        assert client.post(f"/v1/runs/{turn.run_id}/cancel").status_code == 204
        assert client.post(f"/v1/runs/{uuid4()}/cancel").status_code == 404
        assert (
            client.post(
                f"/v1/runs/{turn.run_id}/finalize", json={"decision": "abort"}
            ).status_code
            == 204
        )
        assert client.post(f"/v1/runs/{turn.run_id}/cancel").status_code == 204
        assert client.delete(f"/v1/executions/{turn.run_id}").status_code == 204
        assert not any(str(turn.run_id) in path.name for path in store.root.rglob("*"))


def test_path_id_mismatch_and_busy_are_rejected_without_spawn(tmp_path, monkeypatch):
    module = processes_module()
    turn, settings = fixture_run(tmp_path)
    store = ExecutionStore(settings.runtime_state_root)
    children, _ = fixture_popen(monkeypatch, module)
    with TestClient(create_app(settings=settings, store=store)) as client:
        mismatch = client.post(
            f"/v1/runs/{uuid4()}/execute", json=turn.model_dump(mode="json")
        )
        assert mismatch.status_code == 422
        assert client.get("/v1/executions").json() == []
        client.portal.call(store.reserve, uuid4(), None, [])
        response = client.post(
            f"/v1/runs/{turn.run_id}/execute", json=turn.model_dump(mode="json")
        )
        assert response.status_code == 409 and response.json() == {
            "code": "runtime_busy"
        }
        assert children == []


@pytest.mark.parametrize("failure", ["unlink", "fsync"])
def test_failed_auxiliary_delete_retains_terminal_authority(
    tmp_path, monkeypatch, failure
):
    module = processes_module()
    turn, settings = fixture_run(tmp_path)
    store = ExecutionStore(settings.runtime_state_root)
    children, _ = fixture_popen(monkeypatch, module)
    with TestClient(create_app(settings=settings, store=store)) as client:
        assert (
            client.post(
                f"/v1/runs/{turn.run_id}/execute", json=turn.model_dump(mode="json")
            ).status_code
            == 200
        )
        assert (
            client.post(
                f"/v1/runs/{turn.run_id}/finalize", json={"decision": "commit"}
            ).status_code
            == 204
        )
        with monkeypatch.context() as broken:
            if failure == "unlink":
                original = Path.unlink

                def unlink(path, *args, **kwargs):
                    if path.name.endswith("events.log"):
                        raise OSError("fixture delete failure")
                    return original(path, *args, **kwargs)

                broken.setattr(Path, "unlink", unlink)
            else:
                broken.setattr(
                    os,
                    "fsync",
                    lambda fd: (_ for _ in ()).throw(OSError("fixture sync failure")),
                )
            assert client.delete(f"/v1/executions/{turn.run_id}").status_code == 500
        record = client.portal.call(store.get, turn.run_id)
        assert record is not None and record.disposition == "commit"
        duplicate = client.post(
            f"/v1/runs/{turn.run_id}/execute", json=turn.model_dump(mode="json")
        )
        assert duplicate.json() == {"decision": "commit"}
        assert len(children) == 1
        assert client.delete(f"/v1/executions/{turn.run_id}").status_code == 204


def test_startup_cleanup_completes_before_health_ready(tmp_path):
    processes_module()
    import asyncio

    turn, settings = fixture_run(tmp_path)
    store = ExecutionStore(settings.runtime_state_root)
    asyncio.run(store.initialize())
    asyncio.run(store.reserve(turn.run_id, None, []))
    with TestClient(create_app(settings=settings)) as client:
        assert client.get("/health").json() == {"status": "ok", "active_run_id": None}
        assert (
            client.get("/v1/executions").json()[0]["lifecycle"] == "awaiting_finalize"
        )
        assert (
            client.post(
                f"/v1/runs/{turn.run_id}/finalize", json={"decision": "abort"}
            ).status_code
            == 204
        )
