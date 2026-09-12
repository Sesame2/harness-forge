ALTER TABLE messages ADD COLUMN run_id uuid REFERENCES runs(id);
ALTER TABLE messages ADD COLUMN runtime_sequence bigint;
CREATE UNIQUE INDEX messages_assistant_run_sequence_idx ON messages(run_id,runtime_sequence)
    WHERE role='assistant' AND run_id IS NOT NULL AND runtime_sequence IS NOT NULL;
ALTER TABLE run_events ADD COLUMN dedupe_key text;
CREATE UNIQUE INDEX run_events_dedupe_key_idx ON run_events(run_id,dedupe_key)
    WHERE dedupe_key IS NOT NULL;
