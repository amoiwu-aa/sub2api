package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

const (
	redeemAdjustmentLockBalanceSQL       = `(?s)SELECT balance\s+FROM users\s+WHERE id = \$1 AND deleted_at IS NULL\s+FOR UPDATE`
	redeemAdjustmentSettleExpiredSQL     = `(?s)WITH expired_candidates AS MATERIALIZED .*SELECT COALESCE\(SUM\(remaining_amount\), 0\)\s+FROM marked`
	redeemAdjustmentNextExpirySQL        = `(?s)SELECT MIN\(expires_at\)\s+FROM user_balance_grants\s+WHERE user_id = \$1.*expires_at > NOW\(\)`
	redeemAdjustmentConsumeGrantsSQL     = `(?s)SELECT id, remaining_amount\s+FROM user_balance_grants\s+WHERE user_id = \$1.*ORDER BY expires_at ASC, id ASC\s+FOR UPDATE`
	redeemAdjustmentUpdateUserBalanceSQL = `(?s)UPDATE users\s+SET balance = \$1,\s+total_recharged = total_recharged \+ \$2,\s+updated_at = NOW\(\)\s+WHERE id = \$3 AND deleted_at IS NULL`
)

func newRedeemAdjustmentRepoMock(t *testing.T) (*userRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	driver := entsql.OpenDB(dialect.Postgres, db)
	client := dbent.NewClient(dbent.Driver(driver))
	t.Cleanup(func() { _ = client.Close() })
	return newUserRepositoryWithSQL(client, db), mock
}

func TestApplyRedeemBalanceAdjustment_UsesAtomicFloor(t *testing.T) {
	repo, mock := newRedeemAdjustmentRepoMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(redeemAdjustmentLockBalanceSQL).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(5.0))
	mock.ExpectQuery(redeemAdjustmentSettleExpiredSQL).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"expired"}).AddRow(0))
	mock.ExpectQuery(redeemAdjustmentNextExpirySQL).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"next_expiry"}).AddRow(nil))
	mock.ExpectQuery(redeemAdjustmentConsumeGrantsSQL).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "remaining_amount"}))
	mock.ExpectExec(redeemAdjustmentUpdateUserBalanceSQL).
		WithArgs(0.0, 0.0, int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.ApplyRedeemBalanceAdjustment(context.Background(), 42, -7))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyRedeemConcurrencyAdjustment_UsesAtomicFloor(t *testing.T) {
	repo, mock := newRedeemAdjustmentRepoMock(t)
	mock.ExpectExec(`UPDATE users SET concurrency = GREATEST\(concurrency \+ \$1, 0\), updated_at = NOW\(\) WHERE id = \$2 AND deleted_at IS NULL`).
		WithArgs(-7, int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, repo.ApplyRedeemConcurrencyAdjustment(context.Background(), 42, -7))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyRedeemAdjustment_MissingUser(t *testing.T) {
	repo, mock := newRedeemAdjustmentRepoMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(redeemAdjustmentLockBalanceSQL).
		WithArgs(int64(404)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}))
	mock.ExpectRollback()

	err := repo.ApplyRedeemBalanceAdjustment(context.Background(), 404, -1)
	require.ErrorIs(t, err, service.ErrUserNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}
