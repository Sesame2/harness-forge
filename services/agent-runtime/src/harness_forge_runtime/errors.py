class ExecutionStoreError(Exception):
    """Base error for persisted execution state."""


class ExecutionConflict(ExecutionStoreError):
    """The requested operation conflicts with the persisted execution."""


class InvalidExecutionState(ExecutionStoreError):
    """Persisted or requested execution state violates lifecycle invariants."""
