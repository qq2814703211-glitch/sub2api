CREATE TABLE IF NOT EXISTS price_sync_revisions (
    seq bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    revision text NOT NULL UNIQUE,
    snapshot jsonb NOT NULL CHECK (jsonb_typeof(snapshot) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now()
);