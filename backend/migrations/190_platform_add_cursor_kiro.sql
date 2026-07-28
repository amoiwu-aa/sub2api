-- 把 cursor 与 kiro 两个新平台加入平台枚举相关的 CHECK 约束。
--
-- 背景：这两个平台作为一等公民接入（internal/domain/constants.go 的
-- PlatformCursor / PlatformKiro）。它们会出现在两个带 CHECK 的列上：
--   1. user_platform_quotas.platform —— service.AllowedQuotaPlatforms 已包含二者，
--      自助注册时 snapshotPlatformQuotaDefaults 会写入默认配额行；不放开 CHECK
--      会让整个注册事务 abort（与 157 修 grok 时同一个故障模式）。
--   2. composite_model_routes.target_platform —— composite 分组要能把
--      cursor/kiro 作为路由目标。
--
-- 新约束都是旧约束的超集，存量行瞬时校验通过；DROP ... IF EXISTS 保证可重入。
-- 注意 accounts.platform 本身没有 CHECK，无需处理。
ALTER TABLE user_platform_quotas
    DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;

ALTER TABLE user_platform_quotas
    ADD CONSTRAINT user_platform_quotas_platform_check
    CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'cursor', 'kiro'));

ALTER TABLE composite_model_routes
    DROP CONSTRAINT IF EXISTS composite_model_routes_target_platform_check;

ALTER TABLE composite_model_routes
    ADD CONSTRAINT composite_model_routes_target_platform_check
    CHECK (target_platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'cursor', 'kiro'));