-- 150: Add plan-level seven-day quotas and purchased subscription quota snapshots.
-- Plan quotas are nullable: NULL means the plan does not impose a seven-day USD quota.

ALTER TABLE subscription_plans
    ADD COLUMN IF NOT EXISTS seven_day_quota_usd DECIMAL(20,8);

ALTER TABLE user_subscriptions
    ADD COLUMN IF NOT EXISTS plan_id BIGINT,
    ADD COLUMN IF NOT EXISTS plan_name VARCHAR(100),
    ADD COLUMN IF NOT EXISTS seven_day_limit_usd DECIMAL(20,8);

CREATE INDEX IF NOT EXISTS idx_subscription_plans_group_for_sale
    ON subscription_plans(group_id, for_sale);

CREATE INDEX IF NOT EXISTS idx_subscription_plans_seven_day_quota
    ON subscription_plans(seven_day_quota_usd)
    WHERE seven_day_quota_usd IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_user_subscriptions_plan_id
    ON user_subscriptions(plan_id);

CREATE INDEX IF NOT EXISTS idx_user_subscriptions_plan_id_active
    ON user_subscriptions(plan_id)
    WHERE deleted_at IS NULL AND plan_id IS NOT NULL;

DO $$
DECLARE
    v_group_id BIGINT;
BEGIN
    SELECT id INTO v_group_id
      FROM groups
     WHERE status = 'active'
       AND deleted_at IS NULL
       AND subscription_type = 'subscription'
     ORDER BY id
     LIMIT 1;

    IF v_group_id IS NULL THEN
        INSERT INTO groups (
            name,
            description,
            rate_multiplier,
            is_exclusive,
            status,
            platform,
            subscription_type,
            default_validity_days,
            created_at,
            updated_at,
            deleted_at
        )
        VALUES (
            'LINX2 Subscription',
            'Shared LINX2 subscription group for seeded subscription plans',
            1.0,
            FALSE,
            'active',
            'anthropic',
            'subscription',
            30,
            NOW(),
            NOW(),
            NULL
        )
        ON CONFLICT (name) WHERE deleted_at IS NULL DO UPDATE
            SET description = EXCLUDED.description,
                status = 'active',
                platform = EXCLUDED.platform,
                subscription_type = 'subscription',
                default_validity_days = EXCLUDED.default_validity_days,
                deleted_at = NULL,
                updated_at = NOW()
        RETURNING id INTO v_group_id;
    END IF;

    INSERT INTO subscription_plans (
        group_id,
        name,
        description,
        price,
        original_price,
        seven_day_quota_usd,
        validity_days,
        validity_unit,
        features,
        product_name,
        for_sale,
        sort_order,
        created_at,
        updated_at
    )
    SELECT v_group_id,
           seed.name,
           seed.description,
           seed.price,
           NULL,
           seed.seven_day_quota_usd,
           30,
           'day',
           seed.features,
           seed.product_name,
           TRUE,
           seed.sort_order,
           NOW(),
           NOW()
      FROM (VALUES
          ('Basic monthly', 'Start with LINX2 essentials for focused personal usage and predictable weekly capacity.', 179.00::DECIMAL(20,2), 50.00000000::DECIMAL(20,8), 'Seven-day quota snapshot: $50 USD; Shared subscription access; Monthly renewal window', 'LINX2 Basic Monthly', 10),
          ('Plus monthly', 'More LINX2 room for steady daily work, experiments, and lightweight team support.', 399.00::DECIMAL(20,2), 110.00000000::DECIMAL(20,8), 'Seven-day quota snapshot: $110 USD; Shared subscription access; Monthly renewal window', 'LINX2 Plus Monthly', 20),
          ('Pro monthly', 'Expanded LINX2 capacity for intensive workflows, larger sessions, and production usage.', 799.00::DECIMAL(20,2), 260.00000000::DECIMAL(20,8), 'Seven-day quota snapshot: $260 USD; Shared subscription access; Monthly renewal window', 'LINX2 Pro Monthly', 30),
          ('Max monthly', 'Highest LINX2 monthly tier for heavy usage patterns and broad seven-day capacity.', 1599.00::DECIMAL(20,2), 550.00000000::DECIMAL(20,8), 'Seven-day quota snapshot: $550 USD; Shared subscription access; Monthly renewal window', 'LINX2 Max Monthly', 40)
      ) AS seed(name, description, price, seven_day_quota_usd, features, product_name, sort_order)
     WHERE NOT EXISTS (
         SELECT 1
           FROM subscription_plans sp
          WHERE sp.group_id = v_group_id
            AND sp.name = seed.name
     );

    UPDATE subscription_plans
       SET group_id = v_group_id,
           price = CASE name
               WHEN 'Basic monthly' THEN 179.00
               WHEN 'Plus monthly' THEN 399.00
               WHEN 'Pro monthly' THEN 799.00
               WHEN 'Max monthly' THEN 1599.00
               ELSE price
           END,
           seven_day_quota_usd = CASE name
               WHEN 'Basic monthly' THEN 50.00000000
               WHEN 'Plus monthly' THEN 110.00000000
               WHEN 'Pro monthly' THEN 260.00000000
               WHEN 'Max monthly' THEN 550.00000000
               ELSE seven_day_quota_usd
           END,
           validity_days = 30,
           validity_unit = 'day',
           for_sale = TRUE,
           product_name = CASE name
               WHEN 'Basic monthly' THEN 'LINX2 Basic Monthly'
               WHEN 'Plus monthly' THEN 'LINX2 Plus Monthly'
               WHEN 'Pro monthly' THEN 'LINX2 Pro Monthly'
               WHEN 'Max monthly' THEN 'LINX2 Max Monthly'
               ELSE product_name
           END,
           description = CASE name
               WHEN 'Basic monthly' THEN 'Start with LINX2 essentials for focused personal usage and predictable weekly capacity.'
               WHEN 'Plus monthly' THEN 'More LINX2 room for steady daily work, experiments, and lightweight team support.'
               WHEN 'Pro monthly' THEN 'Expanded LINX2 capacity for intensive workflows, larger sessions, and production usage.'
               WHEN 'Max monthly' THEN 'Highest LINX2 monthly tier for heavy usage patterns and broad seven-day capacity.'
               ELSE description
           END,
           features = CASE name
               WHEN 'Basic monthly' THEN 'Seven-day quota snapshot: $50 USD; Shared subscription access; Monthly renewal window'
               WHEN 'Plus monthly' THEN 'Seven-day quota snapshot: $110 USD; Shared subscription access; Monthly renewal window'
               WHEN 'Pro monthly' THEN 'Seven-day quota snapshot: $260 USD; Shared subscription access; Monthly renewal window'
               WHEN 'Max monthly' THEN 'Seven-day quota snapshot: $550 USD; Shared subscription access; Monthly renewal window'
               ELSE features
           END,
           sort_order = CASE name
               WHEN 'Basic monthly' THEN 10
               WHEN 'Plus monthly' THEN 20
               WHEN 'Pro monthly' THEN 30
               WHEN 'Max monthly' THEN 40
               ELSE sort_order
           END,
           updated_at = NOW()
     WHERE group_id = v_group_id
       AND name IN ('Basic monthly', 'Plus monthly', 'Pro monthly', 'Max monthly');

    RAISE NOTICE '[migration-150] Seeded/updated LINX2 subscription plans on group %', v_group_id;
END $$;
