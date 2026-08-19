package repository

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func newDistributionInviteSQLRepo(t *testing.T) (*distributionInviteRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return &distributionInviteRepository{sql: db}, mock
}

func TestUniquePositiveIDs(t *testing.T) {
	tests := []struct {
		name string
		in   []int64
		want []int64
	}{
		{name: "nil", in: nil, want: nil},
		{name: "empty", in: []int64{}, want: nil},
		{name: "drops non-positive", in: []int64{0, -1, 3, 0}, want: []int64{3}},
		{name: "dedupes keeping first order", in: []int64{5, 3, 5, 3, 7}, want: []int64{5, 3, 7}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, uniquePositiveIDs(tt.in))
		})
	}
}

func TestDistributionInviteGetOrCreateSettings(t *testing.T) {
	repo, mock := newDistributionInviteSQLRepo(t)
	mock.ExpectQuery(regexp.QuoteMeta(getOrCreateInviteSettingsSQL)).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"enabled"}).AddRow(true))

	enabled, err := repo.GetOrCreateSettings(context.Background(), 7)

	require.NoError(t, err)
	require.True(t, enabled)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDistributionInviteGetOrCreateSettingsRejectsInvalidAdmin(t *testing.T) {
	repo, mock := newDistributionInviteSQLRepo(t)

	_, err := repo.GetOrCreateSettings(context.Background(), 0)

	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDistributionInviteUpdateEnabled(t *testing.T) {
	repo, mock := newDistributionInviteSQLRepo(t)
	mock.ExpectExec(regexp.QuoteMeta(updateInviteEnabledSQL)).
		WithArgs(int64(7), false).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, repo.UpdateEnabled(context.Background(), 7, false))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDistributionInviteListDefaultGroupIDs(t *testing.T) {
	repo, mock := newDistributionInviteSQLRepo(t)
	mock.ExpectQuery(regexp.QuoteMeta(listInviteGroupIDsSQL)).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"group_id"}).AddRow(int64(3)).AddRow(int64(5)))

	ids, err := repo.ListDefaultGroupIDs(context.Background(), 7)

	require.NoError(t, err)
	require.Equal(t, []int64{3, 5}, ids)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDistributionInviteReplaceDefaultGroupIDs(t *testing.T) {
	repo, mock := newDistributionInviteSQLRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(deleteInviteGroupsByAdminSQL)).
		WithArgs(int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(insertInviteGroupsSQL)).
		WithArgs(int64(7), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	err := repo.ReplaceDefaultGroupIDs(context.Background(), 7, []int64{3, 0, 3, 5})

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDistributionInviteReplaceDefaultGroupIDsClearsWhenEmpty(t *testing.T) {
	repo, mock := newDistributionInviteSQLRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(deleteInviteGroupsByAdminSQL)).
		WithArgs(int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	err := repo.ReplaceDefaultGroupIDs(context.Background(), 7, []int64{0, -2})

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDistributionInviteRemoveDefaultGroupID(t *testing.T) {
	repo, mock := newDistributionInviteSQLRepo(t)
	mock.ExpectExec(regexp.QuoteMeta(deleteInviteGroupSQL)).
		WithArgs(int64(7), int64(3)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, repo.RemoveDefaultGroupID(context.Background(), 7, 3))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDistributionInviteRemoveDefaultGroupIDForAdmins(t *testing.T) {
	repo, mock := newDistributionInviteSQLRepo(t)
	mock.ExpectExec(regexp.QuoteMeta(deleteInviteGroupForAdminsSQL)).
		WithArgs(sqlmock.AnyArg(), int64(3)).
		WillReturnResult(sqlmock.NewResult(0, 2))

	require.NoError(t, repo.RemoveDefaultGroupIDForAdmins(context.Background(), []int64{7, 8, 7}, 3))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDistributionInviteRemoveDefaultGroupIDForAdminsNoopsWhenEmpty(t *testing.T) {
	repo, mock := newDistributionInviteSQLRepo(t)

	require.NoError(t, repo.RemoveDefaultGroupIDForAdmins(context.Background(), nil, 3))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestNewDistributionInviteRepository(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NotNil(t, NewDistributionInviteRepository(nil, db))
}
