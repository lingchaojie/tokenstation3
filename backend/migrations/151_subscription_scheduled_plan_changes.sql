-- 151: Add scheduled subscription plan change fields for deferred downgrades.

ALTER TABLE user_subscriptions
    ADD COLUMN IF NOT EXISTS scheduled_plan_id BIGINT,
    ADD COLUMN IF NOT EXISTS scheduled_plan_name VARCHAR(100),
    ADD COLUMN IF NOT EXISTS scheduled_seven_day_limit_usd DECIMAL(20,8),
    ADD COLUMN IF NOT EXISTS scheduled_plan_effective_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS scheduled_expires_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS scheduled_order_id BIGINT;

CREATE INDEX IF NOT EXISTS idx_user_subscriptions_scheduled_plan_due
    ON user_subscriptions(scheduled_plan_effective_at)
    WHERE deleted_at IS NULL AND scheduled_plan_effective_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_user_subscriptions_scheduled_order_id
    ON user_subscriptions(scheduled_order_id)
    WHERE deleted_at IS NULL AND scheduled_order_id IS NOT NULL;
