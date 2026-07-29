package kiro

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// usageLimitsFixture 是真实账号上抓到的 GET /getUsageLimits 响应，
// 只把 userId 换成了占位符。保留原样（含 1.7855424E9 这种科学计数法、
// 以及 currentUsage=0 而 currentUsageWithPrecision=0.01 的不一致）
// 是刻意的：这些正是解析时最容易出错的地方。
const usageLimitsFixture = `{
  "daysUntilReset": 0,
  "limits": [],
  "nextDateReset": 1.7855424E9,
  "overageConfiguration": {"overageLimit": null, "overageStatus": "DISABLED"},
  "subscriptionInfo": {
    "overageCapability": "OVERAGE_INCAPABLE",
    "subscriptionManagementTarget": "MANAGE",
    "subscriptionTitle": "KIRO FREE",
    "type": "Q_DEVELOPER_STANDALONE_FREE",
    "upgradeCapability": "UPGRADE_CAPABLE"
  },
  "totalUsage": null,
  "usageBreakdown": null,
  "usageBreakdownList": [{
    "addOnMetadata": null,
    "bonuses": [],
    "currency": "USD",
    "currentOverages": 0,
    "currentOveragesWithPrecision": 0.0,
    "currentUsage": 0,
    "currentUsageWithPrecision": 0.01,
    "dimensionType": null,
    "displayName": "Credit",
    "displayNamePlural": "Credits",
    "freeTrialInfo": {
      "currentUsage": 500,
      "currentUsageWithPrecision": 500.0,
      "freeTrialExpiry": 1.770835382331E9,
      "freeTrialStatus": "EXPIRED",
      "usageLimit": 500,
      "usageLimitWithPrecision": 500.0
    },
    "nextDateReset": 1.7855424E9,
    "overageCap": 10000,
    "overageCapWithPrecision": 10000.0,
    "overageCharges": 0.0,
    "overageCredits": [],
    "overageRate": 0.04,
    "resourceType": "CREDIT",
    "unit": "INVOCATIONS",
    "usageLimit": 50,
    "usageLimitWithPrecision": 50.0
  }],
  "userInfo": {"email": null, "userId": "d-0000000000.00000000-0000-0000-0000-000000000000"}
}`

// TestUserAgentFormatsAreDistinct 钉住两套 UA 的区别：
// auth 服务（/oauth/token、/refreshToken）用连字符，
// Q 数据面（generateAssistantResponse 等）用空格。混用等于自报家门。
func TestUserAgentFormatsAreDistinct(t *testing.T) {
	creds := &Credentials{KiroVersion: "1.0.212", MachineID: "abc123"}

	require.Equal(t, "KiroIDE 1.0.212 abc123", creds.UserAgent())
	require.Equal(t, "KiroIDE-1.0.212-abc123", creds.AuthUserAgent())
}

// TestUserAgentFallbacks 确认兜底值不含产品名。
// 以前 machineId 缺失时会填 "ringstar"，那串东西出现在上游日志里
// 等于把代理流量标记出来。
func TestUserAgentFallbacks(t *testing.T) {
	creds := &Credentials{}

	require.Equal(t, "KiroIDE "+DefaultVersion+" "+DefaultMachineID, creds.UserAgent())
	require.Equal(t, "KiroIDE-"+DefaultVersion+"-"+DefaultMachineID, creds.AuthUserAgent())
	require.NotContains(t, creds.UserAgent(), "ringstar")
	require.NotContains(t, creds.AuthUserAgent(), "ringstar")
}

// TestListAvailableModelsParsesRateAndCaching 用真实响应片段钉住模型目录的
// 计费与缓存字段。这两组字段此前都被丢掉了。
func TestListAvailableModelsParsesRateAndCaching(t *testing.T) {
	client := &stubHTTPClient{responses: []*http.Response{
		jsonResponse(http.StatusOK, `{"models":[
			{"modelId":"claude-sonnet-4.5","modelName":"Claude Sonnet 4.5","rateMultiplier":1.3,"rateUnit":"Credit",
			 "supportedInputTypes":["TEXT","IMAGE"],
			 "tokenLimits":{"maxInputTokens":200000,"maxOutputTokens":64000},
			 "promptCaching":{"supportsPromptCaching":true,"minimumTokensPerCacheCheckpoint":1024,"maximumCacheCheckpointsPerRequest":4}},
			{"modelId":"qwen3-coder-next","rateMultiplier":0.05,"rateUnit":"Credit",
			 "tokenLimits":{"maxInputTokens":256000,"maxOutputTokens":64000}}
		]}`),
	}}
	qClient, err := NewClient(client, testCredentials())
	require.NoError(t, err)

	models, _, err := qClient.ListAvailableModels(context.Background())
	require.NoError(t, err)
	require.Len(t, models, 2)

	sonnet := models[0]
	require.InDelta(t, 1.3, sonnet.RateMultiplier, 1e-9)
	require.Equal(t, "Credit", sonnet.RateUnit)
	require.Equal(t, []string{"TEXT", "IMAGE"}, sonnet.SupportedInputTypes)
	require.Equal(t, int64(200000), sonnet.TokenLimits.MaxInputTokens)
	require.True(t, sonnet.PromptCaching.SupportsPromptCaching)
	require.Equal(t, int64(1024), sonnet.PromptCaching.MinimumTokensPerCacheCheckpoint)
	require.Equal(t, int64(4), sonnet.PromptCaching.MaximumCacheCheckpointsPerRequest)

	// 不支持缓存的模型不下发 promptCaching。
	require.Nil(t, models[1].PromptCaching)
	require.InDelta(t, 0.05, models[1].RateMultiplier, 1e-9)

	// 倍率表：最贵的是最便宜的 26 倍，换模型是最直接的省钱手段。
	rates := RateMultiplierByModel(models)
	require.Len(t, rates, 2)
	require.InDelta(t, 1.3, rates["claude-sonnet-4.5"], 1e-9)
	require.InDelta(t, 0.05, rates["qwen3-coder-next"], 1e-9)
}

func TestRateMultiplierByModelSkipsUnusable(t *testing.T) {
	require.Nil(t, RateMultiplierByModel(nil))
	// 没有 modelId 或倍率非正的条目不能进表，否则按 0 计费会把成本算没。
	require.Nil(t, RateMultiplierByModel([]AvailableModel{
		{ModelID: "", RateMultiplier: 1.0},
		{ModelID: "x", RateMultiplier: 0},
		{ModelID: "y", RateMultiplier: -1},
	}))
}

func TestGetUsageLimitsParsesRealResponse(t *testing.T) {
	client := &stubHTTPClient{responses: []*http.Response{
		jsonResponse(http.StatusOK, usageLimitsFixture),
	}}
	qClient, err := NewClient(client, testCredentials())
	require.NoError(t, err)

	usage, err := qClient.GetUsageLimits(context.Background())
	require.NoError(t, err)

	// 与 ListAvailableModels 同一个 client / 同一个端点，且不带查询参数。
	require.Len(t, client.requests, 1)
	require.Equal(t, http.MethodGet, client.requests[0].Method)
	require.Equal(t, "https://q.eu-west-1.amazonaws.com/getUsageLimits", client.requests[0].URL.String())
	require.Equal(t, "Bearer access-token", client.requests[0].Header.Get("Authorization"))

	require.Equal(t, "KIRO FREE", usage.SubscriptionInfo.SubscriptionTitle)
	require.Equal(t, "Q_DEVELOPER_STANDALONE_FREE", usage.SubscriptionInfo.Type)
	require.Equal(t, "UPGRADE_CAPABLE", usage.SubscriptionInfo.UpgradeCapability)
	require.Equal(t, "DISABLED", usage.OverageConfiguration.OverageStatus)
	require.False(t, usage.OverageEnabled())

	require.Equal(t, "d-0000000000.00000000-0000-0000-0000-000000000000", usage.UserInfo.UserID)
	require.Len(t, usage.UsageBreakdownList, 1)
}

// TestUsageBreakdownPrefersPrecisionFields 钉住最容易踩的那个坑：
// 整数字段会把 0.01 截断成 0，只看它会把「已经在用」误判成「全新未用」。
func TestUsageBreakdownPrefersPrecisionFields(t *testing.T) {
	client := &stubHTTPClient{responses: []*http.Response{
		jsonResponse(http.StatusOK, usageLimitsFixture),
	}}
	qClient, err := NewClient(client, testCredentials())
	require.NoError(t, err)

	usage, err := qClient.GetUsageLimits(context.Background())
	require.NoError(t, err)

	credits := usage.Credits()
	require.NotNil(t, credits)
	require.Equal(t, "CREDIT", credits.ResourceType)
	require.Equal(t, "INVOCATIONS", credits.Unit)

	// 整数版是 0，高精度版是 0.01——必须取后者。
	require.Equal(t, 0, credits.CurrentUsage)
	require.InDelta(t, 0.01, credits.Used(), 1e-9)
	require.InDelta(t, 50.0, credits.Limit(), 1e-9)
	require.InDelta(t, 49.99, credits.Remaining(), 1e-9)
	require.InDelta(t, 0.02, credits.UsedPercent(), 1e-9)
	require.False(t, credits.Exhausted())
	require.False(t, usage.Exhausted())
}

func TestUsageBreakdownFallsBackToIntegerFields(t *testing.T) {
	client := &stubHTTPClient{responses: []*http.Response{
		jsonResponse(http.StatusOK, `{"usageBreakdownList":[{"resourceType":"CREDIT","currentUsage":7,"usageLimit":50}]}`),
	}}
	qClient, err := NewClient(client, testCredentials())
	require.NoError(t, err)

	usage, err := qClient.GetUsageLimits(context.Background())
	require.NoError(t, err)

	credits := usage.Credits()
	require.NotNil(t, credits)
	require.InDelta(t, 7.0, credits.Used(), 1e-9)
	require.InDelta(t, 50.0, credits.Limit(), 1e-9)
}

func TestUsageLimitsResetAt(t *testing.T) {
	client := &stubHTTPClient{responses: []*http.Response{
		jsonResponse(http.StatusOK, usageLimitsFixture),
	}}
	qClient, err := NewClient(client, testCredentials())
	require.NoError(t, err)

	usage, err := qClient.GetUsageLimits(context.Background())
	require.NoError(t, err)

	// 1.7855424E9 秒 = 1785542400 = 20665 整天 = 2026-08-01T00:00:00Z
	require.Equal(t, time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), usage.ResetAt())
	// 注意上游的 daysUntilReset 与 nextDateReset 对不上：抓包当天是 07-26，
	// 距 08-01 还有 6 天，daysUntilReset 却是 0。以 nextDateReset 为准。
	require.Equal(t, 0, usage.DaysUntilReset)
}

func TestUsageLimitsResetAtZeroWhenMissing(t *testing.T) {
	client := &stubHTTPClient{responses: []*http.Response{
		jsonResponse(http.StatusOK, `{"usageBreakdownList":[]}`),
	}}
	qClient, err := NewClient(client, testCredentials())
	require.NoError(t, err)

	usage, err := qClient.GetUsageLimits(context.Background())
	require.NoError(t, err)
	require.True(t, usage.ResetAt().IsZero())
}

// TestUsageLimitsExhausted 覆盖账号池最关心的判断：这个号还能不能用。
func TestUsageLimitsExhausted(t *testing.T) {
	tests := []struct {
		name          string
		body          string
		wantExhausted bool
	}{
		{
			name:          "额度用尽且未开超额",
			body:          `{"overageConfiguration":{"overageStatus":"DISABLED"},"usageBreakdownList":[{"resourceType":"CREDIT","currentUsageWithPrecision":50.0,"usageLimitWithPrecision":50.0}]}`,
			wantExhausted: true,
		},
		{
			// 开了超额就还能继续跑，只是要花钱，不该被判死。
			name:          "额度用尽但开了超额",
			body:          `{"overageConfiguration":{"overageStatus":"ENABLED"},"usageBreakdownList":[{"resourceType":"CREDIT","currentUsageWithPrecision":50.0,"usageLimitWithPrecision":50.0}]}`,
			wantExhausted: false,
		},
		{
			name:          "已超出上限",
			body:          `{"overageConfiguration":{"overageStatus":"DISABLED"},"usageBreakdownList":[{"resourceType":"CREDIT","currentUsageWithPrecision":51.5,"usageLimitWithPrecision":50.0}]}`,
			wantExhausted: true,
		},
		{
			name:          "还有余量",
			body:          `{"overageConfiguration":{"overageStatus":"DISABLED"},"usageBreakdownList":[{"resourceType":"CREDIT","currentUsageWithPrecision":10.0,"usageLimitWithPrecision":50.0}]}`,
			wantExhausted: false,
		},
		{
			// 没有 CREDIT 维度时不能瞎猜，一律当作可用。
			name:          "没有 CREDIT 维度",
			body:          `{"usageBreakdownList":[]}`,
			wantExhausted: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := &stubHTTPClient{responses: []*http.Response{jsonResponse(http.StatusOK, tc.body)}}
			qClient, err := NewClient(client, testCredentials())
			require.NoError(t, err)

			usage, err := qClient.GetUsageLimits(context.Background())
			require.NoError(t, err)
			require.Equal(t, tc.wantExhausted, usage.Exhausted())
		})
	}
}

func TestUsageBreakdownRemainingNeverNegative(t *testing.T) {
	b := &UsageBreakdown{CurrentUsage: 60, UsageLimit: 50}
	require.Zero(t, b.Remaining())
	require.True(t, b.Exhausted())
}

func TestGetUsageLimitsSurfacesAPIError(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError} {
		client := &stubHTTPClient{responses: []*http.Response{
			jsonResponse(status, `{"message":"nope"}`),
		}}
		qClient, err := NewClient(client, testCredentials())
		require.NoError(t, err)

		_, err = qClient.GetUsageLimits(context.Background())

		var apiErr *APIError
		require.ErrorAs(t, err, &apiErr)
		require.Equal(t, status, apiErr.Status)
		require.Equal(t, "GetUsageLimits", apiErr.Operation)
		// 401/403 要能被上层识别成「该刷新凭证了」。
		require.Equal(t, status == http.StatusUnauthorized || status == http.StatusForbidden, apiErr.Unauthorized())
	}
}

// TestGetUsageLimitsFreeTrialInfo 覆盖试用额度字段。
func TestGetUsageLimitsFreeTrialInfo(t *testing.T) {
	client := &stubHTTPClient{responses: []*http.Response{
		jsonResponse(http.StatusOK, usageLimitsFixture),
	}}
	qClient, err := NewClient(client, testCredentials())
	require.NoError(t, err)

	usage, err := qClient.GetUsageLimits(context.Background())
	require.NoError(t, err)

	trial := usage.Credits().FreeTrialInfo
	require.NotNil(t, trial)
	require.Equal(t, FreeTrialStatusExpired, trial.FreeTrialStatus)
	require.Equal(t, 500, trial.UsageLimit)
}
