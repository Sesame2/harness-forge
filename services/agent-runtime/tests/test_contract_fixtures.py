from __future__ import annotations

import json
from pathlib import Path
from typing import Any

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


def set_nested(
    document: dict[str, Any], path: tuple[str | int, ...], value: object
) -> None:
    target: Any = document
    for segment in path[:-1]:
        target = target[segment]
    target[path[-1]] = value


def test_run_request_fixture() -> None:
    request = RunRequest.model_validate_json(
        (FIXTURES / "run-request.json").read_text()
    )
    assert request.version == "1"
    assert request.profile.id == "geo-analysis"
    assert request.limits.max_turns == 8


def test_run_request_fixture_from_decoded_json_dict() -> None:
    document = json.loads((FIXTURES / "run-request.json").read_text())
    request = RunRequest.model_validate(document)
    assert str(request.run_id) == "00000000-0000-0000-0000-000000000001"


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


@pytest.mark.parametrize("blank", ["", " \t "])
def test_run_request_rejects_blank_source_sdk_session_id(blank: str) -> None:
    document = json.loads((FIXTURES / "run-request.json").read_text())
    document["source_sdk_session_id"] = blank
    with pytest.raises(ValidationError) as exc_info:
        RunRequest.model_validate(document)
    assert ("source_sdk_session_id",) in {
        error["loc"] for error in exc_info.value.errors()
    }


def test_non_blank_identifiers_are_validated_without_being_trimmed() -> None:
    request_document = json.loads((FIXTURES / "run-request.json").read_text())
    request_document["source_sdk_session_id"] = " sdk-session-1 "
    request = RunRequest.model_validate(request_document)
    assert request.source_sdk_session_id == " sdk-session-1 "

    event_document = json.loads(
        (FIXTURES / "runtime-events.ndjson").read_text().splitlines()[3]
    )
    event_document["payload"]["tool_call_id"] = " call-1 "
    event = RuntimeEvent.model_validate(event_document)
    assert isinstance(event.payload, ToolStartedPayload)
    assert event.payload.tool_call_id == " call-1 "


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


def test_runtime_event_fixture_from_decoded_json_dict() -> None:
    documents = [
        json.loads(line)
        for line in (FIXTURES / "runtime-events.ndjson").read_text().splitlines()
    ]
    events = [RuntimeEvent.model_validate(document) for document in documents]
    assert [event.type for event in events] == [
        document["type"] for document in documents
    ]


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
    set_nested(document, field_path, invalid_value)

    with pytest.raises(ValidationError):
        RuntimeEvent.model_validate_json(json.dumps(document))


@pytest.mark.parametrize("blank", ["", " \t "])
def test_runtime_event_rejects_blank_type(blank: str) -> None:
    document = json.loads(
        (FIXTURES / "runtime-events.ndjson").read_text().splitlines()[0]
    )
    document["type"] = blank
    with pytest.raises(ValidationError) as exc_info:
        RuntimeEvent.model_validate(document)
    assert ("type",) in {error["loc"] for error in exc_info.value.errors()}


@pytest.mark.parametrize(
    ("event_type", "field_path"),
    [
        ("tool.started", ("payload", "tool_call_id")),
        ("tool.started", ("payload", "name")),
        ("tool.completed", ("payload", "tool_call_id")),
        ("tool.completed", ("payload", "name")),
        ("artifact.candidate", ("payload", "artifacts", 0, "name")),
        ("artifact.candidate", ("payload", "artifacts", 0, "title")),
        ("agent.completed", ("payload", "candidate_sdk_session_id")),
        ("agent.failed", ("payload", "code")),
        ("agent.failed", ("payload", "message")),
    ],
)
@pytest.mark.parametrize("blank", ["", " \t "])
def test_runtime_event_rejects_blank_identifiers(
    event_type: str, field_path: tuple[str | int, ...], blank: str
) -> None:
    documents = [
        json.loads(line)
        for line in (FIXTURES / "runtime-events.ndjson").read_text().splitlines()
    ]
    document = next(item for item in documents if item["type"] == event_type)
    set_nested(document, field_path, blank)
    with pytest.raises(ValidationError) as exc_info:
        RuntimeEvent.model_validate(document)
    assert any(error["loc"][0] == "payload" for error in exc_info.value.errors())


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
    generic_document = dict(document)
    generic_document["type"] = f"future.{document['type']}"
    generic_event = RuntimeEvent.model_validate_json(json.dumps(generic_document))
    assert type(generic_event.payload) is dict

    with pytest.raises(ValidationError) as exc_info:
        RuntimeEvent.model_validate_json(json.dumps(document))
    assert any(error["loc"][0] == "payload" for error in exc_info.value.errors())


def test_artifact_manifest_fixture() -> None:
    manifest = ArtifactManifest.model_validate_json(
        (FIXTURES / "artifact-manifest.json").read_text()
    )
    assert manifest.schema_version == 1
    assert [artifact.name for artifact in manifest.artifacts] == ["report", "dataset"]


@pytest.mark.parametrize("field", ["name", "title"])
@pytest.mark.parametrize("blank", ["", " \t "])
def test_artifact_manifest_rejects_blank_name_and_title(field: str, blank: str) -> None:
    document = json.loads((FIXTURES / "artifact-manifest.json").read_text())
    document["artifacts"][0][field] = blank
    with pytest.raises(ValidationError) as exc_info:
        ArtifactManifest.model_validate(document)
    assert ("artifacts", 0, field) in {
        error["loc"] for error in exc_info.value.errors()
    }


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
