from __future__ import annotations

import importlib
import importlib.util
import json
import os
from pathlib import Path
from uuid import uuid4

import pytest


def sessions(root):
    name = "harness_forge_runtime.sessions"
    assert importlib.util.find_spec(name) is not None, (
        "Session store is not implemented"
    )
    return importlib.import_module(name).SessionStore(root)


def transcript(
    root, bucket="-old-workspace", session_id=None, data=b"opaque\x00\xff\n"
):
    session_id = session_id or str(uuid4())
    project = root / "projects" / bucket
    project.mkdir(parents=True, exist_ok=True)
    main = project / f"{session_id}.jsonl"
    main.write_bytes(data)
    associated = project / session_id / "subagents"
    associated.mkdir(parents=True)
    (associated / "agent-child.jsonl").write_bytes(b"child")
    (associated / "agent-child.meta.json").write_bytes(b"metadata")
    output = project / session_id / "tool-results"
    output.mkdir()
    (output / "large.txt").write_bytes(b"large output")
    return session_id, main


def test_real_layout_empty_partial_and_associated_only_are_listed(tmp_path):
    root = tmp_path / "claude"
    store = sessions(root)
    first, _ = transcript(root, data=b"")
    partial, _ = transcript(root, data=b'{"partial":')
    orphan = str(uuid4())
    (root / "projects" / "-old-workspace" / orphan).mkdir()
    assert store.list_ids() == {first, partial, orphan}
    assert store.exists(first)
    assert not store.exists(orphan)
    store.delete(first)
    store.delete(first)
    assert store.list_ids() == {partial, orphan}


@pytest.mark.parametrize(
    "bad", ["../escape", "/tmp/x", "a/../../x", "bad", str(uuid4()).upper()]
)
def test_rejects_noncanonical_session_ids(tmp_path, bad):
    store = sessions(tmp_path / "claude")
    for operation in (store.exists, store.delete, store.sync_transcript):
        with pytest.raises(ValueError):
            operation(bad)


@pytest.mark.parametrize(
    "node", ["main_link", "tree_link", "fifo", "bucket_link", "root_link"]
)
def test_rejects_links_special_nodes_without_touching_outside(tmp_path, node):
    root = tmp_path / "claude"
    source, main = transcript(root)
    outside = tmp_path / "outside"
    outside.mkdir()
    precious = outside / "precious"
    precious.write_text("untouched")
    if node == "root_link":
        linked = tmp_path / "linked"
        linked.symlink_to(root, target_is_directory=True)
        root = linked
    elif node == "bucket_link":
        (root / "projects" / "-linked").symlink_to(outside, target_is_directory=True)
    elif node == "main_link":
        main.unlink()
        main.symlink_to(precious)
    elif node == "tree_link":
        (main.parent / source / "escape").symlink_to(outside, target_is_directory=True)
    else:
        os.mkfifo(main.parent / source / "pipe")
    with pytest.raises((ValueError, OSError)):
        sessions(root).delete(source)
    assert precious.read_text() == "untouched"


def test_sync_fsyncs_main_associated_files_and_ancestors(tmp_path, monkeypatch):
    root = tmp_path / "claude"
    candidate, main = transcript(root)
    store = sessions(root)
    called = []
    real_fsync = os.fsync

    def fsync(fd):
        info = os.fstat(fd)
        called.append((info.st_dev, info.st_ino))
        real_fsync(fd)

    monkeypatch.setattr(os, "fsync", fsync)
    store.sync_transcript(candidate)
    expected = [
        main,
        *list((main.parent / candidate).rglob("*")),
        main.parent / candidate,
        main.parent,
        root / "projects",
        root,
        root.parent,
    ]
    assert {(p.stat().st_dev, p.stat().st_ino) for p in expected} <= set(called)
    monkeypatch.setattr(os, "fsync", lambda fd: (_ for _ in ()).throw(OSError("disk")))
    with pytest.raises(OSError):
        store.sync_transcript(candidate)


def test_prepare_is_opaque_owned_copy_retained_after_restart(tmp_path):
    root = tmp_path / "claude"
    source, main = transcript(root)
    store = sessions(root)
    run = uuid4()
    cwd = Path("/workspaces") / str(run) / "workspace"
    bucket = store.prepare_run(run, cwd, source)
    staged = bucket / main.name
    assert staged.read_bytes() == main.read_bytes()
    assert staged.stat().st_ino != main.stat().st_ino
    assert (
        bucket / source / "tool-results" / "large.txt"
    ).read_bytes() == b"large output"
    assert store.list_ids() == {source}
    candidate, _ = transcript(root, bucket.name)
    assert sessions(root).validate_candidate(run, candidate, source, (source,))
    sessions(root).sync_transcript(candidate)
    assert main.read_bytes() == b"opaque\x00\xff\n"
    with pytest.raises((ValueError, FileExistsError)):
        sessions(root).prepare_run(run, cwd, source)
    sessions(root).abort(run, (source,), source)
    assert main.exists() and not staged.exists()
    assert store.list_ids() == {source}
    sessions(root).abort(run, (source,), source)


def test_abort_before_init_protects_previous_staging_and_baseline(tmp_path):
    root = tmp_path / "claude"
    source, main = transcript(root)
    store = sessions(root)
    previous, current = uuid4(), uuid4()
    previous_bucket = store.prepare_run(
        previous, Path(f"/workspaces/{previous}/workspace"), source
    )
    baseline_candidate, _ = transcript(root, previous_bucket.name)
    baseline = tuple(store.list_ids())
    bucket = store.prepare_run(
        current, Path(f"/workspaces/{current}/workspace"), source
    )
    unknown_candidate, _ = transcript(root, bucket.name, data=b'{"partial":')
    store.abort(current, baseline, source)
    assert store.list_ids() == {source, baseline_candidate}
    assert main.exists() and (previous_bucket / main.name).exists()
    assert not (bucket / f"{unknown_candidate}.jsonl").exists()


def test_unknown_duplicate_and_existing_target_fail_closed(tmp_path):
    root = tmp_path / "claude"
    source, main = transcript(root)
    store = sessions(root)
    transcript(root, "-unknown", source)
    with pytest.raises(ValueError):
        store.list_ids()
    assert main.exists()


def test_existing_unowned_target_is_never_overwritten_or_removed(tmp_path):
    root = tmp_path / "claude"
    source, main = transcript(root)
    store = sessions(root)
    run = uuid4()
    bucket = f"-workspaces-{run}-workspace"
    target = root / "projects" / bucket
    target.mkdir()
    with pytest.raises(FileExistsError):
        store.prepare_run(run, Path(f"/workspaces/{run}/workspace"), source)
    store.abort(run, (source,), source)
    assert target.exists() and main.exists()


def test_delete_failure_before_candidate_removal_keeps_retry_ownership(
    tmp_path, monkeypatch
):
    root = tmp_path / "claude"
    source, original = transcript(root)
    store = sessions(root)
    run = uuid4()
    bucket = store.prepare_run(run, Path(f"/workspaces/{run}/workspace"), source)
    candidate, main = transcript(root, bucket.name)
    real_remove = store._remove_session

    def fail_copy(bucket_name, session_id):
        if session_id == source:
            raise OSError("disk error")
        real_remove(bucket_name, session_id)

    monkeypatch.setattr(store, "_remove_session", fail_copy)
    with pytest.raises(OSError):
        store.delete(candidate)
    assert main.exists(), "keep candidate locator until its owned copy is cleaned"
    sessions(root).delete(candidate)
    assert original.exists() and not (bucket / original.name).exists()


@pytest.mark.parametrize(
    "field,value",
    [("run_id", None), ("source_id", 42), ("source_bucket", []), ("bucket", 42)],
)
def test_invalid_marker_field_types_fail_closed(tmp_path, field, value):
    root = tmp_path / "claude"
    source, original = transcript(root)
    store = sessions(root)
    run = uuid4()
    bucket = store.prepare_run(run, Path(f"/workspaces/{run}/workspace"), source)
    marker = bucket / ".harness-owner.json"
    data = json.loads(marker.read_text())
    data[field] = value
    marker.write_text(json.dumps(data))
    with pytest.raises(ValueError):
        sessions(root).abort(run, (source,), source)
    assert original.exists()


def test_marked_source_copy_requires_exact_original_bucket(tmp_path):
    root = tmp_path / "claude"
    source, original = transcript(root)
    store = sessions(root)
    run = uuid4()
    bucket = store.prepare_run(run, Path(f"/workspaces/{run}/workspace"), source)
    marker = bucket / ".harness-owner.json"
    data = json.loads(marker.read_text())
    data["source_bucket"] = "-wrong-original"
    marker.write_text(json.dumps(data))
    with pytest.raises(ValueError):
        sessions(root).list_ids()
    assert original.exists()


def test_explicit_purge_candidate_removes_its_copy_not_original_source(tmp_path):
    root = tmp_path / "claude"
    source, main = transcript(root)
    store = sessions(root)
    run = uuid4()
    bucket = store.prepare_run(run, Path(f"/workspaces/{run}/workspace"), source)
    candidate, _ = transcript(root, bucket.name)
    store.delete(candidate)
    assert main.exists()
    assert not (bucket / main.name).exists()
    assert store.list_ids() == {source}
    assert not bucket.exists(), "purged owned bucket must not retain owner metadata"


def test_abort_first_run_prunes_empty_owned_bucket(tmp_path):
    store = sessions(tmp_path / "claude")
    run = uuid4()
    bucket = store.prepare_run(run, Path(f"/workspaces/{run}/workspace"), None)
    store.abort(run, (), None)
    assert not bucket.exists()
    store.abort(run, (), None)


@pytest.mark.parametrize("phase", ["empty", "unowned_nonempty", "owned", "corrupt"])
def test_creation_crash_ownership_and_abort_recovery(tmp_path, monkeypatch, phase):
    root = tmp_path / "claude"
    source, main = transcript(root)
    store = sessions(root)
    run = uuid4()
    cwd = Path(f"/workspaces/{run}/workspace")
    if phase == "owned":
        monkeypatch.setattr(
            os, "rename", lambda *a, **k: (_ for _ in ()).throw(OSError("crash"))
        )
        with pytest.raises(OSError):
            store.prepare_run(run, cwd, source)
    else:
        temporary = root / ".staging" / str(run)
        temporary.mkdir(parents=True)
        if phase == "unowned_nonempty":
            (temporary / "unknown").write_text("not owned")
        if phase == "corrupt":
            (temporary / ".harness-owner.json").write_text(
                json.dumps({"run_id": str(run)})
            )
    if phase in {"corrupt", "unowned_nonempty"}:
        with pytest.raises(ValueError):
            sessions(root).abort(run, (source,), source)
    else:
        sessions(root).abort(run, (source,), source)
        assert not (root / ".staging" / str(run)).exists()
    assert main.exists()


def test_crash_after_atomic_publish_has_exact_abort_ownership(tmp_path, monkeypatch):
    root = tmp_path / "claude"
    source, original = transcript(root)
    store = sessions(root)
    run = uuid4()
    real_rename = os.rename

    def crash_after_publish(*args, **kwargs):
        real_rename(*args, **kwargs)
        raise OSError("crash after rename before parent sync")

    monkeypatch.setattr(os, "rename", crash_after_publish)
    with pytest.raises(OSError):
        store.prepare_run(run, Path(f"/workspaces/{run}/workspace"), source)
    assert sessions(root).list_ids() == {source}
    sessions(root).abort(run, (source,), source)
    assert original.exists()
    assert sessions(root).list_ids() == {source}


def test_unknown_project_metadata_fails_closed(tmp_path):
    root = tmp_path / "claude"
    source, main = transcript(root)
    (main.parent / "unknown-index.json").write_text("{}")
    with pytest.raises(ValueError):
        sessions(root).delete(source)
    assert main.exists()


def test_abort_staging_unlink_failure_keeps_marker_for_retry(tmp_path, monkeypatch):
    root = tmp_path / "claude"
    source, original = transcript(root)
    store = sessions(root)
    run = uuid4()
    real_rename = os.rename
    monkeypatch.setattr(
        os, "rename", lambda *a, **k: (_ for _ in ()).throw(OSError("crash"))
    )
    with pytest.raises(OSError):
        store.prepare_run(run, Path(f"/workspaces/{run}/workspace"), source)
    monkeypatch.setattr(os, "rename", real_rename)
    real_listdir, real_unlink = os.listdir, os.unlink

    def marker_first(fd):
        return sorted(real_listdir(fd), key=lambda name: name != ".harness-owner.json")

    def fail_source_unlink(name, **kwargs):
        if name == f"{source}.jsonl":
            raise OSError("transient deletion failure")
        return real_unlink(name, **kwargs)

    monkeypatch.setattr(os, "listdir", marker_first)
    monkeypatch.setattr(os, "unlink", fail_source_unlink)
    with pytest.raises(OSError):
        store.abort(run, (source,), source)
    assert (root / ".staging" / str(run) / ".harness-owner.json").exists()
    monkeypatch.setattr(os, "unlink", real_unlink)
    sessions(root).abort(run, (source,), source)
    assert original.exists()
    assert not (root / ".staging" / str(run)).exists()


def test_delete_retry_after_unlink_fsync_failure_remains_durable(tmp_path, monkeypatch):
    root = tmp_path / "claude"
    store = sessions(root)
    run = uuid4()
    bucket = store.prepare_run(run, Path(f"/workspaces/{run}/workspace"), None)
    candidate = str(uuid4())
    main = bucket / f"{candidate}.jsonl"
    main.write_bytes(b"main-only transcript")
    real_fsync = os.fsync

    def fail_after_unlink(fd):
        if not main.exists() and os.fstat(fd).st_ino == bucket.stat().st_ino:
            raise OSError("directory sync failed")
        real_fsync(fd)

    monkeypatch.setattr(os, "fsync", fail_after_unlink)
    with pytest.raises(OSError):
        store.delete(candidate)
    synced = []

    def fsync(fd):
        synced.append(os.fstat(fd).st_ino)
        real_fsync(fd)

    monkeypatch.setattr(os, "fsync", fsync)
    sessions(root).delete(candidate)
    assert (root / "projects").stat().st_ino in synced
    assert not bucket.exists()


def test_delete_retry_after_bucket_rmdir_fsync_failure(tmp_path, monkeypatch):
    root = tmp_path / "claude"
    store = sessions(root)
    run = uuid4()
    bucket = store.prepare_run(run, Path(f"/workspaces/{run}/workspace"), None)
    candidate = str(uuid4())
    (bucket / f"{candidate}.jsonl").write_bytes(b"transcript")
    projects_inode = (root / "projects").stat().st_ino
    real_fsync = os.fsync

    def fail_after_rmdir(fd):
        if not bucket.exists() and os.fstat(fd).st_ino == projects_inode:
            raise OSError("parent sync failed")
        real_fsync(fd)

    monkeypatch.setattr(os, "fsync", fail_after_rmdir)
    with pytest.raises(OSError):
        store.delete(candidate)
    synced = []

    def fsync(fd):
        synced.append(os.fstat(fd).st_ino)
        real_fsync(fd)

    monkeypatch.setattr(os, "fsync", fsync)
    sessions(root).delete(candidate)
    assert projects_inode in synced
    assert not bucket.exists()


def test_absent_unmanaged_session_retry_still_syncs_bucket(tmp_path, monkeypatch):
    root = tmp_path / "claude"
    candidate, main = transcript(root)
    store = sessions(root)
    store.delete(candidate)
    synced = []
    real_fsync = os.fsync

    def fsync(fd):
        synced.append(os.fstat(fd).st_ino)
        real_fsync(fd)

    monkeypatch.setattr(os, "fsync", fsync)
    sessions(root).delete(candidate)
    assert main.parent.stat().st_ino in synced
