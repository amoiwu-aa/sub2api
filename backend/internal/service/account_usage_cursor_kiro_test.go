package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

type stubCursorUsageLogRepo struct {
	UsageLogRepository
	fiveHour *usagestats.AccountStats
	sevenDay *usagestats.AccountStats
}

func (s *stubCursorUsageLogRepo) GetAccountWindowStats(_ context.Context, _ int64, startTime time.Time) (*usagestats.AccountStats, error) {
	age := time.Since(startTime)
	if age >= 6*24*time.Hour {
		if s.sevenDay != nil {
			return s.sevenDay, nil
		}
		return &usagestats.AccountStats{}, nil
	}
	if s.fiveHour != nil {
		return s.fiveHour, nil
	}
	return &usagestats.AccountStats{}, nil
}

func TestGetCursorUsageBuildsLocalWindows(t *testing.T) {
	repo := &stubCursorUsageLogRepo{
		fiveHour: &usagestats.AccountStats{Requests: 2, Tokens: 100, Cost: 0.2},
		sevenDay: &usagestats.AccountStats{Requests: 9, Tokens: 900, Cost: 1.8},
	}
	svc := &AccountUsageService{usageLogRepo: repo}
	account := &Account{ID: 7, Platform: PlatformCursor, Type: AccountTypeOAuth}

	usage, err := svc.getCursorUsage(context.Background(), account)
	require.NoError(t, err)
	require.NotNil(t, usage)
	require.Equal(t, "local", usage.Source)
	require.NotNil(t, usage.FiveHour)
	require.NotNil(t, usage.FiveHour.WindowStats)
	require.EqualValues(t, 2, usage.FiveHour.WindowStats.Requests)
	require.NotNil(t, usage.SevenDay)
	require.NotNil(t, usage.SevenDay.WindowStats)
	require.EqualValues(t, 9, usage.SevenDay.WindowStats.Requests)
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
