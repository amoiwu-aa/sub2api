package repository

import (
	"context"
	"database/sql"
	"fmt"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

const getOrCreateInviteSettingsSQL = `
INSERT INTO affiliate_admin_invite_settings (affiliate_admin_id, enabled)
VALUES ($1, TRUE)
ON CONFLICT (affiliate_admin_id) DO UPDATE
SET enabled = affiliate_admin_invite_settings.enabled
RETURNING enabled`

const updateInviteEnabledSQL = `
INSERT INTO affiliate_admin_invite_settings (affiliate_admin_id, enabled, created_at, updated_at)
VALUES ($1, $2, NOW(), NOW())
ON CONFLICT (affiliate_admin_id) DO UPDATE
SET enabled = EXCLUDED.enabled,
    updated_at = NOW()`

const listInviteGroupIDsSQL = `
SELECT group_id
FROM affiliate_admin_invite_groups
WHERE affiliate_admin_id = $1
ORDER BY group_id`

const deleteInviteGroupsByAdminSQL = `
DELETE FROM affiliate_admin_invite_groups
WHERE affiliate_admin_id = $1`

const insertInviteGroupsSQL = `
INSERT INTO affiliate_admin_invite_groups (affiliate_admin_id, group_id, created_at)
SELECT $1, UNNEST($2::bigint[]), NOW()
ON CONFLICT (affiliate_admin_id, group_id) DO NOTHING`

const deleteInviteGroupSQL = `
DELETE FROM affiliate_admin_invite_groups
WHERE affiliate_admin_id = $1 AND group_id = $2`

const deleteInviteGroupForAdminsSQL = `
DELETE FROM affiliate_admin_invite_groups
WHERE affiliate_admin_id = ANY($1) AND group_id = $2`

type distributionInviteRepository struct {
	client *dbent.Client
	sql    sqlExecutor
}

var _ service.DistributionInviteRepository = (*distributionInviteRepository)(nil)

func NewDistributionInviteRepository(client *dbent.Client, sqlDB *sql.DB) service.DistributionInviteRepository {
	return &distributionInviteRepository{client: client, sql: sqlDB}
}

func (r *distributionInviteRepository) queryExec(ctx context.Context) sqlExecutor {
	if r == nil {
		return nil
	}
	if r.client != nil {
		return clientFromContext(ctx, r.client)
	}
	return r.sql
}

func (r *distributionInviteRepository) withTx(ctx context.Context, fn func(ctx context.Context, exec sqlExecutor) error) error {
	if r == nil {
		return fmt.Errorf("nil distribution invite repository")
	}
	if r.client != nil {
		if tx := dbent.TxFromContext(ctx); tx != nil {
			return fn(ctx, tx.Client())
		}
		tx, err := r.client.Tx(ctx)
		if err != nil {
			return fmt.Errorf("begin distribution invite transaction: %w", err)
		}
		defer func() { _ = tx.Rollback() }()

		txCtx := dbent.NewTxContext(ctx, tx)
		if err := fn(txCtx, tx.Client()); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit distribution invite transaction: %w", err)
		}
		return nil
	}
	if db, ok := r.sql.(*sql.DB); ok {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin distribution invite transaction: %w", err)
		}
		defer func() { _ = tx.Rollback() }()
		if err := fn(ctx, tx); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit distribution invite transaction: %w", err)
		}
		return nil
	}
	if r.sql == nil {
		return fmt.Errorf("nil distribution invite executor")
	}
	return fn(ctx, r.sql)
}

func (r *distributionInviteRepository) GetOrCreateSettings(ctx context.Context, adminID int64) (bool, error) {
	if adminID <= 0 {
		return false, fmt.Errorf("invalid distribution admin id")
	}
	exec := r.queryExec(ctx)
	if exec == nil {
		return false, fmt.Errorf("nil distribution invite executor")
	}
	var enabled bool
	if err := scanSingleRow(ctx, exec, getOrCreateInviteSettingsSQL, []any{adminID}, &enabled); err != nil {
		return false, err
	}
	return enabled, nil
}

func (r *distributionInviteRepository) UpdateEnabled(ctx context.Context, adminID int64, enabled bool) error {
	if adminID <= 0 {
		return fmt.Errorf("invalid distribution admin id")
	}
	exec := r.queryExec(ctx)
	if exec == nil {
		return fmt.Errorf("nil distribution invite executor")
	}
	_, err := exec.ExecContext(ctx, updateInviteEnabledSQL, adminID, enabled)
	return err
}

func (r *distributionInviteRepository) ListDefaultGroupIDs(ctx context.Context, adminID int64) ([]int64, error) {
	if adminID <= 0 {
		return nil, nil
	}
	exec := r.queryExec(ctx)
	if exec == nil {
		return nil, fmt.Errorf("nil distribution invite executor")
	}
	rows, err := exec.QueryContext(ctx, listInviteGroupIDsSQL, adminID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

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

func (r *distributionInviteRepository) ReplaceDefaultGroupIDs(ctx context.Context, adminID int64, groupIDs []int64) error {
	if adminID <= 0 {
		return fmt.Errorf("invalid distribution admin id")
	}
	ids := uniquePositiveIDs(groupIDs)
	return r.withTx(ctx, func(ctx context.Context, exec sqlExecutor) error {
		if _, err := exec.ExecContext(ctx, deleteInviteGroupsByAdminSQL, adminID); err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		_, err := exec.ExecContext(ctx, insertInviteGroupsSQL, adminID, pq.Array(ids))
		return err
	})
}

func (r *distributionInviteRepository) RemoveDefaultGroupID(ctx context.Context, adminID, groupID int64) error {
	if adminID <= 0 || groupID <= 0 {
		return nil
	}
	exec := r.queryExec(ctx)
	if exec == nil {
		return fmt.Errorf("nil distribution invite executor")
	}
	_, err := exec.ExecContext(ctx, deleteInviteGroupSQL, adminID, groupID)
	return err
}

func (r *distributionInviteRepository) RemoveDefaultGroupIDForAdmins(ctx context.Context, adminIDs []int64, groupID int64) error {
	if groupID <= 0 {
		return nil
	}
	ids := uniquePositiveIDs(adminIDs)
	if len(ids) == 0 {
		return nil
	}
	exec := r.queryExec(ctx)
	if exec == nil {
		return fmt.Errorf("nil distribution invite executor")
	}
	_, err := exec.ExecContext(ctx, deleteInviteGroupForAdminsSQL, pq.Array(ids), groupID)
	return err
}

func uniquePositiveIDs(ids []int64) []int64 {
	if len(ids) == 0 {
		return nil
	}
	out := make([]int64, 0, len(ids))
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
