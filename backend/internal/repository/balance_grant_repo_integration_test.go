//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestExpiringBalance_UsageBillingConsumesEarliestExpiryFirst(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewUsageBillingRepository(client, integrationDB)

	user := mustCreateUser(t, client, &service.User{
		Email:   fmt.Sprintf("expiring-billing-%d@example.com", time.Now().UnixNano()),
		Balance: 10,
	})
	apiKey := mustCreateApiKey(t, client, &service.APIKey{
		UserID: user.ID,
		Key:    "sk-expiring-billing-" + uuid.NewString(),
		Name:   "expiring-billing",
	})

	_, err := integrationDB.ExecContext(ctx, `
		INSERT INTO user_balance_grants (
			user_id, original_amount, remaining_amount, expires_at
		)
		VALUES
			($1, 2, 2, NOW() + INTERVAL '1 hour'),
			($1, 3, 3, NOW() + INTERVAL '2 hours')
	`, user.ID)
	require.NoError(t, err)

	result, err := repo.Apply(ctx, &service.UsageBillingCommand{
		RequestID:   uuid.NewString(),
		APIKeyID:    apiKey.ID,
		UserID:      user.ID,
		BalanceCost: 2.5,
	})
	require.NoError(t, err)
	require.True(t, result.Applied)
	require.NotNil(t, result.NewBalance)
	require.InDelta(t, 7.5, *result.NewBalance, 0.000001)

	rows, err := integrationDB.QueryContext(ctx, `
		SELECT remaining_amount, exhausted_at IS NOT NULL
		FROM user_balance_grants
		WHERE user_id = $1
		ORDER BY expires_at ASC
	`, user.ID)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	var remaining float64
	var exhausted bool
	require.True(t, rows.Next())
	require.NoError(t, rows.Scan(&remaining, &exhausted))
	require.InDelta(t, 0, remaining, 0.000001)
	require.True(t, exhausted)

	require.True(t, rows.Next())
	require.NoError(t, rows.Scan(&remaining, &exhausted))
	require.InDelta(t, 2.5, remaining, 0.000001)
	require.False(t, exhausted)
	require.False(t, rows.Next())
	require.NoError(t, rows.Err())
}

func TestExpiringBalance_SettlementRemovesOnlyUnspentAmount(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newUserRepositoryWithSQL(client, integrationDB)

	user := mustCreateUser(t, client, &service.User{
		Email:   fmt.Sprintf("expired-balance-%d@example.com", time.Now().UnixNano()),
		Balance: 10,
	})
	_, err := integrationDB.ExecContext(ctx, `
		INSERT INTO user_balance_grants (
			user_id, original_amount, remaining_amount, expires_at
		)
		VALUES ($1, 4, 1.5, NOW() - INTERVAL '1 minute')
	`, user.ID)
	require.NoError(t, err)

	state, err := repo.SettleExpiredBalanceGrants(ctx, user.ID)
	require.NoError(t, err)
	require.InDelta(t, 1.5, state.ExpiredAmount, 0.000001)
	require.InDelta(t, 8.5, state.Balance, 0.000001)
	require.Nil(t, state.NextExpiresAt)

	var remaining float64
	var expired bool
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT remaining_amount, expired_at IS NOT NULL
		FROM user_balance_grants
		WHERE user_id = $1
	`, user.ID).Scan(&remaining, &expired))
	require.InDelta(t, 0, remaining, 0.000001)
	require.True(t, expired)
}
