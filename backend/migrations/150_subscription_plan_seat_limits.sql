-- Subscription plan seat limits.
-- seat_limit NULL means unlimited. 0 means no new openings.
ALTER TABLE subscription_plans
    ADD COLUMN IF NOT EXISTS seat_limit INT;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'subscription_plans_seat_limit_nonnegative'
    ) THEN
        ALTER TABLE subscription_plans
            ADD CONSTRAINT subscription_plans_seat_limit_nonnegative
            CHECK (seat_limit IS NULL OR seat_limit >= 0);
    END IF;
END $$;

ALTER TABLE user_subscriptions
    ADD COLUMN IF NOT EXISTS plan_id BIGINT;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'user_subscriptions_plan_id_subscription_plans_fk'
    ) THEN
        ALTER TABLE user_subscriptions
            ADD CONSTRAINT user_subscriptions_plan_id_subscription_plans_fk
            FOREIGN KEY (plan_id) REFERENCES subscription_plans(id) ON DELETE SET NULL;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_user_subscriptions_plan_active
    ON user_subscriptions(plan_id, status, expires_at)
    WHERE deleted_at IS NULL AND plan_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_user_subscriptions_plan_user_active
    ON user_subscriptions(plan_id, user_id)
    WHERE deleted_at IS NULL AND plan_id IS NOT NULL;
