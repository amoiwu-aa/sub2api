package service

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func cursorParkAccount() *Account {
	return &Account{ID: 7, Platform: PlatformCursor}
}

func cursorUsageAt(auto, api float64, cycleEnd *time.Time) *UsageInfo {
	return &UsageInfo{
		CursorAutoUsage:       &UsageProgress{Utilization: auto},
		CursorAPIUsage:        &UsageProgress{Utilization: api},
		CursorBillingCycleEnd: cycleEnd,
		CursorOnDemandUsed:    32.66,
		CursorOnDemandEnabled: true,
	}
}

// TestParkExhaustedCursorAccount 的核心约束：必须两档额度都打满才停号。
//
// 单档打满不能停——Cursor 的 Auto 与 API 是独立结算的，打满时间点差很远（实测
// API 100% 时 Auto 才 56.67%）。停掉等于把还剩四成、已经付过钱的 Auto 额度作废。
// 单档打满交给网关的模型级门控处理。
func TestParkExhaustedCursorAccount(t *testing.T) {
	t.Run("两档都打满才停到计费周期结束", func(t *testing.T) {
		cycleEnd := time.Now().Add(20 * 24 * time.Hour)
		repo := &parkRecorder{}
		s := &AccountUsageService{accountRepo: repo}

		require.True(t, s.parkExhaustedCursorAccount(cursorParkAccount(), cursorUsageAt(100, 100, &cycleEnd)))
		require.Equal(t, 1, repo.calls)
		require.Equal(t, int64(7), repo.id)
		require.Equal(t, cycleEnd, repo.until)
		require.Contains(t, repo.reason, "auto")
		require.Contains(t, repo.reason, "api")
		// 账单金额进 reason，排查时不必再翻上游接口。
		require.Contains(t, repo.reason, "32.66")
	})

	t.Run("只有 API 打满不停", func(t *testing.T) {
		cycleEnd := time.Now().Add(20 * 24 * time.Hour)
		repo := &parkRecorder{}
		s := &AccountUsageService{accountRepo: repo}

		require.False(t, s.parkExhaustedCursorAccount(cursorParkAccount(), cursorUsageAt(56.67, 100, &cycleEnd)))
		require.Zero(t, repo.calls)
	})

	t.Run("只有 Auto 打满不停", func(t *testing.T) {
		cycleEnd := time.Now().Add(20 * 24 * time.Hour)
		repo := &parkRecorder{}
		s := &AccountUsageService{accountRepo: repo}

		require.False(t, s.parkExhaustedCursorAccount(cursorParkAccount(), cursorUsageAt(100, 12, &cycleEnd)))
		require.Zero(t, repo.calls)
	})

	t.Run("两档都没满不动", func(t *testing.T) {
		cycleEnd := time.Now().Add(20 * 24 * time.Hour)
		repo := &parkRecorder{}
		s := &AccountUsageService{accountRepo: repo}

		require.False(t, s.parkExhaustedCursorAccount(cursorParkAccount(), cursorUsageAt(99.9, 99.9, &cycleEnd)))
		require.Zero(t, repo.calls)
	})

	t.Run("无限量套餐不停", func(t *testing.T) {
		repo := &parkRecorder{}
		s := &AccountUsageService{accountRepo: repo}
		usage := cursorUsageAt(100, 100, nil)
		usage.CursorIsUnlimited = true

		require.False(t, s.parkExhaustedCursorAccount(cursorParkAccount(), usage))
		require.Zero(t, repo.calls)
	})

	t.Run("拿不到周期结束时用兜底时长", func(t *testing.T) {
		repo := &parkRecorder{}
		s := &AccountUsageService{accountRepo: repo}
		before := time.Now()

		require.True(t, s.parkExhaustedCursorAccount(cursorParkAccount(), cursorUsageAt(100, 100, nil)))
		require.WithinDuration(t, before.Add(cursorExhaustedFallbackPark), repo.until, time.Minute)
	})

	t.Run("周期结束时间已过则用兜底时长", func(t *testing.T) {
		past := time.Now().Add(-time.Hour)
		repo := &parkRecorder{}
		s := &AccountUsageService{accountRepo: repo}
		before := time.Now()

		require.True(t, s.parkExhaustedCursorAccount(cursorParkAccount(), cursorUsageAt(100, 100, &past)))
		require.True(t, repo.until.After(before))
	})

	t.Run("写失败时不 panic 且报告未停号", func(t *testing.T) {
		repo := &parkRecorder{err: errors.New("db down")}
		s := &AccountUsageService{accountRepo: repo}

		require.NotPanics(t, func() {
			require.False(t, s.parkExhaustedCursorAccount(cursorParkAccount(), cursorUsageAt(100, 100, nil)))
		})
	})

	t.Run("入参缺失时不动", func(t *testing.T) {
		repo := &parkRecorder{}
		s := &AccountUsageService{accountRepo: repo}

		require.False(t, s.parkExhaustedCursorAccount(nil, cursorUsageAt(100, 100, nil)))
		require.False(t, s.parkExhaustedCursorAccount(cursorParkAccount(), nil))
		// 上游没给这两个维度时不能瞎停。
		require.False(t, s.parkExhaustedCursorAccount(cursorParkAccount(), &UsageInfo{}))
		require.Zero(t, repo.calls)
	})
}

// TestCursorParkStopsEvenWithOnDemandEnabled 锁住 Cursor 与 Kiro 相反的那个取舍。
//
// Kiro 开了超额就不停：还能用，只是花钱，停掉等于平白损失容量（见
// TestParkSkipsOverageEnabledAccount）。Cursor 必须反过来——它的 on-demand 默认
// 开启且没有上限，两档都打满后每个请求都直接进按量账单，不停就没有任何刹车。
func TestCursorParkStopsEvenWithOnDemandEnabled(t *testing.T) {
	repo := &parkRecorder{}
	s := &AccountUsageService{accountRepo: repo}

	usage := cursorUsageAt(100, 100, nil)
	usage.CursorOnDemandEnabled = true

	require.True(t, s.parkExhaustedCursorAccount(cursorParkAccount(), usage))
	require.Equal(t, 1, repo.calls)
}

// TestCursorParkWatermarkIsNotBelowFullUsage 水位不该低于 100%：
// 订阅内额度是已经付过钱的，提前停等于主动作废自己买的量。
func TestCursorParkWatermarkIsNotBelowFullUsage(t *testing.T) {
	require.LessOrEqual(t, cursorUsageParkWatermark, 100.0)
	require.Greater(t, cursorUsageParkWatermark, 0.0)
}
