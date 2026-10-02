"""Fixed deployment identity and cross-route purge, without transcript migration."""

import os
import re
from uuid import uuid4

from harness_forge_runtime.execution_store import ExecutionStore
from harness_forge_runtime.sessions import SessionStore, _directory, _file
from harness_forge_runtime.settings import RuntimeSettings


async def prepare_backend(settings: RuntimeSettings, store: ExecutionStore) -> None:
    # No .json suffix: ExecutionStore reserves that namespace for execution records.
    marker = ".backend"
    with _directory(store.root) as directory:
        try:
            with _file(directory, marker) as descriptor:
                previous = os.read(descriptor, 128).decode("ascii")
        except FileNotFoundError:
            previous = "native"
        except (UnicodeError, ValueError, OSError):
            raise ValueError("invalid backend identity") from None
        if previous != "native" and not re.fullmatch("[0-9a-f]{64}", previous):
            raise ValueError("invalid backend identity")
        if previous != settings.backend_identity and await store.list_unfinalized():
            raise ValueError("backend change requires all executions finalized")
        temporary = f".backend-{uuid4()}.tmp"
        try:
            with _file(directory, temporary, create=True) as descriptor:
                payload = settings.backend_identity.encode("ascii")
                if os.write(descriptor, payload) != len(payload):
                    raise OSError("incomplete backend identity write")
                os.fsync(descriptor)
            os.replace(temporary, marker, src_dir_fd=directory, dst_dir_fd=directory)
            os.fsync(directory)
        finally:
            try:
                os.unlink(temporary, dir_fd=directory)
            except FileNotFoundError:
                pass


def purge_stores(settings: RuntimeSettings) -> list[SessionStore]:
    """Only the native root and strictly named, no-follow managed route roots."""
    stores = [SessionStore(settings.claude_config_dir)]
    registry = settings.claude_config_dir / ".harness-backends"
    with _directory(settings.claude_config_dir, create=True) as base:
        if registry.name not in os.listdir(base):
            return stores
    with _directory(registry) as directory:
        for name in sorted(os.listdir(directory)):
            if not re.fullmatch("[0-9a-f]{64}", name):
                raise ValueError("invalid managed backend directory")
            with _directory(registry / name):
                pass
            stores.append(SessionStore(registry / name))
    return stores
