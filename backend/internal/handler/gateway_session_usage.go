package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const (
	keySessionUsageSchemaVersion = 2
	sessionUsageMaxWaitMS        = int64(3000)
	sessionUsagePollInterval     = 10 * time.Millisecond
)

type keySessionUsageSummary struct {
	Requests                int64    `json:"requests"`
	InputTokens             int64    `json:"input_tokens"`
	OutputTokens            int64    `json:"output_tokens"`
	CacheCreationTokens     int64    `json:"cache_creation_tokens"`
	CacheReadTokens         int64    `json:"cache_read_tokens"`
	ProviderCacheReadTokens int64    `json:"provider_cache_read_tokens"`
	ForcedCacheReadTokens   int64    `json:"forced_cache_read_tokens"`
	CacheHitRequests        int64    `json:"cache_hit_requests"`
	CacheHitRatePercent     *float64 `json:"cache_hit_rate_percent"`
	CacheObservationStatus  string   `json:"cache_observation_status"`
	ReportedRequests        int64    `json:"reported_requests"`
	EstimatedRequests       int64    `json:"estimated_requests"`
	UnavailableRequests     int64    `json:"unavailable_requests"`
	UnknownRequests         int64    `json:"unknown_requests"`
	TotalTokens             int64    `json:"total_tokens"`
	Cost                    float64  `json:"cost"`
	ActualCost              float64  `json:"actual_cost"`
	AverageDurationMs       float64  `json:"average_duration_ms"`
}

type keySessionUsageResponse struct {
	Object        string                 `json:"object"`
	SchemaVersion int                    `json:"schema_version"`
	SessionID     string                 `json:"session_id"`
	Settled       bool                   `json:"settled"`
	SettledAt     *time.Time             `json:"settled_at"`
	Usage         keySessionUsageSummary `json:"usage"`
}

// SessionUsage returns the settled gateway usage for one client-defined session.
// GET /v1/sub2api/usage?session_id=<id>
//
// The authenticated API key and its owner are always included in the filter.
// A caller can therefore only inspect its own usage logs, even if it guesses a
// session identifier used by another account.
func (h *GatewayHandler) SessionUsage(c *gin.Context) {
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	sessionID := service.NormalizeClientSessionID(c.Query("session_id"))
	if sessionID == "" {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "session_id must be a valid non-empty client session identifier")
		return
	}
	minRequests, waitDuration, err := parseSessionUsageWaitOptions(c)
	if err != nil {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if h.usageService == nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Session usage is unavailable")
		return
	}

	filters := usagestats.UsageLogFilters{
		UserID:    apiKey.UserID,
		APIKeyID:  apiKey.ID,
		SessionID: sessionID,
	}
	loadStats := func() (*usagestats.UsageStats, error) {
		stats, loadErr := h.usageService.GetStatsWithFilters(c.Request.Context(), filters)
		if stats == nil {
			stats = &usagestats.UsageStats{}
		}
		return stats, loadErr
	}

	stats, err := loadStats()
	if err != nil {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "Failed to get session usage")
		return
	}
	settled := stats.TotalRequests >= minRequests
	if !settled && waitDuration > 0 {
		stats, settled, err = waitForSessionUsage(c, loadStats, stats, minRequests, waitDuration)
		if err != nil {
			h.errorResponse(c, http.StatusInternalServerError, "api_error", "Failed to get session usage")
			return
		}
	}

	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, buildKeySessionUsage(sessionID, stats, settled, timezone.Now()))
}

func parseSessionUsageWaitOptions(c *gin.Context) (int64, time.Duration, error) {
	minRequests, err := parseSessionUsageNonNegativeInt(c.Query("min_requests"), "min_requests", 0)
	if err != nil {
		return 0, 0, err
	}
	waitMS, err := parseSessionUsageNonNegativeInt(c.Query("wait_ms"), "wait_ms", sessionUsageMaxWaitMS)
	if err != nil {
		return 0, 0, err
	}
	return minRequests, time.Duration(waitMS) * time.Millisecond, nil
}

func parseSessionUsageNonNegativeInt(raw, name string, maxValue int64) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", name)
	}
	if maxValue > 0 && value > maxValue {
		return 0, fmt.Errorf("%s must not exceed %d", name, maxValue)
	}
	return value, nil
}

func waitForSessionUsage(
	c *gin.Context,
	loadStats func() (*usagestats.UsageStats, error),
	current *usagestats.UsageStats,
	minRequests int64,
	waitDuration time.Duration,
) (*usagestats.UsageStats, bool, error) {
	timer := time.NewTimer(waitDuration)
	defer timer.Stop()
	ticker := time.NewTicker(sessionUsagePollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.Request.Context().Done():
			return current, false, c.Request.Context().Err()
		case <-ticker.C:
			stats, err := loadStats()
			if err != nil {
				return current, false, err
			}
			current = stats
			if current.TotalRequests >= minRequests {
				return current, true, nil
			}
		case <-timer.C:
			// One final read at the boundary avoids reporting unsettled when the
			// last usage row committed between the previous tick and timeout.
			stats, err := loadStats()
			if err != nil {
				return current, false, err
			}
			current = stats
			return current, current.TotalRequests >= minRequests, nil
		}
	}
}

func buildKeySessionUsage(sessionID string, stats *usagestats.UsageStats, settled bool, observedAt time.Time) keySessionUsageResponse {
	if stats == nil {
		stats = &usagestats.UsageStats{}
	}
	unknownRequests := cacheUnknownRequests(stats)
	cacheStatus := sessionCacheObservationStatus(stats, unknownRequests)
	cacheHitRatePercent := sessionCacheHitRatePercent(stats)
	var settledAt *time.Time
	if settled {
		value := observedAt.UTC()
		settledAt = &value
	}
	return keySessionUsageResponse{
		Object:        "sub2api.session_usage",
		SchemaVersion: keySessionUsageSchemaVersion,
		SessionID:     sessionID,
		Settled:       settled,
		SettledAt:     settledAt,
		Usage: keySessionUsageSummary{
			Requests:                stats.TotalRequests,
			InputTokens:             stats.TotalInputTokens,
			OutputTokens:            stats.TotalOutputTokens,
			CacheCreationTokens:     stats.TotalCacheCreationTokens,
			CacheReadTokens:         stats.TotalCacheReadTokens,
			ProviderCacheReadTokens: stats.TotalProviderCacheReadTokens,
			ForcedCacheReadTokens:   stats.TotalForcedCacheReadTokens,
			CacheHitRequests:        stats.CacheHitRequests,
			CacheHitRatePercent:     cacheHitRatePercent,
			CacheObservationStatus:  cacheStatus,
			ReportedRequests:        stats.ReportedRequests,
			EstimatedRequests:       stats.EstimatedRequests,
			UnavailableRequests:     stats.UnavailableRequests,
			UnknownRequests:         unknownRequests,
			TotalTokens:             stats.TotalTokens,
			Cost:                    stats.TotalCost,
			ActualCost:              stats.TotalActualCost,
			AverageDurationMs:       stats.AverageDurationMs,
		},
	}
}

func cacheUnknownRequests(stats *usagestats.UsageStats) int64 {
	if stats == nil {
		return 0
	}
	known := stats.ReportedRequests + stats.EstimatedRequests + stats.UnavailableRequests
	if known >= stats.TotalRequests {
		return 0
	}
	return stats.TotalRequests - known
}

func sessionCacheObservationStatus(stats *usagestats.UsageStats, unknownRequests int64) string {
	if stats == nil || stats.TotalRequests == 0 {
		return "no_data"
	}
	if stats.ReportedRequests == stats.TotalRequests &&
		stats.EstimatedRequests == 0 &&
		stats.UnavailableRequests == 0 &&
		unknownRequests == 0 {
		return "fully_reported"
	}
	if stats.ReportedRequests > 0 {
		return "partially_reported"
	}
	return "unobservable"
}

// sessionCacheHitRatePercent returns the request hit rate for the reported
// subset only. nil means no request has trustworthy provider cache usage (the
// normal Cursor state), which must not be presented as a real 0% hit rate.
func sessionCacheHitRatePercent(stats *usagestats.UsageStats) *float64 {
	if stats == nil || stats.ReportedRequests <= 0 {
		return nil
	}
	hits := stats.CacheHitRequests
	if hits < 0 {
		hits = 0
	}
	if hits > stats.ReportedRequests {
		hits = stats.ReportedRequests
	}
	value := float64(hits) / float64(stats.ReportedRequests) * 100
	return &value
}
