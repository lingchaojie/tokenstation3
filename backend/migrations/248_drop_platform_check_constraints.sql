-- Platform identifiers are validated by the application catalog in
-- internal/domain/platforms.go. Keep historical migrations 242..247 unchanged.
-- Admin requests, quota settings, repository SQL writes and ent validation use
-- the same concrete platform registry.
-- Channel monitor provider CHECK constraints describe implemented probe support
-- and remain in place. DROP IF EXISTS allows this migration to be reapplied.

ALTER TABLE user_platform_quotas
    DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;
