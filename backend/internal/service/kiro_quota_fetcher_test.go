package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/stretchr/testify/require"
)

func kiroQuotaAccount() *Account {
	return &Account{
		ID:       1,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "at",
			"profile_arn":  "arn:aws:codewhisperer:us-east-1:1:profile/ABC",
			"auth_method":  "social",
		},
	}
}

func TestKiroQuotaFetcherCanFetch(t *testing.T) {
	f := NewKiroQuotaFetcher(nil)

	require.True(t, f.CanFetch(kiroQuotaAccount()))
	require.False(t, f.CanFetch(nil))

	// 平台不对
	other := kiroQuotaAccount()
	other.Platform = PlatformCursor
	require.False(t, f.CanFetch(other))

	// 缺 access_token
	noToken := kiroQuotaAccount()
	noToken.Credentials["access_token"] = ""
	require.False(t, f.CanFetch(noToken))

	// 缺 profile_arn：它决定往哪个 region 打，缺了构造不出客户端
	noARN := kiroQuotaAccount()
	noARN.Credentials["profile_arn"] = ""
	require.False(t, f.CanFetch(noARN))
}

// TestKiroQuotaFetcherRejectsConfiguredProxyMiss 确认配置了代理却取不到时报错，
// 绝不能返回空串退化成直连——那会让账号流量从服务器出口 IP 打到上游。
func TestKiroQuotaFetcherRejectsConfiguredProxyMiss(t *testing.T) {
	f := NewKiroQuotaFetcher(nil)
	account := kiroQuotaAccount()
	proxyID := int64(7)
	account.ProxyID = &proxyID

	_, err := f.GetProxyURL(t.Context(), account)
	require.Error(t, err)
}

func TestKiroQuotaFetcherNoProxyConfigured(t *testing.T) {
	f := NewKiroQuotaFetcher(nil)
	url, err := f.GetProxyURL(t.Context(), kiroQuotaAccount())
	require.NoError(t, err)
	require.Empty(t, url)
}

func TestBuildKiroUsageInfo(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	used, limit := 0.01, 50.0
	limits := &kiro.UsageLimits{
		NextDateReset: 1785542400, // 2026-08-01T00:00:00Z
		SubscriptionInfo: kiro.SubscriptionInfo{
			SubscriptionTitle: "KIRO FREE",
			Type:              "Q_DEVELOPER_STANDALONE_FREE",
		},
		OverageConfiguration: kiro.OverageConfiguration{OverageStatus: "DISABLED"},
		UsageBreakdownList: []kiro.UsageBreakdown{{
			ResourceType:              kiro.ResourceTypeCredit,
			CurrentUsageWithPrecision: &used,
			UsageLimitWithPrecision:   &limit,
			OverageRate:               0.04,
			Currency:                  "USD",
			FreeTrialInfo:             &kiro.FreeTrialInfo{FreeTrialStatus: "EXPIRED"},
		}},
	}

	usage := buildKiroUsageInfo(limits, now)

	require.Equal(t, "KIRO FREE", usage.SubscriptionTierRaw)
	require.Equal(t, "FREE", usage.SubscriptionTier)
	require.False(t, usage.KiroOverageEnabled)
	require.False(t, usage.KiroExhausted)
	require.InDelta(t, 0.01, usage.KiroCreditsUsed, 1e-9)
	require.InDelta(t, 50.0, usage.KiroCreditsLimit, 1e-9)
	require.InDelta(t, 0.04, usage.KiroOverageRate, 1e-9)
	require.Equal(t, "USD", usage.KiroCurrency)
	require.Equal(t, "EXPIRED", usage.KiroFreeTrialStatus)

	require.NotNil(t, usage.KiroCredits)
	require.InDelta(t, 0.02, usage.KiroCredits.Utilization, 1e-9)
	require.NotNil(t, usage.KiroCredits.ResetsAt)
	require.Equal(t, time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), usage.KiroCredits.ResetsAt.UTC())
	require.Positive(t, usage.KiroCredits.RemainingSeconds)
}

// TestBuildKiroUsageInfoExhausted 覆盖账号池最关心的那个判断。
func TestBuildKiroUsageInfoExhausted(t *testing.T) {
	now := time.Now()
	makeLimits := func(overage string, used float64) *kiro.UsageLimits {
		u, l := used, 50.0
		return &kiro.UsageLimits{
			OverageConfiguration: kiro.OverageConfiguration{OverageStatus: overage},
			UsageBreakdownList: []kiro.UsageBreakdown{{
				ResourceType:              kiro.ResourceTypeCredit,
				CurrentUsageWithPrecision: &u,
				UsageLimitWithPrecision:   &l,
			}},
		}
	}

	require.True(t, buildKiroUsageInfo(makeLimits("DISABLED", 50), now).KiroExhausted)
	// 开了超额就还能继续跑，不该被判死。
	require.False(t, buildKiroUsageInfo(makeLimits("ENABLED", 50), now).KiroExhausted)
	require.False(t, buildKiroUsageInfo(makeLimits("DISABLED", 10), now).KiroExhausted)
}

func TestBuildKiroUsageInfoHandlesNil(t *testing.T) {
	usage := buildKiroUsageInfo(nil, time.Now())
	require.NotNil(t, usage)
	require.NotNil(t, usage.UpdatedAt)
	require.Nil(t, usage.KiroCredits)
}

// TestBuildKiroUsageInfoWithoutCreditBreakdown 覆盖上游没给 CREDIT 维度的情况。
func TestBuildKiroUsageInfoWithoutCreditBreakdown(t *testing.T) {
	usage := buildKiroUsageInfo(&kiro.UsageLimits{
		SubscriptionInfo: kiro.SubscriptionInfo{SubscriptionTitle: "KIRO PRO", Type: "Q_DEVELOPER_PRO"},
	}, time.Now())

	require.Equal(t, "PRO", usage.SubscriptionTier)
	require.Nil(t, usage.KiroCredits)
	require.False(t, usage.KiroExhausted)
}

// TestKiroDegradedUsage 确认「账号自身有问题」的错误被降级成标记而不是硬报错，
// 否则一个待重新授权的账号会把整个用量面板打挂。
func TestKiroDegradedUsage(t *testing.T) {
	tests := []struct {
		status        int
		wantCode      string
		wantReauth    bool
		wantForbidden bool
	}{
		{status: http.StatusUnauthorized, wantCode: "unauthenticated", wantReauth: true},
		{status: http.StatusForbidden, wantCode: "forbidden", wantForbidden: true},
		{status: http.StatusTooManyRequests, wantCode: "rate_limited"},
	}
	for _, tc := range tests {
		usage := kiroDegradedUsage(&kiro.APIError{Status: tc.status, Operation: "GetUsageLimits", Body: "x"})
		require.NotNil(t, usage, "status %d 应降级", tc.status)
		require.Equal(t, tc.wantCode, usage.ErrorCode)
		require.Equal(t, tc.wantReauth, usage.NeedsReauth)
		require.Equal(t, tc.wantForbidden, usage.IsForbidden)
		require.NotEmpty(t, usage.Error)
	}

	// 5xx 与网络错误照常上抛，交给上层重试，不该被当成账号问题缓存下来。
	require.Nil(t, kiroDegradedUsage(&kiro.APIError{Status: http.StatusInternalServerError}))
	require.Nil(t, kiroDegradedUsage(errors.New("connection reset")))
}

func TestNormalizeKiroTier(t *testing.T) {
	require.Equal(t, "FREE", normalizeKiroTier("Q_DEVELOPER_STANDALONE_FREE"))
	require.Equal(t, "PRO", normalizeKiroTier("Q_DEVELOPER_PRO"))
	require.Equal(t, "UNKNOWN", normalizeKiroTier(""))
	require.Equal(t, "UNKNOWN", normalizeKiroTier("SOMETHING_ELSE"))
}

func TestRecalcKiroRemainingSeconds(t *testing.T) {
	future := time.Now().Add(2 * time.Hour)
	usage := &UsageInfo{KiroCredits: &UsageProgress{ResetsAt: &future, RemainingSeconds: 1}}
	recalcKiroRemainingSeconds(usage)
	require.Greater(t, usage.KiroCredits.RemainingSeconds, 7000)

	// 已经过了重置时间时不能返回负数。
	past := time.Now().Add(-time.Hour)
	usage = &UsageInfo{KiroCredits: &UsageProgress{ResetsAt: &past}}
	recalcKiroRemainingSeconds(usage)
	require.Zero(t, usage.KiroCredits.RemainingSeconds)

	// 没有额度信息时不应 panic。
	require.NotPanics(t, func() {
		recalcKiroRemainingSeconds(nil)
		recalcKiroRemainingSeconds(&UsageInfo{})
	})
}

// TestGetKiroUsageWithoutFetcher 确认没配 fetcher 时返回空结果而不是报错，
// 与 Antigravity 的处理一致。
func TestGetKiroUsageWithoutFetcher(t *testing.T) {
	s := &AccountUsageService{cache: NewUsageCache()}
	usage, err := s.getKiroUsage(t.Context(), kiroQuotaAccount())
	require.NoError(t, err)
	require.NotNil(t, usage)
	require.NotNil(t, usage.UpdatedAt)
}

// parkRecorder 记录 SetTempUnschedulable 的调用，其余方法不实现——
// 本测试只关心暂停调度这一个行为。
type parkRecorder struct {
	AccountRepository
	calls  int
	id     int64
	until  time.Time
	reason string
	err    error
}

func (r *parkRecorder) SetTempUnschedulable(_ context.Context, id int64, until time.Time, reason string) error {
	r.calls++
	r.id, r.until, r.reason = id, until, reason
	return r.err
}

func TestParkExhaustedKiroAccount(t *testing.T) {
	t.Run("耗尽时暂停到额度重置", func(t *testing.T) {
		resetAt := time.Now().Add(3 * time.Hour)
		repo := &parkRecorder{}
		s := &AccountUsageService{accountRepo: repo}
		s.parkExhaustedKiroAccount(kiroQuotaAccount(), &UsageInfo{
			KiroExhausted:    true,
			KiroCreditsUsed:  50,
			KiroCreditsLimit: 50,
			KiroCredits:      &UsageProgress{ResetsAt: &resetAt},
		})

		require.Equal(t, 1, repo.calls)
		require.Equal(t, int64(1), repo.id)
		require.Equal(t, resetAt, repo.until)
		require.Contains(t, repo.reason, "exhausted")
	})

	t.Run("没耗尽不动", func(t *testing.T) {
		repo := &parkRecorder{}
		s := &AccountUsageService{accountRepo: repo}
		s.parkExhaustedKiroAccount(kiroQuotaAccount(), &UsageInfo{
			KiroExhausted:    false,
			KiroCreditsUsed:  10,
			KiroCreditsLimit: 50,
		})
		require.Zero(t, repo.calls)
	})

	t.Run("拿不到重置时间时用兜底时长", func(t *testing.T) {
		repo := &parkRecorder{}
		s := &AccountUsageService{accountRepo: repo}
		before := time.Now()
		s.parkExhaustedKiroAccount(kiroQuotaAccount(), &UsageInfo{KiroExhausted: true})

		require.Equal(t, 1, repo.calls)
		// 必须暂停，否则调度器会一直往必然失败的账号上打
		require.True(t, repo.until.After(before))
		require.WithinDuration(t, before.Add(kiroExhaustedFallbackPark), repo.until, time.Minute)
	})

	t.Run("重置时间已过则用兜底时长", func(t *testing.T) {
		repo := &parkRecorder{}
		s := &AccountUsageService{accountRepo: repo}
		past := time.Now().Add(-time.Hour)
		before := time.Now()
		s.parkExhaustedKiroAccount(kiroQuotaAccount(), &UsageInfo{
			KiroExhausted: true,
			KiroCredits:   &UsageProgress{ResetsAt: &past},
		})

		require.Equal(t, 1, repo.calls)
		require.True(t, repo.until.After(before))
	})

	t.Run("写失败不 panic", func(t *testing.T) {
		repo := &parkRecorder{err: errors.New("db down")}
		s := &AccountUsageService{accountRepo: repo}
		require.NotPanics(t, func() {
			s.parkExhaustedKiroAccount(kiroQuotaAccount(), &UsageInfo{KiroExhausted: true})
		})
	})

	t.Run("入参为空时不动", func(t *testing.T) {
		repo := &parkRecorder{}
		s := &AccountUsageService{accountRepo: repo}
		s.parkExhaustedKiroAccount(nil, &UsageInfo{KiroExhausted: true})
		s.parkExhaustedKiroAccount(kiroQuotaAccount(), nil)
		require.Zero(t, repo.calls)
	})
}

// TestParkSkipsOverageEnabledAccount 开了超额的账号仍然可用（只是要花钱），
// 停掉它等于平白损失可用容量。这条由 kiro_exhausted 的语义保证：
// buildKiroUsageInfo 在开了超额时不会置位。
func TestParkSkipsOverageEnabledAccount(t *testing.T) {
	repo := &parkRecorder{}
	s := &AccountUsageService{accountRepo: repo}

	u, l := 50.0, 50.0
	usage := buildKiroUsageInfo(&kiro.UsageLimits{
		OverageConfiguration: kiro.OverageConfiguration{OverageStatus: "ENABLED"},
		UsageBreakdownList: []kiro.UsageBreakdown{{
			ResourceType:              kiro.ResourceTypeCredit,
			CurrentUsageWithPrecision: &u,
			UsageLimitWithPrecision:   &l,
		}},
	}, time.Now())

	require.False(t, usage.KiroExhausted)
	s.parkExhaustedKiroAccount(kiroQuotaAccount(), usage)
	require.Zero(t, repo.calls)
}

// TestKiroUsageCacheTTL 确认降级结果用更短的 TTL——
// 账号恢复后应当较快反映出来，不能被正常 TTL 焊死。
func TestKiroUsageCacheTTL(t *testing.T) {
	s := &AccountUsageService{cache: NewUsageCache()}
	now := time.Now()

	// 正常结果：2 分钟内仍然命中
	s.cache.kiroCache.Store(int64(1), &kiroUsageCache{
		usageInfo: &UsageInfo{UpdatedAt: &now},
		timestamp: now.Add(-2 * time.Minute),
	})
	_, ok := s.loadKiroCache(1)
	require.True(t, ok)

	// 降级结果：2 分钟已超过 kiroErrorTTL，应当失效
	s.cache.kiroCache.Store(int64(2), &kiroUsageCache{
		usageInfo: &UsageInfo{UpdatedAt: &now, Error: "boom"},
		timestamp: now.Add(-2 * time.Minute),
	})
	_, ok = s.loadKiroCache(2)
	require.False(t, ok)
}
