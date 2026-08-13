package service

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
)

// cursorQuotaSnapshotReader 读账号的 Cursor 额度快照。
//
// 抽成接口而不是直接依赖 *AccountUsageService，是为了让网关这一侧只声明它真正
// 需要的那一个能力，测试里也能塞一个假实现而不必构造整个用量服务。
type cursorQuotaSnapshotReader interface {
	CursorQuotaSnapshot(accountID int64) (*UsageInfo, bool)
}

// cursorAutoQuotaModels 是消耗 Auto 额度的模型；其余具名模型计入订阅内的 API 额度。
//
// Cursor 把额度分成两档独立结算，打满的时间点通常差很远——实测 API 已经 100% 时
// Auto 才 56.67%。所以不能用一个开关把账号整个关掉：那会把还剩四成的 Auto 额度
// 一起作废，而这部分是已经付过钱的。
//
// 键用的是 ResolveModel 之后的 upstream modelID，所以 MAX 变体（grok-4.6-max）
// 会被归到同一个 grok-4.6 上，不必单独列。
var cursorAutoQuotaModels = map[string]struct{}{
	cursor.AutoModelID: {},
	"grok-4.6":         {},
	"grok-4.5":         {},
	"composer-2.5":     {},
}

// cursorModelUsesAutoQuota 判断模型走哪一档额度。
//
// 未知模型按 API 档处理：新模型绝大多数是接进来的第三方前沿模型，归错到 Auto 档
// 会让它在 API 已满时继续放行，直接落进无上限的 on-demand 账单。
func cursorModelUsesAutoQuota(modelID string) bool {
	_, ok := cursorAutoQuotaModels[strings.TrimSpace(modelID)]
	return ok
}

// quotaBlockReason 判断该模型对应那一档额度是否已经打满。
//
// 返回空串表示放行。判定与响应写入刻意分开：三个入口（chat completions /
// messages / responses）的错误体格式不同，由各自的 writer 去渲染。
//
// 拿不到快照时一律放行：宁可漏拦一次，也不能因为额度状态未知就把通道封死。
func (s *CursorGatewayService) quotaBlockReason(account *Account, modelID string) string {
	if s.quotaReader == nil || account == nil || account.IsCursorForceUseEnabled() {
		return ""
	}
	usage, ok := s.quotaReader.CursorQuotaSnapshot(account.ID)
	if !ok || usage == nil || usage.CursorIsUnlimited {
		return ""
	}

	resetHint := ""
	if usage.CursorBillingCycleEnd != nil {
		resetHint = fmt.Sprintf("，%s 重置", usage.CursorBillingCycleEnd.Local().Format("2006-01-02 15:04"))
	}

	if cursorModelUsesAutoQuota(modelID) {
		if usage.CursorAutoUsage != nil && usage.CursorAutoUsage.Utilization >= cursorUsageParkWatermark {
			return fmt.Sprintf(
				"Cursor Auto 额度已用尽（%.1f%%%s）。%s 走 Auto 额度，可改用 cursor/claude-sonnet-5、cursor/gpt-5.6-sol 等走 API 额度的模型。",
				usage.CursorAutoUsage.Utilization, resetHint, modelID)
		}
		return ""
	}

	if usage.CursorAPIUsage != nil && usage.CursorAPIUsage.Utilization >= cursorUsageParkWatermark {
		return fmt.Sprintf(
			"Cursor API 额度已用尽（%.1f%%%s）。%s 走 API 额度，可改用 cursor/default、cursor/grok-4.6、cursor/composer-2.5 等走 Auto 额度的模型。",
			usage.CursorAPIUsage.Utilization, resetHint, modelID)
	}
	return ""
}

// ensureModelQuota 是 chat completions / messages 两个入口的便捷包装。
// writeQuotaError 由各入口传自己的错误渲染器（OpenAI 与 Anthropic 的错误体
// 包装不同），保持与该入口其它错误一致的形状。
func (s *CursorGatewayService) ensureModelQuota(
	account *Account,
	modelID string,
	writeQuotaError func(status int, errType, message string) error,
) error {
	if reason := s.quotaBlockReason(account, modelID); reason != "" {
		return writeQuotaError(http.StatusTooManyRequests, "quota_exceeded", reason)
	}
	return nil
}
