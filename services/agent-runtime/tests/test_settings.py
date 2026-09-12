from pathlib import Path

import pytest
from pydantic import ValidationError

from harness_forge_runtime.settings import RuntimeSettings


def test_settings_use_runtime_container_defaults() -> None:
    settings = RuntimeSettings(_env_file=None)

    assert settings.runtime_state_root == Path("/sessions/executions")
    assert settings.claude_config_dir == Path("/sessions/claude")
    assert settings.run_workspace_root == Path("/workspaces")


@pytest.mark.parametrize(
    ("field", "value"),
    [
        ("runtime_state_root", "relative/executions"),
        ("claude_config_dir", "relative/claude"),
        ("run_workspace_root", "relative/workspaces"),
    ],
)
def test_settings_require_absolute_paths(field: str, value: str) -> None:
    with pytest.raises(ValidationError):
        RuntimeSettings(_env_file=None, **{field: value})


def test_session_paths_must_be_children_of_the_mount(tmp_path: Path) -> None:
    mount_root = tmp_path / "sessions"
    with pytest.raises(ValidationError):
        RuntimeSettings(
            _env_file=None,
            session_mount_root=mount_root,
            runtime_state_root=tmp_path / "outside",
            claude_config_dir=mount_root / "claude",
            run_workspace_root=tmp_path / "workspaces",
        )


@pytest.mark.parametrize("runtime_is_parent", [True, False])
def test_session_paths_must_not_be_nested(
    tmp_path: Path, runtime_is_parent: bool
) -> None:
    mount_root = tmp_path / "sessions"
    parent = mount_root / "shared"
    child = parent / "nested"
    runtime_root, claude_root = (parent, child) if runtime_is_parent else (child, parent)
    with pytest.raises(ValidationError):
        RuntimeSettings(
            _env_file=None,
            session_mount_root=mount_root,
            runtime_state_root=runtime_root,
            claude_config_dir=claude_root,
            run_workspace_root=tmp_path / "workspaces",
        )


def test_session_mount_root_can_be_injected_for_tests(tmp_path: Path) -> None:
    mount_root = tmp_path / "sessions"
    settings = RuntimeSettings(
        _env_file=None,
        session_mount_root=mount_root,
        runtime_state_root=mount_root / "executions",
        claude_config_dir=mount_root / "claude",
        run_workspace_root=tmp_path / "workspaces",
    )

    assert settings.runtime_state_root == mount_root / "executions"
