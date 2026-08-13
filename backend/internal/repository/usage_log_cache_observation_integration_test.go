//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestUsageLogCacheObservationRoundTripAndSessionAggregation(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	repo := newUsageLogRepositoryWithSQL(client, tx)

	user := mustCreateUser(t, client, &service.User{Email: "cache-observation-" + uuid.NewString() + "@example.com"})
	apiKey := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "sk-cache-" + uuid.NewString(), Name: "cache"})
	account := mustCreateAccount(t, client, &service.Account{Name: "cache-observation-" + uuid.NewString()})
	sessionID := "session-" + uuid.NewString()
	source := service.CacheUsageSourceReported
	now := time.Now().UTC()

	log := &service.UsageLog{
		UserID:                user.ID,
		APIKeyID:              apiKey.ID,
		AccountID:             account.ID,
		RequestID:             uuid.NewString(),
		Model:                 "claude-sonnet",
		CacheReadTokens:       100,
		CacheUsageSource:      &source,
		ForcedCacheReadTokens: 40,
		SessionID:             &sessionID,
		CreatedAt:             now,
	}
	inserted, err := repo.Create(ctx, log)
	require.NoError(t, err)
	require.True(t, inserted)

	estimatedSource := service.CacheUsageSourceEstimated
	estimatedLog := &service.UsageLog{
		UserID:           user.ID,
		APIKeyID:         apiKey.ID,
		AccountID:        account.ID,
		RequestID:        uuid.NewString(),
		Model:            "cursor-auto",
		CacheReadTokens:  75,
		CacheUsageSource: &estimatedSource,
		SessionID:        &sessionID,
		CreatedAt:        now,
	}
	inserted, err = repo.Create(ctx, estimatedLog)
	require.NoError(t, err)
	require.True(t, inserted)

	got, err := repo.GetByID(ctx, log.ID)
	require.NoError(t, err)
	require.NotNil(t, got.CacheUsageSource)
	require.Equal(t, source, *got.CacheUsageSource)
	require.Equal(t, 40, got.ForcedCacheReadTokens)
	require.Equal(t, 60, got.ProviderCacheReadTokens())

	stats, err := repo.GetStatsWithFilters(ctx, usagestats.UsageLogFilters{
		UserID:    user.ID,
		APIKeyID:  apiKey.ID,
		SessionID: sessionID,
	})
	require.NoError(t, err)
	require.Equal(t, int64(2), stats.TotalRequests)
	require.Equal(t, int64(175), stats.TotalCacheReadTokens)
	require.Equal(t, int64(60), stats.TotalProviderCacheReadTokens)
	require.Equal(t, int64(1), stats.CacheHitRequests)
	require.Equal(t, int64(40), stats.TotalForcedCacheReadTokens)
	require.Equal(t, int64(1), stats.ReportedRequests)
	require.Equal(t, int64(1), stats.EstimatedRequests)
	require.Zero(t, stats.UnavailableRequests)
}
