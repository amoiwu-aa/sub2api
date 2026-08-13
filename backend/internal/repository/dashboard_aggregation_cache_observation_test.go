package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/stretchr/testify/require"
)

func TestDashboardAggregationPersistsCacheObservationFields(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := newDashboardAggregationRepositoryWithSQL(db)
	start := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)

	mock.ExpectExec(`(?s)WITH hourly AS .*GREATEST\(COALESCE\(forced_cache_read_tokens, 0\), 0\).*provider_cache_read_tokens.*cache_hit_requests.*reported_requests.*estimated_requests.*unavailable_requests.*INSERT INTO usage_dashboard_hourly.*ON CONFLICT`).
		WithArgs(start, end, timezone.Name()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, repo.upsertHourlyAggregates(context.Background(), start, end))

	mock.ExpectExec(`(?s)WITH daily AS .*SUM\(provider_cache_read_tokens\).*SUM\(cache_hit_requests\).*SUM\(reported_requests\).*SUM\(estimated_requests\).*SUM\(unavailable_requests\).*INSERT INTO usage_dashboard_daily.*ON CONFLICT`).
		WithArgs(start, end, start, end, timezone.Name()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, repo.upsertDailyAggregates(context.Background(), start, end))
	require.NoError(t, mock.ExpectationsWereMet())
}
