ALTER TABLE api_keys
  ADD COLUMN IF NOT EXISTS key_type VARCHAR(20);

CREATE TABLE IF NOT EXISTS user_api_key_routes (
  id BIGSERIAL PRIMARY KEY,
  user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key_type VARCHAR(20) NOT NULL,
  group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CONSTRAINT user_api_key_routes_key_type_check CHECK (key_type IN ('anthropic', 'openai'))
);

CREATE UNIQUE INDEX IF NOT EXISTS user_api_key_routes_user_type_uq
  ON user_api_key_routes(user_id, key_type);

CREATE INDEX IF NOT EXISTS user_api_key_routes_group_id_idx
  ON user_api_key_routes(group_id);
