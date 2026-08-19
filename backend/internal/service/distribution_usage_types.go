package service

import (
	"context"
	"time"
)

const (
	// DistributionUsageMaxRangeDays is the longest window a scoped query may cover.
	DistributionUsageMaxRangeDays = 31
	// DistributionUsageDefaultLimit is the ranking page size when the caller omits one.
	DistributionUsageDefaultLimit = 10
	// DistributionUsageMaxLimit caps ranking rows so a client cannot pull the full fleet.
	DistributionUsageMaxLimit = 50

	DistributionUsageGranularityDay  = "day"
	DistributionUsageGranularityHour = "hour"

	DistributionUsageSortRequests = "requests"
	DistributionUsageSortTokens   = "tokens"
	DistributionUsageSortActual   = "actual"
)

// DistributionUsageRepository reads usage_logs scoped to users managed by a
// distribution administrator. Scope always comes from the managed-user CTE
// (created_by_admin_id OR affiliate inviter). Callers must never pass a
// client-provided []userID as the sole filter — optional single-user
// drill-down is AND user_id = $x after the CTE.
type DistributionUsageRepository interface {
	Snapshot(ctx context.Context, adminID int64, start, end time.Time) (*DistributionUsageSnapshot, error)
	Trend(ctx context.Context, adminID int64, start, end time.Time, granularity string, filter DistributionUsageFilter) ([]DistributionUsageTrendPoint, error)
	ModelStats(ctx context.Context, adminID int64, start, end time.Time, userID int64) ([]DistributionUsageModelStat, error)
	UserRanking(ctx context.Context, adminID int64, start, end time.Time, sort string, limit int) ([]DistributionUsageUserRankingItem, error)
	ErrorSummary(ctx context.Context, adminID int64, start, end time.Time) (*DistributionUsageErrorSummary, error)
	UserSummary(ctx context.Context, adminID int64, userID int64, start, end time.Time) (*DistributionUsageUserSummary, error)
}

// DistributionUsageFilter is an optional drill-down applied after the managed-user CTE.
type DistributionUsageFilter struct {
	UserID   int64
	Model    string
	Platform string
}

// DistributionUsageSnapshot is a range total for one administrator's managed users.
// Request counts include every usage_logs row (same as the user dashboard).
// Spend uses actual_cost; failed placeholders contribute 0.
type DistributionUsageSnapshot struct {
	Requests            int64   `json:"requests"`
	InputTokens         int64   `json:"input_tokens"`
	OutputTokens        int64   `json:"output_tokens"`
	CacheCreationTokens int64   `json:"cache_creation_tokens"`
	CacheReadTokens     int64   `json:"cache_read_tokens"`
	TotalTokens         int64   `json:"total_tokens"`
	ActualCost          float64 `json:"actual_cost"`
	SuccessRequests     int64   `json:"success_requests"`
	ErrorRequests       int64   `json:"error_requests"`
	CacheHitRequests    int64   `json:"cache_hit_requests"`
	ActiveUsers         int64   `json:"active_users"`
}

// DistributionUsageTrendPoint mirrors usagestats.TrendDataPoint field names.
// Account cost is intentionally omitted.
type DistributionUsageTrendPoint struct {
	Date                          string  `json:"date"`
	Requests                      int64   `json:"requests"`
	InputTokens                   int64   `json:"input_tokens"`
	OutputTokens                  int64   `json:"output_tokens"`
	CacheCreationTokens           int64   `json:"cache_creation_tokens"`
	CacheReadTokens               int64   `json:"cache_read_tokens"`
	ProviderCacheReadTokens       int64   `json:"provider_cache_read_tokens"`
	CacheHitRequests              int64   `json:"cache_hit_requests"`
	ForcedCacheReadTokens         int64   `json:"forced_cache_read_tokens"`
	ReportedInputTokens           int64   `json:"reported_input_tokens"`
	ReportedCacheCreationTokens   int64   `json:"reported_cache_creation_tokens"`
	ReportedForcedCacheReadTokens int64   `json:"reported_forced_cache_read_tokens"`
	ReportedRequests              int64   `json:"reported_requests"`
	EstimatedRequests             int64   `json:"estimated_requests"`
	UnavailableRequests           int64   `json:"unavailable_requests"`
	TotalTokens                   int64   `json:"total_tokens"`
	Cost                          float64 `json:"cost"`
	ActualCost                    float64 `json:"actual_cost"`
}

// DistributionUsageModelStat mirrors usagestats.ModelStat field names except
// account_cost, which must not leave this scoped repository.
type DistributionUsageModelStat struct {
	Model                         string  `json:"model"`
	Requests                      int64   `json:"requests"`
	InputTokens                   int64   `json:"input_tokens"`
	OutputTokens                  int64   `json:"output_tokens"`
	CacheCreationTokens           int64   `json:"cache_creation_tokens"`
	CacheReadTokens               int64   `json:"cache_read_tokens"`
	ProviderCacheReadTokens       int64   `json:"provider_cache_read_tokens"`
	CacheHitRequests              int64   `json:"cache_hit_requests"`
	ForcedCacheReadTokens         int64   `json:"forced_cache_read_tokens"`
	ReportedInputTokens           int64   `json:"reported_input_tokens"`
	ReportedCacheCreationTokens   int64   `json:"reported_cache_creation_tokens"`
	ReportedForcedCacheReadTokens int64   `json:"reported_forced_cache_read_tokens"`
	ReportedRequests              int64   `json:"reported_requests"`
	EstimatedRequests             int64   `json:"estimated_requests"`
	UnavailableRequests           int64   `json:"unavailable_requests"`
	TotalTokens                   int64   `json:"total_tokens"`
	Cost                          float64 `json:"cost"`
	ActualCost                    float64 `json:"actual_cost"`
}

// DistributionUsageUserRankingItem identifies a managed user only.
// Email and username come from the managed-user CTE join, never from an unscoped users scan.
type DistributionUsageUserRankingItem struct {
	UserID     int64   `json:"user_id"`
	Email      string  `json:"email"`
	Username   string  `json:"username"`
	Requests   int64   `json:"requests"`
	Tokens     int64   `json:"tokens"`
	ActualCost float64 `json:"actual_cost"`
}

// DistributionUsageErrorSummary counts billed vs failed/unbilled rows from
// usage_logs only. usage_logs has no status column; actual_cost<=0 is the
// failure proxy used elsewhere. Do not read ops_error_logs (leaks upstream).
type DistributionUsageErrorSummary struct {
	BilledRequests           int64 `json:"billed_requests"`
	FailedOrUnbilledRequests int64 `json:"failed_or_unbilled_requests"`
}

// DistributionUsageUserSummary is a single managed-user drill-down.
type DistributionUsageUserSummary struct {
	UserID   int64  `json:"user_id"`
	Email    string `json:"email"`
	Username string `json:"username"`
	DistributionUsageSnapshot
}
