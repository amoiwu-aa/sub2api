package repository

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

func newUserOwnershipRepositoryTest(t *testing.T) (*userRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	return newUserRepositoryWithSQL(client, db), mock
}

func TestUserRepositoryListManagedUserIDsUsesCreatorOrInviter(t *testing.T) {
	repo, mock := newUserOwnershipRepositoryTest(t)
	query := regexp.QuoteMeta(managedUserIDsSQL + ` AND u.deleted_at IS NULL`)
	mock.ExpectQuery(query).
		WithArgs(int64(7), service.RoleUser).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).
			AddRow(int64(11)).
			AddRow(int64(12)))

	ids, err := repo.listManagedUserIDs(context.Background(), 7, false)

	require.NoError(t, err)
	require.Equal(t, []int64{11, 12}, ids)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUserRepositoryUserIsManagedByUsesCreatorOrInviter(t *testing.T) {
	repo, mock := newUserOwnershipRepositoryTest(t)
	mock.ExpectQuery(regexp.QuoteMeta(userIsManagedBySQL)).
		WithArgs(int64(11), int64(7), service.RoleUser).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

	ok, err := repo.UserIsManagedBy(context.Background(), 11, 7)

	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, mock.ExpectationsWereMet())
}
