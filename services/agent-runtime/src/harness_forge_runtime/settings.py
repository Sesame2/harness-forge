from pathlib import Path
from typing import Self

from pydantic import field_validator, model_validator
from pydantic_settings import BaseSettings, SettingsConfigDict


class RuntimeSettings(BaseSettings):
    model_config = SettingsConfigDict(extra="forbid")

    runtime_state_root: Path = Path("/sessions/executions")
    claude_config_dir: Path = Path("/sessions/claude")
    run_workspace_root: Path = Path("/workspaces")
    session_mount_root: Path = Path("/sessions")

    @field_validator(
        "runtime_state_root",
        "claude_config_dir",
        "run_workspace_root",
        "session_mount_root",
    )
    @classmethod
    def require_absolute_path(cls, value: Path) -> Path:
        if not value.is_absolute():
            raise ValueError("runtime paths must be absolute")
        return value

    @model_validator(mode="after")
    def require_separate_session_children(self) -> Self:
        mount = self.session_mount_root.resolve()
        state = self.runtime_state_root.resolve()
        claude = self.claude_config_dir.resolve()
        if not state.is_relative_to(mount) or state == mount:
            raise ValueError("runtime_state_root must be within the session mount")
        if not claude.is_relative_to(mount) or claude == mount:
            raise ValueError("claude_config_dir must be within the session mount")
        if state.is_relative_to(claude) or claude.is_relative_to(state):
            raise ValueError("runtime state and Claude config paths must not overlap")
        return self
