ALTER TABLE users
    ADD COLUMN IF NOT EXISTS beginner_guide_prompt_state VARCHAR(20),
    ADD COLUMN IF NOT EXISTS beginner_guide_progress JSONB,
    ADD COLUMN IF NOT EXISTS beginner_guide_completed_at TIMESTAMPTZ;

UPDATE users
SET beginner_guide_prompt_state = 'suppressed'
WHERE beginner_guide_prompt_state IS NULL;

ALTER TABLE users
    ALTER COLUMN beginner_guide_prompt_state SET DEFAULT 'eligible',
    ALTER COLUMN beginner_guide_prompt_state SET NOT NULL;

ALTER TABLE users
    DROP CONSTRAINT IF EXISTS users_beginner_guide_prompt_state_check,
    ADD CONSTRAINT users_beginner_guide_prompt_state_check
        CHECK (beginner_guide_prompt_state IN ('eligible', 'suppressed', 'completed'));
