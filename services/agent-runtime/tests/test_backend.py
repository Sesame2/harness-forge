from __future__ import annotations

import asyncio
import os
import re
from pathlib import Path
from uuid import uuid4

import pytest
from fastapi.testclient import TestClient
from pydantic import ValidationError

from harness_forge_runtime.api import create_app
from harness_forge_runtime.execution_store import ExecutionStore
from harness_forge_runtime.settings import RuntimeSettings
from test_sessions import sessions, transcript


def configured(tmp_path: Path, mode="openai-chat", **changes):
    return RuntimeSettings(
        **{
            "session_mount_root": tmp_path / "sessions",
            "runtime_state_root": tmp_path / "sessions" / "executions",
            "claude_config_dir": tmp_path / "sessions" / "claude",
            "run_workspace_root": tmp_path / "workspaces",
            "hf_model_backend": mode,
            "hf_gateway_url": "http://model-gateway:4000",
            "hf_gateway_key": "fixture-gateway-key",
            "hf_openai_base_url": "https://upstream.test/",
            "hf_openai_model": "fixture-model",
            **changes,
        }
    )


def test_route_identity_paths_normalization_and_secret_rotation(tmp_path):
    chat = configured(tmp_path)
    assert chat.hf_openai_base_url == "https://upstream.test/v1"
    assert chat.claude_config_dir == tmp_path / "sessions" / "claude"
    assert (
        chat.effective_claude_config_dir.parent
        == chat.claude_config_dir / ".harness-backends"
    )
    assert re.fullmatch("[0-9a-f]{64}", chat.effective_claude_config_dir.name)
    assert (
        RuntimeSettings(**chat.model_dump()).effective_claude_config_dir
        == chat.effective_claude_config_dir
    )
    assert (
        configured(tmp_path, hf_gateway_key="rotated").effective_claude_config_dir
        == chat.effective_claude_config_dir
    )
    assert (
        configured(
            tmp_path, hf_openai_base_url="https://UPSTREAM.test:443/v1/"
        ).effective_claude_config_dir
        == chat.effective_claude_config_dir
    )
    for changes in (
        {"hf_model_backend": "openai-responses"},
        {"hf_openai_model": "other"},
        {"hf_openai_base_url": "https://other.test"},
    ):
        assert (
            configured(tmp_path, **changes).effective_claude_config_dir
            != chat.effective_claude_config_dir
        )
    native = configured(tmp_path, "native")
    assert native.effective_claude_config_dir == native.claude_config_dir
    assert (
        configured(
            tmp_path, hf_openai_base_url="https://upstream.test/api/v2/"
        ).hf_openai_base_url
        == "https://upstream.test/api/v2"
    )
    assert "fixture-gateway-key" not in repr(chat)


@pytest.mark.parametrize(
    "changes",
    [
        {"hf_model_backend": "unknown"},
        {"hf_gateway_key": ""},
        {"hf_openai_model": " "},
        {"hf_openai_base_url": "http://external.test"},
        {"hf_openai_base_url": "https://user:password@external.test"},
        {"hf_openai_base_url": "https://external.test?secret=value"},
        {"hf_gateway_url": "http://gateway/#fragment"},
        {"hf_gateway_url": "http://user:password@gateway"},
    ],
)
def test_invalid_backend_settings_fail_closed(tmp_path, changes):
    with pytest.raises(ValidationError):
        configured(tmp_path, **changes)


@pytest.mark.parametrize("state", ["starting", "running", "awaiting_finalize"])
@pytest.mark.parametrize("legacy", [False, True])
def test_route_change_refused_before_recovery_or_directory_mutation(
    tmp_path, state, legacy
):
    original = configured(tmp_path, "native" if legacy else "openai-chat")
    if not legacy:
        with TestClient(create_app(settings=original)):
            pass
    store = ExecutionStore(original.runtime_state_root)
    run = uuid4()

    async def pending():
        await store.initialize()
        await store.reserve(run, None, [])
        if state != "starting":
            await store.mark_running(run, 987654, 987654)
        if state == "awaiting_finalize":
            await store.mark_awaiting_finalize(run)

    asyncio.run(pending())
    record = (store.root / f"{run}.json").read_bytes()
    marker = store.root / ".backend"
    before = marker.read_bytes() if marker.exists() else None
    changed = configured(tmp_path, "openai-responses")
    with pytest.raises(ValueError, match="backend"):
        with TestClient(create_app(settings=changed)):
            pass
    assert (store.root / f"{run}.json").read_bytes() == record
    assert (marker.read_bytes() if marker.exists() else None) == before
    assert not changed.effective_claude_config_dir.exists()


def test_route_switch_back_keeps_sources_and_corrupt_marker_fails(tmp_path):
    a, b = configured(tmp_path), configured(tmp_path, "openai-responses")
    with TestClient(create_app(settings=a)) as client:
        source, original = transcript(a.effective_claude_config_dir)
        assert client.head(f"/v1/sessions/{source}").status_code == 200
    with TestClient(create_app(settings=b)) as client:
        assert client.head(f"/v1/sessions/{source}").status_code == 404
        with pytest.raises(FileNotFoundError):
            sessions(b.effective_claude_config_dir).prepare_run(
                uuid4(), Path("/workspaces/new"), source
            )
    with TestClient(create_app(settings=a)) as client:
        assert client.head(f"/v1/sessions/{source}").status_code == 200
    assert original.exists()
    (a.runtime_state_root / ".backend").write_text("invalid")
    with pytest.raises(ValueError, match="backend"):
        with TestClient(create_app(settings=a)):
            pass


@pytest.mark.parametrize("old_mode", ["native", "openai-chat"])
def test_delete_covers_old_routes_and_staged_sources(tmp_path, old_mode):
    old, active = (
        configured(tmp_path, old_mode),
        configured(tmp_path, "openai-responses"),
    )
    with TestClient(create_app(settings=old)):
        source, original = transcript(old.effective_claude_config_dir)
        sdk = sessions(old.effective_claude_config_dir)
        bucket = sdk.prepare_run(uuid4(), Path("/workspaces/fork"), source)
        candidate, candidate_path = transcript(
            old.effective_claude_config_dir, bucket.name
        )
    with TestClient(create_app(settings=active)) as client:
        assert client.head(f"/v1/sessions/{candidate}").status_code == 404
        assert client.delete(f"/v1/sessions/{candidate}").status_code == 204
        assert not candidate_path.exists()
        assert not (bucket / f"{source}.jsonl").exists()
        assert original.exists()
        assert client.delete(f"/v1/sessions/{source}").status_code == 204
    assert not original.exists()
    assert old.effective_claude_config_dir.exists()


def test_busy_delete_checks_all_routes_and_syncs_absent_without_pruning(
    tmp_path, monkeypatch
):
    old, active = configured(tmp_path), configured(tmp_path, "openai-responses")
    source, original = transcript(old.effective_claude_config_dir)
    with TestClient(create_app(settings=active)) as client:
        store = client.app.state.execution_store
        run = uuid4()
        client.portal.call(store.reserve, run, None, [])
        bucket = client.app.state.session_store.prepare_run(
            run, Path("/workspaces/active"), None
        )
        assert client.delete(f"/v1/sessions/{source}").status_code == 409
        assert original.exists()
        synced = []
        cls = type(client.app.state.session_store)
        real = cls.sync_deletions

        def sync(self):
            synced.append(self.root)
            real(self)

        monkeypatch.setattr(cls, "sync_deletions", sync)
        assert client.delete(f"/v1/sessions/{uuid4()}").status_code == 204
        assert set(synced) == {
            old.effective_claude_config_dir,
            active.effective_claude_config_dir,
            active.claude_config_dir,
        }
        assert bucket.exists()
        client.portal.call(store.mark_awaiting_finalize, run)


@pytest.mark.parametrize("failure", ["unlink", "fsync"])
def test_old_route_unlink_fsync_failure_retries_even_when_absent(
    tmp_path, monkeypatch, failure
):
    old, active = configured(tmp_path), configured(tmp_path, "openai-responses")
    source, original = transcript(old.effective_claude_config_dir)
    with TestClient(create_app(settings=active)) as client:
        real_unlink, real_fsync = os.unlink, os.fsync
        bucket_stat = original.parent.stat()
        failed = False

        def unlink(path, *args, **kwargs):
            nonlocal failed
            if failure == "unlink" and path == original.name and not failed:
                failed = True
                raise OSError("private diagnostic")
            return real_unlink(path, *args, **kwargs)

        def fsync(descriptor):
            nonlocal failed
            info = os.fstat(descriptor)
            if (
                failure == "fsync"
                and not failed
                and (info.st_dev, info.st_ino)
                == (bucket_stat.st_dev, bucket_stat.st_ino)
            ):
                failed = True
                raise OSError("private diagnostic")
            return real_fsync(descriptor)

        monkeypatch.setattr(os, "unlink", unlink)
        monkeypatch.setattr(os, "fsync", fsync)
        assert client.delete(f"/v1/sessions/{source}").status_code == 500
        assert failed and original.exists() is (failure == "unlink")
        assert client.delete(f"/v1/sessions/{source}").status_code == 204
        assert not original.exists()


@pytest.mark.parametrize("kind", ["symlink", "file", "invalid_name"])
def test_purge_rejects_invalid_managed_roots_without_touching_outside(tmp_path, kind):
    active = configured(tmp_path)
    source, outside = transcript(tmp_path / "outside")
    with TestClient(create_app(settings=active)) as client:
        registry = active.effective_claude_config_dir.parent
        registry.mkdir(parents=True, exist_ok=True)
        entry = registry / ("not-a-fingerprint" if kind == "invalid_name" else "a" * 64)
        if kind == "symlink":
            entry.symlink_to(tmp_path / "outside", target_is_directory=True)
        elif kind == "file":
            entry.write_text("wrong type")
        else:
            entry.mkdir()
        response = client.delete(f"/v1/sessions/{source}")
        assert response.status_code == 500
        assert response.json() == {"code": "session_operation_failed"}
    assert outside.exists()


def test_purge_does_not_hide_failure_opening_an_enumerated_route(tmp_path, monkeypatch):
    import harness_forge_runtime.backend as backend
    from contextlib import contextmanager

    old, active = configured(tmp_path), configured(tmp_path, "openai-responses")
    source, original = transcript(old.effective_claude_config_dir)
    with TestClient(create_app(settings=active)) as client:
        real = backend._directory

        @contextmanager
        def directory(path, **kwargs):
            if path == old.effective_claude_config_dir:
                raise FileNotFoundError("directory vanished during inventory")
            with real(path, **kwargs) as descriptor:
                yield descriptor

        monkeypatch.setattr(backend, "_directory", directory)
        assert client.delete(f"/v1/sessions/{source}").status_code == 500
        assert original.exists()


def test_absent_busy_delete_still_reports_old_route_sync_failure(tmp_path, monkeypatch):
    old, active = configured(tmp_path), configured(tmp_path, "openai-responses")
    transcript(old.effective_claude_config_dir)
    with TestClient(create_app(settings=active)) as client:
        store, sdk = client.app.state.execution_store, client.app.state.session_store
        run = uuid4()
        client.portal.call(store.reserve, run, None, [])
        bucket = sdk.prepare_run(run, Path("/workspaces/active"), None)
        real = type(sdk).sync_deletions

        def sync(self):
            if self.root == old.effective_claude_config_dir:
                raise OSError("private sync failure")
            real(self)

        monkeypatch.setattr(type(sdk), "sync_deletions", sync)
        assert client.delete(f"/v1/sessions/{uuid4()}").status_code == 500
        assert bucket.exists()
        client.portal.call(store.mark_awaiting_finalize, run)


def test_injected_session_store_does_not_enumerate_configured_roots(
    tmp_path, monkeypatch
):
    import harness_forge_runtime.api as api

    def unexpected(*args):
        raise AssertionError("injected stores must not inspect other roots")

    monkeypatch.setattr(api, "purge_stores", unexpected)
    sdk = sessions(tmp_path / "injected")
    source, original = transcript(sdk.root)
    with TestClient(create_app(settings=configured(tmp_path), sessions=sdk)) as client:
        assert client.delete(f"/v1/sessions/{source}").status_code == 204
    assert not original.exists()


@pytest.mark.parametrize("kind", ["symlink", "hardlink"])
def test_backend_marker_links_are_rejected_without_modifying_target(tmp_path, kind):
    settings = configured(tmp_path)
    settings.runtime_state_root.mkdir(parents=True)
    target = tmp_path / "outside-marker"
    target.write_text("native")
    marker = settings.runtime_state_root / ".backend"
    if kind == "symlink":
        marker.symlink_to(target)
    else:
        marker.hardlink_to(target)
    with pytest.raises(ValueError, match="backend"):
        with TestClient(create_app(settings=settings)):
            pass
    assert target.read_text() == "native"


def test_backend_identity_short_write_preserves_previous_marker(tmp_path, monkeypatch):
    settings = configured(tmp_path)
    settings.runtime_state_root.mkdir(parents=True)
    marker = settings.runtime_state_root / ".backend"
    marker.write_text("native")
    write = os.write

    def short_write(descriptor, payload):
        return write(descriptor, payload[:32])

    monkeypatch.setattr(os, "write", short_write)
    with pytest.raises(OSError, match="incomplete backend identity write"):
        with TestClient(create_app(settings=settings)):
            pass
    assert marker.read_text() == "native"
    assert not list(settings.runtime_state_root.glob(".backend-*.tmp"))
