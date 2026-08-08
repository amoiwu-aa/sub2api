package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetCursorUsageReportsIncompleteCredentials(t *testing.T) {
	svc := &AccountUsageService{cursorQuotaFetcher: NewCursorQuotaFetcher(nil), cache: NewUsageCache()}
	account := &Account{
		ID:       7,
		Platform: PlatformCursor,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "tok",
		},
	}

	usage, err := svc.getCursorUsage(context.Background(), account, false)
	require.NoError(t, err)
	require.NotNil(t, usage)
	require.Contains(t, usage.Error, "user_id")
	require.Equal(t, "incomplete_credentials", usage.ErrorCode)
}

// Cursor 按计费周期结算，没有 5h / 7d 滚动窗口。早先的实现拿本地 usage_logs
// 拼了这两条窗口，把 Claude 的模型硬套了上来；这里守住不再回退到那个形态。
func TestGetCursorUsageDoesNotBuildRollingWindows(t *testing.T) {
	svc := &AccountUsageService{cursorQuotaFetcher: NewCursorQuotaFetcher(nil), cache: NewUsageCache()}
	account := &Account{
		ID:          9,
		Platform:    PlatformCursor,
		Type:        AccountTypeOAuth,
		Credentials: map[string]any{},
	}

	usage, err := svc.getCursorUsage(context.Background(), account, false)
	require.NoError(t, err)
	require.NotNil(t, usage)
	require.Nil(t, usage.FiveHour)
	require.Nil(t, usage.SevenDay)
}

func TestGetCursorUsageWithoutFetcher(t *testing.T) {
	svc := &AccountUsageService{cache: NewUsageCache()}
	account := &Account{ID: 10, Platform: PlatformCursor, Type: AccountTypeOAuth}

	usage, err := svc.getCursorUsage(context.Background(), account, false)
	require.NoError(t, err)
	require.NotNil(t, usage)
	require.Equal(t, "unavailable", usage.ErrorCode)
}

func TestGetKiroUsageReportsIncompleteCredentials(t *testing.T) {
	svc := &AccountUsageService{kiroQuotaFetcher: NewKiroQuotaFetcher(nil)}
	account := &Account{
		ID:       8,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "tok",
		},
	}

	usage, err := svc.getKiroUsage(context.Background(), account)
	require.NoError(t, err)
	require.NotNil(t, usage)
	require.Contains(t, usage.Error, "profile_arn")
	require.Equal(t, "incomplete_credentials", usage.ErrorCode)
}

func TestKiroProfileARNAcceptsCamelCase(t *testing.T) {
	account := &Account{
		Platform: PlatformKiro,
		Credentials: map[string]any{
			"access_token": "tok",
			"profileArn":   "arn:aws:codewhisperer:us-east-1:1:profile/ABC",
		},
	}
	require.Equal(t, "arn:aws:codewhisperer:us-east-1:1:profile/ABC", kiroProfileARN(account))
	require.True(t, NewKiroQuotaFetcher(nil).CanFetch(account))
}
