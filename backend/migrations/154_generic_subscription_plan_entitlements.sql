-- 154: Decouple subscription plans from groups and allow generic plan entitlements.
--
-- New subscription plans sell generic quota/validity and no longer select a
-- concrete subscription group. Historical plan/order/subscription group columns
-- stay in place for compatibility with existing rows.

ALTER TABLE subscription_plans
    ALTER COLUMN group_id DROP NOT NULL;

ALTER TABLE user_subscriptions
    ALTER COLUMN group_id DROP NOT NULL;

DROP INDEX IF EXISTS idx_subscription_plans_group_id;
DROP INDEX IF EXISTS subscriptionplan_group_id;
DROP INDEX IF EXISTS idx_subscription_plans_group_for_sale;

CREATE INDEX IF NOT EXISTS idx_subscription_plans_for_sale_sort
    ON subscription_plans(for_sale, sort_order);

-- Existing (user_id, group_id) unique indexes do not deduplicate NULL group_id
-- rows in PostgreSQL. Add a dedicated active-row guard for generic plan
-- entitlements.
CREATE UNIQUE INDEX IF NOT EXISTS user_subscriptions_user_generic_unique_active
    ON user_subscriptions(user_id)
    WHERE deleted_at IS NULL AND group_id IS NULL;
