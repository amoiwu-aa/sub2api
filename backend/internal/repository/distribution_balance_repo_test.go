package repository

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func newDistributionBalanceSQLRepo(t *testing.T) (*distributionBalanceRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return &distributionBalanceRepository{sql: db}, mock
}

func TestValidateDistributionTransferInsert(t *testing.T) {
	tests := []struct {
		name    string
		row     *service.DistributionBalanceTransfer
		wantErr string
	}{
		{name: "nil", row: nil, wantErr: "required"},
		{name: "admin", row: &service.DistributionBalanceTransfer{TargetUserID: 2, Amount: 1, IdempotencyKey: "k"}, wantErr: "admin id"},
		{name: "target", row: &service.DistributionBalanceTransfer{AffiliateAdminID: 1, Amount: 1, IdempotencyKey: "k"}, wantErr: "target user"},
		{name: "amount zero", row: &service.DistributionBalanceTransfer{AffiliateAdminID: 1, TargetUserID: 2, Amount: 0, IdempotencyKey: "k"}, wantErr: "amount"},
		{name: "amount negative", row: &service.DistributionBalanceTransfer{AffiliateAdminID: 1, TargetUserID: 2, Amount: -1, IdempotencyKey: "k"}, wantErr: "amount"},
		{name: "empty key", row: &service.DistributionBalanceTransfer{AffiliateAdminID: 1, TargetUserID: 2, Amount: 1}, wantErr: "idempotency"},
		{name: "key too long", row: &service.DistributionBalanceTransfer{AffiliateAdminID: 1, TargetUserID: 2, Amount: 1, IdempotencyKey: strings.Repeat("a", 65)}, wantErr: "exceeds"},
		{name: "ok", row: &service.DistributionBalanceTransfer{AffiliateAdminID: 1, TargetUserID: 2, Amount: 1.5, IdempotencyKey: "  abc  "}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDistributionTransferInsert(tt.row)
			if tt.wantErr == "" {
				require.NoError(t, err)
				require.Equal(t, "abc", tt.row.IdempotencyKey)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestDistributionBalanceGetTransferByIdempotency(t *testing.T) {
	repo, mock := newDistributionBalanceSQLRepo(t)
	created := time.Date(2026, 8, 19, 3, 0, 0, 0, time.UTC)
	mock.ExpectQuery(regexp.QuoteMeta(getTransferByIdempotencySQL)).
		WithArgs(int64(7), "k-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "affiliate_admin_id", "target_user_id", "amount",
			"source_balance_before", "source_balance_after",
			"target_balance_before", "target_balance_after",
			"idempotency_key", "notes", "created_at",
		}).AddRow(int64(11), int64(7), int64(22), 3.5, 10.0, 6.5, 1.0, 4.5, "k-1", "note", created))

	got, err := repo.GetTransferByIdempotency(context.Background(), 7, " k-1 ")

	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, int64(11), got.ID)
	require.Equal(t, int64(22), got.TargetUserID)
	require.Equal(t, 3.5, got.Amount)
	require.Equal(t, created, got.CreatedAt)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDistributionBalanceGetTransferByIdempotencyNotFound(t *testing.T) {
	repo, mock := newDistributionBalanceSQLRepo(t)
	mock.ExpectQuery(regexp.QuoteMeta(getTransferByIdempotencySQL)).
		WithArgs(int64(7), "missing").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "affiliate_admin_id", "target_user_id", "amount",
			"source_balance_before", "source_balance_after",
			"target_balance_before", "target_balance_after",
			"idempotency_key", "notes", "created_at",
		}))

	got, err := repo.GetTransferByIdempotency(context.Background(), 7, "missing")

	require.NoError(t, err)
	require.Nil(t, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDistributionBalanceInsertTransfer(t *testing.T) {
	repo, mock := newDistributionBalanceSQLRepo(t)
	created := time.Date(2026, 8, 19, 3, 0, 0, 0, time.UTC)
	row := &service.DistributionBalanceTransfer{
		AffiliateAdminID:    7,
		TargetUserID:        22,
		Amount:              3.5,
		SourceBalanceBefore: 10,
		SourceBalanceAfter:  6.5,
		TargetBalanceBefore: 1,
		TargetBalanceAfter:  4.5,
		IdempotencyKey:      "k-1",
		Notes:               "note",
	}
	mock.ExpectQuery(regexp.QuoteMeta(insertTransferSQL)).
		WithArgs(int64(7), int64(22), 3.5, 10.0, 6.5, 1.0, 4.5, "k-1", "note").
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(int64(11), created))

	require.NoError(t, repo.InsertTransfer(context.Background(), row))
	require.Equal(t, int64(11), row.ID)
	require.Equal(t, created, row.CreatedAt)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDistributionBalanceInsertTransferUniqueViolation(t *testing.T) {
	repo, mock := newDistributionBalanceSQLRepo(t)
	row := &service.DistributionBalanceTransfer{
		AffiliateAdminID: 7,
		TargetUserID:     22,
		Amount:           1,
		IdempotencyKey:   "k-1",
	}
	mock.ExpectQuery(regexp.QuoteMeta(insertTransferSQL)).
		WithArgs(int64(7), int64(22), 1.0, 0.0, 0.0, 0.0, 0.0, "k-1", "").
		WillReturnError(&pq.Error{Code: "23505"})

	err := repo.InsertTransfer(context.Background(), row)

	require.Error(t, err)
	require.True(t, IsDistributionTransferConflict(err))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDistributionBalanceListTransfers(t *testing.T) {
	repo, mock := newDistributionBalanceSQLRepo(t)
	created := time.Date(2026, 8, 19, 3, 0, 0, 0, time.UTC)
	mock.ExpectQuery(regexp.QuoteMeta(countTransfersSQL)).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(1)))
	mock.ExpectQuery(regexp.QuoteMeta(listTransfersSQL)).
		WithArgs(int64(7), 20, 0).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "affiliate_admin_id", "target_user_id", "amount",
			"source_balance_before", "source_balance_after",
			"target_balance_before", "target_balance_after",
			"idempotency_key", "notes", "created_at",
		}).AddRow(int64(11), int64(7), int64(22), 3.5, 10.0, 6.5, 1.0, 4.5, "k-1", "", created))

	rows, total, err := repo.ListTransfers(context.Background(), 7, 0, 0)

	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, rows, 1)
	require.Equal(t, int64(11), rows[0].ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDistributionBalanceSumSuccessfulAllocated(t *testing.T) {
	repo, mock := newDistributionBalanceSQLRepo(t)
	mock.ExpectQuery(regexp.QuoteMeta(sumSuccessfulAllocatedSQL)).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"sum"}).AddRow(12.5))

	sum, err := repo.SumSuccessfulAllocated(context.Background(), 7)

	require.NoError(t, err)
	require.Equal(t, 12.5, sum)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestNewDistributionBalanceRepository(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NotNil(t, NewDistributionBalanceRepository(nil, db))
}

func TestDistributionBalanceLockUsersForUpdateSortedIDs(t *testing.T) {
	repo, mock := newDistributionBalanceSQLRepo(t)
	mock.ExpectQuery(regexp.QuoteMeta(lockUsersForUpdateSQL)).
		WithArgs(int64(3), int64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "role", "status", "balance", "frozen_balance"}).
			AddRow(int64(3), service.RoleUser, service.StatusActive, 1.5, 0.0).
			AddRow(int64(9), service.RoleAffiliateAdmin, service.StatusActive, 10.0, 2.0))

	got, err := repo.LockUsersForUpdate(context.Background(), 9, 3)

	require.NoError(t, err)
	require.Equal(t, 1.5, got[3].Balance)
	require.Equal(t, service.RoleAffiliateAdmin, got[9].Role)
	require.Equal(t, 2.0, got[9].FrozenBalance)
	require.NoError(t, mock.ExpectationsWereMet())
}
