package service

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/stretchr/testify/require"
)

func cursorQuotaAccount() *Account {
	return &Account{
		ID:       1,
		Platform: PlatformCursor,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "eyJhbGciOi.payload.sig",
			"user_id":      "user_01KVMQ6VJTTA7TPPSMBJKYFA7A",
		},
	}
}

func TestCursorQuotaFetcherCanFetch(t *testing.T) {
	f := NewCursorQuotaFetcher(nil)

	require.True(t, f.CanFetch(cursorQuotaAccount()))

	// user_id 缺失时构造不出 dashboard 的会话 cookie，查额度必然 401。
	noUser := cursorQuotaAccount()
	noUser.Credentials = map[string]any{"access_token": "tok"}
	require.False(t, f.CanFetch(noUser))

	sandWithoutUser := cursorQuotaAccount()
	sandWithoutUser.Credentials = map[string]any{
		"access_token":         "tok",
		"cursor_agent_profile": "sand",
	}
	require.True(t, f.CanFetch(sandWithoutUser))

	noToken := cursorQuotaAccount()
	noToken.Credentials = map[string]any{"user_id": "user_1"}
	require.False(t, f.CanFetch(noToken))

	wrongPlatform := cursorQuotaAccount()
	wrongPlatform.Platform = PlatformKiro
	require.False(t, f.CanFetch(wrongPlatform))
}

func TestCursorUserIDAcceptsCamelCase(t *testing.T) {
	account := &Account{
		Platform: PlatformCursor,
		Credentials: map[string]any{
			"access_token": "tok",
			"userId":       "user_2",
		},
	}
	require.Equal(t, "user_2", cursorUserID(account))
	require.True(t, NewCursorQuotaFetcher(nil).CanFetch(account))
}

// 配置了代理却取不到时必须报错，绝不能返回空串退化成直连——
// 那会让账号流量从服务器出口 IP 打到上游。
func TestCursorQuotaFetcherRejectsConfiguredProxyMiss(t *testing.T) {
	f := NewCursorQuotaFetcher(nil)
	account := cursorQuotaAccount()
	proxyID := int64(7)
	account.ProxyID = &proxyID

	_, err := f.GetProxyURL(t.Context(), account)
	require.Error(t, err)
}

// 取自线上账号的真实响应，字段名和单位（美分）以此为准。
const cursorUsageSummaryFixture = `{
  "billingCycleStart": "2026-07-25T13:35:47.000Z",
  "billingCycleEnd": "2026-08-25T13:35:47.000Z",
  "membershipType": "pro",
  "limitType": "user",
  "isUnlimited": false,
  "autoModelSelectedDisplayMessage": "You've used 68% of your included total usage",
  "namedModelSelectedDisplayMessage": "You've used 100% of your included API usage",
  "individualUsage": {
    "plan": {
      "enabled": true,
      "used": 2000,
      "limit": 2000,
      "remaining": 0,
      "breakdown": { "included": 2000, "bonus": 21506, "total": 23506 },
      "autoPercentUsed": 45.74666666666667,
      "apiPercentUsed": 100,
      "totalPercentUsed": 68.13333333333334
    },
    "onDemand": { "enabled": true, "used": 3266, "limit": null, "remaining": null }
  },
  "teamUsage": {}
}`

func TestBuildCursorUsageInfoMapsUpstreamDimensions(t *testing.T) {
	var summary cursor.UsageSummary
	require.NoError(t, json.Unmarshal([]byte(cursorUsageSummaryFixture), &summary))

	now := time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC)
	usage := buildCursorUsageInfo(&summary, now)

	require.Equal(t, "pro", usage.CursorPlan)
	require.False(t, usage.CursorIsUnlimited)

	// Auto 与 API 是两个独立维度，不是同一条进度条的两段。
	require.NotNil(t, usage.CursorAutoUsage)
	require.InDelta(t, 45.746, usage.CursorAutoUsage.Utilization, 0.01)
	require.NotNil(t, usage.CursorAPIUsage)
	require.InDelta(t, 100, usage.CursorAPIUsage.Utilization, 0.01)

	// 上游一律用美分计价，落到 UsageInfo 时统一换成美元。
	require.InDelta(t, 20, usage.CursorIncludedUsed, 0.001)
	require.InDelta(t, 20, usage.CursorIncludedLimit, 0.001)
	require.InDelta(t, 32.66, usage.CursorOnDemandUsed, 0.001)
	require.InDelta(t, 235.06, usage.CursorPeriodTotal, 0.001)
	require.InDelta(t, 215.06, usage.CursorPeriodBonus, 0.001)
	require.True(t, usage.CursorOnDemandEnabled)
	require.Zero(t, usage.CursorOnDemandLimit, "limit 为 null 时应保持 0，表示未设上限")

	// 重置时间是计费周期结束，不是任何滚动窗口。
	require.NotNil(t, usage.CursorBillingCycleEnd)
	require.Equal(t, 2026, usage.CursorBillingCycleEnd.Year())
	require.NotNil(t, usage.CursorAutoUsage.ResetsAt)
	require.Positive(t, usage.CursorAutoUsage.RemainingSeconds)

	// 不得再产出 5h / 7d 窗口。
	require.Nil(t, usage.FiveHour)
	require.Nil(t, usage.SevenDay)
}

func TestApplyCursorSandUsageStatusMapsOnlyWeeklyQuota(t *testing.T) {
	now := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)
	sandPercent := 0.024486
	sandReset := time.Date(2026, 8, 27, 21, 58, 49, 552000000, time.UTC)
	usage := &UsageInfo{UpdatedAt: &now, Source: "upstream"}
	applyCursorSandUsageStatus(usage, &cursor.SandUsageStatus{
		UsagePercent:              &sandPercent,
		NextReset:                 sandReset,
		HasAvailableUsage:         true,
		AvailableBankedResetCount: 2,
	}, now)
	require.NotNil(t, usage.CursorSandUsage)
	require.InDelta(t, sandPercent, usage.CursorSandUsage.Utilization, 0.000001)
	require.Equal(t, sandReset, *usage.CursorSandUsage.ResetsAt)
	require.NotNil(t, usage.CursorSandHasAvailableUsage)
	require.True(t, *usage.CursorSandHasAvailableUsage)
	require.Equal(t, int64(2), usage.CursorSandAvailableBankedResets)
	require.Nil(t, usage.CursorAutoUsage)
	require.Nil(t, usage.CursorAPIUsage)
	require.Zero(t, usage.CursorIncludedLimit)
}

func TestApplyCursorStripeProfileFlagsPastDue(t *testing.T) {
	usage := &UsageInfo{}
	applyCursorStripeProfile(usage, &cursor.StripeProfile{
		MembershipType:        "pro",
		SubscriptionStatus:    "past_due",
		LastPaymentFailed:     true,
		PaymentRecoveryAction: "update_payment_method",
	})

	require.Equal(t, "past_due", usage.CursorSubscriptionStatus)
	require.True(t, usage.CursorPaymentFailed)
	require.Equal(t, "update_payment_method", usage.CursorPaymentAction)
	require.Equal(t, "pro", usage.CursorPlan)
}

// 生产 Team Enterprise：teamUsage 只有 onDemand。映射必须落到个人 plan，
// 否则前端只看得到 Enterprise 徽标，Auto / API 进度条都是空的。
func TestBuildCursorUsageInfoUsesIndividualPlanWhenTeamHasOnlyOnDemand(t *testing.T) {
	const enterpriseTeamFixture = `{
  "billingCycleStart": "2026-08-15T08:36:23.000Z",
  "billingCycleEnd": "2026-09-15T08:36:23.000Z",
  "membershipType": "enterprise",
  "limitType": "team",
  "isUnlimited": false,
  "individualUsage": {
    "plan": {
      "enabled": true,
      "used": 2000,
      "limit": 2000,
      "remaining": 0,
      "breakdown": { "included": 2000, "bonus": 51507, "total": 53507 },
      "autoPercentUsed": 47.58571428571429,
      "apiPercentUsed": 100,
      "totalPercentUsed": 59.45222222222222
    },
    "onDemand": { "enabled": true, "used": 0, "limit": null, "remaining": null }
  },
  "teamUsage": {
    "onDemand": { "enabled": true, "used": 12105, "limit": null, "remaining": null }
  }
}`

	var summary cursor.UsageSummary
	require.NoError(t, json.Unmarshal([]byte(enterpriseTeamFixture), &summary))

	usage := buildCursorUsageInfo(&summary, time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC))
	require.Equal(t, "enterprise", usage.CursorPlan)
	require.NotNil(t, usage.CursorAutoUsage)
	require.InDelta(t, 47.586, usage.CursorAutoUsage.Utilization, 0.01)
	require.NotNil(t, usage.CursorAPIUsage)
	require.InDelta(t, 100, usage.CursorAPIUsage.Utilization, 0.01)
	require.InDelta(t, 20, usage.CursorIncludedUsed, 0.001)
	require.InDelta(t, 20, usage.CursorIncludedLimit, 0.001)
}

func TestCursorDegradedUsageClassifiesUpstreamErrors(t *testing.T) {
	unauthorized := cursorDegradedUsage(&cursor.HTTPError{Status: http.StatusUnauthorized, Operation: "usage"})
	require.NotNil(t, unauthorized)
	require.True(t, unauthorized.NeedsReauth)
	require.Equal(t, "unauthenticated", unauthorized.ErrorCode)

	forbidden := cursorDegradedUsage(&cursor.HTTPError{Status: http.StatusForbidden, Operation: "usage"})
	require.NotNil(t, forbidden)
	require.True(t, forbidden.IsForbidden)

	// 5xx 是上游抖动，应当照常上抛让调用方重试，而不是缓存成账号问题。
	require.Nil(t, cursorDegradedUsage(&cursor.HTTPError{Status: http.StatusBadGateway, Operation: "usage"}))
}
