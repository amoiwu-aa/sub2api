-- Reported-only prompt token buckets so mixed traffic (Cursor/Grok estimated
-- rows plus provider-reported Claude/OpenAI rows) can still show a real
-- cache-read coverage percentage. The existing provider_cache_read_tokens
-- column is already reported-only; the denominator was not.
ALTER TABLE usage_dashboard_hourly
    ADD COLUMN IF NOT EXISTS reported_input_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS reported_cache_creation_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS reported_forced_cache_read_tokens BIGINT NOT NULL DEFAULT 0;

ALTER TABLE usage_dashboard_daily
    ADD COLUMN IF NOT EXISTS reported_input_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS reported_cache_creation_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS reported_forced_cache_read_tokens BIGINT NOT NULL DEFAULT 0;

-- Backfill existing hourly buckets from usage_logs. The join is bounded by
-- the hourly table, which is already retention-trimmed.
UPDATE usage_dashboard_hourly AS h
SET
    reported_input_tokens = s.reported_input_tokens,
    reported_cache_creation_tokens = s.reported_cache_creation_tokens,
    reported_forced_cache_read_tokens = s.reported_forced_cache_read_tokens
FROM (
    SELECT
        h2.bucket_start,
        COALESCE(SUM(ul.input_tokens) FILTER (WHERE ul.cache_usage_source = 'reported'), 0) AS reported_input_tokens,
        COALESCE(SUM(ul.cache_creation_tokens) FILTER (WHERE ul.cache_usage_source = 'reported'), 0) AS reported_cache_creation_tokens,
        COALESCE(SUM(GREATEST(COALESCE(ul.forced_cache_read_tokens, 0), 0)) FILTER (WHERE ul.cache_usage_source = 'reported'), 0) AS reported_forced_cache_read_tokens
    FROM usage_dashboard_hourly h2
    LEFT JOIN usage_logs ul
      ON ul.created_at >= h2.bucket_start
     AND ul.created_at < h2.bucket_start + INTERVAL '1 hour'
    GROUP BY h2.bucket_start
) AS s
WHERE h.bucket_start = s.bucket_start;

-- Roll the hourly buckets we still have into daily rows. Days older than the
-- hourly retention window stay 0 until the next daily recompute; the panel
-- will keep showing "partially observable" for those mixed totals.
UPDATE usage_dashboard_daily AS d
SET
    reported_input_tokens = s.reported_input_tokens,
    reported_cache_creation_tokens = s.reported_cache_creation_tokens,
    reported_forced_cache_read_tokens = s.reported_forced_cache_read_tokens
FROM (
    SELECT
        d2.bucket_date,
        COALESCE(SUM(h.reported_input_tokens), 0) AS reported_input_tokens,
        COALESCE(SUM(h.reported_cache_creation_tokens), 0) AS reported_cache_creation_tokens,
        COALESCE(SUM(h.reported_forced_cache_read_tokens), 0) AS reported_forced_cache_read_tokens
    FROM usage_dashboard_daily d2
    LEFT JOIN usage_dashboard_hourly h
      ON h.bucket_start >= d2.bucket_date
     AND h.bucket_start < d2.bucket_date + 1
    GROUP BY d2.bucket_date
) AS s
WHERE d.bucket_date = s.bucket_date;
