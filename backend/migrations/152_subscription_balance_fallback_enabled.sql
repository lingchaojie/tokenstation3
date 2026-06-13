ALTER TABLE users
    ADD COLUMN IF NOT EXISTS subscription_balance_fallback_enabled BOOLEAN NOT NULL DEFAULT false;
