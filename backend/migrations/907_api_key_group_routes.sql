-- Ordered API Key fallback routes. Upstream: cdf13f1991c369dbc5a4585ad197c103f0772451
-- No candidate-count policy: position must only be positive and unique per key.
CREATE TABLE IF NOT EXISTS api_key_group_routes (
    id BIGSERIAL PRIMARY KEY,
    api_key_id BIGINT NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    position INT NOT NULL CHECK (position > 0),
    UNIQUE (api_key_id, group_id),
    UNIQUE (api_key_id, position)
);
CREATE INDEX IF NOT EXISTS idx_api_key_group_routes_group_id ON api_key_group_routes(group_id);
COMMENT ON TABLE api_key_group_routes IS 'Ordered API Key fallback candidates; api_keys.group_id is the primary';
