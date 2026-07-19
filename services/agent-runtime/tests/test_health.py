from fastapi.testclient import TestClient

from harness_forge_runtime.api import create_app


def test_health() -> None:
    response = TestClient(create_app()).get("/health")

    assert response.status_code == 200
    assert response.json() == {"status": "ok", "active_run_id": None}
