from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from uuid import UUID

from fastapi import FastAPI
from fastapi.responses import JSONResponse, Response

from harness_forge_runtime.errors import ExecutionConflict
from harness_forge_runtime.execution_store import ExecutionStore
from harness_forge_runtime.settings import RuntimeSettings


def create_app(
    *,
    settings: RuntimeSettings | None = None,
    store: ExecutionStore | None = None,
) -> FastAPI:
    @asynccontextmanager
    async def lifespan(app: FastAPI) -> AsyncIterator[None]:
        execution_store = store or ExecutionStore(
            (settings or RuntimeSettings()).runtime_state_root
        )
        await execution_store.initialize()
        app.state.execution_store = execution_store
        yield

    app = FastAPI(lifespan=lifespan)

    @app.get("/health")
    async def health() -> dict[str, str | None]:
        execution_store = getattr(app.state, "execution_store", None)
        active_run_id = None
        if execution_store is not None:
            active = next(
                (
                    record
                    for record in await execution_store.list_unfinalized()
                    if record.lifecycle == "running"
                ),
                None,
            )
            active_run_id = str(active.run_id) if active is not None else None
        return {"status": "ok", "active_run_id": active_run_id}

    @app.get("/v1/executions")
    async def list_executions() -> list[dict[str, str | None]]:
        records = await app.state.execution_store.list_unfinalized()
        return [
            {
                "run_id": str(record.run_id),
                "lifecycle": record.lifecycle,
                "candidate_sdk_session_id": record.candidate_sdk_session_id,
            }
            for record in records
        ]

    @app.delete("/v1/executions/{run_id}", status_code=204)
    async def delete_execution(run_id: UUID) -> Response:
        try:
            await app.state.execution_store.delete(run_id)
        except ExecutionConflict:
            return JSONResponse(status_code=409, content={"code": "conflict"})
        return Response(status_code=204)

    return app
