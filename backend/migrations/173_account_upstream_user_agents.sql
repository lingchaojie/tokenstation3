-- Persist the exact User-Agent observed on real upstream requests per account.
-- The unique key dedupes display rows while seen_count/last_seen_at keep history
-- useful for account sharing and fingerprint drift diagnostics.

CREATE TABLE IF NOT EXISTS account_upstream_user_agents (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    user_agent TEXT NOT NULL,
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    seen_count BIGINT NOT NULL DEFAULT 1,
    CONSTRAINT account_upstream_user_agents_account_ua_unique UNIQUE (account_id, user_agent)
);

CREATE INDEX IF NOT EXISTS idx_account_upstream_user_agents_account_last_seen
    ON account_upstream_user_agents (account_id, last_seen_at DESC);
