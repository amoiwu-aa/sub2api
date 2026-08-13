-- Session Usage v2 always scopes by owner + API key + explicit session and may
-- poll recent rows, so keep the time dimension last for range/index scans.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_session_usage_v2
    ON usage_logs (user_id, api_key_id, session_id, created_at)
    WHERE session_id IS NOT NULL;
