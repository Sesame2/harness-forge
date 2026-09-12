from __future__ import annotations

import os
from pathlib import Path
from uuid import UUID, uuid4

import pytest

from harness_forge_runtime.models import RunLimits, RunPaths, RunRequest, RuntimeProfile
from harness_forge_runtime.workspaces import validate_workspace


def run_request(run_id: UUID, paths: RunPaths) -> RunRequest:
    return RunRequest(
        version="1",
        run_id=run_id,
        project_id=uuid4(),
        conversation_id=uuid4(),
        prompt="Analyze the inputs",
        source_sdk_session_id=None,
        profile=RuntimeProfile(
            id="geo-analysis",
            version="1",
            digest="sha256:test",
            config={},
        ),
        paths=paths,
        limits=RunLimits(max_turns=8, max_budget_usd=2.0),
    )


def materialized_workspace(root: Path, run_id: UUID) -> RunPaths:
    run_root = root / str(run_id)
    for name in ("inputs", "workspace", "outputs"):
        (run_root / name).mkdir(parents=True, exist_ok=True)
    os.chmod(run_root / "inputs", 0o550)
    os.chmod(run_root / "workspace", 0o770)
    os.chmod(run_root / "outputs", 0o770)
    return RunPaths(
        inputs=str(run_root / "inputs"),
        workspace=str(run_root / "workspace"),
        outputs=str(run_root / "outputs"),
    )


def test_validates_existing_go_materialized_workspace(tmp_path: Path) -> None:
    run_id = uuid4()
    paths = materialized_workspace(tmp_path, run_id)

    validated = validate_workspace(run_request(run_id, paths), tmp_path)

    assert validated.inputs == (tmp_path / str(run_id) / "inputs").resolve()
    assert validated.workspace == (tmp_path / str(run_id) / "workspace").resolve()
    assert validated.outputs == (tmp_path / str(run_id) / "outputs").resolve()
    with pytest.raises(AttributeError):
        validated.outputs = tmp_path  # type: ignore[misc]


def test_rejects_paths_for_another_run(tmp_path: Path) -> None:
    requested_run_id = uuid4()
    other_paths = materialized_workspace(tmp_path, uuid4())

    with pytest.raises(ValueError, match="canonical run workspace"):
        validate_workspace(run_request(requested_run_id, other_paths), tmp_path)


@pytest.mark.parametrize("bad_path", ["relative/inputs", "/tmp/../tmp/inputs"])
def test_rejects_relative_or_parent_traversal_paths(
    tmp_path: Path, bad_path: str
) -> None:
    run_id = uuid4()
    paths = materialized_workspace(tmp_path, run_id)
    malformed = RunPaths.model_construct(
        inputs=bad_path,
        workspace=paths.workspace,
        outputs=paths.outputs,
    )
    request = run_request(run_id, paths).model_copy(update={"paths": malformed})

    with pytest.raises(ValueError, match="absolute|parent traversal"):
        validate_workspace(request, tmp_path)


def test_rejects_missing_workspace_directory_without_creating_it(
    tmp_path: Path,
) -> None:
    run_id = uuid4()
    paths = materialized_workspace(tmp_path, run_id)
    missing = Path(paths.workspace)
    missing.rmdir()

    with pytest.raises(ValueError, match="existing directory"):
        validate_workspace(run_request(run_id, paths), tmp_path)

    assert not missing.exists()


def test_rejects_symlink_workspace_root(tmp_path: Path) -> None:
    real_root = tmp_path / "real"
    real_root.mkdir()
    root_link = tmp_path / "workspaces"
    root_link.symlink_to(real_root, target_is_directory=True)
    run_id = uuid4()
    paths = materialized_workspace(real_root, run_id)

    with pytest.raises(ValueError, match="symlink"):
        validate_workspace(run_request(run_id, paths), root_link)


@pytest.mark.parametrize(
    ("directory", "mode", "message"),
    [
        ("inputs", 0o770, "inputs must not be writable"),
        ("workspace", 0o550, "workspace must be writable"),
        ("outputs", 0o550, "outputs must be writable"),
    ],
)
def test_enforces_go_workspace_permissions(
    tmp_path: Path, directory: str, mode: int, message: str
) -> None:
    run_id = uuid4()
    paths = materialized_workspace(tmp_path, run_id)
    os.chmod(Path(getattr(paths, directory)), mode)

    with pytest.raises(ValueError, match=message):
        validate_workspace(run_request(run_id, paths), tmp_path)
