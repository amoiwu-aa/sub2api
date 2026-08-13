package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCacheUsageObservationMigrationAddsPersistenceAndSessionIndex(t *testing.T) {
	content, err := FS.ReadFile("221_cache_usage_observation.sql")
	require.NoError(t, err)

	sql := strings.ToLower(string(content))
	require.Contains(t, sql, "add column if not exists cache_usage_source")
	require.Contains(t, sql, "add column if not exists forced_cache_read_tokens")
	require.NotContains(t, sql, "cache_usage_source varchar(16) not null")
	require.NotContains(t, sql, "forced_cache_read_tokens integer not null")

	require.Contains(t, sql, "usage_logs_cache_usage_source_valid")
	require.Contains(t, sql, "usage_logs_forced_cache_read_tokens_nonnegative")
	require.Contains(t, sql, "alter table usage_dashboard_hourly")
	require.Contains(t, sql, "alter table usage_dashboard_daily")
	require.Contains(t, sql, "provider_cache_read_tokens bigint not null default 0")
	require.Contains(t, sql, "cache_hit_requests bigint not null default 0")
	require.Contains(t, sql, "estimated_requests bigint not null default 0")

	indexContent, err := FS.ReadFile("222_cache_usage_session_index_notx.sql")
	require.NoError(t, err)
	indexSQL := strings.ToLower(string(indexContent))
	require.Contains(t, indexSQL, "create index concurrently if not exists idx_usage_logs_session_usage_v2")
	require.Contains(t, indexSQL, "on usage_logs (user_id, api_key_id, session_id, created_at)")
	require.Contains(t, indexSQL, "where session_id is not null")
}
