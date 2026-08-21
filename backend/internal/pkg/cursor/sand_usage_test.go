package cursor

import (
	"encoding/binary"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func protoResponse(status int, body []byte) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(string(body))),
		Header:     http.Header{"Content-Type": []string{"application/proto"}},
	}
}

func encodeTestFixed64(number int, value float64) []byte {
	out := appendTag(nil, number, wireFixed64)
	var raw [8]byte
	binary.LittleEndian.PutUint64(raw[:], math.Float64bits(value))
	return append(out, raw[:]...)
}

func encodeTestTimestamp(value time.Time) []byte {
	return concat(
		EncodeVarintField(1, uint64(value.Unix())),
		EncodeVarintField(2, uint64(value.Nanosecond())),
	)
}

func TestFetchSandUsageStatusUsesNativeDashboardRPC(t *testing.T) {
	periodStart := time.Date(2026, 8, 20, 21, 58, 49, 552000000, time.UTC)
	nextReset := periodStart.Add(7 * 24 * time.Hour)
	body := concat(
		EncodeBytesField(1, encodeTestTimestamp(periodStart)),
		EncodeBytesField(2, encodeTestTimestamp(nextReset)),
		encodeTestFixed64(3, 0.024486),
		EncodeBoolField(7, true),
		EncodeBoolField(8, true),
	)
	client := &stubHTTPClient{responses: []*http.Response{protoResponse(http.StatusOK, body)}}

	fixedNow := time.Date(2026, 8, 21, 1, 50, 53, 0, time.UTC)
	originalNow := timeNow
	timeNow = func() time.Time { return fixedNow }
	t.Cleanup(func() { timeNow = originalNow })

	status, err := FetchSandUsageStatus(
		t.Context(),
		&Options{HTTPClient: client},
		"test-token",
		"",
		"",
		"",
	)
	require.NoError(t, err)
	require.NotNil(t, status)
	require.NotNil(t, status.UsagePercent)
	require.InDelta(t, 0.024486, *status.UsagePercent, 0.000001)
	require.Equal(t, periodStart, status.CurrentPeriodStart)
	require.Equal(t, nextReset, status.NextReset)
	require.True(t, status.HasAvailableUsage)
	require.True(t, status.HasNonZeroIncludedLimit)

	require.Len(t, client.requests, 1)
	req := client.requests[0]
	require.Equal(t,
		"/aiserver.v1.DashboardService/GetSandUsageStatus",
		req.URL.Path,
	)
	require.Equal(t, "Bearer test-token", req.Header.Get("Authorization"))
	require.Equal(t, "application/proto", req.Header.Get("Content-Type"))
	require.Equal(t, "1", req.Header.Get("Connect-Protocol-Version"))
	require.Equal(t, "sand", req.Header.Get("X-Cursor-Client-Type"))
	require.Equal(t, SandClientVersion, req.Header.Get("X-Cursor-Client-Version"))
	require.Equal(t, "prod", req.Header.Get("X-Sand-Box-Namespace"))
	require.Equal(t, "true", req.Header.Get("X-Ghost-Mode"))
	require.Equal(t,
		SandChecksum(DeriveTelemetryIDs("test-token").MachineID, fixedNow),
		req.Header.Get("X-Cursor-Checksum"),
	)
}

func TestFetchSandCurrentPeriodUsageDecodesBillingBreakdown(t *testing.T) {
	cycleStart := time.Date(2026, 8, 1, 5, 44, 2, 0, time.UTC)
	cycleEnd := time.Date(2026, 9, 1, 5, 44, 2, 0, time.UTC)
	plan := concat(
		EncodeVarintField(1, 136012),
		EncodeVarintField(2, 40000),
		EncodeVarintField(3, 96012),
		EncodeVarintField(5, 40000),
		encodeTestFixed64(12, 64.2545),
		encodeTestFixed64(13, 15.006),
		encodeTestFixed64(14, 54.4048),
	)
	spend := concat(
		EncodeVarintField(5, 20000),
		EncodeVarintField(6, 1250),
		EncodeVarintField(7, 18750),
		EncodeStringField(8, "user"),
	)
	body := concat(
		EncodeVarintField(1, uint64(cycleStart.UnixMilli())),
		EncodeVarintField(2, uint64(cycleEnd.UnixMilli())),
		EncodeBytesField(3, plan),
		EncodeBytesField(4, spend),
		EncodeBoolField(6, true),
		EncodeStringField(7, "usage display"),
		EncodeStringField(11, "auto display"),
		EncodeStringField(12, "api display"),
		EncodeStringField(13, "default"),
		EncodeStringField(13, "composer-2.5"),
	)
	client := &stubHTTPClient{responses: []*http.Response{protoResponse(http.StatusOK, body)}}

	usage, err := FetchSandCurrentPeriodUsage(
		t.Context(),
		&Options{HTTPClient: client},
		"test-token",
		"machine-id",
		"0.20.0",
		"prod",
	)
	require.NoError(t, err)
	require.Equal(t, cycleStart, usage.BillingCycleStart)
	require.Equal(t, cycleEnd, usage.BillingCycleEnd)
	require.True(t, usage.Enabled)
	require.Equal(t, "auto display", usage.AutoSelectedDisplayMessage)
	require.Equal(t, "api display", usage.NamedSelectedDisplayMessage)
	require.Equal(t, []string{"default", "composer-2.5"}, usage.AutoBucketModels)

	require.NotNil(t, usage.PlanUsage)
	require.InDelta(t, 136012, usage.PlanUsage.TotalSpendCents, 0.001)
	require.InDelta(t, 40000, usage.PlanUsage.IncludedSpendCents, 0.001)
	require.NotNil(t, usage.PlanUsage.AutoPercentUsed)
	require.InDelta(t, 64.2545, *usage.PlanUsage.AutoPercentUsed, 0.0001)
	require.NotNil(t, usage.PlanUsage.APIPercentUsed)
	require.InDelta(t, 15.006, *usage.PlanUsage.APIPercentUsed, 0.0001)

	require.NotNil(t, usage.SpendLimitUsage)
	require.NotNil(t, usage.SpendLimitUsage.IndividualLimitCents)
	require.InDelta(t, 20000, *usage.SpendLimitUsage.IndividualLimitCents, 0.001)
	require.InDelta(t, 1250, usage.SpendLimitUsage.IndividualUsedCents, 0.001)
	require.Equal(t, "user", usage.SpendLimitUsage.LimitType)
}

func TestFetchSandUsageStatusClassifiesUnauthorized(t *testing.T) {
	client := &stubHTTPClient{
		responses: []*http.Response{jsonResponse(http.StatusUnauthorized, `{"error":"expired"}`)},
	}

	_, err := FetchSandUsageStatus(
		t.Context(),
		&Options{HTTPClient: client},
		"expired-token",
		"machine-id",
		"",
		"",
	)
	require.Error(t, err)
	var apiErr *HTTPError
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, http.StatusUnauthorized, apiErr.Status)
}
