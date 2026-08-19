package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// distributionManagedUsersCTE is the only allowed user scope for distribution
// usage queries. It matches user_repo_ownership.go: role=user, not deleted,
// created_by_admin_id OR affiliate inviter. $1 is always adminID.
//
// Global dashboard tables (usage_dashboard_daily/hourly) have no user_id and
// must never be read here.
const distributionManagedUsersCTE = `
managed_users AS (
	SELECT u.id, u.email, u.username
	FROM users u
	WHERE u.role = 'user'
	  AND u.deleted_at IS NULL
	  AND (u.created_by_admin_id = $1 OR EXISTS (
	        SELECT 1 FROM user_affiliates ua
	        WHERE ua.user_id = u.id AND ua.inviter_id = $1
	      ))
)`

// distributionUsageRequestedModelExpr matches the default requested-model
// dimension used by user-dashboard model stats.
const distributionUsageRequestedModelExpr = "COALESCE(NULLIF(TRIM(ul.requested_model), ''), ul.model)"

// distributionUsagePlatformExpr matches usageLogEffectivePlatformExpr
// (group.platform, falling back to account.platform; composite uses account).
const distributionUsagePlatformExpr = "CASE WHEN g.platform = 'composite' THEN a.platform ELSE COALESCE(NULLIF(g.platform,''), a.platform) END"

const distributionUsageTokenSelect = `
			COUNT(*) as requests,
			COALESCE(SUM(ul.input_tokens), 0) as input_tokens,
			COALESCE(SUM(ul.output_tokens), 0) as output_tokens,
			COALESCE(SUM(ul.cache_creation_tokens), 0) as cache_creation_tokens,
			COALESCE(SUM(ul.cache_read_tokens), 0) as cache_read_tokens,
			COALESCE(SUM(GREATEST(ul.cache_read_tokens - GREATEST(COALESCE(ul.forced_cache_read_tokens, 0), 0), 0))
				FILTER (WHERE ul.cache_usage_source = 'reported'), 0) as provider_cache_read_tokens,
			COUNT(*) FILTER (
				WHERE ul.cache_usage_source = 'reported'
				  AND GREATEST(ul.cache_read_tokens - GREATEST(COALESCE(ul.forced_cache_read_tokens, 0), 0), 0) > 0
			) as cache_hit_requests,
			COALESCE(SUM(GREATEST(COALESCE(ul.forced_cache_read_tokens, 0), 0)), 0) as forced_cache_read_tokens,
			COALESCE(SUM(ul.input_tokens) FILTER (WHERE ul.cache_usage_source = 'reported'), 0) as reported_input_tokens,
			COALESCE(SUM(ul.cache_creation_tokens) FILTER (WHERE ul.cache_usage_source = 'reported'), 0) as reported_cache_creation_tokens,
			COALESCE(SUM(GREATEST(COALESCE(ul.forced_cache_read_tokens, 0), 0)) FILTER (WHERE ul.cache_usage_source = 'reported'), 0) as reported_forced_cache_read_tokens,
			COUNT(*) FILTER (WHERE ul.cache_usage_source = 'reported') as reported_requests,
			COUNT(*) FILTER (WHERE ul.cache_usage_source = 'estimated') as estimated_requests,
			COUNT(*) FILTER (WHERE ul.cache_usage_source = 'unavailable') as unavailable_requests,
			COALESCE(SUM(ul.input_tokens + ul.output_tokens + ul.cache_creation_tokens + ul.cache_read_tokens), 0) as total_tokens,
			COALESCE(SUM(ul.total_cost), 0) as cost,
			COALESCE(SUM(ul.actual_cost), 0) as actual_cost`

const distributionUsageLogsFromManaged = `
		FROM usage_logs ul
		INNER JOIN managed_users mu ON mu.id = ul.user_id
		WHERE ul.created_at >= $2 AND ul.created_at < $3`

const distributionUsageLogsFromManagedWithPlatform = `
		FROM usage_logs ul
		INNER JOIN managed_users mu ON mu.id = ul.user_id
		LEFT JOIN groups g ON g.id = ul.group_id
		LEFT JOIN accounts a ON a.id = ul.account_id
		WHERE ul.created_at >= $2 AND ul.created_at < $3`

type distributionUsageRepository struct {
	sql sqlExecutor
}

var _ service.DistributionUsageRepository = (*distributionUsageRepository)(nil)

func NewDistributionUsageRepository(_ *dbent.Client, sqlDB *sql.DB) service.DistributionUsageRepository {
	return newDistributionUsageRepositoryWithSQL(sqlDB)
}

func newDistributionUsageRepositoryWithSQL(sqlq sqlExecutor) *distributionUsageRepository {
	return &distributionUsageRepository{sql: sqlq}
}

func (r *distributionUsageRepository) Snapshot(ctx context.Context, adminID int64, start, end time.Time) (*service.DistributionUsageSnapshot, error) {
	if adminID <= 0 {
		return &service.DistributionUsageSnapshot{}, nil
	}
	start, end, ok := clampDistributionUsageRange(start, end)
	if !ok {
		return &service.DistributionUsageSnapshot{}, nil
	}
	q := buildDistributionSnapshotQuery(adminID, start, end)
	stats := &service.DistributionUsageSnapshot{}
	if err := scanSingleRow(
		ctx,
		r.sql,
		q.sql,
		q.args,
		&stats.Requests,
		&stats.InputTokens,
		&stats.OutputTokens,
		&stats.CacheCreationTokens,
		&stats.CacheReadTokens,
		&stats.TotalTokens,
		&stats.ActualCost,
		&stats.SuccessRequests,
		&stats.ErrorRequests,
		&stats.CacheHitRequests,
		&stats.ActiveUsers,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &service.DistributionUsageSnapshot{}, nil
		}
		return nil, err
	}
	return stats, nil
}

func (r *distributionUsageRepository) Trend(ctx context.Context, adminID int64, start, end time.Time, granularity string, filter service.DistributionUsageFilter) (results []service.DistributionUsageTrendPoint, err error) {
	results = make([]service.DistributionUsageTrendPoint, 0)
	if adminID <= 0 {
		return results, nil
	}
	start, end, ok := clampDistributionUsageRange(start, end)
	if !ok {
		return results, nil
	}
	q := buildDistributionTrendQuery(adminID, start, end, granularity, filter)
	rows, err := r.sql.QueryContext(ctx, q.sql, q.args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
			results = nil
		}
	}()
	results, err = scanDistributionTrendRows(rows)
	if err != nil {
		return nil, err
	}
	return results, nil
}

func (r *distributionUsageRepository) ModelStats(ctx context.Context, adminID int64, start, end time.Time, userID int64) (results []service.DistributionUsageModelStat, err error) {
	results = make([]service.DistributionUsageModelStat, 0)
	if adminID <= 0 {
		return results, nil
	}
	start, end, ok := clampDistributionUsageRange(start, end)
	if !ok {
		return results, nil
	}
	q := buildDistributionModelStatsQuery(adminID, start, end, userID)
	rows, err := r.sql.QueryContext(ctx, q.sql, q.args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
			results = nil
		}
	}()
	results, err = scanDistributionModelStatRows(rows)
	if err != nil {
		return nil, err
	}
	return results, nil
}

func (r *distributionUsageRepository) UserRanking(ctx context.Context, adminID int64, start, end time.Time, sort string, limit int) (results []service.DistributionUsageUserRankingItem, err error) {
	results = make([]service.DistributionUsageUserRankingItem, 0)
	if adminID <= 0 {
		return results, nil
	}
	start, end, ok := clampDistributionUsageRange(start, end)
	if !ok {
		return results, nil
	}
	q := buildDistributionUserRankingQuery(adminID, start, end, sort, limit)
	rows, err := r.sql.QueryContext(ctx, q.sql, q.args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
			results = nil
		}
	}()
	for rows.Next() {
		var row service.DistributionUsageUserRankingItem
		if err = rows.Scan(&row.UserID, &row.Email, &row.Username, &row.Requests, &row.Tokens, &row.ActualCost); err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func (r *distributionUsageRepository) ErrorSummary(ctx context.Context, adminID int64, start, end time.Time) (*service.DistributionUsageErrorSummary, error) {
	if adminID <= 0 {
		return &service.DistributionUsageErrorSummary{}, nil
	}
	start, end, ok := clampDistributionUsageRange(start, end)
	if !ok {
		return &service.DistributionUsageErrorSummary{}, nil
	}
	q := buildDistributionErrorSummaryQuery(adminID, start, end)
	summary := &service.DistributionUsageErrorSummary{}
	if err := scanSingleRow(ctx, r.sql, q.sql, q.args, &summary.BilledRequests, &summary.FailedOrUnbilledRequests); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &service.DistributionUsageErrorSummary{}, nil
		}
		return nil, err
	}
	return summary, nil
}

func (r *distributionUsageRepository) UserSummary(ctx context.Context, adminID int64, userID int64, start, end time.Time) (*service.DistributionUsageUserSummary, error) {
	if adminID <= 0 || userID <= 0 {
		return &service.DistributionUsageUserSummary{}, nil
	}
	start, end, ok := clampDistributionUsageRange(start, end)
	if !ok {
		return &service.DistributionUsageUserSummary{}, nil
	}
	q := buildDistributionUserSummaryQuery(adminID, userID, start, end)
	summary := &service.DistributionUsageUserSummary{}
	if err := scanSingleRow(
		ctx,
		r.sql,
		q.sql,
		q.args,
		&summary.UserID,
		&summary.Email,
		&summary.Username,
		&summary.Requests,
		&summary.InputTokens,
		&summary.OutputTokens,
		&summary.CacheCreationTokens,
		&summary.CacheReadTokens,
		&summary.TotalTokens,
		&summary.ActualCost,
		&summary.SuccessRequests,
		&summary.ErrorRequests,
		&summary.CacheHitRequests,
		&summary.ActiveUsers,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &service.DistributionUsageUserSummary{}, nil
		}
		return nil, err
	}
	return summary, nil
}

type distributionUsageQuery struct {
	sql  string
	args []any
}

func buildDistributionSnapshotQuery(adminID int64, start, end time.Time) distributionUsageQuery {
	sqlText := `
		WITH ` + distributionManagedUsersCTE + `
		SELECT
			COUNT(*) as requests,
			COALESCE(SUM(ul.input_tokens), 0) as input_tokens,
			COALESCE(SUM(ul.output_tokens), 0) as output_tokens,
			COALESCE(SUM(ul.cache_creation_tokens), 0) as cache_creation_tokens,
			COALESCE(SUM(ul.cache_read_tokens), 0) as cache_read_tokens,
			COALESCE(SUM(ul.input_tokens + ul.output_tokens + ul.cache_creation_tokens + ul.cache_read_tokens), 0) as total_tokens,
			COALESCE(SUM(ul.actual_cost), 0) as actual_cost,
			COUNT(*) FILTER (WHERE ul.actual_cost > 0) as success_requests,
			COUNT(*) FILTER (WHERE ul.actual_cost <= 0) as error_requests,
			COUNT(*) FILTER (
				WHERE ul.cache_usage_source = 'reported'
				  AND GREATEST(ul.cache_read_tokens - GREATEST(COALESCE(ul.forced_cache_read_tokens, 0), 0), 0) > 0
			) as cache_hit_requests,
			COUNT(DISTINCT ul.user_id) as active_users
		` + distributionUsageLogsFromManaged
	return distributionUsageQuery{sql: sqlText, args: []any{adminID, start, end}}
}

func buildDistributionTrendQuery(adminID int64, start, end time.Time, granularity string, filter service.DistributionUsageFilter) distributionUsageQuery {
	dateFormat := distributionUsageDateFormat(granularity)
	usePlatform := strings.TrimSpace(filter.Platform) != ""
	from := distributionUsageLogsFromManaged
	if usePlatform {
		from = distributionUsageLogsFromManagedWithPlatform
	}
	sqlText := fmt.Sprintf(`
		WITH `+distributionManagedUsersCTE+`
		SELECT
			TO_CHAR(ul.created_at, '%s') as date,
			`+distributionUsageTokenSelect+`
		`+from, dateFormat)
	args := []any{adminID, start, end}
	sqlText, args = appendDistributionUserIDFilter(sqlText, args, filter.UserID)
	sqlText, args = appendDistributionModelFilter(sqlText, args, filter.Model)
	sqlText, args = appendDistributionPlatformFilter(sqlText, args, filter.Platform)
	sqlText += " GROUP BY date ORDER BY date ASC"
	return distributionUsageQuery{sql: sqlText, args: args}
}

func buildDistributionModelStatsQuery(adminID int64, start, end time.Time, userID int64) distributionUsageQuery {
	sqlText := `
		WITH ` + distributionManagedUsersCTE + `
		SELECT
			` + distributionUsageRequestedModelExpr + ` as model,
			` + distributionUsageTokenSelect + `
		` + distributionUsageLogsFromManaged
	args := []any{adminID, start, end}
	sqlText, args = appendDistributionUserIDFilter(sqlText, args, userID)
	sqlText += " GROUP BY " + distributionUsageRequestedModelExpr + " ORDER BY total_tokens DESC"
	return distributionUsageQuery{sql: sqlText, args: args}
}

func buildDistributionUserRankingQuery(adminID int64, start, end time.Time, sort string, limit int) distributionUsageQuery {
	orderBy := distributionUsageRankingOrderBy(sort)
	limit = clampDistributionUsageLimit(limit)
	sqlText := `
		WITH ` + distributionManagedUsersCTE + `
		SELECT
			mu.id as user_id,
			COALESCE(mu.email, '') as email,
			COALESCE(mu.username, '') as username,
			COUNT(*) as requests,
			COALESCE(SUM(ul.input_tokens + ul.output_tokens + ul.cache_creation_tokens + ul.cache_read_tokens), 0) as tokens,
			COALESCE(SUM(ul.actual_cost), 0) as actual_cost
		` + distributionUsageLogsFromManaged + `
		GROUP BY mu.id, mu.email, mu.username
		ORDER BY ` + orderBy + `
		LIMIT $4`
	return distributionUsageQuery{sql: sqlText, args: []any{adminID, start, end, limit}}
}

func buildDistributionErrorSummaryQuery(adminID int64, start, end time.Time) distributionUsageQuery {
	sqlText := `
		WITH ` + distributionManagedUsersCTE + `
		SELECT
			COUNT(*) FILTER (WHERE ul.actual_cost > 0) as billed_requests,
			COUNT(*) FILTER (WHERE ul.actual_cost <= 0) as failed_or_unbilled_requests
		` + distributionUsageLogsFromManaged
	return distributionUsageQuery{sql: sqlText, args: []any{adminID, start, end}}
}

func buildDistributionUserSummaryQuery(adminID, userID int64, start, end time.Time) distributionUsageQuery {
	// Drill-down is AND mu.id = $4 after the CTE. An unmanaged user yields no row.
	sqlText := `
		WITH ` + distributionManagedUsersCTE + `
		SELECT
			mu.id as user_id,
			COALESCE(mu.email, '') as email,
			COALESCE(mu.username, '') as username,
			COUNT(ul.id) as requests,
			COALESCE(SUM(ul.input_tokens), 0) as input_tokens,
			COALESCE(SUM(ul.output_tokens), 0) as output_tokens,
			COALESCE(SUM(ul.cache_creation_tokens), 0) as cache_creation_tokens,
			COALESCE(SUM(ul.cache_read_tokens), 0) as cache_read_tokens,
			COALESCE(SUM(ul.input_tokens + ul.output_tokens + ul.cache_creation_tokens + ul.cache_read_tokens), 0) as total_tokens,
			COALESCE(SUM(ul.actual_cost), 0) as actual_cost,
			COUNT(ul.id) FILTER (WHERE ul.actual_cost > 0) as success_requests,
			COUNT(ul.id) FILTER (WHERE ul.actual_cost <= 0) as error_requests,
			COUNT(ul.id) FILTER (
				WHERE ul.cache_usage_source = 'reported'
				  AND GREATEST(ul.cache_read_tokens - GREATEST(COALESCE(ul.forced_cache_read_tokens, 0), 0), 0) > 0
			) as cache_hit_requests,
			COUNT(DISTINCT ul.user_id) as active_users
		FROM managed_users mu
		LEFT JOIN usage_logs ul ON ul.user_id = mu.id
			AND ul.created_at >= $2 AND ul.created_at < $3
		WHERE mu.id = $4
		GROUP BY mu.id, mu.email, mu.username`
	return distributionUsageQuery{sql: sqlText, args: []any{adminID, start, end, userID}}
}

func appendDistributionUserIDFilter(query string, args []any, userID int64) (string, []any) {
	if userID <= 0 {
		return query, args
	}
	query += fmt.Sprintf(" AND ul.user_id = $%d", len(args)+1)
	args = append(args, userID)
	return query, args
}

func appendDistributionModelFilter(query string, args []any, model string) (string, []any) {
	model = strings.TrimSpace(model)
	if model == "" {
		return query, args
	}
	query += fmt.Sprintf(" AND %s = $%d", distributionUsageRequestedModelExpr, len(args)+1)
	args = append(args, model)
	return query, args
}

func appendDistributionPlatformFilter(query string, args []any, platform string) (string, []any) {
	platform = strings.TrimSpace(platform)
	if platform == "" {
		return query, args
	}
	query += fmt.Sprintf(" AND %s = $%d", distributionUsagePlatformExpr, len(args)+1)
	args = append(args, platform)
	return query, args
}

func distributionUsageDateFormat(granularity string) string {
	if strings.EqualFold(strings.TrimSpace(granularity), service.DistributionUsageGranularityHour) {
		return "YYYY-MM-DD HH24:00"
	}
	return "YYYY-MM-DD"
}

func distributionUsageRankingOrderBy(sort string) string {
	switch strings.ToLower(strings.TrimSpace(sort)) {
	case service.DistributionUsageSortRequests:
		return "requests DESC, tokens DESC, user_id ASC"
	case service.DistributionUsageSortTokens:
		return "tokens DESC, requests DESC, user_id ASC"
	default:
		return "actual_cost DESC, tokens DESC, user_id ASC"
	}
}

func clampDistributionUsageRange(start, end time.Time) (time.Time, time.Time, bool) {
	start = start.UTC()
	end = end.UTC()
	if !end.After(start) {
		return start, end, false
	}
	maxRange := time.Duration(service.DistributionUsageMaxRangeDays) * 24 * time.Hour
	if end.Sub(start) > maxRange {
		start = end.Add(-maxRange)
	}
	return start, end, true
}

func clampDistributionUsageLimit(limit int) int {
	if limit <= 0 {
		return service.DistributionUsageDefaultLimit
	}
	if limit > service.DistributionUsageMaxLimit {
		return service.DistributionUsageMaxLimit
	}
	return limit
}

func scanDistributionTrendRows(rows *sql.Rows) ([]service.DistributionUsageTrendPoint, error) {
	results := make([]service.DistributionUsageTrendPoint, 0)
	for rows.Next() {
		var row service.DistributionUsageTrendPoint
		if err := rows.Scan(
			&row.Date,
			&row.Requests,
			&row.InputTokens,
			&row.OutputTokens,
			&row.CacheCreationTokens,
			&row.CacheReadTokens,
			&row.ProviderCacheReadTokens,
			&row.CacheHitRequests,
			&row.ForcedCacheReadTokens,
			&row.ReportedInputTokens,
			&row.ReportedCacheCreationTokens,
			&row.ReportedForcedCacheReadTokens,
			&row.ReportedRequests,
			&row.EstimatedRequests,
			&row.UnavailableRequests,
			&row.TotalTokens,
			&row.Cost,
			&row.ActualCost,
		); err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func scanDistributionModelStatRows(rows *sql.Rows) ([]service.DistributionUsageModelStat, error) {
	results := make([]service.DistributionUsageModelStat, 0)
	for rows.Next() {
		var row service.DistributionUsageModelStat
		if err := rows.Scan(
			&row.Model,
			&row.Requests,
			&row.InputTokens,
			&row.OutputTokens,
			&row.CacheCreationTokens,
			&row.CacheReadTokens,
			&row.ProviderCacheReadTokens,
			&row.CacheHitRequests,
			&row.ForcedCacheReadTokens,
			&row.ReportedInputTokens,
			&row.ReportedCacheCreationTokens,
			&row.ReportedForcedCacheReadTokens,
			&row.ReportedRequests,
			&row.EstimatedRequests,
			&row.UnavailableRequests,
			&row.TotalTokens,
			&row.Cost,
			&row.ActualCost,
		); err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}
