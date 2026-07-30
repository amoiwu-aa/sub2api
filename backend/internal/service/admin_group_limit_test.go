package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEnsureGroupLimitsApplicable 锁住「标准分组不接受限额」这条校验。
//
// 背景：日/周/月限额只有订阅模式会读，标准模式走余额检查那一支、完全不碰这三个
// 字段。此前面板照单全收，于是设了限额却一次都不会拦。生产上就踩过这个坑——
// Cursor 分组三个限额都存着 0，看着像封了顶，实际 24 小时照跑 122 个请求。
func TestEnsureGroupLimitsApplicable(t *testing.T) {
	f := func(v float64) *float64 { return &v }

	t.Run("订阅模式接受限额", func(t *testing.T) {
		require.NoError(t, ensureGroupLimitsApplicable(SubscriptionTypeSubscription, f(10), f(50), f(200)))
	})

	t.Run("标准模式拒绝正数限额", func(t *testing.T) {
		for _, tc := range []struct {
			name                   string
			daily, weekly, monthly *float64
		}{
			{"仅日限额", f(10), nil, nil},
			{"仅周限额", nil, f(50), nil},
			{"仅月限额", nil, nil, f(200)},
			{"三者齐全", f(10), f(50), f(200)},
		} {
			t.Run(tc.name, func(t *testing.T) {
				err := ensureGroupLimitsApplicable(SubscriptionTypeStandard, tc.daily, tc.weekly, tc.monthly)
				require.Error(t, err)
				// 错误信息要指出替代路径，否则用户只知道被拒、不知道该怎么办。
				require.Contains(t, err.Error(), "配额")
			})
		}
	})

	t.Run("标准模式允许未设置", func(t *testing.T) {
		require.NoError(t, ensureGroupLimitsApplicable(SubscriptionTypeStandard, nil, nil, nil))
	})

	// 存量分组里躺着 0（既不生效也没意义）。一并拒绝会让这些分组连改名都做不了。
	t.Run("标准模式放行存量的 0", func(t *testing.T) {
		require.NoError(t, ensureGroupLimitsApplicable(SubscriptionTypeStandard, f(0), f(0), f(0)))
	})
}
