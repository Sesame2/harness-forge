from __future__ import annotations

import json
from pathlib import Path

import pytest
from pydantic import ValidationError

from harness_forge_runtime.models import (
    AgentCompletedPayload,
    AgentFailedPayload,
    ArtifactCandidatePayload,
    ArtifactManifest,
    AssistantDeltaPayload,
    AssistantMessagePayload,
    PhaseChangedPayload,
    RunRequest,
    RuntimeEvent,
    ToolCompletedPayload,
    ToolStartedPayload,
    is_terminal_event,
)


FIXTURES = Path(__file__).resolve().parents[3] / "tests" / "fixtures" / "contracts"


def test_run_request_fixture() -> None:
    request = RunRequest.model_validate_json(
        (FIXTURES / "run-request.json").read_text()
    )
    assert request.version == "1"
    assert request.profile.id == "geo-analysis"
    assert request.limits.max_turns == 8


@pytest.mark.parametrize(
    ("field", "value", "expected_location"),
    [
        ("run_id", "not-a-uuid", ("run_id",)),
        ("prompt", "", ("prompt",)),
        (
            "paths",
            {"inputs": "relative", "workspace": "/workspace", "outputs": "/outputs"},
            ("paths", "inputs"),
        ),
    ],
)
def test_run_request_rejects_invalid_values(
    field: str, value: object, expected_location: tuple[str, ...]
) -> None:
    document = json.loads((FIXTURES / "run-request.json").read_text())
    document[field] = value
    with pytest.raises(ValidationError) as exc_info:
        RunRequest.model_validate_json(json.dumps(document))
    assert expected_location in {error["loc"] for error in exc_info.value.errors()}


def test_run_request_rejects_unknown_top_level_field() -> None:
    document = json.loads((FIXTURES / "run-request.json").read_text())
    document["unexpected"] = True
    with pytest.raises(ValidationError) as exc_info:
        RunRequest.model_validate_json(json.dumps(document))
    assert ("unexpected",) in {error["loc"] for error in exc_info.value.errors()}


def test_runtime_event_fixture_has_all_typed_payloads() -> None:
    lines = (FIXTURES / "runtime-events.ndjson").read_text().splitlines()
    events = [RuntimeEvent.model_validate_json(line) for line in lines]

    assert events[0].version == "1"
    assert events[0].type == "phase.changed"
    assert events[-1].type == "agent.completed"
    assert [type(event.payload) for event in events] == [
        PhaseChangedPayload,
        AssistantDeltaPayload,
        AssistantMessagePayload,
        ToolStartedPayload,
        ToolCompletedPayload,
        ArtifactCandidatePayload,
        AgentFailedPayload,
        AgentCompletedPayload,
    ]
    candidate = events[5].payload
    completed = events[-1].payload
    assert isinstance(candidate, ArtifactCandidatePayload)
    assert isinstance(completed, AgentCompletedPayload)
    assert completed.artifacts == candidate.artifacts


@pytest.mark.parametrize(
    "occurred_at",
    ["2026-07-19", "2026-07-19T00:00:00"],
)
def test_runtime_event_rejects_non_rfc3339_datetime(occurred_at: str) -> None:
    document = json.loads(
        (FIXTURES / "runtime-events.ndjson").read_text().splitlines()[0]
    )
    document["occurred_at"] = occurred_at
    with pytest.raises(ValidationError) as exc_info:
        RuntimeEvent.model_validate_json(json.dumps(document))
    assert ("occurred_at",) in {error["loc"] for error in exc_info.value.errors()}


def test_runtime_event_accepts_rfc3339_offset_datetime() -> None:
    document = json.loads(
        (FIXTURES / "runtime-events.ndjson").read_text().splitlines()[0]
    )
    document["occurred_at"] = "2026-07-19T08:00:00+08:00"
    event = RuntimeEvent.model_validate_json(json.dumps(document))
    assert event.occurred_at.isoformat() == "2026-07-19T08:00:00+08:00"


def test_unknown_event_fixture_is_parseable_but_not_terminal() -> None:
    unknown_event_fixture = json.dumps(
        {
            "version": "1",
            "run_id": "00000000-0000-0000-0000-000000000001",
            "sequence": 9,
            "type": "agent.paused",
            "occurred_at": "2026-07-19T00:00:08Z",
            "payload": {"reason": "approval"},
        }
    )
    event = RuntimeEvent.model_validate_json(unknown_event_fixture)
    assert event.payload == {"reason": "approval"}
    assert not is_terminal_event(event)


def test_unknown_event_payload_that_matches_known_shape_stays_generic() -> None:
    unknown_event_fixture = json.dumps(
        {
            "version": "1",
            "run_id": "00000000-0000-0000-0000-000000000001",
            "sequence": 10,
            "type": "phase.previewed",
            "occurred_at": "2026-07-19T00:00:09Z",
            "payload": {"phase": "agent"},
        }
    )
    event = RuntimeEvent.model_validate_json(unknown_event_fixture)
    assert type(event.payload) is dict
    assert event.payload == {"phase": "agent"}
    assert not is_terminal_event(event)


def test_only_known_completion_and_failure_events_are_terminal() -> None:
    lines = (FIXTURES / "runtime-events.ndjson").read_text().splitlines()
    events = [RuntimeEvent.model_validate_json(line) for line in lines]
    assert [event.type for event in events if is_terminal_event(event)] == [
        "agent.failed",
        "agent.completed",
    ]


@pytest.mark.parametrize(
    ("event_type", "field_path", "invalid_value"),
    [
        ("phase.changed", ("sequence",), "1"),
        ("agent.failed", ("payload", "retryable"), 1),
        ("artifact.candidate", ("payload", "artifacts", 0, "primary"), 1),
        ("tool.completed", ("payload", "output"), None),
        ("tool.completed", ("payload", "error"), None),
    ],
)
def test_runtime_event_rejects_json_type_coercion_and_explicit_null_optionals(
    event_type: str, field_path: tuple[str | int, ...], invalid_value: object
) -> None:
    documents = [
        json.loads(line)
        for line in (FIXTURES / "runtime-events.ndjson").read_text().splitlines()
    ]
    document = next(item for item in documents if item["type"] == event_type)
    target: object = document
    for segment in field_path[:-1]:
        target = target[segment]  # type: ignore[index]
    target[field_path[-1]] = invalid_value  # type: ignore[index]

    with pytest.raises(ValidationError):
        RuntimeEvent.model_validate_json(json.dumps(document))


@pytest.mark.parametrize(
    "document",
    [
        {
            "version": "1",
            "run_id": "00000000-0000-0000-0000-000000000001",
            "sequence": 1,
            "type": "phase.changed",
            "occurred_at": "2026-07-19T00:00:00Z",
            "payload": {},
        },
        {
            "version": "1",
            "run_id": "00000000-0000-0000-0000-000000000001",
            "sequence": 1,
            "type": "assistant.delta",
            "occurred_at": "2026-07-19T00:00:00Z",
            "payload": {"text": 42},
        },
        {
            "version": "1",
            "run_id": "00000000-0000-0000-0000-000000000001",
            "sequence": 1,
            "type": "artifact.candidate",
            "occurred_at": "2026-07-19T00:00:00Z",
            "payload": {
                "artifacts": [
                    {
                        "name": "map",
                        "title": "Map",
                        "type": "video",
                        "entry": "map.mp4",
                        "primary": True,
                    }
                ]
            },
        },
        {
            "version": "1",
            "run_id": "00000000-0000-0000-0000-000000000001",
            "sequence": 1,
            "type": "tool.completed",
            "occurred_at": "2026-07-19T00:00:00Z",
            "payload": {
                "tool_call_id": "call-1",
                "name": "write",
                "outcome": "failed",
            },
        },
    ],
)
def test_runtime_event_rejects_invalid_known_payloads(
    document: dict[str, object],
) -> None:
    with pytest.raises(ValidationError):
        RuntimeEvent.model_validate(document)


def test_artifact_manifest_fixture() -> None:
    manifest = ArtifactManifest.model_validate_json(
        (FIXTURES / "artifact-manifest.json").read_text()
    )
    assert manifest.schema_version == 1
    assert [artifact.name for artifact in manifest.artifacts] == ["report", "dataset"]


@pytest.mark.parametrize(
    "artifacts",
    [
        [
            {
                "name": "map",
                "title": "Map",
                "type": "html",
                "entry": "map.html",
                "primary": True,
            },
            {
                "name": "map",
                "title": "Data",
                "type": "data",
                "entry": "map.json",
                "primary": False,
            },
        ],
        [
            {
                "name": "map",
                "title": "Map",
                "type": "html",
                "entry": "map.html",
                "primary": True,
            },
            {
                "name": "data",
                "title": "Data",
                "type": "data",
                "entry": "map.json",
                "primary": True,
            },
        ],
        [
            {
                "name": "map",
                "title": "Map",
                "type": "html",
                "entry": "../map.html",
                "primary": True,
            },
        ],
    ],
)
def test_artifact_manifest_rejects_invalid_documents(
    artifacts: list[dict[str, object]],
) -> None:
    with pytest.raises(ValidationError):
        ArtifactManifest.model_validate({"schema_version": 1, "artifacts": artifacts})
