-- Persist Kiro gateway group controls and usage credit accounting.
ALTER TABLE groups
  ADD COLUMN IF NOT EXISTS kiro_cache_emulation_enabled BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN IF NOT EXISTS kiro_auto_sticky_enabled BOOLEAN NOT NULL DEFAULT TRUE,
  ADD COLUMN IF NOT EXISTS kiro_sticky_session_ttl_seconds INT NOT NULL DEFAULT 3600,
  ADD COLUMN IF NOT EXISTS kiro_cache_emulation_ratio DECIMAL(5,4) NOT NULL DEFAULT 1.0,
  ADD COLUMN IF NOT EXISTS kiro_endpoint_mode VARCHAR(8) NOT NULL DEFAULT 'q';

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint WHERE conname = 'groups_kiro_cache_emulation_ratio_range'
  ) THEN
    ALTER TABLE groups
      ADD CONSTRAINT groups_kiro_cache_emulation_ratio_range
      CHECK (kiro_cache_emulation_ratio >= 0 AND kiro_cache_emulation_ratio <= 1);
  END IF;
END $$;

ALTER TABLE usage_logs
  ADD COLUMN IF NOT EXISTS kiro_credits NUMERIC(20,10);

COMMENT ON COLUMN usage_logs.kiro_credits IS 'Kiro credits consumed by this usage log';
