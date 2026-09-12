from __future__ import annotations

import os
import posixpath
import stat
from pathlib import Path
from typing import Any

from harness_forge_runtime.models import ArtifactCandidate, ArtifactManifest


MANIFEST_NAME = "artifact-manifest.json"


def validate_artifacts(
    outputs: Path, profile_config: dict[str, Any]
) -> tuple[ArtifactCandidate, ...]:
    schema_version, allowed_types, max_file_bytes, max_total_bytes = (
        _artifact_policy(profile_config)
    )
    root = Path(outputs)
    try:
        root_info = root.lstat()
    except OSError as exc:
        raise ValueError("outputs root must be an existing directory") from exc
    if stat.S_ISLNK(root_info.st_mode) or not stat.S_ISDIR(root_info.st_mode):
        raise ValueError("outputs root must be a real directory, not a symlink")
    root = root.resolve(strict=True)

    files: dict[str, tuple[Path, int]] = {}
    total = 0
    for path in _output_entries(root):
        relative = path.relative_to(root).as_posix()
        if not _valid_relative_path(relative):
            raise ValueError(f'unsafe output path "{relative}"')
        try:
            path.lstat()
            resolved = path.resolve(strict=True)
        except (OSError, RuntimeError) as exc:
            raise ValueError(f'output "{relative}" must resolve to a file') from exc
        if not resolved.is_relative_to(root):
            raise ValueError(f'output "{relative}" escapes outputs root')
        try:
            info = resolved.stat()
        except OSError as exc:
            raise ValueError(f'output "{relative}" must be a regular file') from exc
        if not stat.S_ISREG(info.st_mode):
            raise ValueError(f'output "{relative}" must be a regular file')
        if info.st_size > max_file_bytes:
            raise ValueError(f'output "{relative}" exceeds max_file_bytes')
        total += info.st_size
        if total > max_total_bytes:
            raise ValueError("outputs exceed max_total_bytes")
        files[relative] = (resolved, info.st_size)

    manifest_file = files.pop(MANIFEST_NAME, None)
    if manifest_file is None:
        if not files:
            return ()
        raise ValueError("nonempty outputs require artifact-manifest.json")

    manifest_path, manifest_size = manifest_file
    with manifest_path.open("rb") as stream:
        manifest_data = stream.read(max_file_bytes + 1)
    if len(manifest_data) > max_file_bytes:
        raise ValueError(f'output "{MANIFEST_NAME}" exceeds max_file_bytes')
    if total + len(manifest_data) - manifest_size > max_total_bytes:
        raise ValueError("outputs exceed max_total_bytes")
    manifest = ArtifactManifest.model_validate_json(manifest_data)
    if manifest.schema_version != schema_version:
        raise ValueError("manifest schema version is not allowed by snapshot")
    for artifact in manifest.artifacts:
        if artifact.type not in allowed_types:
            raise ValueError(f'artifact type "{artifact.type}" is not allowed')
        if (
            not _valid_relative_path(artifact.entry)
            or artifact.entry not in files
        ):
            raise ValueError(f'artifact entry "{artifact.entry}" is not an output file')
    return tuple(manifest.artifacts)


def _artifact_policy(
    profile_config: dict[str, Any],
) -> tuple[int, frozenset[str], int, int]:
    try:
        policy = profile_config["artifacts"]
        schema_version = policy["manifest_schema_version"]
        allowed_types = policy["allowed_types"]
        max_file_bytes = policy["max_file_bytes"]
        max_total_bytes = policy["max_total_bytes"]
    except (KeyError, TypeError) as exc:
        raise ValueError("invalid artifact snapshot policy") from exc
    if (
        type(schema_version) is not int
        or schema_version != 1
        or not isinstance(allowed_types, list)
        or not allowed_types
        or any(not isinstance(value, str) or not value for value in allowed_types)
        or type(max_file_bytes) is not int
        or max_file_bytes <= 0
        or type(max_total_bytes) is not int
        or max_total_bytes <= 0
    ):
        raise ValueError("invalid artifact snapshot policy")
    return (
        schema_version,
        frozenset(allowed_types),
        max_file_bytes,
        max_total_bytes,
    )


def _output_entries(root: Path) -> list[Path]:
    entries: list[Path] = []
    for directory, directory_names, file_names in os.walk(
        root, onerror=_raise_walk_error, followlinks=False
    ):
        base = Path(directory)
        for name in directory_names[:]:
            path = base / name
            if path.is_symlink():
                entries.append(path)
                directory_names.remove(name)
        entries.extend(base / name for name in file_names)
    return entries


def _raise_walk_error(error: OSError) -> None:
    raise ValueError("unable to inspect all outputs") from error


def _valid_relative_path(value: str) -> bool:
    if (
        not value
        or value == "."
        or value.startswith("/")
        or posixpath.normpath(value) != value
        or any(part in ("", ".", "..") for part in value.split("/"))
        or any(character in value for character in "\\:\x00\r\n")
    ):
        return False
    lower = value.lower()
    if any(encoded in lower for encoded in ("%2e", "%2f", "%5c")):
        return False
    return not (value.startswith("projects/") and "/artifacts/" in value)
