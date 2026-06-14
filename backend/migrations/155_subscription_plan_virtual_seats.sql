-- Virtual subscription plan seat display ranges.
-- When both fields are set, seat_limit is derived as virtual_seat_total - virtual_seat_start.
ALTER TABLE subscription_plans
    ADD COLUMN IF NOT EXISTS virtual_seat_start INT;

ALTER TABLE subscription_plans
    ADD COLUMN IF NOT EXISTS virtual_seat_total INT;

UPDATE subscription_plans
SET virtual_seat_start = 0,
    virtual_seat_total = seat_limit
WHERE seat_limit IS NOT NULL
  AND virtual_seat_start IS NULL
  AND virtual_seat_total IS NULL;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'subscription_plans_virtual_seat_start_nonnegative'
          AND conrelid = 'subscription_plans'::regclass
    ) THEN
        ALTER TABLE subscription_plans
            ADD CONSTRAINT subscription_plans_virtual_seat_start_nonnegative
            CHECK (virtual_seat_start IS NULL OR virtual_seat_start >= 0);
    END IF;
END $$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'subscription_plans_virtual_seat_total_nonnegative'
          AND conrelid = 'subscription_plans'::regclass
    ) THEN
        ALTER TABLE subscription_plans
            ADD CONSTRAINT subscription_plans_virtual_seat_total_nonnegative
            CHECK (virtual_seat_total IS NULL OR virtual_seat_total >= 0);
    END IF;
END $$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'subscription_plans_virtual_seat_range_pair'
          AND conrelid = 'subscription_plans'::regclass
    ) THEN
        ALTER TABLE subscription_plans
            ADD CONSTRAINT subscription_plans_virtual_seat_range_pair
            CHECK (
                (virtual_seat_start IS NULL AND virtual_seat_total IS NULL)
                OR
                (virtual_seat_start IS NOT NULL AND virtual_seat_total IS NOT NULL)
            );
    END IF;
END $$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'subscription_plans_virtual_seat_range_order'
          AND conrelid = 'subscription_plans'::regclass
    ) THEN
        ALTER TABLE subscription_plans
            ADD CONSTRAINT subscription_plans_virtual_seat_range_order
            CHECK (virtual_seat_start IS NULL OR virtual_seat_total >= virtual_seat_start);
    END IF;
END $$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'subscription_plans_virtual_seat_limit_matches_range'
          AND conrelid = 'subscription_plans'::regclass
    ) THEN
        ALTER TABLE subscription_plans
            ADD CONSTRAINT subscription_plans_virtual_seat_limit_matches_range
            CHECK (
                (seat_limit IS NULL AND virtual_seat_start IS NULL AND virtual_seat_total IS NULL)
                OR
                (
                    seat_limit IS NOT NULL
                    AND virtual_seat_start IS NOT NULL
                    AND virtual_seat_total IS NOT NULL
                    AND seat_limit = virtual_seat_total - virtual_seat_start
                )
            );
    END IF;
END $$;
