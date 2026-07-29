package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
)

// kiroTokenRefreshSkew 是刷新窗口的下限。Kiro 的 access token 通常只有 1 小时，
// 比 Grok 短，因此窗口取 15 分钟而非 1 小时——取 1 小时会让每一轮巡检都判定
// 需要刷新，把刷新变成事实上的高频轮询。
const kiroTokenRefreshSkew = 15 * time.Minute

type KiroTokenRefresher struct {
	kiroOAuthService *KiroOAuthService
}

func NewKiroTokenRefresher(kiroOAuthService *KiroOAuthService) *KiroTokenRefresher {
	return &KiroTokenRefresher{kiroOAuthService: kiroOAuthService}
}

func (r *KiroTokenRefresher) CacheKey(account *Account) string {
	return KiroTokenCacheKey(account)
}

func (r *KiroTokenRefresher) CanRefresh(account *Account) bool {
	if account == nil || account.Platform != PlatformKiro || account.Type != AccountTypeOAuth {
		return false
	}
	if strings.TrimSpace(account.GetKiroRefreshToken()) == "" {
		return false
	}
	// IdC 缺 client 注册时刷新必然失败。提前判定为不可刷新，避免后台
	// 每一轮都打一次注定 400 的请求，把账号刷成 error 状态。
	if account.GetKiroAuthMethod() == kiro.AuthMethodIdC {
		if strings.TrimSpace(account.GetCredential("client_id")) == "" ||
			strings.TrimSpace(account.GetCredential("client_secret")) == "" {
			return false
		}
	}
	return true
}

func (r *KiroTokenRefresher) NeedsRefresh(account *Account, refreshWindow time.Duration) bool {
	if account == nil || strings.TrimSpace(account.GetKiroRefreshToken()) == "" {
		return false
	}
	if strings.TrimSpace(account.GetKiroAccessToken()) == "" {
		return true
	}
	expiresAt := account.GetCredentialAsTime("expires_at")
	if expiresAt == nil {
		return true
	}
	if refreshWindow < kiroTokenRefreshSkew {
		refreshWindow = kiroTokenRefreshSkew
	}
	return time.Until(*expiresAt) < refreshWindow
}

func (r *KiroTokenRefresher) Refresh(ctx context.Context, account *Account) (map[string]any, error) {
	if r == nil || r.kiroOAuthService == nil {
		return nil, errors.New("kiro oauth service is not configured")
	}
	tokenInfo, err := r.kiroOAuthService.RefreshAccountToken(ctx, account)
	if err != nil {
		return nil, err
	}
	newCredentials := r.kiroOAuthService.BuildAccountCredentials(tokenInfo)
	return MergeCredentials(account.Credentials, newCredentials), nil
}
