-- Record the upstream's own billing quantity alongside our token-derived cost.
--
-- Kiro settles in "credits" (meteringEvent.usage), not tokens: a free-tier account
-- gets 50 credits per cycle and is hard-cut when they run out. Our token pricing is
-- a downstream price list and tracks that consumption only loosely -- measured at
-- 20x apart on real traffic -- so cost columns cannot answer "how much upstream
-- quota is left". Persisting the upstream figure makes per-request attribution
-- possible and lets the dashboard reconcile against GET /getUsageLimits.
--
-- 0 means "upstream reported nothing" (every non-Kiro platform today), which is why
-- this is NOT NULL DEFAULT 0 rather than nullable -- callers sum it without guards.
ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS upstream_credits DECIMAL(20, 10) NOT NULL DEFAULT 0;
