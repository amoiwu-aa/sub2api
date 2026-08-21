package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
)

const (
	cursorTokenProviderSkew   = 5 * time.Minute
	cursorTokenCacheSkew      = 10 * time.Minute
	cursorRequestRefreshLimit = 10 * time.Second
	// cursorTokenCacheMaxTTL 给缓存条目一个硬上限。Cursor 的 session JWT 可以有
	// 好几天的有效期，按过期时间缓存意味着一份坏掉的 token 要好几天才会被重新拉取。
	cursorTokenCacheMaxTTL = 30 * time.Minute
)

var (
	errCursorAccessTokenMissing  = errors.New("cursor access token is missing")
	errCursorConfiguredProxyMiss = errors.New("cursor configured proxy is missing")
	errCursorTokenNotSession     = errors.New("cursor access token is not a session token")
	errCursorTokenInvalid        = errors.New("cursor access token is not usable by the Sand client")
)

// CursorTokenCache 复用与 Gemini/Grok 相同的 token 缓存接口。
type CursorTokenCache = GeminiTokenCache

// CursorTokenProvider 在请求路径上提供可用的 cursor session token。
type CursorTokenProvider struct {
	accountRepo      AccountRepository
	tokenCache       CursorTokenCache
	refreshAPI       *OAuthRefreshAPI
	executor         OAuthRefreshExecutor
	tempUnschedCache TempUnschedCache
}

func NewCursorTokenProvider(accountRepo AccountRepository, tokenCache CursorTokenCache) *CursorTokenProvider {
	return &CursorTokenProvider{accountRepo: accountRepo, tokenCache: tokenCache}
}

func (p *CursorTokenProvider) SetRefreshAPI(api *OAuthRefreshAPI, executor OAuthRefreshExecutor) {
	p.refreshAPI = api
	p.executor = executor
}

func (p *CursorTokenProvider) SetTempUnschedCache(cache TempUnschedCache) {
	p.tempUnschedCache = cache
}

// GetAccessToken 返回一个 session 态的 access token，必要时就地刷新。
func (p *CursorTokenProvider) GetAccessToken(ctx context.Context, account *Account) (string, error) {
	if account == nil {
		return "", errors.New("account is nil")
	}
	if account.Platform != PlatformCursor || account.Type != AccountTypeOAuth {
		return "", errors.New("not a cursor oauth account")
	}
	if account.ProxyID != nil && account.Proxy == nil {
		return "", errCursorConfiguredProxyMiss
	}
	profile := account.CursorAgentProfile()

	cacheKey := CursorTokenCacheKey(account)
	if p.tokenCache != nil {
		if token, err := p.tokenCache.GetAccessToken(ctx, cacheKey); err == nil {
			if cached := strings.TrimSpace(token); cursorTokenUsableForAccount(account, cached) {
				return cached, nil
			}
		}
	}

	accessToken := strings.TrimSpace(account.GetCursorAccessToken())
	expiresAt := cursorAccessTokenExpiry(account, accessToken)
	// web 态必须换成 session 才能调 Agent，这与「快过期了」是两回事。
	needsRefresh := accessToken == "" || !cursorTokenUsableForAccount(account, accessToken)
	if profile != cursor.AgentProfileSand {
		needsRefresh = needsRefresh ||
			expiresAt == nil ||
			time.Until(*expiresAt) <= cursorTokenProviderSkew
	} else if expiresAt != nil {
		needsRefresh = needsRefresh || time.Until(*expiresAt) <= cursorTokenProviderSkew
	}

	if needsRefresh && p.refreshAPI != nil && p.executor != nil {
		refreshCtx, cancel := context.WithTimeout(ctx, cursorRequestRefreshLimit)
		defer cancel()
		result, err := p.refreshAPI.RefreshIfNeeded(refreshCtx, account, p.executor, cursorTokenProviderSkew)
		switch {
		case err != nil:
			p.markTempUnschedulable(account, err)
			if accessToken == "" || !cursorTokenUsableForAccount(account, accessToken) ||
				expiresAt == nil || !time.Now().Before(*expiresAt) {
				return "", err
			}
		case result != nil && result.LockHeld:
			if p.tokenCache != nil {
				if token, cacheErr := p.tokenCache.GetAccessToken(ctx, cacheKey); cacheErr == nil {
					if cached := strings.TrimSpace(token); cursorTokenUsableForAccount(account, cached) {
						return cached, nil
					}
				}
			}
		case result != nil && result.Account != nil:
			if !cursorProxyIDsEqual(result.Account.ProxyID, account.ProxyID) {
				return "", errOAuthRefreshAccountStateChanged
			}
			account = result.Account
			profile = account.CursorAgentProfile()
			accessToken = strings.TrimSpace(account.GetCursorAccessToken())
			expiresAt = cursorAccessTokenExpiry(account, accessToken)
		}
	}

	if accessToken == "" {
		return "", errCursorAccessTokenMissing
	}
	// 走到这里还不是 session 就必须失败：拿 web token 打 Agent 只会拿到
	// ERROR_NOT_LOGGED_IN，那是个和真实故障无关的误导性错误。
	if !cursorTokenUsableForAccount(account, accessToken) {
		if profile == cursor.AgentProfileSand {
			return "", errCursorTokenInvalid
		}
		return "", errCursorTokenNotSession
	}

	if p.tokenCache != nil {
		// 与 kiro/grok 同一条守则：写缓存前先确认手里这份账号没有被别处刷新过。
		// 少了这一步，并发刷新时会把已经作废的旧 token 覆盖回缓存，
		// 表现为「后台明明刷新成功了，网关还在用旧 token」。
		latestAccount, isStale := CheckTokenVersion(ctx, account, p.accountRepo)
		if isStale && latestAccount != nil {
			latestToken := strings.TrimSpace(latestAccount.GetCursorAccessToken())
			if latestToken == "" {
				return "", errCursorAccessTokenMissing
			}
			if !cursorTokenUsableForAccount(latestAccount, latestToken) {
				if latestAccount.CursorAgentProfile() == cursor.AgentProfileSand {
					return "", errCursorTokenInvalid
				}
				return "", errCursorTokenNotSession
			}
			return latestToken, nil
		}

		ttl := cursorTokenCacheMaxTTL
		if expiresAt != nil {
			switch until := time.Until(*expiresAt); {
			case until > cursorTokenCacheSkew:
				ttl = until - cursorTokenCacheSkew
			case until > 0:
				ttl = until
			default:
				ttl = time.Minute
			}
		}
		// 上限兜底：Cursor 的 session JWT 有效期可以长达数天，照抄过期时间会让
		// 一份卡住的缓存在几天里都不自愈。压到一个刷新周期内，代价只是多几次缓存未命中。
		if ttl > cursorTokenCacheMaxTTL {
			ttl = cursorTokenCacheMaxTTL
		}
		_ = p.tokenCache.SetAccessToken(ctx, cacheKey, accessToken, ttl)
	}
	return accessToken, nil
}

func (p *CursorTokenProvider) InvalidateToken(ctx context.Context, account *Account) error {
	if p == nil || p.tokenCache == nil || account == nil {
		return nil
	}
	return p.tokenCache.DeleteAccessToken(ctx, CursorTokenCacheKey(account))
}

func (p *CursorTokenProvider) markTempUnschedulable(account *Account, refreshErr error) {
	if p.accountRepo == nil || account == nil {
		return
	}
	now := time.Now()
	until := now.Add(tokenRefreshTempUnschedDuration)
	reason := "token refresh failed on request path: " + refreshErr.Error()
	bgCtx := context.Background()
	if err := p.accountRepo.SetTempUnschedulable(bgCtx, account.ID, until, reason); err != nil {
		slog.Warn("cursor_token_provider.set_temp_unschedulable_failed", "account_id", account.ID, "error", err)
		return
	}
	if p.tempUnschedCache != nil {
		state := &TempUnschedState{
			UntilUnix:       until.Unix(),
			TriggeredAtUnix: now.Unix(),
			ErrorMessage:    reason,
		}
		if err := p.tempUnschedCache.SetTempUnsched(bgCtx, account.ID, state); err != nil {
			slog.Warn("cursor_token_provider.temp_unsched_cache_set_failed", "account_id", account.ID, "error", err)
		}
	}
}

// cursorAccessTokenExpiry 优先用凭证里记录的过期时间，缺失时退回 JWT 的 exp。
func cursorAccessTokenExpiry(account *Account, accessToken string) *time.Time {
	if expiresAt := account.GetCredentialAsTime("expires_at"); expiresAt != nil {
		return expiresAt
	}
	if jwtExpiry := cursor.TokenExpiry(accessToken); !jwtExpiry.IsZero() {
		return &jwtExpiry
	}
	return nil
}

func cursorProxyIDsEqual(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
