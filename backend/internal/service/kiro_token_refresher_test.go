package service

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func kiroAccount(credentials map[string]any) *Account {
	return &Account{
		ID:          77,
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Credentials: credentials,
	}
}

// 漏注册刷新器不会报错，只会让 kiro 账号在后台永不续期。这个断言是那处静默故障的守门人。
func TestKiroRefresherIsRegisteredInTokenRefreshRegistry(t *testing.T) {
	svc := NewTokenRefreshService(nil, nil, nil, nil, nil, nil, nil, &config.Config{}, nil)

	require.Contains(t, svc.eligiblePlatforms(), PlatformKiro)
	for _, registration := range svc.registrations {
		if registration.platform != PlatformKiro {
			continue
		}
		require.NotNil(t, registration.refresher)
		require.NotNil(t, registration.executor)
		return
	}
	t.Fatal("kiro is not present in the token refresh registry")
}

func TestKiroTokenRefresherCanRefresh(t *testing.T) {
	refresher := NewKiroTokenRefresher(nil)

	cases := []struct {
		name    string
		account *Account
		expect  bool
	}{
		{name: "nil account", account: nil, expect: false},
		{
			name:    "social with refresh token",
			account: kiroAccount(map[string]any{"refresh_token": "r", "auth_method": "social"}),
			expect:  true,
		},
		{
			name:    "no refresh token",
			account: kiroAccount(map[string]any{"auth_method": "social"}),
			expect:  false,
		},
		{
			// IdC 缺 client 注册时刷新必然 400。判为不可刷新，避免后台每轮都打一次必败请求。
			name:    "idc without client registration",
			account: kiroAccount(map[string]any{"refresh_token": "r", "auth_method": "idc"}),
			expect:  false,
		},
		{
			name: "idc with client registration",
			account: kiroAccount(map[string]any{
				"refresh_token": "r", "auth_method": "idc",
				"client_id": "cid", "client_secret": "secret",
			}),
			expect: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.expect, refresher.CanRefresh(tc.account))
		})
	}

	apiKeyAccount := kiroAccount(map[string]any{"refresh_token": "r", "auth_method": "social"})
	apiKeyAccount.Type = AccountTypeAPIKey
	require.False(t, refresher.CanRefresh(apiKeyAccount))

	otherPlatform := kiroAccount(map[string]any{"refresh_token": "r", "auth_method": "social"})
	otherPlatform.Platform = PlatformGrok
	require.False(t, refresher.CanRefresh(otherPlatform))
}

func TestKiroTokenRefresherNeedsRefresh(t *testing.T) {
	refresher := NewKiroTokenRefresher(nil)
	rfc3339 := func(d time.Duration) string { return time.Now().Add(d).UTC().Format(time.RFC3339) }

	cases := []struct {
		name    string
		account *Account
		window  time.Duration
		expect  bool
	}{
		{
			name:    "no refresh token never refreshes",
			account: kiroAccount(map[string]any{"access_token": "a", "expires_at": rfc3339(-time.Hour)}),
			expect:  false,
		},
		{
			name:    "missing access token",
			account: kiroAccount(map[string]any{"refresh_token": "r"}),
			expect:  true,
		},
		{
			name:    "missing expiry",
			account: kiroAccount(map[string]any{"refresh_token": "r", "access_token": "a"}),
			expect:  true,
		},
		{
			name:    "expired",
			account: kiroAccount(map[string]any{"refresh_token": "r", "access_token": "a", "expires_at": rfc3339(-time.Minute)}),
			expect:  true,
		},
		{
			// 窗口小于 skew 时按 skew 算，所以 5 分钟后过期仍要刷新。
			name:    "inside skew floor",
			account: kiroAccount(map[string]any{"refresh_token": "r", "access_token": "a", "expires_at": rfc3339(5 * time.Minute)}),
			window:  time.Minute,
			expect:  true,
		},
		{
			name:    "fresh",
			account: kiroAccount(map[string]any{"refresh_token": "r", "access_token": "a", "expires_at": rfc3339(2 * time.Hour)}),
			window:  time.Minute,
			expect:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.expect, refresher.NeedsRefresh(tc.account, tc.window))
		})
	}
}

func TestKiroTokenRefresherCacheKeyMatchesInvalidator(t *testing.T) {
	account := kiroAccount(nil)
	require.Equal(t, KiroTokenCacheKey(account), NewKiroTokenRefresher(nil).CacheKey(account))
	require.Equal(t, "kiro:account:77", KiroTokenCacheKey(account))
}

func TestKiroOAuthServiceImportRejectsIdCWithoutClientRegistration(t *testing.T) {
	svc := NewKiroOAuthService(nil)
	_, err := svc.Import(t.Context(), KiroImportInput{TokenJSON: `{
		"accessToken": "a",
		"refreshToken": "r",
		"expiresAt": "2999-01-01T00:00:00.000Z",
		"authMethod": "IdC",
		"profileArn": "arn:aws:codewhisperer:us-east-1:1:profile/X"
	}`})
	require.ErrorContains(t, err, "KIRO_CLIENT_REGISTRATION_REQUIRED")
}

func TestKiroOAuthServiceImportSkipsRefreshForFreshToken(t *testing.T) {
	svc := NewKiroOAuthService(nil)
	info, err := svc.Import(t.Context(), KiroImportInput{TokenJSON: `{
		"accessToken": "fresh-token",
		"refreshToken": "r",
		"expiresAt": "2999-01-01T00:00:00.000Z",
		"authMethod": "Social",
		"profileArn": "arn:aws:codewhisperer:eu-west-1:1:profile/X"
	}`})
	require.NoError(t, err)

	// 未过期的 token 不该触发一次多余的上游刷新。
	require.False(t, info.Refreshed)
	require.Equal(t, "fresh-token", info.AccessToken)
	require.Equal(t, "eu-west-1", info.QRegion)

	credentials := svc.BuildAccountCredentials(info)
	require.Equal(t, "fresh-token", credentials["access_token"])
	require.Equal(t, "social", credentials["auth_method"])
	require.NotContains(t, credentials, "client_secret")
}
