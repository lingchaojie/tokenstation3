CREATE TABLE IF NOT EXISTS daily_check_in_claims (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    activity_start_at TIMESTAMPTZ NOT NULL,
    check_in_date DATE NOT NULL,
    reward_amount DECIMAL(20,8) NOT NULL CHECK (reward_amount > 0),
    balance_after DECIMAL(20,8) NOT NULL,
    claimed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS daily_check_in_claims_user_activity_date_uq
    ON daily_check_in_claims (user_id, activity_start_at, check_in_date);

CREATE INDEX IF NOT EXISTS daily_check_in_claims_claimed_at_idx
    ON daily_check_in_claims (claimed_at);
