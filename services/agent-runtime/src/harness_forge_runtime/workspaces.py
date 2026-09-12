from __future__ import annotations

import os
import stat
from pathlib import Path
from typing import NamedTuple

from harness_forge_runtime.models import RunRequest


class ValidatedRunPaths(NamedTuple):
    inputs: Path
    workspace: Path
    outputs: Path


def validate_workspace(
    request: RunRequest, run_workspace_root: Path
) -> ValidatedRunPaths:
    root = Path(run_workspace_root)
    if not root.is_absolute():
        raise ValueError("run workspace root must be absolute")
    try:
        root_info = root.lstat()
    except OSError as exc:
        raise ValueError("run workspace root must be an existing directory") from exc
    if stat.S_ISLNK(root_info.st_mode):
        raise ValueError("run workspace root must not be a symlink")
    if not stat.S_ISDIR(root_info.st_mode):
        raise ValueError("run workspace root must be an existing directory")

    resolved_root = root.resolve(strict=True)
    resolved: dict[str, Path] = {}
    for name in ("inputs", "workspace", "outputs"):
        raw = getattr(request.paths, name)
        if not isinstance(raw, str) or not Path(raw).is_absolute():
            raise ValueError(f"{name} path must be absolute")
        path = Path(raw)
        if ".." in path.parts:
            raise ValueError(f"{name} path must not contain parent traversal")
        try:
            info = path.lstat()
            actual = path.resolve(strict=True)
        except OSError as exc:
            raise ValueError(f"{name} must be an existing directory") from exc
        expected = resolved_root / str(request.run_id) / name
        if actual != expected:
            raise ValueError(f"{name} must match the canonical run workspace")
        if stat.S_ISLNK(info.st_mode) or not stat.S_ISDIR(info.st_mode):
            raise ValueError(f"{name} must be an existing directory")
        resolved[name] = actual

    write_bits = stat.S_IWUSR | stat.S_IWGRP | stat.S_IWOTH
    if resolved["inputs"].stat().st_mode & write_bits or _has_access(
        resolved["inputs"], os.W_OK
    ):
        raise ValueError("inputs must not be writable")
    for name in ("workspace", "outputs"):
        if not resolved[name].stat().st_mode & write_bits or not _has_access(
            resolved[name], os.W_OK | os.X_OK
        ):
            raise ValueError(f"{name} must be writable")

    return ValidatedRunPaths(**resolved)


def _has_access(path: Path, mode: int) -> bool:
    if os.access in os.supports_effective_ids:
        return os.access(path, mode, effective_ids=True)
    return os.access(path, mode)
