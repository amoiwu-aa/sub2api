package service

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

// 自有上游桥（cursor / kiro）的「401/403 → 作废缓存 token → 重试一次」。
//
// 为什么需要它：这两个平台的 access token 都可能在 TTL 到期前就被上游作废
// （用户在别处重新登录、上游轮换、IdC 会话被吊销）。provider 只按「快过期了」
// 判断要不要刷新，对「没过期但已失效」无能为力——结果是这个账号会持续 401，
// 直到缓存自然过期或后台刷新器碰巧跑到。
//
// 其他平台由 gateway_upstream_response.go 的公共链路覆盖这件事，自有桥不经过
// 那条链路，只能自己做。

type nativeBridgeAuthRetryKey struct{}

// markNativeBridgeAuthRetried 标记本次请求已经用过那一次重试机会。
// 用 context 而不是参数传递，是为了让重试标记跟着请求走、不被中间层丢掉，
// 与 openai_agent_identity.go 的 markAgentIdentityTaskRecoveryTried 同一手法。
func markNativeBridgeAuthRetried(ctx context.Context) context.Context {
	return context.WithValue(ctx, nativeBridgeAuthRetryKey{}, true)
}

func nativeBridgeAuthRetried(ctx context.Context) bool {
	retried, _ := ctx.Value(nativeBridgeAuthRetryKey{}).(bool)
	return retried
}

// shouldRetryNativeBridgeAuth 判断这次失败是否值得作废 token 后重试一次。
//
// 四个条件缺一不可：
//   - 还没重试过（否则凭证是真的坏了，重试只会放大延迟）
//   - 确实是鉴权失败（401/403）
//   - 一个字节都还没写给客户端（写了就不能重来）
//   - 有 invalidator 可用
func shouldRetryNativeBridgeAuth(
	ctx context.Context,
	c *gin.Context,
	writerSizeBefore int,
	err error,
	invalidate func(context.Context) error,
	logLabel string,
	accountID int64,
) bool {
	if err == nil || nativeBridgeAuthRetried(ctx) || invalidate == nil {
		return false
	}
	var failover *UpstreamFailoverError
	if !errors.As(err, &failover) {
		return false
	}
	if failover.StatusCode != http.StatusUnauthorized && failover.StatusCode != http.StatusForbidden {
		return false
	}
	if c != nil && c.Writer.Size() != writerSizeBefore {
		return false
	}
	if invalidateErr := invalidate(ctx); invalidateErr != nil {
		// 作废失败就别重试了：缓存里还是那个坏 token，重试必然拿到同一个结果。
		slog.Warn(logLabel+"_auth_retry_invalidate_failed", "account_id", accountID, "error", invalidateErr)
		return false
	}
	slog.Info(logLabel+"_auth_retry", "account_id", accountID, "status", failover.StatusCode)
	return true
}

// accountIDOrZero 只为日志取 ID，nil 账号不该让日志调用点各写一遍判空。
func accountIDOrZero(account *Account) int64 {
	if account == nil {
		return 0
	}
	return account.ID
}
