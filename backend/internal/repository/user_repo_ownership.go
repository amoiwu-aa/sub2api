package repository

import (
	"context"
	"fmt"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const managedUserIDsSQL = `
SELECT u.id
FROM users u
WHERE u.role = $2
  AND (u.created_by_admin_id = $1 OR EXISTS (
        SELECT 1 FROM user_affiliates ua
        WHERE ua.user_id = u.id AND ua.inviter_id = $1
      ))`

const userIsManagedBySQL = `
SELECT EXISTS (
    SELECT 1
    FROM users u
    WHERE u.id = $1
      AND u.deleted_at IS NULL
      AND u.role = $3
      AND (u.created_by_admin_id = $2 OR EXISTS (
            SELECT 1 FROM user_affiliates ua
            WHERE ua.user_id = u.id AND ua.inviter_id = $2
          ))
)`

// CreateManagedUser creates the user and both ownership records in one
// transaction. A failed affiliate binding rolls back the user row as well.
func (r *userRepository) CreateManagedUser(ctx context.Context, user *service.User, adminID int64) error {
	if user == nil {
		return nil
	}
	if adminID <= 0 {
		return fmt.Errorf("invalid distribution admin id")
	}
	createdByAdminID := adminID
	user.CreatedByAdminID = &createdByAdminID

	if tx := dbent.TxFromContext(ctx); tx != nil {
		return r.createManagedUserInTx(ctx, user, adminID)
	}

	tx, err := r.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin managed user transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	txCtx := dbent.NewTxContext(ctx, tx)
	if err := r.createManagedUserInTx(txCtx, user, adminID); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit managed user transaction: %w", err)
	}
	return nil
}

func (r *userRepository) createManagedUserInTx(ctx context.Context, user *service.User, adminID int64) error {
	if err := r.create(ctx, user, false, ""); err != nil {
		return err
	}

	bound, err := (&affiliateRepository{client: r.client}).BindInviter(ctx, user.ID, adminID)
	if err != nil {
		return fmt.Errorf("bind managed user inviter: %w", err)
	}
	if !bound {
		return fmt.Errorf("bind managed user inviter: ownership was not created")
	}
	return nil
}

func (r *userRepository) listManagedUserIDs(ctx context.Context, adminID int64, includeDeleted bool) ([]int64, error) {
	if adminID <= 0 {
		return nil, nil
	}
	query := managedUserIDsSQL
	if !includeDeleted {
		query += ` AND u.deleted_at IS NULL`
	}
	rows, err := clientFromContext(ctx, r.client).QueryContext(ctx, query, adminID, service.RoleUser)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *userRepository) UserIsManagedBy(ctx context.Context, userID, adminID int64) (bool, error) {
	if userID <= 0 || adminID <= 0 {
		return false, nil
	}
	var ok bool
	rows, err := clientFromContext(ctx, r.client).QueryContext(ctx, userIsManagedBySQL, userID, adminID, service.RoleUser)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return false, rows.Err()
	}
	if err := rows.Scan(&ok); err != nil {
		return false, err
	}
	return ok, rows.Err()
}
