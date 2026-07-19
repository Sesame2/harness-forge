FROM python:3.12.11-slim-bookworm

RUN apt-get update \
    && apt-get install -y --no-install-recommends curl \
    && rm -rf /var/lib/apt/lists/* \
    && pip install --no-cache-dir uv

WORKDIR /app
COPY pyproject.toml uv.lock README.md ./
COPY src ./src
RUN uv sync --frozen --no-dev

CMD ["uv", "run", "--no-sync", "uvicorn", "harness_forge_runtime.api:create_app", "--factory", "--host", "0.0.0.0", "--port", "8090"]
