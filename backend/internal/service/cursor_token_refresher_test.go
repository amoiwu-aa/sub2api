package service

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// cursorJWT 造一个只有 payload 有意义的 JWT；本包不验签。
func cursorJWT(t *testing.T, tokenType string, expiresIn time.Duration) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"type": tokenType,
		"sub":  "auth0|user_01TEST",
		"exp":  time.Now().Add(expiresIn).Unix(),
	})
	require.NoError(t, err)
	encode := base64.RawURLEncoding.EncodeToString
	return encode([]byte(`{"alg":"none"}`)) + "." + encode(payload) + ".sig"
}

func cursorAccount(credentials map[string]any) *Account {
	return &Account{
		ID:          88,
		Platform:    PlatformCursor,
		Type:        AccountTypeOAuth,
		Credentials: credentials,
	}
}

// 漏注册刷新器不会报错，只会让 cursor 账号在后台永不续期。
func TestCursorRefresherIsRegisteredInTokenRefreshRegistry(t *testing.T) {
	svc := NewTokenRefreshService(nil, nil, nil, nil, nil, nil, nil, &config.Config{}, nil)

	require.Contains(t, svc.eligiblePlatforms(), PlatformCursor)
	for _, registration := range svc.registrations {
		if registration.platform != PlatformCursor {
			continue
		}
		require.NotNil(t, registration.refresher)
		require.NotNil(t, registration.executor)
		return
	}
	t.Fatal("cursor is not present in the token refresh registry")
}

func TestCursorTokenRefresherCanRefresh(t *testing.T) {
	refresher := NewCursorTokenRefresher(nil)

	require.False(t, refresher.CanRefresh(nil))
	require.False(t, refresher.CanRefresh(cursorAccount(map[string]any{})))
	require.True(t, refresher.CanRefresh(cursorAccount(map[string]any{"refresh_token": "r"})))
	// 刷新链的最后一步是拿 access 去 exchange，所以只有 access 也算可刷新。
	require.True(t, refresher.CanRefresh(cursorAccount(map[string]any{"access_token": "a"})))

	wrongPlatform := cursorAccount(map[string]any{"refresh_token": "r"})
	wrongPlatform.Platform = PlatformGrok
	require.False(t, refresher.CanRefresh(wrongPlatform))
}

func TestCursorTokenRefresherAlwaysRefreshesWebTokens(t *testing.T) {
	refresher := NewCursorTokenRefresher(nil)

	// web 态令牌调不动 Agent，即使远未过期也必须先换成 session。
	freshWeb := cursorAccount(map[string]any{
		"access_token": cursorJWT(t, "web", 30*24*time.Hour),
		"expires_at":   time.Now().Add(30 * 24 * time.Hour).Format(time.RFC3339),
	})
	require.True(t, refresher.NeedsRefresh(freshWeb, time.Minute))

	freshSession := cursorAccount(map[string]any{
		"access_token": cursorJWT(t, "session", 30*24*time.Hour),
		"expires_at":   time.Now().Add(30 * 24 * time.Hour).Format(time.RFC3339),
	})
	require.False(t, refresher.NeedsRefresh(freshSession, time.Minute))
}

func TestCursorTokenRefresherFallsBackToJWTExpiry(t *testing.T) {
	refresher := NewCursorTokenRefresher(nil)

	// 凭证里没记 expires_at 时应该读 JWT 自己的 exp，而不是无脑判定要刷新。
	longLived := cursorAccount(map[string]any{"access_token": cursorJWT(t, "session", 30*24*time.Hour)})
	require.False(t, refresher.NeedsRefresh(longLived, time.Minute))

	expiring := cursorAccount(map[string]any{"access_token": cursorJWT(t, "session", time.Minute)})
	require.True(t, refresher.NeedsRefresh(expiring, time.Minute))
}

func TestCursorTokenRefresherCacheKeyMatchesInvalidator(t *testing.T) {
	account := cursorAccount(nil)
	require.Equal(t, CursorTokenCacheKey(account), NewCursorTokenRefresher(nil).CacheKey(account))
	require.Equal(t, "cursor:account:88", CursorTokenCacheKey(account))
}

func TestCursorTokenProviderRejectsWebTokens(t *testing.T) {
	provider := NewCursorTokenProvider(nil, nil)
	account := cursorAccount(map[string]any{
		"access_token": cursorJWT(t, "web", time.Hour),
		"expires_at":   time.Now().Add(time.Hour).Format(time.RFC3339),
	})

	// 拿 web token 打 Agent 只会得到 ERROR_NOT_LOGGED_IN，那是个误导性错误。
	_, err := provider.GetAccessToken(t.Context(), account)
	require.ErrorIs(t, err, errCursorTokenNotSession)
}

func TestCursorTokenProviderRejectsConfiguredProxyMiss(t *testing.T) {
	provider := NewCursorTokenProvider(nil, nil)
	proxyID := int64(3)
	account := cursorAccount(map[string]any{"access_token": cursorJWT(t, "session", time.Hour)})
	account.ProxyID = &proxyID

	_, err := provider.GetAccessToken(t.Context(), account)
	require.ErrorIs(t, err, errCursorConfiguredProxyMiss)
}

func TestCursorTokenProviderReturnsSessionToken(t *testing.T) {
	provider := NewCursorTokenProvider(nil, nil)
	token := cursorJWT(t, "session", 24*time.Hour)
	account := cursorAccount(map[string]any{
		"access_token": token,
		"expires_at":   time.Now().Add(24 * time.Hour).Format(time.RFC3339),
	})

	got, err := provider.GetAccessToken(t.Context(), account)
	require.NoError(t, err)
	require.Equal(t, token, got)
}

func TestCursorOAuthServiceBuildAccountCredentials(t *testing.T) {
	svc := NewCursorOAuthService(nil)
	defer svc.Stop()

	credentials := svc.BuildAccountCredentials(&CursorTokenInfo{
		AccessToken: "a", RefreshToken: "r", UserID: "user_01",
		ExpiresAt: time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC),
	})
	require.Equal(t, "a", credentials["access_token"])
	require.Equal(t, "r", credentials["refresh_token"])
	require.Equal(t, "user_01", credentials["user_id"])
	require.Equal(t, "2026-07-26T10:00:00Z", credentials["expires_at"])
	// 空值不落库：MergeCredentials 会用它覆盖掉已有的字段。
	require.NotContains(t, credentials, "email")
}

func TestCursorOAuthServicePollRejectsUnknownSession(t *testing.T) {
	svc := NewCursorOAuthService(nil)
	defer svc.Stop()

	_, err := svc.PollLogin(t.Context(), "does-not-exist")
	require.ErrorContains(t, err, "CURSOR_SESSION_NOT_FOUND")
}

func TestCursorOAuthServiceStartLoginKeepsVerifierServerSide(t *testing.T) {
	svc := NewCursorOAuthService(nil)
	defer svc.Stop()

	result, err := svc.StartLogin(t.Context(), nil)
	require.NoError(t, err)
	require.Contains(t, result.LoginURL, "https://cursor.com/loginDeepControl")
	require.NotEmpty(t, result.SessionID)

	// verifier 是 PKCE 的私密部分，不能出现在返回给浏览器的任何字段里。
	require.NotContains(t, result.LoginURL, "verifier")
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "verifier")
}
