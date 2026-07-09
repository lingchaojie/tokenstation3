-- 重建 user_platform_quotas.platform 的 CHECK 约束，使其覆盖全部平台。
--
-- 背景：迁移 157 曾把 grok 加入约束（anthropic/openai/gemini/antigravity/grok），
-- 但随后的迁移 162 为加入 kiro 而 DROP 重建约束时漏掉了 grok，导致最终约束回退为
-- anthropic/openai/gemini/antigravity/kiro —— grok 又被排除在外。
-- 结果：157 修复的注册 bug（snapshotPlatformQuotaDefaults 写 grok 默认配额 → 违反
-- CHECK → 注册事务 aborted → 500/404）在跑过 162 的库上复发。
--
-- 修复：把约束与代码平台列表（internal/domain/constants.go 的 Platform* 常量）对齐，
-- 一次性包含全部 6 个平台。DROP ... IF EXISTS 保证可重入；新约束是旧约束的超集，
-- 存量行瞬时校验通过，故用 NOT VALID + VALIDATE 避免长时间持锁。
ALTER TABLE user_platform_quotas
    DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;

ALTER TABLE user_platform_quotas
    ADD CONSTRAINT user_platform_quotas_platform_check
    CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'kiro'))
    NOT VALID;

ALTER TABLE user_platform_quotas
    VALIDATE CONSTRAINT user_platform_quotas_platform_check;
