from fastapi import FastAPI


def create_app() -> FastAPI:
    app = FastAPI()

    @app.get("/health")
    async def health() -> dict[str, str | None]:
        return {"status": "ok", "active_run_id": None}

    return app
