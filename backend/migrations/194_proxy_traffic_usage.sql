-- Track proxy transfer volume independently of token usage. These counters
-- start from this migration; historical proxy bandwidth cannot be reconstructed
-- from usage logs because wire bytes were not persisted previously.
ALTER TABLE proxies
    ADD COLUMN IF NOT EXISTS traffic_upload_bytes BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS traffic_download_bytes BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS traffic_today_upload_bytes BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS traffic_today_download_bytes BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS traffic_today_date DATE NOT NULL DEFAULT CURRENT_DATE;
