-- 153: Backfill plan quota snapshots for subscriptions created before snapshot columns existed.
-- The payment fulfillment path records "payment order <id>" in subscription notes, which lets
-- us recover the exact purchased plan without guessing among multiple plans on the same group.

WITH candidates AS (
    SELECT DISTINCT ON (us.id)
           us.id AS subscription_id,
           po.plan_id,
           sp.name AS plan_name,
           sp.seven_day_quota_usd
      FROM user_subscriptions us
      JOIN payment_orders po
        ON po.subscription_group_id = us.group_id
       AND po.user_id = us.user_id
      JOIN subscription_plans sp
        ON sp.id = po.plan_id
     WHERE us.deleted_at IS NULL
       AND po.order_type = 'subscription'
       AND po.status = 'COMPLETED'
       AND po.plan_id IS NOT NULL
       AND (us.plan_id IS NULL OR us.plan_id = po.plan_id)
       AND (
           us.plan_id IS NULL
           OR us.plan_name IS NULL
           OR us.seven_day_limit_usd IS NULL
       )
       AND POSITION(E'\npayment order ' || po.id::text || E'\n' IN E'\n' || COALESCE(us.notes, '') || E'\n') > 0
     ORDER BY us.id,
              po.completed_at DESC NULLS LAST,
              po.paid_at DESC NULLS LAST,
              po.id DESC
)
UPDATE user_subscriptions us
   SET plan_id = COALESCE(us.plan_id, candidates.plan_id),
       plan_name = COALESCE(us.plan_name, candidates.plan_name),
       seven_day_limit_usd = COALESCE(us.seven_day_limit_usd, candidates.seven_day_quota_usd),
       updated_at = NOW()
  FROM candidates
 WHERE us.id = candidates.subscription_id;
