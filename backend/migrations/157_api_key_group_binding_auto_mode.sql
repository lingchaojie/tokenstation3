-- Allow the provider-agnostic "auto" binding mode for unified API keys.
-- An auto key stores no key_type and no group_id; the forwarding service
-- resolves the effective default group per request from the detected
-- ingress provider (anthropic/openai).
ALTER TABLE api_keys
  DROP CONSTRAINT IF EXISTS api_keys_group_binding_mode_check;

ALTER TABLE api_keys
  ADD CONSTRAINT api_keys_group_binding_mode_check
  CHECK (group_binding_mode IN ('static', 'default_follow', 'auto'));
