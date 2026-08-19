package repository

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestClampDistributionUsageRange(t *testing.T) {
	end := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	start := end.Add(-10 * 24 * time.Hour)

	gotStart, gotEnd, ok := clampDistributionUsageRange(start, end)
	require.True(t, ok)
	require.Equal(t, start, gotStart)
	require.Equal(t, end, gotEnd)

	tooWideStart := end.Add(-40 * 24 * time.Hour)
	gotStart, gotEnd, ok = clampDistributionUsageRange(tooWideStart, end)
	require.True(t, ok)
	require.Equal(t, end, gotEnd)
	require.Equal(t, end.Add(-31*24*time.Hour), gotStart)

	_, _, ok = clampDistributionUsageRange(end, start)
	require.False(t, ok)
	_, _, ok = clampDistributionUsageRange(end, end)
	require.False(t, ok)
}

func TestClampDistributionUsageLimit(t *testing.T) {
	require.Equal(t, 10, clampDistributionUsageLimit(0))
	require.Equal(t, 10, clampDistributionUsageLimit(-3))
	require.Equal(t, 10, clampDistributionUsageLimit(10))
	require.Equal(t, 50, clampDistributionUsageLimit(51))
	require.Equal(t, 25, clampDistributionUsageLimit(25))
}

func TestDistributionUsageDateFormatAndSortAllowlist(t *testing.T) {
	require.Equal(t, "YYYY-MM-DD HH24:00", distributionUsageDateFormat("hour"))
	require.Equal(t, "YYYY-MM-DD HH24:00", distributionUsageDateFormat("HOUR"))
	require.Equal(t, "YYYY-MM-DD", distributionUsageDateFormat("day"))
	require.Equal(t, "YYYY-MM-DD", distributionUsageDateFormat("week"))
	require.Equal(t, "YYYY-MM-DD", distributionUsageDateFormat(""))

	require.Equal(t, "requests DESC, tokens DESC, user_id ASC", distributionUsageRankingOrderBy("requests"))
	require.Equal(t, "tokens DESC, requests DESC, user_id ASC", distributionUsageRankingOrderBy("tokens"))
	require.Equal(t, "actual_cost DESC, tokens DESC, user_id ASC", distributionUsageRankingOrderBy("actual"))
	require.Equal(t, "actual_cost DESC, tokens DESC, user_id ASC", distributionUsageRankingOrderBy("DROP TABLE users"))
}

func TestDistributionUsageSQLBuildersUseManagedCTE(t *testing.T) {
	adminID := int64(7)
	userID := int64(11)
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC)

	queries := []distributionUsageQuery{
		buildDistributionSnapshotQuery(adminID, start, end),
		buildDistributionTrendQuery(adminID, start, end, "day", service.DistributionUsageFilter{}),
		buildDistributionTrendQuery(adminID, start, end, "hour", service.DistributionUsageFilter{
			UserID:   userID,
			Model:    "claude-opus-4",
			Platform: "anthropic",
		}),
		buildDistributionModelStatsQuery(adminID, start, end, 0),
		buildDistributionModelStatsQuery(adminID, start, end, userID),
		buildDistributionUserRankingQuery(adminID, start, end, "actual", 10),
		buildDistributionErrorSummaryQuery(adminID, start, end),
		buildDistributionUserSummaryQuery(adminID, userID, start, end),
	}

	for _, q := range queries {
		assertDistributionManagedScopeSQL(t, q.sql)
		require.Equal(t, adminID, q.args[0], "first bind arg must be adminID for the CTE")
	}
}

func TestDistributionUsageSQLBuildersRejectUnscopedFilters(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC)
	q := buildDistributionTrendQuery(9, start, end, "day", service.DistributionUsageFilter{UserID: 42})
	require.Contains(t, q.sql, "AND ul.user_id = $")
	require.NotContains(t, strings.ToLower(q.sql), "user_id in")
	require.NotContains(t, strings.ToLower(q.sql), "user_id = any")
	require.Equal(t, int64(42), q.args[len(q.args)-1])

	summary := buildDistributionUserSummaryQuery(9, 42, start, end)
	require.Contains(t, summary.sql, "WHERE mu.id = $4")
	require.Equal(t, []any{int64(9), start, end, int64(42)}, summary.args)

	ranking := buildDistributionUserRankingQuery(9, start, end, "tokens", 99)
	require.Contains(t, ranking.sql, "LIMIT $4")
	require.Equal(t, 50, ranking.args[3])
	require.Contains(t, ranking.sql, "tokens DESC, requests DESC, user_id ASC")
	require.Contains(t, ranking.sql, "COALESCE(mu.email, '')")
	require.Contains(t, ranking.sql, "COALESCE(mu.username, '')")
}

func TestDistributionUsageTrendOptionalFilters(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)

	plain := buildDistributionTrendQuery(3, start, end, "day", service.DistributionUsageFilter{})
	require.NotContains(t, plain.sql, "LEFT JOIN groups")
	require.NotContains(t, plain.sql, "LEFT JOIN accounts")
	require.Contains(t, plain.sql, "YYYY-MM-DD")
	require.NotContains(t, plain.sql, "HH24:00")

	filtered := buildDistributionTrendQuery(3, start, end, "hour", service.DistributionUsageFilter{
		Platform: "openai",
		Model:    "gpt-5",
	})
	require.Contains(t, filtered.sql, "LEFT JOIN groups g ON g.id = ul.group_id")
	require.Contains(t, filtered.sql, "LEFT JOIN accounts a ON a.id = ul.account_id")
	require.Contains(t, filtered.sql, distributionUsagePlatformExpr)
	require.Contains(t, filtered.sql, distributionUsageRequestedModelExpr)
	require.Contains(t, filtered.sql, "YYYY-MM-DD HH24:00")
	require.Equal(t, "gpt-5", filtered.args[3])
	require.Equal(t, "openai", filtered.args[4])
}

func TestDistributionUsageEmptyAdminIDSkipsQuery(t *testing.T) {
	repo := &distributionUsageRepository{sql: nil}
	ctx := context.Background()
	start := time.Now().Add(-time.Hour)
	end := time.Now()

	snapshot, err := repo.Snapshot(ctx, 0, start, end)
	require.NoError(t, err)
	require.Equal(t, &service.DistributionUsageSnapshot{}, snapshot)

	snapshot, err = repo.Snapshot(ctx, -1, start, end)
	require.NoError(t, err)
	require.Equal(t, &service.DistributionUsageSnapshot{}, snapshot)

	trend, err := repo.Trend(ctx, 0, start, end, "day", service.DistributionUsageFilter{UserID: 99})
	require.NoError(t, err)
	require.Empty(t, trend)

	models, err := repo.ModelStats(ctx, 0, start, end, 99)
	require.NoError(t, err)
	require.Empty(t, models)

	ranking, err := repo.UserRanking(ctx, 0, start, end, "actual", 10)
	require.NoError(t, err)
	require.Empty(t, ranking)

	errorsum, err := repo.ErrorSummary(ctx, 0, start, end)
	require.NoError(t, err)
	require.Equal(t, &service.DistributionUsageErrorSummary{}, errorsum)

	summary, err := repo.UserSummary(ctx, 0, 11, start, end)
	require.NoError(t, err)
	require.Equal(t, &service.DistributionUsageUserSummary{}, summary)

	summary, err = repo.UserSummary(ctx, 7, 0, start, end)
	require.NoError(t, err)
	require.Equal(t, &service.DistributionUsageUserSummary{}, summary)
}

func TestDistributionUsageEmptyInvalidRangeSkipsQuery(t *testing.T) {
	repo := &distributionUsageRepository{sql: nil}
	ctx := context.Background()
	now := time.Now()

	snapshot, err := repo.Snapshot(ctx, 7, now, now.Add(-time.Hour))
	require.NoError(t, err)
	require.Equal(t, &service.DistributionUsageSnapshot{}, snapshot)
}

func assertDistributionManagedScopeSQL(t *testing.T, query string) {
	t.Helper()
	require.Contains(t, query, "managed_users AS")
	require.Contains(t, query, distributionManagedUsersCTE)
	require.Contains(t, query, "u.role = 'user'")
	require.Contains(t, query, "u.deleted_at IS NULL")
	require.Contains(t, query, "u.created_by_admin_id = $1")
	require.Contains(t, query, "user_affiliates")
	require.Contains(t, query, "ua.inviter_id = $1")
	require.NotContains(t, query, "usage_dashboard_daily")
	require.NotContains(t, query, "usage_dashboard_hourly")
	require.NotContains(t, query, "ops_error_logs")
	require.NotContains(t, query, "account_cost")
	require.NotContains(t, strings.ToLower(query), "user_id in")
}
