-- 158: Make subscription plan entitlements generic across providers.
--
-- Plan/redeem-code subscriptions are now universal entitlements. Routing still
-- chooses the user's provider default/priority group, but the purchased plan and
-- active subscription should not be bound to any one group.

DO $$
BEGIN
    IF EXISTS (
        WITH existing_generic_subscriptions AS (
            SELECT user_id, COUNT(*) AS row_count
              FROM user_subscriptions
             WHERE deleted_at IS NULL
               AND group_id IS NULL
             GROUP BY user_id
        ),
        subscriptions_to_genericize AS (
            SELECT user_id, COUNT(*) AS row_count
              FROM user_subscriptions
             WHERE deleted_at IS NULL
               AND group_id IS NOT NULL
               AND status = 'active'
               AND expires_at > NOW()
             GROUP BY user_id
        ),
        generic_subscription_conflicts AS (
            SELECT COALESCE(existing_generic_subscriptions.user_id, subscriptions_to_genericize.user_id) AS user_id
              FROM existing_generic_subscriptions
              FULL OUTER JOIN subscriptions_to_genericize USING (user_id)
             WHERE COALESCE(existing_generic_subscriptions.row_count, 0) +
                   COALESCE(subscriptions_to_genericize.row_count, 0) > 1
        )
        SELECT 1 FROM generic_subscription_conflicts
    ) THEN
        RAISE EXCEPTION 'migration-158 would create duplicate generic subscriptions for at least one user; merge/expire duplicates before clearing group_id';
    END IF;
END $$;

UPDATE subscription_plans
   SET group_id = NULL,
       updated_at = NOW()
 WHERE group_id IS NOT NULL;

UPDATE user_subscriptions
   SET group_id = NULL,
       updated_at = NOW()
 WHERE deleted_at IS NULL
   AND group_id IS NOT NULL
   AND status = 'active'
   AND expires_at > NOW();
