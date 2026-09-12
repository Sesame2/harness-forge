from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from typing import Literal
from uuid import UUID

from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse, Response

from harness_forge_runtime.errors import ExecutionConflict, InvalidExecutionState
from harness_forge_runtime.execution_store import ExecutionStore
from harness_forge_runtime.models import ContractModel
from harness_forge_runtime.sessions import SessionStore
from harness_forge_runtime.settings import RuntimeSettings


class FinalizeRequest(ContractModel):
    decision: Literal["commit", "abort"]


def create_app(
    *,
    settings: RuntimeSettings | None = None,
    store: ExecutionStore | None = None,
    sessions: SessionStore | None = None,
) -> FastAPI:
    @asynccontextmanager
    async def lifespan(app: FastAPI) -> AsyncIterator[None]:
        execution_store = store or ExecutionStore(
            (settings or RuntimeSettings()).runtime_state_root
        )
        await execution_store.initialize()
        app.state.execution_store = execution_store
        app.state.session_store = sessions or SessionStore(
            (settings or RuntimeSettings()).claude_config_dir
        )
        yield

    app = FastAPI(lifespan=lifespan)

    @app.exception_handler(InvalidExecutionState)
    async def unavailable_execution(
        request: Request, error: InvalidExecutionState
    ) -> JSONResponse:
        return JSONResponse(status_code=500, content={"code": "execution_unavailable"})

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
            async with app.state.execution_store.lifecycle_lock:
                await app.state.execution_store.delete(run_id)
        except ExecutionConflict:
            return JSONResponse(status_code=409, content={"code": "conflict"})
        except OSError:
            return JSONResponse(
                status_code=500, content={"code": "execution_operation_failed"}
            )
        return Response(status_code=204)

    @app.head("/v1/sessions/{session_id}")
    async def head_session(session_id: UUID) -> Response:
        try:
            present = app.state.session_store.exists(str(session_id))
            return Response(status_code=200 if present else 404)
        except (OSError, ValueError):
            return JSONResponse(
                status_code=500, content={"code": "session_operation_failed"}
            )

    @app.delete("/v1/sessions/{session_id}", status_code=204)
    async def delete_session(session_id: UUID) -> Response:
        execution_store = app.state.execution_store
        async with execution_store.lifecycle_lock:
            try:
                present = str(session_id) in app.state.session_store.list_ids()
                if await execution_store.list_unfinalized():
                    if present:
                        return JSONResponse(
                            status_code=409, content={"code": "runtime_busy"}
                        )
                    # Read-only durability retry; never prune an active run's empty bucket.
                    app.state.session_store.sync_deletions()
                    return Response(status_code=204)
                app.state.session_store.delete(str(session_id))
            except (OSError, ValueError):
                return JSONResponse(
                    status_code=500, content={"code": "session_operation_failed"}
                )
        return Response(status_code=204)

    @app.post("/v1/runs/{run_id}/finalize", status_code=204)
    async def finalize_run(run_id: UUID, body: FinalizeRequest) -> Response:
        execution_store = app.state.execution_store
        session_store = app.state.session_store
        async with execution_store.lifecycle_lock:
            record = await execution_store.get(run_id)
            if record is None:
                return JSONResponse(
                    status_code=404, content={"code": "execution_not_found"}
                )
            if record.disposition is not None:
                if record.disposition == body.decision:
                    return Response(status_code=204)
                return JSONResponse(
                    status_code=409, content={"code": "conflicting_decision"}
                )
            if record.lifecycle != "awaiting_finalize":
                return JSONResponse(
                    status_code=409, content={"code": "not_awaiting_finalize"}
                )
            try:
                if body.decision == "commit":
                    if (
                        record.candidate_sdk_session_id is None
                        or record.candidate_durable_at is None
                        or not session_store.validate_candidate(
                            run_id,
                            record.candidate_sdk_session_id,
                            record.source_sdk_session_id,
                            record.baseline_session_ids,
                        )
                    ):
                        return JSONResponse(
                            status_code=409, content={"code": "candidate_not_durable"}
                        )
                    # Keep source staging and ownership after commit and record deletion.
                else:
                    session_store.abort(
                        run_id,
                        record.baseline_session_ids,
                        record.source_sdk_session_id,
                    )
            except (OSError, ValueError):
                return JSONResponse(
                    status_code=500, content={"code": "session_operation_failed"}
                )
            try:
                await execution_store.finalize(run_id, body.decision)
            except (ExecutionConflict, InvalidExecutionState):
                return JSONResponse(status_code=409, content={"code": "conflict"})
            except OSError:
                return JSONResponse(
                    status_code=500, content={"code": "execution_operation_failed"}
                )
        return Response(status_code=204)

    return app
