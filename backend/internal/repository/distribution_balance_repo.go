package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const maxDistributionIdempotencyKeyLen = 64

const getTransferByIdempotencySQL = `
SELECT id,
       affiliate_admin_id,
       target_user_id,
       amount::double precision,
       source_balance_before::double precision,
       source_balance_after::double precision,
       target_balance_before::double precision,
       target_balance_after::double precision,
       idempotency_key,
       notes,
       created_at
FROM affiliate_admin_balance_transfers
WHERE affiliate_admin_id = $1 AND idempotency_key = $2`

const insertTransferSQL = `
INSERT INTO affiliate_admin_balance_transfers (
    affiliate_admin_id,
    target_user_id,
    amount,
    source_balance_before,
    source_balance_after,
    target_balance_before,
    target_balance_after,
    idempotency_key,
    notes
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING id, created_at`

const countTransfersSQL = `
SELECT COUNT(*)
FROM affiliate_admin_balance_transfers
WHERE affiliate_admin_id = $1`

const listTransfersSQL = `
SELECT id,
       affiliate_admin_id,
       target_user_id,
       amount::double precision,
       source_balance_before::double precision,
       source_balance_after::double precision,
       target_balance_before::double precision,
       target_balance_after::double precision,
       idempotency_key,
       notes,
       created_at
FROM affiliate_admin_balance_transfers
WHERE affiliate_admin_id = $1
ORDER BY created_at DESC, id DESC
LIMIT $2 OFFSET $3`

const sumSuccessfulAllocatedSQL = `
SELECT COALESCE(SUM(amount), 0)::double precision
FROM affiliate_admin_balance_transfers
WHERE affiliate_admin_id = $1`

type distributionBalanceRepository struct {
	client *dbent.Client
	sql    sqlExecutor
}

var _ service.DistributionBalanceRepository = (*distributionBalanceRepository)(nil)

func NewDistributionBalanceRepository(client *dbent.Client, sqlDB *sql.DB) service.DistributionBalanceRepository {
	return &distributionBalanceRepository{client: client, sql: sqlDB}
}

func (r *distributionBalanceRepository) queryExec(ctx context.Context) sqlExecutor {
	if r == nil {
		return nil
	}
	if r.client != nil {
		return clientFromContext(ctx, r.client)
	}
	return r.sql
}

func (r *distributionBalanceRepository) GetTransferByIdempotency(ctx context.Context, adminID int64, key string) (*service.DistributionBalanceTransfer, error) {
	key = strings.TrimSpace(key)
	if adminID <= 0 || key == "" {
		return nil, nil
	}
	exec := r.queryExec(ctx)
	if exec == nil {
		return nil, fmt.Errorf("nil distribution balance executor")
	}
	row := &service.DistributionBalanceTransfer{}
	err := scanSingleRow(ctx, exec, getTransferByIdempotencySQL, []any{adminID, key},
		&row.ID,
		&row.AffiliateAdminID,
		&row.TargetUserID,
		&row.Amount,
		&row.SourceBalanceBefore,
		&row.SourceBalanceAfter,
		&row.TargetBalanceBefore,
		&row.TargetBalanceAfter,
		&row.IdempotencyKey,
		&row.Notes,
		&row.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return row, nil
}

func (r *distributionBalanceRepository) InsertTransfer(ctx context.Context, row *service.DistributionBalanceTransfer) error {
	if err := validateDistributionTransferInsert(row); err != nil {
		return err
	}
	exec := r.queryExec(ctx)
	if exec == nil {
		return fmt.Errorf("nil distribution balance executor")
	}
	err := scanSingleRow(ctx, exec, insertTransferSQL, []any{
		row.AffiliateAdminID,
		row.TargetUserID,
		row.Amount,
		row.SourceBalanceBefore,
		row.SourceBalanceAfter,
		row.TargetBalanceBefore,
		row.TargetBalanceAfter,
		row.IdempotencyKey,
		row.Notes,
	}, &row.ID, &row.CreatedAt)
	if err != nil {
		if isUniqueConstraintViolation(err) {
			return fmt.Errorf("insert distribution transfer: %w: %w", service.ErrDistributionTransferUnique, err)
		}
		return fmt.Errorf("insert distribution transfer: %w", err)
	}
	return nil
}

func (r *distributionBalanceRepository) ListTransfers(ctx context.Context, adminID int64, page, pageSize int) ([]service.DistributionBalanceTransfer, int64, error) {
	if adminID <= 0 {
		return []service.DistributionBalanceTransfer{}, 0, nil
	}
	exec := r.queryExec(ctx)
	if exec == nil {
		return nil, 0, fmt.Errorf("nil distribution balance executor")
	}
	params := pagination.PaginationParams{Page: page, PageSize: pageSize}

	var total int64
	if err := scanSingleRow(ctx, exec, countTransfersSQL, []any{adminID}, &total); err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return []service.DistributionBalanceTransfer{}, 0, nil
	}

	rows, err := exec.QueryContext(ctx, listTransfersSQL, adminID, params.Limit(), params.Offset())
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]service.DistributionBalanceTransfer, 0)
	for rows.Next() {
		var row service.DistributionBalanceTransfer
		if err := rows.Scan(
			&row.ID,
			&row.AffiliateAdminID,
			&row.TargetUserID,
			&row.Amount,
			&row.SourceBalanceBefore,
			&row.SourceBalanceAfter,
			&row.TargetBalanceBefore,
			&row.TargetBalanceAfter,
			&row.IdempotencyKey,
			&row.Notes,
			&row.CreatedAt,
		); err != nil {
			return nil, 0, err
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

func (r *distributionBalanceRepository) SumSuccessfulAllocated(ctx context.Context, adminID int64) (float64, error) {
	if adminID <= 0 {
		return 0, nil
	}
	exec := r.queryExec(ctx)
	if exec == nil {
		return 0, fmt.Errorf("nil distribution balance executor")
	}
	var sum float64
	if err := scanSingleRow(ctx, exec, sumSuccessfulAllocatedSQL, []any{adminID}, &sum); err != nil {
		return 0, err
	}
	return sum, nil
}

const lockUsersForUpdateSQL = `
SELECT id,
       role,
       status,
       balance::double precision,
       frozen_balance::double precision
FROM users
WHERE id IN ($1, $2) AND deleted_at IS NULL
ORDER BY id
FOR UPDATE`

// LockUsersForUpdate locks both users in ascending ID order to avoid deadlocks.
func (r *distributionBalanceRepository) LockUsersForUpdate(ctx context.Context, idA, idB int64) (map[int64]service.LockedDistributionUser, error) {
	if idA <= 0 || idB <= 0 {
		return nil, fmt.Errorf("invalid user id")
	}
	first, second := idA, idB
	if first > second {
		first, second = second, first
	}
	exec := r.queryExec(ctx)
	if exec == nil {
		return nil, fmt.Errorf("nil distribution balance executor")
	}
	rows, err := exec.QueryContext(ctx, lockUsersForUpdateSQL, first, second)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make(map[int64]service.LockedDistributionUser, 2)
	for rows.Next() {
		var row service.LockedDistributionUser
		if err := rows.Scan(&row.ID, &row.Role, &row.Status, &row.Balance, &row.FrozenBalance); err != nil {
			return nil, err
		}
		out[row.ID] = row
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func validateDistributionTransferInsert(row *service.DistributionBalanceTransfer) error {
	if row == nil {
		return fmt.Errorf("distribution transfer is required")
	}
	if row.AffiliateAdminID <= 0 {
		return fmt.Errorf("invalid distribution admin id")
	}
	if row.TargetUserID <= 0 {
		return fmt.Errorf("invalid distribution target user id")
	}
	if row.Amount <= 0 {
		return fmt.Errorf("invalid distribution transfer amount")
	}
	row.IdempotencyKey = strings.TrimSpace(row.IdempotencyKey)
	if row.IdempotencyKey == "" {
		return fmt.Errorf("distribution transfer idempotency key is required")
	}
	if len(row.IdempotencyKey) > maxDistributionIdempotencyKeyLen {
		return fmt.Errorf("distribution transfer idempotency key exceeds %d characters", maxDistributionIdempotencyKeyLen)
	}
	return nil
}

// IsDistributionTransferConflict reports a unique-constraint failure on
// affiliate_admin_balance_transfers (typically the per-admin idempotency key).
func IsDistributionTransferConflict(err error) bool {
	return errors.Is(err, service.ErrDistributionTransferUnique) || isUniqueConstraintViolation(err)
}
