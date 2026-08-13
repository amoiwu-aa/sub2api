package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type sessionUsageRepoCapture struct {
	service.UsageLogRepository
	filters       usagestats.UsageLogFilters
	stats         *usagestats.UsageStats
	statsSequence []*usagestats.UsageStats
	calls         int
	err           error
}

func (s *sessionUsageRepoCapture) GetStatsWithFilters(
	_ context.Context,
	filters usagestats.UsageLogFilters,
) (*usagestats.UsageStats, error) {
	s.filters = filters
	s.calls++
	if len(s.statsSequence) > 0 {
		index := s.calls - 1
		if index >= len(s.statsSequence) {
			index = len(s.statsSequence) - 1
		}
		return s.statsSequence[index], s.err
	}
	return s.stats, s.err
}

func newSessionUsageTestContext(t *testing.T, rawQuery string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/sub2api/usage?"+rawQuery, nil)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 73, UserID: 41})
	return c, recorder
}

func TestGatewaySessionUsageScopesStatsToCurrentKeyAndSession(t *testing.T) {
	repo := &sessionUsageRepoCapture{stats: &usagestats.UsageStats{
		TotalRequests:                3,
		TotalInputTokens:             1200,
		TotalOutputTokens:            340,
		TotalCacheCreationTokens:     100,
		TotalCacheReadTokens:         560,
		TotalProviderCacheReadTokens: 510,
		CacheHitRequests:             1,
		TotalForcedCacheReadTokens:   50,
		ReportedRequests:             2,
		EstimatedRequests:            1,
		UnavailableRequests:          0,
		TotalTokens:                  2200,
		TotalCost:                    0.012,
		TotalActualCost:              0.018,
		AverageDurationMs:            234.5,
	}}
	handler := &GatewayHandler{usageService: service.NewUsageService(repo, nil, nil, nil)}
	c, recorder := newSessionUsageTestContext(t, "session_id=autoclaw%3Asession-123")

	handler.SessionUsage(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	require.Equal(t, int64(41), repo.filters.UserID)
	require.Equal(t, int64(73), repo.filters.APIKeyID)
	require.Equal(t, "autoclaw:session-123", repo.filters.SessionID)

	var response keySessionUsageResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, "sub2api.session_usage", response.Object)
	require.Equal(t, keySessionUsageSchemaVersion, response.SchemaVersion)
	require.Equal(t, "autoclaw:session-123", response.SessionID)
	require.True(t, response.Settled)
	require.NotNil(t, response.SettledAt)
	require.Equal(t, int64(3), response.Usage.Requests)
	require.Equal(t, int64(2200), response.Usage.TotalTokens)
	require.Equal(t, int64(510), response.Usage.ProviderCacheReadTokens)
	require.Equal(t, int64(50), response.Usage.ForcedCacheReadTokens)
	require.Equal(t, int64(1), response.Usage.CacheHitRequests)
	require.NotNil(t, response.Usage.CacheHitRatePercent)
	require.InDelta(t, 50, *response.Usage.CacheHitRatePercent, 0.000001)
	require.Equal(t, "partially_reported", response.Usage.CacheObservationStatus)
	require.Equal(t, int64(2), response.Usage.ReportedRequests)
	require.Equal(t, int64(1), response.Usage.EstimatedRequests)
	require.Zero(t, response.Usage.UnavailableRequests)
	require.Zero(t, response.Usage.UnknownRequests)
	require.InDelta(t, 0.018, response.Usage.ActualCost, 0.0000001)
}

func TestGatewaySessionUsageMarksCursorEstimatedCacheAsUnobservable(t *testing.T) {
	repo := &sessionUsageRepoCapture{stats: &usagestats.UsageStats{
		TotalRequests:     2,
		EstimatedRequests: 2,
		TotalInputTokens:  120,
		TotalOutputTokens: 30,
		TotalTokens:       150,
	}}
	handler := &GatewayHandler{usageService: service.NewUsageService(repo, nil, nil, nil)}
	c, recorder := newSessionUsageTestContext(t, "session_id=cursor-session")

	handler.SessionUsage(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"cache_hit_rate_percent":null`)
	require.Contains(t, recorder.Body.String(), `"cache_observation_status":"unobservable"`)
	var response keySessionUsageResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, "unobservable", response.Usage.CacheObservationStatus)
	require.Nil(t, response.Usage.CacheHitRatePercent, "unobservable Cursor cache must be null, not a fake 0%")
	require.Zero(t, response.Usage.CacheHitRequests)
	require.Equal(t, int64(2), response.Usage.EstimatedRequests)
	require.Zero(t, response.Usage.UnknownRequests)
}

func TestBuildKeySessionUsageCountsLegacyRowsAsUnknown(t *testing.T) {
	response := buildKeySessionUsage("legacy-session", &usagestats.UsageStats{
		TotalRequests:       3,
		ReportedRequests:    1,
		EstimatedRequests:   1,
		CacheHitRequests:    1,
		UnavailableRequests: 0,
	}, true, time.Now())

	require.Equal(t, int64(1), response.Usage.UnknownRequests)
	require.Equal(t, "partially_reported", response.Usage.CacheObservationStatus)
	require.NotNil(t, response.Usage.CacheHitRatePercent)
	require.InDelta(t, 100, *response.Usage.CacheHitRatePercent, 0.000001)
}

func TestBuildKeySessionUsagePreservesObservableZeroHitRate(t *testing.T) {
	response := buildKeySessionUsage("reported-miss", &usagestats.UsageStats{
		TotalRequests:     2,
		ReportedRequests:  2,
		CacheHitRequests:  0,
		TotalInputTokens:  100,
		TotalOutputTokens: 10,
	}, true, time.Now())

	require.Equal(t, "fully_reported", response.Usage.CacheObservationStatus)
	require.NotNil(t, response.Usage.CacheHitRatePercent, "reported zero hits must remain distinguishable from unobservable")
	require.Zero(t, *response.Usage.CacheHitRatePercent)
	require.Zero(t, response.Usage.UnknownRequests)
}

func TestGatewaySessionUsageRejectsInvalidSessionID(t *testing.T) {
	handler := &GatewayHandler{}
	c, recorder := newSessionUsageTestContext(t, "session_id=%20%20")

	handler.SessionUsage(c)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestGatewaySessionUsageWaitTimeoutReturnsUnsettled(t *testing.T) {
	repo := &sessionUsageRepoCapture{stats: &usagestats.UsageStats{TotalRequests: 1}}
	handler := &GatewayHandler{usageService: service.NewUsageService(repo, nil, nil, nil)}
	c, recorder := newSessionUsageTestContext(t, "session_id=session-timeout&min_requests=2&wait_ms=40")

	handler.SessionUsage(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.GreaterOrEqual(t, repo.calls, 2, "handler must poll while below min_requests")

	var response keySessionUsageResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.False(t, response.Settled)
	require.Nil(t, response.SettledAt)
	require.Equal(t, int64(1), response.Usage.Requests)
}

func TestGatewaySessionUsageWaitReachesMinimum(t *testing.T) {
	repo := &sessionUsageRepoCapture{statsSequence: []*usagestats.UsageStats{
		{TotalRequests: 1},
		{TotalRequests: 2, TotalCacheReadTokens: 90, TotalProviderCacheReadTokens: 70, TotalForcedCacheReadTokens: 20},
	}}
	handler := &GatewayHandler{usageService: service.NewUsageService(repo, nil, nil, nil)}
	c, recorder := newSessionUsageTestContext(t, "session_id=session-ready&min_requests=2&wait_ms=200")

	handler.SessionUsage(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 2, repo.calls)

	var response keySessionUsageResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Settled)
	require.NotNil(t, response.SettledAt)
	require.Equal(t, int64(2), response.Usage.Requests)
	require.Equal(t, int64(70), response.Usage.ProviderCacheReadTokens)
	require.Equal(t, int64(20), response.Usage.ForcedCacheReadTokens)
}

func TestGatewaySessionUsageRejectsWaitOverMaximum(t *testing.T) {
	handler := &GatewayHandler{}
	c, recorder := newSessionUsageTestContext(t, "session_id=session-invalid-wait&min_requests=1&wait_ms=3001")

	handler.SessionUsage(c)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
}
