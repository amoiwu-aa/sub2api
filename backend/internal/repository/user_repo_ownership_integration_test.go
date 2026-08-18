//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUserRepositoryManagedOwnershipCreatorAndInviter(t *testing.T) {
	ctx := context.Background()
	tx, err := integrationEntClient.Tx(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	client := tx.Client()

	suffix := time.Now().UnixNano()
	admin, err := client.User.Create().
		SetEmail(fmt.Sprintf("affiliate-admin-%d@example.com", suffix)).
		SetPasswordHash("hash").
		SetRole(service.RoleAffiliateAdmin).
		SetStatus(service.StatusActive).
		SetConcurrency(1).
		Save(txCtx)
	require.NoError(t, err)

	repo := newUserRepositoryWithSQL(integrationEntClient, integrationDB)
	created := &service.User{
		Email:        fmt.Sprintf("managed-created-%d@example.com", suffix),
		PasswordHash: "hash",
		Role:         service.RoleUser,
		Status:       service.StatusActive,
		Concurrency:  1,
	}
	require.NoError(t, repo.CreateManagedUser(txCtx, created, admin.ID))

	createdEntity, err := client.User.Get(txCtx, created.ID)
	require.NoError(t, err)
	require.NotNil(t, createdEntity.CreatedByAdminID)
	require.Equal(t, admin.ID, *createdEntity.CreatedByAdminID)

	var createdInviterID int64
	rows, err := client.QueryContext(txCtx, "SELECT inviter_id FROM user_affiliates WHERE user_id = $1", created.ID)
	require.NoError(t, err)
	require.True(t, rows.Next())
	require.NoError(t, rows.Scan(&createdInviterID))
	require.NoError(t, rows.Close())
	require.Equal(t, admin.ID, createdInviterID)

	invited := &service.User{
		Email:        fmt.Sprintf("managed-invited-%d@example.com", suffix),
		PasswordHash: "hash",
		Role:         service.RoleUser,
		Status:       service.StatusActive,
		Concurrency:  1,
	}
	require.NoError(t, repo.Create(txCtx, invited))
	bound, err := (&affiliateRepository{client: integrationEntClient}).BindInviter(txCtx, invited.ID, admin.ID)
	require.NoError(t, err)
	require.True(t, bound)

	unrelated := &service.User{
		Email:        fmt.Sprintf("unrelated-%d@example.com", suffix),
		PasswordHash: "hash",
		Role:         service.RoleUser,
		Status:       service.StatusActive,
		Concurrency:  1,
	}
	require.NoError(t, repo.Create(txCtx, unrelated))

	ids, err := repo.listManagedUserIDs(txCtx, admin.ID, false)
	require.NoError(t, err)
	require.ElementsMatch(t, []int64{created.ID, invited.ID}, ids)

	ok, err := repo.UserIsManagedBy(txCtx, created.ID, admin.ID)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = repo.UserIsManagedBy(txCtx, invited.ID, admin.ID)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = repo.UserIsManagedBy(txCtx, unrelated.ID, admin.ID)
	require.NoError(t, err)
	require.False(t, ok)
}
