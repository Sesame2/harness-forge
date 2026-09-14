import hashlib
import tomllib
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[3]
PROFILE = ROOT / "profiles/geo-analysis"


def test_vendored_echarts_assets():
    assets = PROFILE / "workspace-template/assets"
    expected = {
        "echarts.min.js": "bf4a223524e40b77c304bec67e1222cf551f14880cf42c69dc046558e11c07b1",
        "LICENSE.echarts.txt": "634293835b43a6dd2094fa39182a3d9a6b9ca43b7fdb9ac354e8037af2a3093a",
    }
    for name, digest in expected.items():
        assert (assets / name).is_file(), name
        assert hashlib.sha256((assets / name).read_bytes()).hexdigest() == digest
    instructions = (PROFILE / "workspace-template/README.md").read_text()
    assert "outputs/report/index.html" in instructions
    assert "./vendor/echarts.min.js" in instructions


def test_profile_policy_and_prompt():
    config = yaml.safe_load((PROFILE / "profile.yaml").read_text())
    assert config == {
        "id": "geo-analysis",
        "version": "1",
        "display_name": "Geo Analysis",
        "system_prompt": "system-prompt.md",
        "workspace_template": "workspace-template",
        "tools": {
            "allowed": ["Read", "Write", "Edit", "Bash", "Glob", "Grep"],
            "disallowed": ["WebFetch", "WebSearch", "NotebookEdit"],
            "permission_mode": "default",
        },
        "agent": {"max_turns": 8, "max_budget_usd": 2.0},
        "inputs": {"accepted_media_types": ["text/csv", "application/geo+json"]},
        "artifacts": {
            "manifest_schema_version": 1,
            "allowed_types": ["html", "markdown", "image", "data"],
            "max_file_bytes": 10485760,
            "max_total_bytes": 52428800,
        },
    }
    prompt = (PROFILE / "system-prompt.md").read_text()
    for required in (
        "Python",
        "fields",
        "types",
        "missing",
        "Project inputs",
        "workspace",
        "outputs",
        "artifact-manifest.json",
        "uncomputed",
        "workspace/assets/echarts.min.js",
        "LICENSE.echarts.txt",
        "outputs/report/vendor/",
        "outputs/report/index.html",
        "./vendor/echarts.min.js",
        "outputs/data/analysis-evidence.json",
        "input_digest",
        "row_count",
        "computed_fields",
    ):
        assert required in prompt


def test_dependency_lock_and_runtime_user():
    runtime = ROOT / "services/agent-runtime"
    dependencies = tomllib.loads((runtime / "pyproject.toml").read_text())["project"][
        "dependencies"
    ]
    locked = {
        package["name"]: package["version"]
        for package in tomllib.loads((runtime / "uv.lock").read_text())["package"]
    }
    for name, version in {
        "pandas": "2.3.1",
        "geopandas": "1.1.1",
        "shapely": "2.1.1",
        "pyproj": "3.7.1",
        "duckdb": "1.3.2",
        "pyarrow": "20.0.0",
    }.items():
        assert f"{name}=={version}" in dependencies
        assert locked[name] == version
    dockerfile = (runtime / "Dockerfile").read_text()
    assert dockerfile.startswith("FROM python:3.12.11-slim-bookworm\n")
    assert "uv sync --frozen --no-dev" in dockerfile
    assert "USER 10001:10001" in dockerfile
    assert 'ENV PATH="/app/.venv/bin:$PATH"' in dockerfile
    copies = [line for line in dockerfile.splitlines() if line.startswith("COPY ")]
    assert copies == ["COPY pyproject.toml uv.lock README.md ./", "COPY src ./src"]
