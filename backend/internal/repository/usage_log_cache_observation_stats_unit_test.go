//go:build unit

package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

func TestUsageLogRepositorySessionStatsAggregatesCacheObservation(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}

	mock.ExpectQuery("GREATEST\\(cache_read_tokens - GREATEST\\(COALESCE\\(forced_cache_read_tokens, 0\\), 0\\), 0\\)").
		WithArgs(int64(41), int64(73), "session-123").
		WillReturnRows(sqlmock.NewRows([]string{
			"total_requests",
			"total_input_tokens",
			"total_output_tokens",
			"total_cache_tokens",
			"total_cache_creation_tokens",
			"total_cache_read_tokens",
			"provider_cache_read_tokens",
			"cache_hit_requests",
			"forced_cache_read_tokens",
			"reported_requests",
			"estimated_requests",
			"unavailable_requests",
			"total_cost",
			"total_actual_cost",
			"total_account_cost",
			"avg_duration_ms",
		}).AddRow(
			int64(3),
			int64(10),
			int64(20),
			int64(330),
			int64(30),
			int64(300),
			int64(225),
			int64(1),
			int64(75),
			int64(1),
			int64(1),
			int64(1),
			1.2,
			1.0,
			1.2,
			20.0,
		))

	stats, err := repo.GetStatsWithFilters(context.Background(), usagestats.UsageLogFilters{
		UserID:    41,
		APIKeyID:  73,
		SessionID: "session-123",
	})
	require.NoError(t, err)
	require.Equal(t, int64(225), stats.TotalProviderCacheReadTokens)
	require.Equal(t, int64(1), stats.CacheHitRequests)
	require.Equal(t, int64(75), stats.TotalForcedCacheReadTokens)
	require.Equal(t, int64(1), stats.ReportedRequests)
	require.Equal(t, int64(1), stats.EstimatedRequests)
	require.Equal(t, int64(1), stats.UnavailableRequests)
	require.Equal(t, int64(360), stats.TotalTokens)
	require.Empty(t, stats.Endpoints, "session summary must not run unrelated endpoint aggregations")
	require.NoError(t, mock.ExpectationsWereMet())
}
