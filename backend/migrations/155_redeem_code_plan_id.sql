-- Add optional subscription plan target for plan-based subscription redeem codes.
ALTER TABLE redeem_codes
    ADD COLUMN IF NOT EXISTS plan_id BIGINT;

CREATE INDEX IF NOT EXISTS idx_redeem_codes_plan_id ON redeem_codes(plan_id);
