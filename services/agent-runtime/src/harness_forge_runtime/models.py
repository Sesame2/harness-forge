from __future__ import annotations

from datetime import datetime
from pathlib import PurePosixPath
from typing import Any, Literal
from uuid import UUID

from pydantic import BaseModel, ConfigDict, Field, field_validator, model_validator


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
    run_id: UUID
    project_id: UUID
    conversation_id: UUID
    prompt: str = Field(min_length=1)
    source_sdk_session_id: str | None
    profile: RuntimeProfile
    paths: RunPaths
    limits: RunLimits

    @field_validator("prompt")
    @classmethod
    def require_non_whitespace_prompt(cls, value: str) -> str:
        if not value.strip():
            raise ValueError("prompt must not be blank")
        return value


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
        if not value.strip():
            raise ValueError("artifact name and title must not be blank")
        return value

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


class ToolCompletedPayload(ContractModel):
    tool_call_id: str
    name: str
    outcome: Literal["succeeded", "failed"]
    output: str | None = None
    error: str | None = None

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


class AgentFailedPayload(ContractModel):
    code: str = Field(min_length=1)
    message: str = Field(min_length=1)
    retryable: bool


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
    run_id: UUID
    sequence: int = Field(ge=0)
    type: str = Field(min_length=1)
    occurred_at: datetime = Field(strict=False)
    payload: object

    @field_validator("occurred_at", mode="before")
    @classmethod
    def require_json_datetime_string(cls, value: Any) -> Any:
        if not isinstance(value, str):
            raise ValueError("occurred_at must be a JSON date-time string")
        return value

    @model_validator(mode="before")
    @classmethod
    def parse_known_payload(cls, value: Any) -> Any:
        if not isinstance(value, dict):
            return value
        event_type = value.get("type")
        payload_model = (
            PAYLOAD_MODELS.get(event_type) if isinstance(event_type, str) else None
        )
        payload = value.get("payload")
        if payload_model is None:
            if not isinstance(payload, dict):
                raise ValueError("event payload must be an object")
            return value
        typed = dict(value)
        typed["payload"] = payload_model.model_validate(payload)
        return typed


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
