package service

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/gin-gonic/gin"
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
	for _, m := range []string{cursor.AutoModelID, "grok-4.6", "grok-4.5", "composer-2.5"} {
		require.True(t, cursorModelUsesAutoQuota(m), "%s 应算 Auto 档", m)
	}
	for _, m := range []string{"claude-sonnet-5", "claude-opus-4-8", "gpt-5.6-sol", "gpt-5.6-terra"} {
		require.False(t, cursorModelUsesAutoQuota(m), "%s 应算 API 档", m)
	}

	// MAX 变体经 ResolveModel 后归一到基础模型名，不必单独登记。
	require.Equal(t, "grok-4.6", cursor.ResolveModel("cursor/grok-4.6"+cursor.MaxModeSuffix).ModelID)
	require.True(t, cursorModelUsesAutoQuota(cursor.ResolveModel("cursor/grok-4.6"+cursor.MaxModeSuffix).ModelID))

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
		for _, m := range []string{cursor.AutoModelID, "grok-4.6", "grok-4.5", "composer-2.5"} {
			require.Empty(t, s.quotaBlockReason(account, m), "%s 不该被 API 额度连累", m)
		}
	})

	t.Run("走 API 额度的模型被拦下并说明原因", func(t *testing.T) {
		reason := s.quotaBlockReason(account, "claude-sonnet-5")
		require.NotEmpty(t, reason)
		require.Contains(t, reason, "API 额度已用尽")
		// 得告诉用户还能用什么，否则只知道被拒。
		require.Contains(t, reason, "cursor/default")
		require.Contains(t, reason, "cursor/grok-4.6")
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

func TestQuotaBlockReasonForceUseBypassesLocalQuotaGate(t *testing.T) {
	full := &UsageInfo{
		CursorAutoUsage: &UsageProgress{Utilization: 100},
		CursorAPIUsage:  &UsageProgress{Utilization: 100},
	}
	account := cursorParkAccount()
	account.Extra = map[string]any{CursorForceUseExtraKey: true}

	require.Empty(t, gateWith(full, true).quotaBlockReason(account, "claude-sonnet-5"))
	require.Empty(t, gateWith(full, true).quotaBlockReason(account, cursor.AutoModelID))
}

func TestQuotaBlockReasonStopsAllModelsWhenSandWeeklyUsageIsUnavailable(t *testing.T) {
	available := false
	resetAt := time.Now().Add(6 * 24 * time.Hour)
	usage := &UsageInfo{
		CursorSandHasAvailableUsage: &available,
		CursorSandUsage:             &UsageProgress{Utilization: 100, ResetsAt: &resetAt},
		CursorAutoUsage:             &UsageProgress{Utilization: 10},
		CursorAPIUsage:              &UsageProgress{Utilization: 10},
	}
	s := gateWith(usage, true)
	account := cursorParkAccount()
	account.Credentials = map[string]any{CursorAgentProfileCredentialKey: "sand"}
	account.Extra = map[string]any{CursorForceUseExtraKey: true}

	for _, model := range []string{cursor.AutoModelID, "claude-sonnet-5", "grok-4.6"} {
		reason := s.quotaBlockReason(account, model)
		require.Contains(t, reason, "Grok Bot weekly usage is exhausted")
		require.Contains(t, reason, resetAt.Local().Format("2006-01-02"))
	}
}

func TestQuotaBlockReasonSandIgnoresCursorIDEQuotaPools(t *testing.T) {
	available := true
	usage := &UsageInfo{
		CursorSandHasAvailableUsage: &available,
		CursorSandUsage:             &UsageProgress{Utilization: 12},
		CursorAutoUsage:             &UsageProgress{Utilization: 100},
		CursorAPIUsage:              &UsageProgress{Utilization: 100},
	}
	account := cursorParkAccount()
	account.Credentials = map[string]any{CursorAgentProfileCredentialKey: "sand"}

	for _, model := range []string{cursor.AutoModelID, "claude-sonnet-5", "grok-4.6"} {
		require.Empty(t, gateWith(usage, true).quotaBlockReason(account, model))
	}
}

// 配额 429 的错误体必须与各入口的协议一致：Anthropic 客户端解析的是
// {"type":"error","error":{...}}，塞给它 OpenAI 形状会显示不出错误详情。
func TestEnsureModelQuotaUsesProtocolSpecificErrorBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	apiExhausted := &UsageInfo{
		CursorAutoUsage: &UsageProgress{Utilization: 10},
		CursorAPIUsage:  &UsageProgress{Utilization: 100},
	}
	s := gateWith(apiExhausted, true)
	account := cursorParkAccount()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	err := s.ensureModelQuota(account, "claude-sonnet-5", func(status int, errType, message string) error {
		return s.writeAnthropicError(c, status, errType, message)
	})
	require.Error(t, err)
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.Contains(t, rec.Body.String(), `"type":"error"`)
	require.Contains(t, rec.Body.String(), `"quota_exceeded"`)
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
