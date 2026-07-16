-- Unified expiring reward credits for daily check-in and affiliate rewards.
-- This migration is transactional. In particular, the legacy affiliate wallet
-- transfer and wallet zeroing must commit or roll back together.

CREATE TABLE IF NOT EXISTS user_reward_credits (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    credit_type VARCHAR(32) NOT NULL,
    source_key VARCHAR(191) NOT NULL,
    original_amount DECIMAL(20,8) NOT NULL,
    remaining_amount DECIMAL(20,8) NOT NULL,
    reserved_amount DECIMAL(20,8) NOT NULL DEFAULT 0,
    granted_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ NULL,
    expired_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, credit_type, source_key),
    CHECK (credit_type IN ('daily_check_in', 'affiliate_inviter', 'affiliate_invitee')),
    CHECK (original_amount > 0),
    CHECK (remaining_amount >= 0),
    CHECK (reserved_amount >= 0),
    CHECK (remaining_amount + reserved_amount <= original_amount),
    CHECK (expires_at > granted_at)
);

CREATE INDEX IF NOT EXISTS idx_user_reward_credits_active
    ON user_reward_credits (user_id, credit_type, expires_at, id)
    WHERE remaining_amount > 0 AND expired_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_user_reward_credits_reserved
    ON user_reward_credits (user_id, expires_at, id)
    WHERE reserved_amount > 0;

CREATE INDEX IF NOT EXISTS idx_user_reward_credits_expiry
    ON user_reward_credits (expires_at, id)
    WHERE remaining_amount > 0 AND expired_at IS NULL;

CREATE TABLE IF NOT EXISTS user_reward_credit_events (
    id BIGSERIAL PRIMARY KEY,
    credit_id BIGINT NOT NULL REFERENCES user_reward_credits(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_type VARCHAR(16) NOT NULL,
    event_key VARCHAR(191) NOT NULL,
    amount DECIMAL(20,8) NOT NULL,
    request_id VARCHAR(191) NULL,
    batch_id VARCHAR(191) NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (credit_id, event_type, event_key),
    CHECK (event_type IN ('grant', 'consume', 'reserve', 'capture', 'release', 'expire')),
    CHECK (amount > 0)
);

CREATE INDEX IF NOT EXISTS idx_user_reward_credit_events_user_created
    ON user_reward_credit_events (user_id, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_user_reward_credit_events_request
    ON user_reward_credit_events (request_id, id)
    WHERE request_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_user_reward_credit_events_batch
    ON user_reward_credit_events (batch_id, id)
    WHERE batch_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS batch_image_reward_allocations (
    id BIGSERIAL PRIMARY KEY,
    hold_key VARCHAR(191) NOT NULL,
    credit_id BIGINT NOT NULL REFERENCES user_reward_credits(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reserved_amount DECIMAL(20,8) NOT NULL,
    captured_amount DECIMAL(20,8) NOT NULL DEFAULT 0,
    released_amount DECIMAL(20,8) NOT NULL DEFAULT 0,
    expires_at_snapshot TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (hold_key, credit_id),
    CHECK (reserved_amount > 0),
    CHECK (captured_amount >= 0),
    CHECK (released_amount >= 0),
    CHECK (captured_amount + released_amount <= reserved_amount)
);

CREATE INDEX IF NOT EXISTS idx_batch_image_reward_allocations_hold
    ON batch_image_reward_allocations (hold_key, id);

CREATE INDEX IF NOT EXISTS idx_batch_image_reward_allocations_user
    ON batch_image_reward_allocations (user_id, created_at DESC, id DESC);

ALTER TABLE user_affiliates
    ADD COLUMN IF NOT EXISTS inviter_reward_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS reward_mode VARCHAR(32),
    ADD COLUMN IF NOT EXISTS reward_status VARCHAR(32),
    ADD COLUMN IF NOT EXISTS reward_resolved_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS reward_source_order_id BIGINT REFERENCES payment_orders(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS inviter_rewarded BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS invitee_rewarded BOOLEAN NOT NULL DEFAULT FALSE;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'user_affiliates_reward_mode_check'
          AND conrelid = 'user_affiliates'::regclass
    ) THEN
        ALTER TABLE user_affiliates
            ADD CONSTRAINT user_affiliates_reward_mode_check
            CHECK (reward_mode IS NULL OR reward_mode IN ('immediate', 'first_recharge'));
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'user_affiliates_reward_status_check'
          AND conrelid = 'user_affiliates'::regclass
    ) THEN
        ALTER TABLE user_affiliates
            ADD CONSTRAINT user_affiliates_reward_status_check
            CHECK (reward_status IS NULL OR reward_status IN ('pending', 'resolved'));
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'user_affiliates_inviter_reward_count_check'
          AND conrelid = 'user_affiliates'::regclass
    ) THEN
        ALTER TABLE user_affiliates
            ADD CONSTRAINT user_affiliates_inviter_reward_count_check
            CHECK (inviter_reward_count >= 0);
    END IF;
END
$$;

CREATE INDEX IF NOT EXISTS idx_user_affiliates_reward_status
    ON user_affiliates (reward_status, user_id)
    WHERE inviter_id IS NOT NULL;

-- Backfill the number of distinct invitees that actually generated a positive
-- legacy inviter reward. Re-running the migration never decreases the counter.
WITH legacy_reward_counts AS (
    SELECT user_id,
           COUNT(DISTINCT source_user_id)::INTEGER AS reward_count
    FROM user_affiliate_ledger
    WHERE action = 'accrue'
      AND source_user_id IS NOT NULL
      AND amount > 0
    GROUP BY user_id
)
UPDATE user_affiliates ua
SET inviter_reward_count = GREATEST(ua.inviter_reward_count, counts.reward_count),
    updated_at = NOW()
FROM legacy_reward_counts counts
WHERE ua.user_id = counts.user_id;

-- Preserve historical first-recharge semantics without issuing back-payments.
-- Any relationship with a successful recharge or a terminal affiliate audit is
-- resolved. Other historical bindings remain pending in first-recharge mode.
UPDATE user_affiliates ua
SET reward_mode = 'first_recharge',
    reward_status = CASE
        WHEN EXISTS (
            SELECT 1
            FROM payment_orders po
            WHERE po.user_id = ua.user_id
              AND po.order_type IN ('balance', 'subscription')
              AND po.status IN ('PAID', 'RECHARGING', 'COMPLETED')
        ) OR EXISTS (
            SELECT 1
            FROM payment_audit_logs pal
            JOIN payment_orders po ON po.id::text = pal.order_id
            WHERE po.user_id = ua.user_id
              AND pal.action IN ('AFFILIATE_REBATE_APPLIED', 'AFFILIATE_REBATE_SKIPPED')
        ) THEN 'resolved'
        ELSE 'pending'
    END,
    reward_resolved_at = CASE
        WHEN EXISTS (
            SELECT 1
            FROM payment_orders po
            WHERE po.user_id = ua.user_id
              AND po.order_type IN ('balance', 'subscription')
              AND po.status IN ('PAID', 'RECHARGING', 'COMPLETED')
        ) OR EXISTS (
            SELECT 1
            FROM payment_audit_logs pal
            JOIN payment_orders po ON po.id::text = pal.order_id
            WHERE po.user_id = ua.user_id
              AND pal.action IN ('AFFILIATE_REBATE_APPLIED', 'AFFILIATE_REBATE_SKIPPED')
        ) THEN COALESCE(
            (
                SELECT MIN(pal.created_at)
                FROM payment_audit_logs pal
                JOIN payment_orders po ON po.id::text = pal.order_id
                WHERE po.user_id = ua.user_id
                  AND pal.action IN ('AFFILIATE_REBATE_APPLIED', 'AFFILIATE_REBATE_SKIPPED')
            ),
            (
                SELECT MIN(COALESCE(po.completed_at, po.paid_at, po.updated_at))
                FROM payment_orders po
                WHERE po.user_id = ua.user_id
                  AND po.order_type IN ('balance', 'subscription')
                  AND po.status IN ('PAID', 'RECHARGING', 'COMPLETED')
            ),
            NOW()
        )
        ELSE NULL
    END,
    reward_source_order_id = (
        SELECT po.id
        FROM payment_orders po
        WHERE po.user_id = ua.user_id
          AND po.order_type IN ('balance', 'subscription')
          AND po.status IN ('PAID', 'RECHARGING', 'COMPLETED')
        ORDER BY COALESCE(po.completed_at, po.paid_at, po.updated_at), po.id
        LIMIT 1
    ),
    inviter_rewarded = EXISTS (
        SELECT 1
        FROM user_affiliate_ledger ual
        WHERE ual.user_id = ua.inviter_id
          AND ual.source_user_id = ua.user_id
          AND ual.action = 'accrue'
          AND ual.amount > 0
    ),
    invitee_rewarded = EXISTS (
        SELECT 1
        FROM payment_audit_logs pal
        JOIN payment_orders po ON po.id::text = pal.order_id
        WHERE po.user_id = ua.user_id
          AND pal.action = 'AFFILIATE_REBATE_APPLIED'
    ),
    updated_at = NOW()
WHERE ua.inviter_id IS NOT NULL
  AND ua.reward_mode IS NULL;

-- One-time conversion of the legacy affiliate wallet into permanent account
-- balance. The migration runner wraps this file in a transaction, so crediting
-- users and zeroing the legacy wallet are atomic.
WITH legacy AS MATERIALIZED (
    SELECT ua.user_id,
           ua.aff_quota + ua.aff_frozen_quota AS total_amount
    FROM user_affiliates ua
    WHERE ua.aff_quota + ua.aff_frozen_quota > 0
)
UPDATE users u
SET balance = u.balance + legacy.total_amount,
    updated_at = NOW()
FROM legacy
WHERE u.id = legacy.user_id;

UPDATE user_affiliates
SET aff_quota = 0,
    aff_frozen_quota = 0,
    updated_at = NOW()
WHERE aff_quota <> 0 OR aff_frozen_quota <> 0;

-- Existing installations used 20 and 5 as the shipped defaults. Only those
-- exact numeric values move to the new defaults; administrator custom values
-- are preserved. Missing keys receive the new defaults.
UPDATE settings
SET value = '0', updated_at = NOW()
WHERE key = 'affiliate_first_recharge_threshold'
  AND value ~ '^[[:space:]]*[+]?[0-9]+([.][0-9]+)?[[:space:]]*$'
  AND value::numeric = 20;

UPDATE settings
SET value = '10', updated_at = NOW()
WHERE key = 'affiliate_inviter_reward'
  AND value ~ '^[[:space:]]*[+]?[0-9]+([.][0-9]+)?[[:space:]]*$'
  AND value::numeric = 5;

INSERT INTO settings (key, value, updated_at)
VALUES
    ('affiliate_first_recharge_threshold', '0', NOW()),
    ('affiliate_inviter_reward', '10', NOW()),
    ('affiliate_invitee_reward', '5', NOW()),
    ('affiliate_reward_validity_days', '7', NOW()),
    ('affiliate_inviter_reward_limit', '0', NOW())
ON CONFLICT (key) DO NOTHING;

COMMENT ON TABLE user_reward_credits IS 'Expiring reward credit lots included in users.balance';
COMMENT ON TABLE user_reward_credit_events IS 'Immutable reward credit grant and spending audit events';
COMMENT ON TABLE batch_image_reward_allocations IS 'Reward lots reserved by an asynchronous batch image hold';
