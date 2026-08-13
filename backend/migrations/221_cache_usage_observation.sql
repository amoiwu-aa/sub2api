-- Preserve whether cache-token usage was reported by the provider, estimated,
-- or unavailable. NULL remains reserved for rows written before this
-- observation contract existed.
ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS cache_usage_source VARCHAR(16);

-- Accounting-only cache-read tokens introduced by ForceCacheBilling. The
-- existing cache_read_tokens value remains unchanged for billing compatibility.
-- Historical rows stay NULL and aggregate as zero.
ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS forced_cache_read_tokens INTEGER;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'usage_logs_cache_usage_source_valid'
          AND conrelid = 'usage_logs'::regclass
    ) THEN
        ALTER TABLE usage_logs
            ADD CONSTRAINT usage_logs_cache_usage_source_valid
            CHECK (
                cache_usage_source IS NULL
                OR cache_usage_source IN ('reported', 'estimated', 'unavailable')
            )
            NOT VALID;
    END IF;
END
$$;

-- Reject future negative adjustments while allowing reads of any historical
-- dirty data to be clamped safely by the application.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'usage_logs_forced_cache_read_tokens_nonnegative'
          AND conrelid = 'usage_logs'::regclass
    ) THEN
        ALTER TABLE usage_logs
            ADD CONSTRAINT usage_logs_forced_cache_read_tokens_nonnegative
            CHECK (forced_cache_read_tokens IS NULL OR forced_cache_read_tokens >= 0)
            NOT VALID;
    END IF;
END
$$;

-- Dashboard aggregates must retain the observation contract; otherwise Cursor
-- estimates would be indistinguishable from provider-reported cache hits.
ALTER TABLE usage_dashboard_hourly
    ADD COLUMN IF NOT EXISTS provider_cache_read_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cache_hit_requests BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS forced_cache_read_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS reported_requests BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS estimated_requests BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS unavailable_requests BIGINT NOT NULL DEFAULT 0;

ALTER TABLE usage_dashboard_daily
    ADD COLUMN IF NOT EXISTS provider_cache_read_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cache_hit_requests BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS forced_cache_read_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS reported_requests BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS estimated_requests BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS unavailable_requests BIGINT NOT NULL DEFAULT 0;
