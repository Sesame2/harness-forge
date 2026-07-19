from __future__ import annotations

import re
from datetime import datetime
from pathlib import PurePosixPath
from typing import Any, Literal
from uuid import UUID

from pydantic import (
    BaseModel,
    ConfigDict,
    Field,
    ValidationInfo,
    field_validator,
    model_validator,
)


RFC3339_DATETIME = re.compile(
    r"^\d{4}-\d{2}-\d{2}T(?:[01]\d|2[0-3]):[0-5]\d:[0-5]\d"
    r"(?:\.\d+)?(?:Z|[+-](?:[01]\d|2[0-3]):[0-5]\d)$"
)


def require_non_blank(value: str, label: str) -> str:
    if not value.strip():
        raise ValueError(f"{label} must not be blank")
    return value


class ContractModel(BaseModel):
    model_config = ConfigDict(extra="forbid", strict=True)


class RuntimeProfile(ContractModel):
    id: str = Field(min_length=1)
    version: str = Field(min_length=1)
    digest: str = Field(min_length=1)
    config: dict[str, Any]


class RunPaths(ContractModel):
    inputs: str
    workspace: str
    outputs: str

    @field_validator("inputs", "workspace", "outputs")
    @classmethod
    def require_absolute_container_path(cls, value: str) -> str:
        if not value or not PurePosixPath(value).is_absolute():
            raise ValueError("container paths must be absolute")
        return value


class RunLimits(ContractModel):
    max_turns: int = Field(ge=1)
    max_budget_usd: float = Field(ge=0)


class RunRequest(ContractModel):
    version: Literal["1"]
    run_id: UUID = Field(strict=False)
    project_id: UUID = Field(strict=False)
    conversation_id: UUID = Field(strict=False)
    prompt: str = Field(min_length=1)
    source_sdk_session_id: str | None
    profile: RuntimeProfile
    paths: RunPaths
    limits: RunLimits

    @field_validator("run_id", "project_id", "conversation_id", mode="before")
    @classmethod
    def require_uuid_or_json_string(cls, value: Any) -> Any:
        if not isinstance(value, (str, UUID)):
            raise ValueError("UUID fields must be UUID values or JSON strings")
        return value

    @field_validator("source_sdk_session_id")
    @classmethod
    def require_non_blank_source_sdk_session_id(cls, value: str | None) -> str | None:
        if value is not None:
            require_non_blank(value, "source_sdk_session_id")
        return value

    @field_validator("prompt")
    @classmethod
    def require_non_whitespace_prompt(cls, value: str) -> str:
        return require_non_blank(value, "prompt")


ArtifactType = Literal["html", "markdown", "image", "data"]


class ArtifactCandidate(ContractModel):
    name: str = Field(min_length=1)
    title: str = Field(min_length=1)
    type: ArtifactType
    entry: str = Field(min_length=1)
    primary: bool

    @field_validator("name", "title")
    @classmethod
    def require_non_whitespace_text(cls, value: str) -> str:
        return require_non_blank(value, "artifact name and title")

    @field_validator("entry")
    @classmethod
    def require_safe_relative_entry(cls, value: str) -> str:
        entry = PurePosixPath(value)
        if entry.is_absolute() or value == "." or ".." in entry.parts:
            raise ValueError("artifact entry must be a safe relative path")
        return value


class PhaseChangedPayload(ContractModel):
    phase: Literal["preparing", "agent"]


class AssistantDeltaPayload(ContractModel):
    text: str


class AssistantMessagePayload(ContractModel):
    text: str


class ToolStartedPayload(ContractModel):
    tool_call_id: str
    name: str
    input: dict[str, Any]

    @field_validator("tool_call_id", "name")
    @classmethod
    def require_non_blank_identifier(cls, value: str) -> str:
        return require_non_blank(value, "tool identifier")


class ToolCompletedPayload(ContractModel):
    tool_call_id: str
    name: str
    outcome: Literal["succeeded", "failed"]
    output: str | None = None
    error: str | None = None

    @field_validator("tool_call_id", "name")
    @classmethod
    def require_non_blank_identifier(cls, value: str) -> str:
        return require_non_blank(value, "tool identifier")

    @model_validator(mode="before")
    @classmethod
    def reject_explicit_null_optionals(cls, value: Any) -> Any:
        if isinstance(value, dict):
            for field_name in ("output", "error"):
                if field_name in value and value[field_name] is None:
                    raise ValueError(f"{field_name} must be a string when present")
        return value

    @model_validator(mode="after")
    def failed_outcome_requires_error(self) -> ToolCompletedPayload:
        if self.outcome == "failed" and self.error is None:
            raise ValueError("failed tool completion requires error")
        return self


class ArtifactCandidatePayload(ContractModel):
    artifacts: list[ArtifactCandidate]


class AgentCompletedPayload(ContractModel):
    candidate_sdk_session_id: str = Field(min_length=1)
    artifacts: list[ArtifactCandidate]

    @field_validator("candidate_sdk_session_id")
    @classmethod
    def require_non_blank_candidate_session_id(cls, value: str) -> str:
        return require_non_blank(value, "candidate_sdk_session_id")


class AgentFailedPayload(ContractModel):
    code: str = Field(min_length=1)
    message: str = Field(min_length=1)
    retryable: bool

    @field_validator("code", "message")
    @classmethod
    def require_non_blank_failure_text(cls, value: str) -> str:
        return require_non_blank(value, "agent failure field")


KnownPayload = (
    PhaseChangedPayload
    | AssistantDeltaPayload
    | AssistantMessagePayload
    | ToolStartedPayload
    | ToolCompletedPayload
    | ArtifactCandidatePayload
    | AgentCompletedPayload
    | AgentFailedPayload
)

PAYLOAD_MODELS: dict[str, type[ContractModel]] = {
    "phase.changed": PhaseChangedPayload,
    "assistant.delta": AssistantDeltaPayload,
    "assistant.message": AssistantMessagePayload,
    "tool.started": ToolStartedPayload,
    "tool.completed": ToolCompletedPayload,
    "artifact.candidate": ArtifactCandidatePayload,
    "agent.completed": AgentCompletedPayload,
    "agent.failed": AgentFailedPayload,
}


class RuntimeEvent(ContractModel):
    version: Literal["1"]
    run_id: UUID = Field(strict=False)
    sequence: int = Field(ge=0)
    type: str = Field(min_length=1)
    occurred_at: datetime = Field(strict=False)
    payload: object

    @field_validator("type")
    @classmethod
    def require_non_blank_type(cls, value: str) -> str:
        return require_non_blank(value, "runtime event type")

    @field_validator("run_id", mode="before")
    @classmethod
    def require_run_uuid_or_json_string(cls, value: Any) -> Any:
        if not isinstance(value, (str, UUID)):
            raise ValueError("run_id must be a UUID value or JSON string")
        return value

    @field_validator("occurred_at", mode="before")
    @classmethod
    def require_json_datetime_string(cls, value: Any) -> Any:
        if not isinstance(value, str) or RFC3339_DATETIME.fullmatch(value) is None:
            raise ValueError("occurred_at must be an RFC3339 date-time with timezone")
        return value

    @field_validator("payload", mode="before")
    @classmethod
    def parse_known_payload(cls, payload: Any, info: ValidationInfo) -> object:
        if not isinstance(payload, dict):
            raise ValueError("event payload must be an object")
        event_type = info.data.get("type")
        payload_model = (
            PAYLOAD_MODELS.get(event_type) if isinstance(event_type, str) else None
        )
        if payload_model is None:
            return payload
        return payload_model.model_validate(payload)


def is_terminal_event(event: RuntimeEvent) -> bool:
    return (
        event.type == "agent.completed"
        and isinstance(event.payload, AgentCompletedPayload)
    ) or (
        event.type == "agent.failed" and isinstance(event.payload, AgentFailedPayload)
    )


class ArtifactManifest(ContractModel):
    schema_version: Literal[1]
    artifacts: list[ArtifactCandidate]

    @model_validator(mode="after")
    def enforce_manifest_invariants(self) -> ArtifactManifest:
        names = [artifact.name for artifact in self.artifacts]
        if len(names) != len(set(names)):
            raise ValueError("artifact names must be unique")
        if sum(artifact.primary for artifact in self.artifacts) > 1:
            raise ValueError("at most one artifact may be primary")
        return self
