-- Add TypeSafe (Jev System One) as a first-class platform.
--
-- Extend the local user_platform_quotas.platform CHECK, retaining KIRO.
--
-- TypeSafe 不是对话模型，不进入渠道监控 provider，因此 channel_monitors /
-- channel_monitor_request_templates 的约束保持不变。
--
-- Runs after local 243_opencode_go_platform.sql. DROP ... IF EXISTS 保证可重入；
-- 新约束是本地旧约束的超集，存量行（包括全 NULL 额度）保持不变。
-- Composite is not deployed locally; do not reference its tables.

ALTER TABLE user_platform_quotas
    DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;

ALTER TABLE user_platform_quotas
    ADD CONSTRAINT user_platform_quotas_platform_check
    CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'kiro', 'grok',
                        'kimi', 'zhipu', 'deepseek', 'minimax', 'opencode_go', 'typesafe'));
