ALTER TABLE api_keys
  ADD COLUMN IF NOT EXISTS group_binding_mode VARCHAR(30) NOT NULL DEFAULT 'static';

ALTER TABLE api_keys
  DROP CONSTRAINT IF EXISTS api_keys_group_binding_mode_check;

ALTER TABLE api_keys
  ADD CONSTRAINT api_keys_group_binding_mode_check
  CHECK (group_binding_mode IN ('static', 'default_follow'));

UPDATE api_keys AS ak
SET group_binding_mode = 'default_follow',
    updated_at = NOW()
FROM groups AS g
WHERE ak.deleted_at IS NULL
  AND ak.group_id = g.id
  AND g.deleted_at IS NULL
  AND g.platform IN ('anthropic', 'openai')
  AND (ak.key_type = g.platform OR ak.key_type IS NULL OR ak.key_type = '');

UPDATE api_keys AS ak
SET key_type = g.platform,
    updated_at = NOW()
FROM groups AS g
WHERE ak.deleted_at IS NULL
  AND ak.group_binding_mode = 'default_follow'
  AND ak.group_id = g.id
  AND g.deleted_at IS NULL
  AND g.platform IN ('anthropic', 'openai')
  AND (ak.key_type IS NULL OR ak.key_type = '');
