from pathlib import Path
from typing import Literal, Self
from hashlib import sha256
import json
from urllib.parse import urlsplit, urlunsplit

from pydantic import Field, field_validator, model_validator
from pydantic_settings import BaseSettings, SettingsConfigDict


class RuntimeSettings(BaseSettings):
    model_config = SettingsConfigDict(extra="forbid", hide_input_in_errors=True)

    runtime_state_root: Path = Path("/sessions/executions")
    claude_config_dir: Path = Path("/sessions/claude")
    run_workspace_root: Path = Path("/workspaces")
    session_mount_root: Path = Path("/sessions")
    hf_model_backend: Literal["native", "openai-chat", "openai-responses"] = "native"
    hf_gateway_url: str = ""
    hf_gateway_key: str = Field(default="", repr=False)
    hf_openai_base_url: str = ""
    hf_openai_model: str = ""

    @property
    def backend_identity(self) -> str:
        if self.hf_model_backend == "native":
            return "native"
        return sha256(
            json.dumps(
                [self.hf_model_backend, self.hf_openai_base_url, self.hf_openai_model],
                separators=(",", ":"),
            ).encode()
        ).hexdigest()

    @property
    def effective_claude_config_dir(self) -> Path:
        # Keep the serialized base unchanged when the worker reparses its environment.
        if self.hf_model_backend == "native":
            return self.claude_config_dir
        return self.claude_config_dir / ".harness-backends" / self.backend_identity

    @model_validator(mode="after")
    def require_backend_config(self) -> Self:
        if self.hf_model_backend == "native":
            return self
        if not self.hf_gateway_key.strip() or not self.hf_openai_model.strip():
            raise ValueError("backend credentials and model are required")
        for field in ("hf_gateway_url", "hf_openai_base_url"):
            value = getattr(self, field)
            try:
                parsed = urlsplit(value)
                port = parsed.port
                allowed = (
                    {"https"} if field == "hf_openai_base_url" else {"http", "https"}
                )
                if (
                    parsed.scheme not in allowed
                    or not parsed.hostname
                    or parsed.username is not None
                    or parsed.password is not None
                    or "?" in value
                    or "#" in value
                    or "\\" in value
                    or any(char.isspace() or ord(char) < 32 for char in value)
                ):
                    raise ValueError
                host = parsed.hostname.lower()
                if ":" in host:
                    host = f"[{host}]"
                if (
                    port is not None
                    and port != {"http": 80, "https": 443}[parsed.scheme]
                ):
                    host += f":{port}"
                path = parsed.path.rstrip("/")
                if not path and field == "hf_openai_base_url":
                    path = "/v1"
                setattr(self, field, urlunsplit((parsed.scheme, host, path, "", "")))
            except ValueError:
                raise ValueError("invalid backend URL") from None
        return self

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
