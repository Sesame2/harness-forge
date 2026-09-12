from pathlib import Path
from uuid import uuid4

from fastapi.testclient import TestClient

from harness_forge_runtime.api import create_app
from harness_forge_runtime.execution_store import ExecutionStore


def test_list_executions_returns_only_unfinalized_records(tmp_path: Path) -> None:
    store = ExecutionStore(tmp_path / "executions")
    with TestClient(create_app(store=store)) as client:
        awaiting_id, terminal_id = uuid4(), uuid4()
        client.portal.call(store.reserve, terminal_id, None, [])
        client.portal.call(store.mark_awaiting_finalize, terminal_id)
        client.portal.call(store.finalize, terminal_id, "abort")
        client.portal.call(store.reserve, awaiting_id, None, [])
        client.portal.call(store.mark_running, awaiting_id, 10, 10)
        client.portal.call(store.record_candidate, awaiting_id, "candidate")
        client.portal.call(store.mark_awaiting_finalize, awaiting_id)

        response = client.get("/v1/executions")

    assert response.status_code == 200
    assert response.json() == [
        {
            "run_id": str(awaiting_id),
            "lifecycle": "awaiting_finalize",
            "candidate_sdk_session_id": "candidate",
        }
    ]


def test_delete_execution_is_idempotent_and_rejects_unfinalized(
    tmp_path: Path,
) -> None:
    store = ExecutionStore(tmp_path / "executions")
    with TestClient(create_app(store=store)) as client:
        active_id, missing_id = uuid4(), uuid4()
        client.portal.call(store.reserve, active_id, None, [])

        conflict = client.delete(f"/v1/executions/{active_id}")
        assert conflict.status_code == 409
        assert conflict.json() == {"code": "conflict"}

        client.portal.call(store.mark_awaiting_finalize, active_id)
        client.portal.call(store.finalize, active_id, "abort")
        assert client.delete(f"/v1/executions/{active_id}").status_code == 204
        assert client.delete(f"/v1/executions/{active_id}").status_code == 204
        assert client.delete(f"/v1/executions/{missing_id}").status_code == 204


def test_delete_execution_rejects_invalid_run_id_without_path_access(
    tmp_path: Path,
) -> None:
    store = ExecutionStore(tmp_path / "executions")
    with TestClient(create_app(store=store)) as client:
        response = client.delete("/v1/executions/not-a-uuid")

    assert response.status_code == 422


def test_storage_failure_returns_safe_codes_and_remains_fail_closed(
    tmp_path, monkeypatch
):
    store = ExecutionStore(tmp_path / "executions")
    with TestClient(create_app(store=store)) as client:
        run = uuid4()
        client.portal.call(store.reserve, run, None, [])
        client.portal.call(store.mark_awaiting_finalize, run)
        client.portal.call(store.finalize, run, "abort")
        monkeypatch.setattr(
            store,
            "_fsync_root",
            lambda: (_ for _ in ()).throw(OSError("secret storage path")),
        )
        response = client.delete(f"/v1/executions/{run}")
        assert response.status_code == 500
        assert response.json() == {"code": "execution_operation_failed"}
        retry = client.get("/v1/executions")
        assert retry.status_code == 500
        assert retry.json() == {"code": "execution_unavailable"}
