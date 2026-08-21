package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
)

// cursorTokenRefreshSkew 是刷新窗口的下限。
// Cursor 的 session JWT 有效期以天计，30 分钟足够覆盖一轮巡检间隔。
const cursorTokenRefreshSkew = 30 * time.Minute

type CursorTokenRefresher struct {
	cursorOAuthService *CursorOAuthService
}

func NewCursorTokenRefresher(cursorOAuthService *CursorOAuthService) *CursorTokenRefresher {
	return &CursorTokenRefresher{cursorOAuthService: cursorOAuthService}
}

func (r *CursorTokenRefresher) CacheKey(account *Account) string {
	return CursorTokenCacheKey(account)
}

func (r *CursorTokenRefresher) CanRefresh(account *Account) bool {
	if account == nil || account.Platform != PlatformCursor || account.Type != AccountTypeOAuth {
		return false
	}
	// access 或 refresh 任一存在即可：刷新链的最后一步是拿 access 去 exchange。
	return strings.TrimSpace(account.GetCursorRefreshToken()) != "" ||
		strings.TrimSpace(account.GetCursorAccessToken()) != ""
}

func (r *CursorTokenRefresher) NeedsRefresh(account *Account, refreshWindow time.Duration) bool {
	if account == nil {
		return false
	}
	accessToken := strings.TrimSpace(account.GetCursorAccessToken())
	if accessToken == "" {
		return strings.TrimSpace(account.GetCursorRefreshToken()) != ""
	}
	// web 态的令牌调不动 Agent，无论过期与否都要先换成 session。
	if cursor.IsWebToken(accessToken) {
		return true
	}
	if account.CursorAgentProfile() == cursor.AgentProfileSand &&
		cursorTokenUsableForAccount(account, accessToken) &&
		cursor.TokenExpiry(accessToken).IsZero() &&
		account.GetCredentialAsTime("expires_at") == nil {
		return false
	}

	expiresAt := account.GetCredentialAsTime("expires_at")
	if expiresAt == nil {
		// 凭证里没记过期时间时退回读 JWT 自身的 exp。
		if jwtExpiry := cursor.TokenExpiry(accessToken); !jwtExpiry.IsZero() {
			expiresAt = &jwtExpiry
		}
	}
	if expiresAt == nil {
		return true
	}
	if refreshWindow < cursorTokenRefreshSkew {
		refreshWindow = cursorTokenRefreshSkew
	}
	return time.Until(*expiresAt) < refreshWindow
}

func (r *CursorTokenRefresher) Refresh(ctx context.Context, account *Account) (map[string]any, error) {
	if r == nil || r.cursorOAuthService == nil {
		return nil, errors.New("cursor oauth service is not configured")
	}
	tokenInfo, err := r.cursorOAuthService.RefreshAccountToken(ctx, account)
	if err != nil {
		return nil, err
	}
	newCredentials := r.cursorOAuthService.BuildAccountCredentials(tokenInfo)
	return MergeCredentials(account.Credentials, newCredentials), nil
}
