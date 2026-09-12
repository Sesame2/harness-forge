"""Runtime-private Claude JSONL layout and exact source-copy ownership.

Call mutations only under the parent's lifecycle gate, with SDK writers stopped.
No SDK listing filters, transcript rewriting, global config copies, or automatic GC.
"""

from __future__ import annotations

import json
import os
import re
import shutil
import stat
from collections.abc import Iterator
from contextlib import contextmanager
from dataclasses import dataclass
from pathlib import Path
from typing import Any
from uuid import UUID


OWNER = ".harness-owner.json"
DIRECTORY_FLAGS = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW


def _id(value: str) -> str:
    if not isinstance(value, str) or str(UUID(value)) != value:
        raise ValueError("session ID must be a canonical UUID")
    return value


def _bucket(value: str) -> str:
    if not isinstance(value, str) or not re.fullmatch(r"[A-Za-z0-9-]{1,200}", value):
        raise ValueError("unsupported Claude project bucket")
    return value


@contextmanager
def _directory(path: Path, *, create: bool = False) -> Iterator[int]:
    if not path.is_absolute() or ".." in path.parts:
        raise ValueError("session paths must be absolute without parent traversal")
    fd = os.open("/", DIRECTORY_FLAGS)
    try:
        for component in path.parts[1:]:
            if create:
                try:
                    os.mkdir(component, mode=0o700, dir_fd=fd)
                    os.fsync(fd)
                except FileExistsError:
                    pass
            child = os.open(component, DIRECTORY_FLAGS, dir_fd=fd)
            os.close(fd)
            fd = child
        yield fd
    finally:
        os.close(fd)


@contextmanager
def _file(directory: int, name: str, *, create: bool = False) -> Iterator[int]:
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL if create else os.O_RDONLY
    fd = os.open(name, flags | os.O_NOFOLLOW | os.O_NONBLOCK, 0o600, dir_fd=directory)
    try:
        info = os.fstat(fd)
        if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1:
            raise ValueError("session files must be regular unlinked files")
        yield fd
    finally:
        os.close(fd)


def _kind(directory: int, name: str) -> str:
    info = os.stat(name, dir_fd=directory, follow_symlinks=False)
    if stat.S_ISDIR(info.st_mode):
        return "directory"
    if stat.S_ISREG(info.st_mode) and info.st_nlink == 1:
        return "file"
    raise ValueError("session symlinks, hardlinks and special nodes are forbidden")


def _tree(fd: int, *, sync: bool = False) -> None:
    for name in os.listdir(fd):
        if _kind(fd, name) == "directory":
            child = os.open(name, DIRECTORY_FLAGS, dir_fd=fd)
            try:
                _tree(child, sync=sync)
            finally:
                os.close(child)
        else:
            with _file(fd, name) as file_fd:
                if sync:
                    os.fsync(file_fd)
    if sync:
        os.fsync(fd)


def _remove(fd: int, name: str) -> None:
    try:
        kind = _kind(fd, name)
    except FileNotFoundError:
        return
    if kind == "file":
        os.unlink(name, dir_fd=fd)
    else:
        child = os.open(name, DIRECTORY_FLAGS, dir_fd=fd)
        try:
            _tree(child)  # Validate all descendants before deleting any of them.
            # A partial cleanup must retain its ownership proof for the next retry.
            for entry in sorted(os.listdir(child), key=lambda entry: entry == OWNER):
                _remove(child, entry)
        finally:
            os.close(child)
        os.rmdir(name, dir_fd=fd)
    os.fsync(fd)


def _copy(source: int, target: int, name: str) -> None:
    if _kind(source, name) == "directory":
        os.mkdir(name, mode=0o700, dir_fd=target)
        src = os.open(name, DIRECTORY_FLAGS, dir_fd=source)
        dst = os.open(name, DIRECTORY_FLAGS, dir_fd=target)
        try:
            for entry in os.listdir(src):
                _copy(src, dst, entry)
            os.fsync(dst)
        finally:
            os.close(src)
            os.close(dst)
    else:
        with _file(source, name) as src, _file(target, name, create=True) as dst:
            with (
                os.fdopen(os.dup(src), "rb") as reader,
                os.fdopen(os.dup(dst), "wb") as writer,
            ):
                shutil.copyfileobj(reader, writer)
                writer.flush()
                os.fsync(dst)
    os.fsync(target)


def _owner(fd: int, bucket: str | None = None) -> dict[str, Any] | None:
    if OWNER not in os.listdir(fd):
        return None
    with _file(fd, OWNER) as file_fd, os.fdopen(os.dup(file_fd), "rb") as reader:
        value = json.load(reader)
    if (
        not isinstance(value, dict)
        or set(value) != {"version", "run_id", "bucket", "source_id", "source_bucket"}
        or value["version"] != 1
    ):
        raise ValueError("invalid session ownership marker")
    _id(value["run_id"])
    _bucket(value["bucket"])
    if bucket is not None and value["bucket"] != bucket:
        raise ValueError("session ownership bucket mismatch")
    if value["source_id"] is not None:
        _id(value["source_id"])
        _bucket(value["source_bucket"])
        if value["source_bucket"] == value["bucket"]:
            raise ValueError("source copy must have a distinct original bucket")
    elif value["source_bucket"] is not None:
        raise ValueError("source ownership is incomplete")
    return value


@dataclass(frozen=True)
class _Location:
    bucket: str
    main: bool
    staged: bool


class SessionStore:
    def __init__(self, root: Path) -> None:
        if not root.is_absolute() or ".." in root.parts:
            raise ValueError("session root must be absolute without parent traversal")
        self.root = root
        self.projects = root / "projects"
        self.staging = root / ".staging"

    def _inventory(
        self,
    ) -> tuple[dict[str, list[_Location]], dict[str, dict[str, Any]]]:
        sessions: dict[str, list[_Location]] = {}
        owners: dict[str, dict[str, Any]] = {}
        with _directory(self.projects, create=True) as projects:
            for bucket in os.listdir(projects):
                _bucket(bucket)
                fd = os.open(bucket, DIRECTORY_FLAGS, dir_fd=projects)
                try:
                    owner = _owner(fd, bucket)
                    if owner is not None:
                        if any(
                            item["run_id"] == owner["run_id"]
                            for item in owners.values()
                        ):
                            raise ValueError("duplicate session run ownership")
                        owners[bucket] = owner
                    ids: dict[str, bool] = {}
                    for name in os.listdir(fd):
                        if name == OWNER:
                            continue
                        kind = _kind(fd, name)
                        session_id = _id(
                            name[:-6]
                            if kind == "file" and name.endswith(".jsonl")
                            else name
                        )
                        if kind == "file" and not name.endswith(".jsonl"):
                            raise ValueError("unknown session file layout")
                        ids[session_id] = ids.get(session_id, False) or kind == "file"
                    _tree(fd)
                    for session_id, main in ids.items():
                        staged = owner is not None and owner["source_id"] == session_id
                        sessions.setdefault(session_id, []).append(
                            _Location(bucket, main, staged)
                        )
                finally:
                    os.close(fd)
        for locations in sessions.values():
            originals = [item for item in locations if not item.staged]
            if len(originals) > 1:
                raise ValueError("unowned duplicate session ID")
            if originals and any(
                owners[item.bucket]["source_bucket"] != originals[0].bucket
                for item in locations
                if item.staged
            ):
                raise ValueError("source copy original bucket mismatch")
        return sessions, owners

    def list_ids(self) -> set[str]:
        return set(self._inventory()[0])

    def exists(self, session_id: str) -> bool:
        _id(session_id)
        locations = self._inventory()[0].get(session_id, [])
        return any(item.main and not item.staged for item in locations)

    def validate_candidate(
        self,
        run_id: UUID,
        candidate: str,
        source: str | None,
        baseline: tuple[str, ...],
    ) -> bool:
        try:
            _id(candidate)
        except ValueError:
            return False
        if candidate == source or candidate in baseline:
            return False
        sessions, owners = self._inventory()
        return any(
            item.main
            and not item.staged
            and item.bucket in owners
            and owners[item.bucket]["run_id"] == str(run_id)
            and owners[item.bucket]["source_id"] == source
            for item in sessions.get(candidate, [])
        )

    def prepare_run(self, run_id: UUID, cwd: Path, source: str | None) -> Path:
        """Parent-only: call after durable baseline reservation, before worker launch."""
        run = _id(str(run_id))
        if not cwd.is_absolute() or ".." in cwd.parts:
            raise ValueError("run cwd must be canonical and absolute")
        # Pinned CLI's short-path encoding only; never invent its long-path hash.
        bucket = _bucket(re.sub(r"[^A-Za-z0-9]", "-", str(cwd)))
        sessions, _ = self._inventory()
        original = None
        if source is not None:
            _id(source)
            original = next(
                (
                    item
                    for item in sessions.get(source, [])
                    if not item.staged and item.main
                ),
                None,
            )
            if original is None:
                raise FileNotFoundError("source session transcript is missing")
        with (
            _directory(self.projects) as projects,
            _directory(self.staging, create=True) as staging,
        ):
            if bucket in os.listdir(projects):
                raise FileExistsError("run session bucket already exists")
            os.mkdir(run, mode=0o700, dir_fd=staging)
            os.fsync(staging)
            temporary = os.open(run, DIRECTORY_FLAGS, dir_fd=staging)
            try:
                marker = {
                    "version": 1,
                    "run_id": run,
                    "bucket": bucket,
                    "source_id": source,
                    "source_bucket": original.bucket if original is not None else None,
                }
                with _file(temporary, OWNER, create=True) as marker_fd:
                    with os.fdopen(os.dup(marker_fd), "wb") as writer:
                        writer.write(json.dumps(marker, sort_keys=True).encode())
                        writer.flush()
                        os.fsync(marker_fd)
                os.fsync(temporary)
                if original is not None:
                    with _directory(self.projects / original.bucket) as original_fd:
                        _copy(original_fd, temporary, f"{source}.jsonl")
                        if source in os.listdir(original_fd):
                            _copy(original_fd, temporary, source)
                _tree(temporary, sync=True)
                # ponytail: one runtime lifecycle writer; use renameat2 NOREPLACE if multi-writer is introduced.
                if bucket in os.listdir(projects):
                    raise FileExistsError("run session bucket already exists")
                os.rename(run, bucket, src_dir_fd=staging, dst_dir_fd=projects)
                os.fsync(projects)
                os.fsync(staging)
            finally:
                os.close(temporary)
        self._sync_ancestors(self.projects / bucket)
        return self.projects / bucket

    def sync_transcript(self, session_id: str) -> None:
        _id(session_id)
        location = next(
            (
                item
                for item in self._inventory()[0].get(session_id, [])
                if item.main and not item.staged
            ),
            None,
        )
        if location is None:
            raise FileNotFoundError("session transcript is missing")
        with _directory(self.projects / location.bucket) as fd:
            with _file(fd, f"{session_id}.jsonl") as main:
                os.fsync(main)
            if session_id in os.listdir(fd):
                child = os.open(session_id, DIRECTORY_FLAGS, dir_fd=fd)
                try:
                    _tree(child, sync=True)
                finally:
                    os.close(child)
            os.fsync(fd)
        self._sync_ancestors(self.projects / location.bucket)

    def _sync_ancestors(self, path: Path) -> None:
        while True:
            with _directory(path) as fd:
                os.fsync(fd)
            if path == self.root.parent:
                break
            path = path.parent

    def _remove_session(self, bucket: str, session_id: str) -> None:
        with _directory(self.projects) as projects:
            if bucket not in os.listdir(projects):
                return
        with _directory(self.projects / bucket) as fd:
            _remove(fd, f"{session_id}.jsonl")
            _remove(fd, session_id)
        self._sync_ancestors(self.projects / bucket)

    def _prune_owned_bucket(self, bucket: str) -> None:
        with _directory(self.projects) as projects:
            if bucket not in os.listdir(projects):
                return
            with _directory(self.projects / bucket) as fd:
                if _owner(fd, bucket) is None or set(os.listdir(fd)) != {OWNER}:
                    return
                _remove(fd, OWNER)
            os.rmdir(bucket, dir_fd=projects)
            os.fsync(projects)
        self._sync_ancestors(self.projects)

    def delete(self, session_id: str) -> None:
        """Explicit whole-conversation purge only; caller prohibits active executions."""
        _id(session_id)
        sessions, owners = self._inventory()
        for location in sessions.get(session_id, []):
            owner = owners.get(location.bucket)
            if (
                owner is not None
                and not location.staged
                and owner["source_id"] is not None
            ):
                other_candidates = any(
                    key != session_id
                    and any(
                        item.bucket == location.bucket and not item.staged
                        for item in entries
                    )
                    for key, entries in sessions.items()
                )
                if not other_candidates:
                    self._remove_session(location.bucket, owner["source_id"])
            # Keep the candidate locator until dependent staging cleanup succeeds.
            self._remove_session(location.bucket, session_id)
        self.sync_deletions()
        # No active execution: proven owner-only buckets contain no live session data.
        for bucket in owners:
            self._prune_owned_bucket(bucket)

    def sync_deletions(self) -> None:
        """Retry namespace durability even when a prior unlink lost its ID locator."""
        self._inventory()
        # ponytail: V0 global bucket fsync; add a targeted journal only if scale requires it.
        with _directory(self.projects) as projects:
            for bucket in os.listdir(projects):
                with _directory(self.projects / bucket) as fd:
                    os.fsync(fd)
        self._sync_ancestors(self.projects)

    def abort(
        self, run_id: UUID, baseline: tuple[str, ...], source: str | None
    ) -> None:
        """All writers must have stopped. Repeated cleanup is safe after a crash."""
        run = _id(str(run_id))
        sessions, owners = self._inventory()
        owned = [
            (bucket, owner)
            for bucket, owner in owners.items()
            if owner["run_id"] == run
        ]
        if any(owner["source_id"] != source for _, owner in owned):
            raise ValueError("source ownership mismatch")
        temporary = self.staging / run
        with _directory(self.staging, create=True) as staging:
            if run in os.listdir(staging):
                with _directory(temporary) as fd:
                    marker = _owner(fd)
                    if marker is None:
                        if os.listdir(fd):
                            raise ValueError("unowned nonempty staging directory")
                    elif marker["run_id"] != run or marker["source_id"] != source:
                        raise ValueError("staging ownership mismatch")
                    else:
                        allowed = (
                            {OWNER, source, f"{source}.jsonl"} if source else {OWNER}
                        )
                        if set(os.listdir(fd)) - allowed:
                            raise ValueError("unknown staging contents")
                    _tree(fd)
                _remove(staging, run)
        for session_id in set(sessions) - set(baseline) - {source}:
            self.delete(session_id)
        for bucket, owner in owned:
            if owner["source_id"] is not None:
                self._remove_session(bucket, owner["source_id"])
            self._prune_owned_bucket(bucket)
        self.sync_deletions()
        self._sync_ancestors(self.staging)
