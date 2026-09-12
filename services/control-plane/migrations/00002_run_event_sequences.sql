ALTER TABLE runs ADD COLUMN next_event_sequence bigint NOT NULL DEFAULT 1;

-- Existing durable events retain their sequence; the next append follows them.
UPDATE runs
SET next_event_sequence = (SELECT COALESCE(MAX(sequence), 0) + 1 FROM run_events WHERE run_id = runs.id);

ALTER TABLE run_events ADD COLUMN runtime_sequence bigint;

CREATE UNIQUE INDEX run_events_runtime_sequence_idx
    ON run_events (run_id, runtime_sequence)
    WHERE runtime_sequence IS NOT NULL;
