CREATE TABLE projects (
    id uuid PRIMARY KEY,
    name text NOT NULL,
    profile_id text NOT NULL,
    profile_version integer NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);

CREATE TABLE input_files (
    id uuid PRIMARY KEY,
    project_id uuid NOT NULL REFERENCES projects (id),
    display_name text NOT NULL,
    media_type text NOT NULL,
    size_bytes bigint NOT NULL,
    sha256_digest text NOT NULL,
    object_key text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE conversations (
    id uuid PRIMARY KEY,
    project_id uuid NOT NULL REFERENCES projects (id),
    title text NOT NULL,
    active_sdk_session_id text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);

CREATE TABLE messages (
    id uuid PRIMARY KEY,
    conversation_id uuid NOT NULL REFERENCES conversations (id),
    role text NOT NULL CHECK (role IN ('user', 'assistant')),
    content text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE runs (
    id uuid PRIMARY KEY,
    conversation_id uuid NOT NULL REFERENCES conversations (id),
    trigger_message_id uuid NOT NULL REFERENCES messages (id),
    status text NOT NULL CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'cancelled', 'interrupted')),
    phase text CHECK (phase IN ('preparing', 'agent', 'publishing')),
    finalized_at timestamptz,
    source_sdk_session_id text,
    candidate_sdk_session_id text,
    sandbox_provider text,
    sandbox_ref text,
    error jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (sandbox_ref IS NULL OR sandbox_provider IS NOT NULL)
);

CREATE INDEX runs_pending_fifo_idx ON runs (status, created_at, id);

CREATE TABLE run_events (
    run_id uuid NOT NULL REFERENCES runs (id),
    sequence bigint NOT NULL,
    type text NOT NULL,
    payload jsonb NOT NULL,
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (run_id, sequence)
);

CREATE TABLE artifacts (
    id uuid PRIMARY KEY,
    run_id uuid NOT NULL REFERENCES runs (id),
    title text NOT NULL,
    type text NOT NULL CHECK (type IN ('html', 'markdown', 'image', 'data')),
    entry_path text NOT NULL,
    object_prefix text NOT NULL,
    is_primary boolean NOT NULL,
    manifest_version integer NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
