package migrations

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProxyTrafficUsageMigrationAddsCounters(t *testing.T) {
	content, err := FS.ReadFile("194_proxy_traffic_usage.sql")
	require.NoError(t, err)

	sql := string(content)
	require.Contains(t, sql, "traffic_upload_bytes")
	require.Contains(t, sql, "traffic_download_bytes")
	require.Contains(t, sql, "traffic_today_upload_bytes")
	require.Contains(t, sql, "traffic_today_download_bytes")
	require.Contains(t, sql, "traffic_today_date")
}
