package service

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/stretchr/testify/require"
)

type stubQuotaReader struct {
	usage *UsageInfo
	ok    bool
}

func (r stubQuotaReader) CursorQuotaSnapshot(int64) (*UsageInfo, bool) { return r.usage, r.ok }

func gateWith(usage *UsageInfo, ok bool) *CursorGatewayService {
	return &CursorGatewayService{quotaReader: stubQuotaReader{usage: usage, ok: ok}}
}

// TestCursorModelUsesAutoQuota 钉住模型到额度档的归属。
//
// 归错档的代价不对称：把 API 档模型误判成 Auto 档，会在 API 已满时继续放行，
// 每个请求直接进无上限的 on-demand 账单；反过来只是少放行一些请求。所以未知
// 模型必须落到 API 档。
func TestCursorModelUsesAutoQuota(t *testing.T) {
	for _, m := range []string{cursor.AutoModelID, "grok-4.5", "composer-2.5"} {
		require.True(t, cursorModelUsesAutoQuota(m), "%s 应算 Auto 档", m)
	}
	for _, m := range []string{"claude-sonnet-5", "claude-opus-4-8", "gpt-5.6-sol", "gpt-5.6-terra"} {
		require.False(t, cursorModelUsesAutoQuota(m), "%s 应算 API 档", m)
	}

	// MAX 变体经 ResolveModel 后归一到基础模型名，不必单独登记。
	require.Equal(t, "grok-4.5", cursor.ResolveModel("cursor/grok-4.5"+cursor.MaxModeSuffix).ModelID)
	require.True(t, cursorModelUsesAutoQuota(cursor.ResolveModel("cursor/grok-4.5"+cursor.MaxModeSuffix).ModelID))

	// 没见过的新模型一律按 API 档，保守拦截。
	require.False(t, cursorModelUsesAutoQuota("some-future-model"))
}

// TestQuotaBlockReasonIsolatesDimensions 是这次改动的核心诉求：
// API 打满不能连累走 Auto 额度的模型。
func TestQuotaBlockReasonIsolatesDimensions(t *testing.T) {
	cycleEnd := time.Now().Add(20 * 24 * time.Hour)
	apiExhausted := &UsageInfo{
		CursorAutoUsage:       &UsageProgress{Utilization: 56.67},
		CursorAPIUsage:        &UsageProgress{Utilization: 100},
		CursorBillingCycleEnd: &cycleEnd,
	}
	s := gateWith(apiExhausted, true)
	account := cursorParkAccount()

	t.Run("走 Auto 额度的模型照常放行", func(t *testing.T) {
		for _, m := range []string{cursor.AutoModelID, "grok-4.5", "composer-2.5"} {
			require.Empty(t, s.quotaBlockReason(account, m), "%s 不该被 API 额度连累", m)
		}
	})

	t.Run("走 API 额度的模型被拦下并说明原因", func(t *testing.T) {
		reason := s.quotaBlockReason(account, "claude-sonnet-5")
		require.NotEmpty(t, reason)
		require.Contains(t, reason, "API 额度已用尽")
		// 得告诉用户还能用什么，否则只知道被拒。
		require.Contains(t, reason, "cursor/default")
		// 带上重置时间，用户才知道要等多久。
		require.Contains(t, reason, cycleEnd.Local().Format("2006-01-02"))
	})
}

func TestQuotaBlockReasonAutoExhausted(t *testing.T) {
	autoExhausted := &UsageInfo{
		CursorAutoUsage: &UsageProgress{Utilization: 100},
		CursorAPIUsage:  &UsageProgress{Utilization: 30},
	}
	s := gateWith(autoExhausted, true)
	account := cursorParkAccount()

	reason := s.quotaBlockReason(account, cursor.AutoModelID)
	require.Contains(t, reason, "Auto 额度已用尽")
	require.Contains(t, reason, "cursor/claude-sonnet-5")

	// 反过来，API 档还有余量就该放行。
	require.Empty(t, s.quotaBlockReason(account, "claude-sonnet-5"))
}

// TestQuotaBlockReasonFailsOpen 额度状态未知时必须放行：
// 宁可漏拦一次，也不能因为缓存没数据就把整个通道封死。
func TestQuotaBlockReasonFailsOpen(t *testing.T) {
	account := cursorParkAccount()
	full := &UsageInfo{
		CursorAutoUsage: &UsageProgress{Utilization: 100},
		CursorAPIUsage:  &UsageProgress{Utilization: 100},
	}

	require.Empty(t, gateWith(nil, false).quotaBlockReason(account, "claude-sonnet-5"), "缓存未命中应放行")
	require.Empty(t, gateWith(nil, true).quotaBlockReason(account, "claude-sonnet-5"), "快照为空应放行")
	require.Empty(t, (&CursorGatewayService{}).quotaBlockReason(account, "claude-sonnet-5"), "没有 reader 应放行")
	require.Empty(t, gateWith(full, true).quotaBlockReason(nil, "claude-sonnet-5"), "没有账号应放行")

	unlimited := &UsageInfo{
		CursorIsUnlimited: true,
		CursorAutoUsage:   &UsageProgress{Utilization: 100},
		CursorAPIUsage:    &UsageProgress{Utilization: 100},
	}
	require.Empty(t, gateWith(unlimited, true).quotaBlockReason(account, "claude-sonnet-5"), "无限量套餐应放行")
}
