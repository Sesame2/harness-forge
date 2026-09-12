from __future__ import annotations

import json
import os
from pathlib import Path
from typing import Any

import pytest
from pydantic import ValidationError

from harness_forge_runtime.artifacts import validate_artifacts
from harness_forge_runtime.models import ArtifactCandidate


def profile_config(
    *,
    schema_version: int = 1,
    allowed_types: list[str] | None = None,
    max_file_bytes: int = 10 * 1024 * 1024,
    max_total_bytes: int = 50 * 1024 * 1024,
) -> dict[str, Any]:
    return {
        "system_prompt": "Analyze the supplied inputs.",
        "tools": {
            "allowed": ["Read", "Write"],
            "disallowed": ["WebFetch"],
            "permission_mode": "default",
        },
        "artifacts": {
            "manifest_schema_version": schema_version,
            "allowed_types": allowed_types
            if allowed_types is not None
            else ["html", "markdown", "image", "data"],
            "max_file_bytes": max_file_bytes,
            "max_total_bytes": max_total_bytes,
        },
        "inputs": {"accepted_media_types": ["text/csv"]},
    }


def write_manifest(outputs: Path, artifacts: list[dict[str, object]]) -> None:
    (outputs / "artifact-manifest.json").write_text(
        json.dumps({"schema_version": 1, "artifacts": artifacts})
    )


def candidate(
    *,
    name: str = "report",
    kind: str = "html",
    entry: str = "report/index.html",
    primary: bool = True,
) -> dict[str, object]:
    return {
        "name": name,
        "title": name.title(),
        "type": kind,
        "entry": entry,
        "primary": primary,
    }


def test_empty_outputs_without_manifest_has_no_artifact_candidates(
    tmp_path: Path,
) -> None:
    assert validate_artifacts(tmp_path, profile_config()) == ()


def test_nonempty_outputs_require_manifest(tmp_path: Path) -> None:
    (tmp_path / "report.html").write_text("report")

    with pytest.raises(ValueError, match="require artifact-manifest.json"):
        validate_artifacts(tmp_path, profile_config())


def test_returns_only_typed_manifest_candidates(tmp_path: Path) -> None:
    report = tmp_path / "report" / "index.html"
    report.parent.mkdir()
    report.write_text("<html></html>")
    write_manifest(tmp_path, [candidate()])

    result = validate_artifacts(tmp_path, profile_config())

    assert result == (
        ArtifactCandidate(
            name="report",
            title="Report",
            type="html",
            entry="report/index.html",
            primary=True,
        ),
    )
    assert all(item.entry != "artifact-manifest.json" for item in result)


@pytest.mark.parametrize(
    "config",
    [
        {},
        {"artifacts": {}},
        profile_config(schema_version=2),
        profile_config(max_file_bytes=0),
        profile_config(max_total_bytes=0),
        profile_config(allowed_types=[]),
    ],
)
def test_rejects_missing_or_malformed_snapshot_artifact_policy(
    tmp_path: Path, config: dict[str, Any]
) -> None:
    with pytest.raises(ValueError, match="artifact snapshot policy"):
        validate_artifacts(tmp_path, config)


def test_rejects_manifest_schema_version_not_allowed_by_snapshot(
    tmp_path: Path,
) -> None:
    (tmp_path / "artifact-manifest.json").write_text(
        '{"schema_version":2,"artifacts":[]}'
    )

    with pytest.raises(ValidationError):
        validate_artifacts(tmp_path, profile_config())


def test_rejects_boolean_manifest_schema_version(tmp_path: Path) -> None:
    (tmp_path / "artifact-manifest.json").write_text(
        '{"schema_version":true,"artifacts":[]}'
    )

    with pytest.raises(ValidationError):
        validate_artifacts(tmp_path, profile_config())


def test_rejects_artifact_type_disallowed_by_snapshot(tmp_path: Path) -> None:
    entry = tmp_path / "report.html"
    entry.write_text("report")
    write_manifest(tmp_path, [candidate(entry="report.html")])

    with pytest.raises(ValueError, match='type "html" is not allowed'):
        validate_artifacts(tmp_path, profile_config(allowed_types=["data"]))


@pytest.mark.parametrize("as_directory", [False, True])
def test_rejects_missing_or_directory_manifest_entry(
    tmp_path: Path, as_directory: bool
) -> None:
    if as_directory:
        (tmp_path / "missing.html").mkdir()
    write_manifest(tmp_path, [candidate(entry="missing.html")])

    with pytest.raises(ValueError, match="is not an output file"):
        validate_artifacts(tmp_path, profile_config())


def test_rejects_escaping_symlink_even_when_not_manifest_entry(tmp_path: Path) -> None:
    outside = tmp_path.parent / f"{tmp_path.name}-secret.txt"
    outside.write_text("secret")
    (tmp_path / "leak.txt").symlink_to(outside)
    write_manifest(tmp_path, [])

    with pytest.raises(ValueError, match="escapes outputs root"):
        validate_artifacts(tmp_path, profile_config())


def test_allows_safe_internal_symlink_like_go_validator(tmp_path: Path) -> None:
    report = tmp_path / "report"
    report.mkdir()
    (report / "index.html").write_text("report")
    (report / "alias.html").symlink_to("index.html")
    write_manifest(tmp_path, [candidate(entry="report/alias.html")])

    assert validate_artifacts(tmp_path, profile_config())[0].entry == (
        "report/alias.html"
    )


@pytest.mark.parametrize(
    "artifacts",
    [
        [candidate(), candidate(entry="other.html")],
        [candidate(), candidate(name="other", entry="other.html", primary=True)],
    ],
)
def test_reuses_manifest_model_invariants(
    tmp_path: Path, artifacts: list[dict[str, object]]
) -> None:
    (tmp_path / "report").mkdir()
    (tmp_path / "report" / "index.html").write_text("report")
    (tmp_path / "other.html").write_text("other")
    write_manifest(tmp_path, artifacts)

    with pytest.raises(ValidationError):
        validate_artifacts(tmp_path, profile_config())


def test_counts_unreferenced_files_and_manifest_against_snapshot_limits(
    tmp_path: Path,
) -> None:
    (tmp_path / "data.bin").write_bytes(b"x" * 8)
    (tmp_path / "unreferenced.bin").write_bytes(b"x" * 8)
    write_manifest(tmp_path, [candidate(kind="data", entry="data.bin")])
    manifest_size = (tmp_path / "artifact-manifest.json").stat().st_size

    with pytest.raises(ValueError, match="max_file_bytes"):
        validate_artifacts(
            tmp_path,
            profile_config(max_file_bytes=7, max_total_bytes=manifest_size + 16),
        )
    with pytest.raises(ValueError, match="max_total_bytes"):
        validate_artifacts(
            tmp_path,
            profile_config(max_file_bytes=manifest_size, max_total_bytes=manifest_size + 15),
        )


def test_rejects_nonregular_output_tree_entry(tmp_path: Path) -> None:
    os.mkfifo(tmp_path / "pipe")
    write_manifest(tmp_path, [])

    with pytest.raises(ValueError, match="regular file"):
        validate_artifacts(tmp_path, profile_config())


def test_rejects_unreadable_output_subtree(tmp_path: Path) -> None:
    unreadable = tmp_path / "unreadable"
    unreadable.mkdir()
    write_manifest(tmp_path, [])
    unreadable.chmod(0)
    try:
        with pytest.raises(ValueError, match="inspect all outputs"):
            validate_artifacts(tmp_path, profile_config())
    finally:
        unreadable.chmod(0o700)


@pytest.mark.parametrize(
    "unsafe_name",
    ["back\\slash.txt", "colon:name.txt", "encoded%2e.txt", "line\nbreak.txt"],
)
def test_rejects_output_paths_gateway_cannot_serve(
    tmp_path: Path, unsafe_name: str
) -> None:
    (tmp_path / unsafe_name).write_text("unsafe")
    write_manifest(tmp_path, [])

    with pytest.raises(ValueError, match="unsafe output path"):
        validate_artifacts(tmp_path, profile_config())


def test_manifest_cannot_publish_itself(tmp_path: Path) -> None:
    write_manifest(tmp_path, [candidate(entry="artifact-manifest.json")])

    with pytest.raises(ValueError, match="is not an output file"):
        validate_artifacts(tmp_path, profile_config())


@pytest.mark.parametrize("limit", ["file", "total"])
def test_manifest_growth_after_stat_still_obeys_limits(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, limit: str
) -> None:
    write_manifest(tmp_path, [])
    manifest_path = tmp_path / "artifact-manifest.json"
    initial_size = manifest_path.stat().st_size
    original_open = Path.open
    grew = False

    def grow_before_open(path: Path, *args: object, **kwargs: object) -> Any:
        nonlocal grew
        if path == manifest_path and not grew:
            grew = True
            with original_open(path, "ab") as stream:
                stream.write(b" ")
        return original_open(path, *args, **kwargs)

    monkeypatch.setattr(Path, "open", grow_before_open)
    config = profile_config(
        max_file_bytes=initial_size if limit == "file" else initial_size + 1,
        max_total_bytes=initial_size + 1 if limit == "file" else initial_size,
    )

    with pytest.raises(ValueError, match=f"max_{limit}_bytes"):
        validate_artifacts(tmp_path, config)
