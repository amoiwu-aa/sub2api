package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"
)

const (
	// kiroTokenProviderSkew 是请求路径上判定「该刷新了」的提前量。
	// Kiro 的 access token 只有 1 小时，取 3 分钟而不是 Grok 的 1 小时。
	kiroTokenProviderSkew = 3 * time.Minute
	kiroTokenCacheSkew    = 5 * time.Minute
	// kiroRequestRefreshTimeout 限制请求路径上的刷新等待。超时就放弃并标记
	// 临时不可调度，让后台刷新服务继续重试，而不是把客户端请求拖死。
	kiroRequestRefreshTimeout = 8 * time.Second
)

var (
	errKiroAccessTokenMissing  = errors.New("kiro access token is missing")
	errKiroConfiguredProxyMiss = errors.New("kiro configured proxy is missing")
)

// KiroTokenCache 复用与 Gemini/Grok 相同的 token 缓存接口。
type KiroTokenCache = GeminiTokenCache

// KiroTokenProvider 在请求路径上提供可用的 kiro access token。
type KiroTokenProvider struct {
	accountRepo      AccountRepository
	tokenCache       KiroTokenCache
	refreshAPI       *OAuthRefreshAPI
	executor         OAuthRefreshExecutor
	tempUnschedCache TempUnschedCache
}

func NewKiroTokenProvider(accountRepo AccountRepository, tokenCache KiroTokenCache) *KiroTokenProvider {
	return &KiroTokenProvider{accountRepo: accountRepo, tokenCache: tokenCache}
}

func (p *KiroTokenProvider) SetRefreshAPI(api *OAuthRefreshAPI, executor OAuthRefreshExecutor) {
	p.refreshAPI = api
	p.executor = executor
}

func (p *KiroTokenProvider) SetTempUnschedCache(cache TempUnschedCache) {
	p.tempUnschedCache = cache
}

// GetAccessToken 返回一个可用的 access token，必要时就地刷新。
func (p *KiroTokenProvider) GetAccessToken(ctx context.Context, account *Account) (string, error) {
	if account == nil {
		return "", errors.New("account is nil")
	}
	if account.Platform != PlatformKiro || account.Type != AccountTypeOAuth {
		return "", errors.New("not a kiro oauth account")
	}
	// 配置了代理却没加载到代理对象，是硬凭证失败。退化成直连会让该账号的
	// 流量从服务器出口 IP 打到上游，风控上等同于自曝。
	if account.ProxyID != nil && account.Proxy == nil {
		return "", errKiroConfiguredProxyMiss
	}

	cacheKey := KiroTokenCacheKey(account)
	if p.tokenCache != nil {
		if token, err := p.tokenCache.GetAccessToken(ctx, cacheKey); err == nil && strings.TrimSpace(token) != "" {
			return token, nil
		}
	}

	expiresAt := account.GetCredentialAsTime("expires_at")
	needsRefresh := expiresAt == nil || time.Until(*expiresAt) <= kiroTokenProviderSkew
	if needsRefresh && p.refreshAPI != nil && p.executor != nil {
		refreshCtx, cancel := context.WithTimeout(ctx, kiroRequestRefreshTimeout)
		defer cancel()
		result, err := p.refreshAPI.RefreshIfNeeded(refreshCtx, account, p.executor, kiroTokenProviderSkew)
		switch {
		case err != nil:
			p.markTempUnschedulable(account, err)
			// 刷新失败时若手上的 token 还没真过期，先用着；彻底过期才报错。
			if expiresAt == nil || !time.Now().Before(*expiresAt) {
				return "", err
			}
		case result != nil && result.LockHeld:
			// 别的请求正在刷新。缓存里若已有新 token 就用它，否则沿用当前的。
			if p.tokenCache != nil {
				if token, cacheErr := p.tokenCache.GetAccessToken(ctx, cacheKey); cacheErr == nil && strings.TrimSpace(token) != "" {
					return token, nil
				}
			}
		case result != nil && result.Account != nil:
			// 刷新期间账号的代理绑定若发生变化，本次调度的前提已经不成立，
			// 必须让上层重新选号，而不是拿新 token 走旧代理。
			if !kiroProxyIDsEqual(result.Account.ProxyID, account.ProxyID) {
				return "", errOAuthRefreshAccountStateChanged
			}
			account = result.Account
			expiresAt = account.GetCredentialAsTime("expires_at")
		}
	}

	accessToken := strings.TrimSpace(account.GetKiroAccessToken())
	if accessToken == "" {
		return "", errKiroAccessTokenMissing
	}

	if p.tokenCache != nil {
		latestAccount, isStale := CheckTokenVersion(ctx, account, p.accountRepo)
		if isStale && latestAccount != nil {
			latestToken := strings.TrimSpace(latestAccount.GetKiroAccessToken())
			if latestToken == "" {
				return "", errKiroAccessTokenMissing
			}
			return latestToken, nil
		}
		ttl := 30 * time.Minute
		if expiresAt != nil {
			switch until := time.Until(*expiresAt); {
			case until > kiroTokenCacheSkew:
				ttl = until - kiroTokenCacheSkew
			case until > 0:
				ttl = until
			default:
				ttl = time.Minute
			}
		}
		_ = p.tokenCache.SetAccessToken(ctx, cacheKey, accessToken, ttl)
	}

	return accessToken, nil
}

func (p *KiroTokenProvider) InvalidateToken(ctx context.Context, account *Account) error {
	if p == nil || p.tokenCache == nil || account == nil {
		return nil
	}
	return p.tokenCache.DeleteAccessToken(ctx, KiroTokenCacheKey(account))
}

func (p *KiroTokenProvider) markTempUnschedulable(account *Account, refreshErr error) {
	if p.accountRepo == nil || account == nil {
		return
	}
	now := time.Now()
	until := now.Add(tokenRefreshTempUnschedDuration)
	reason := "token refresh failed on request path: " + refreshErr.Error()
	// 请求 context 可能已超时，这里用 background 保证标记一定写下去。
	bgCtx := context.Background()
	if err := p.accountRepo.SetTempUnschedulable(bgCtx, account.ID, until, reason); err != nil {
		slog.Warn("kiro_token_provider.set_temp_unschedulable_failed", "account_id", account.ID, "error", err)
		return
	}
	if p.tempUnschedCache != nil {
		state := &TempUnschedState{
			UntilUnix:       until.Unix(),
			TriggeredAtUnix: now.Unix(),
			ErrorMessage:    reason,
		}
		if err := p.tempUnschedCache.SetTempUnsched(bgCtx, account.ID, state); err != nil {
			slog.Warn("kiro_token_provider.temp_unsched_cache_set_failed", "account_id", account.ID, "error", err)
		}
	}
}

func kiroProxyIDsEqual(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
