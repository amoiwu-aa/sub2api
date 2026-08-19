package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.ExpiringBalanceRepository = (*userRepository)(nil)

func (r *userRepository) CreateExpiringBalanceGrant(
	ctx context.Context,
	userID, redeemCodeID int64,
	amount float64,
	validityDays int,
) error {
	if userID <= 0 || redeemCodeID <= 0 || amount <= 0 || validityDays <= 0 {
		return fmt.Errorf("invalid expiring balance grant")
	}
	return r.withBalanceGrantTransaction(ctx, func(exec sqlQueryExecutor) error {
		result, err := exec.ExecContext(ctx, `
			INSERT INTO user_balance_grants (
				user_id,
				redeem_code_id,
				original_amount,
				remaining_amount,
				expires_at
			)
			VALUES ($1, $2, $3, $3, NOW() + ($4 * INTERVAL '1 day'))
			ON CONFLICT (redeem_code_id) WHERE redeem_code_id IS NOT NULL DO NOTHING
		`, userID, redeemCodeID, amount, validityDays)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return nil
		}
		return nil
	})
}

func (r *userRepository) SettleExpiredBalanceGrants(ctx context.Context, userID int64) (*service.ExpiringBalanceState, error) {
	if userID <= 0 {
		return nil, service.ErrUserNotFound
	}

	var state *service.ExpiringBalanceState
	err := r.withBalanceGrantTransaction(ctx, func(exec sqlQueryExecutor) error {
		balance, err := lockUserBalance(ctx, exec, userID)
		if err != nil {
			return err
		}
		expired, nextExpiry, err := settleExpiredBalanceGrantsLocked(ctx, exec, userID)
		if err != nil {
			return err
		}
		if expired > 0 {
			balance -= expired
			if _, err := exec.ExecContext(ctx, `
				UPDATE users
				SET balance = $1, updated_at = NOW()
				WHERE id = $2 AND deleted_at IS NULL
			`, balance, userID); err != nil {
				return err
			}
		}
		state = &service.ExpiringBalanceState{
			Balance:       balance,
			ExpiredAmount: expired,
			NextExpiresAt: nextExpiry,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return state, nil
}

func (r *userRepository) withBalanceGrantTransaction(
	ctx context.Context,
	fn func(exec sqlQueryExecutor) error,
) error {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		exec := sqlExecutorFromEntClient(tx.Client())
		if exec == nil {
			return errors.New("transaction SQL executor is not configured")
		}
		return fn(exec)
	}

	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	exec := sqlExecutorFromEntClient(tx.Client())
	if exec == nil {
		return errors.New("transaction SQL executor is not configured")
	}
	if err := fn(exec); err != nil {
		return err
	}
	return tx.Commit()
}

func lockUserBalance(ctx context.Context, exec sqlQueryExecutor, userID int64) (float64, error) {
	rows, err := exec.QueryContext(ctx, `
		SELECT balance
		FROM users
		WHERE id = $1 AND deleted_at IS NULL
		FOR UPDATE
	`, userID)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return 0, err
		}
		return 0, service.ErrUserNotFound
	}
	var balance float64
	if err := rows.Scan(&balance); err != nil {
		return 0, err
	}
	return balance, rows.Err()
}

func settleExpiredBalanceGrantsLocked(
	ctx context.Context,
	exec sqlQueryExecutor,
	userID int64,
) (float64, *time.Time, error) {
	rows, err := exec.QueryContext(ctx, `
		WITH expired_candidates AS MATERIALIZED (
			SELECT id, remaining_amount
			FROM user_balance_grants
			WHERE user_id = $1
			  AND remaining_amount > 0
			  AND expired_at IS NULL
			  AND expires_at <= NOW()
			FOR UPDATE
		),
		marked AS (
			UPDATE user_balance_grants AS grants
			SET remaining_amount = 0,
				expired_at = NOW(),
				updated_at = NOW()
			FROM expired_candidates AS candidates
			WHERE grants.id = candidates.id
			RETURNING candidates.remaining_amount
		)
		SELECT COALESCE(SUM(remaining_amount), 0)
		FROM marked
	`, userID)
	if err != nil {
		return 0, nil, err
	}
	var expired float64
	if rows.Next() {
		if err := rows.Scan(&expired); err != nil {
			_ = rows.Close()
			return 0, nil, err
		}
	}
	if err := rows.Close(); err != nil {
		return 0, nil, err
	}

	nextRows, err := exec.QueryContext(ctx, `
		SELECT MIN(expires_at)
		FROM user_balance_grants
		WHERE user_id = $1
		  AND remaining_amount > 0
		  AND expired_at IS NULL
		  AND expires_at > NOW()
	`, userID)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = nextRows.Close() }()
	var next sql.NullTime
	if nextRows.Next() {
		if err := nextRows.Scan(&next); err != nil {
			return 0, nil, err
		}
	}
	if err := nextRows.Err(); err != nil {
		return 0, nil, err
	}
	if !next.Valid {
		return expired, nil, nil
	}
	nextExpiry := next.Time.UTC()
	return expired, &nextExpiry, nil
}

func consumeExpiringBalanceGrantsLocked(
	ctx context.Context,
	exec sqlQueryExecutor,
	userID int64,
	amount float64,
) (float64, error) {
	if amount <= 0 {
		return 0, nil
	}
	rows, err := exec.QueryContext(ctx, `
		SELECT id, remaining_amount
		FROM user_balance_grants
		WHERE user_id = $1
		  AND remaining_amount > 0
		  AND expired_at IS NULL
		  AND expires_at > NOW()
		ORDER BY expires_at ASC, id ASC
		FOR UPDATE
	`, userID)
	if err != nil {
		return 0, err
	}

	type grantBalance struct {
		id        int64
		remaining float64
	}
	grants := make([]grantBalance, 0)
	for rows.Next() {
		var grant grantBalance
		if err := rows.Scan(&grant.id, &grant.remaining); err != nil {
			_ = rows.Close()
			return 0, err
		}
		grants = append(grants, grant)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}

	left := amount
	consumed := 0.0
	for _, grant := range grants {
		if left <= 0 {
			break
		}
		use := grant.remaining
		if use > left {
			use = left
		}
		if use <= 0 {
			continue
		}
		if _, err := exec.ExecContext(ctx, `
			UPDATE user_balance_grants
			SET remaining_amount = remaining_amount - $1,
				exhausted_at = CASE
					WHEN remaining_amount - $1 <= 0 THEN NOW()
					ELSE exhausted_at
				END,
				updated_at = NOW()
			WHERE id = $2
		`, use, grant.id); err != nil {
			return 0, err
		}
		left -= use
		consumed += use
	}
	return consumed, nil
}

func (r *userRepository) adjustBalanceWithGrantLedger(
	ctx context.Context,
	userID int64,
	delta float64,
	floorAtZero bool,
	requireNonnegative bool,
	countRecharge bool,
) (service.BalanceChange, error) {
	var change service.BalanceChange
	err := r.withBalanceGrantTransaction(ctx, func(exec sqlQueryExecutor) error {
		current, err := lockUserBalance(ctx, exec, userID)
		if err != nil {
			return err
		}
		expired, _, err := settleExpiredBalanceGrantsLocked(ctx, exec, userID)
		if err != nil {
			return err
		}
		current -= expired
		next := current + delta
		if floorAtZero {
			next = math.Max(next, 0)
		}
		if requireNonnegative && next < 0 {
			change = service.BalanceChange{Old: current, New: next}
			return service.ErrBalanceNegative
		}

		decrease := current - next
		if decrease > 0 {
			if _, err := consumeExpiringBalanceGrantsLocked(ctx, exec, userID, decrease); err != nil {
				return err
			}
		}

		rechargeDelta := 0.0
		if countRecharge && delta > 0 {
			rechargeDelta = delta
		}
		result, err := exec.ExecContext(ctx, `
			UPDATE users
			SET balance = $1,
				total_recharged = total_recharged + $2,
				updated_at = NOW()
			WHERE id = $3 AND deleted_at IS NULL
		`, next, rechargeDelta, userID)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return service.ErrUserNotFound
		}
		change = service.BalanceChange{Old: current, New: next}
		return nil
	})
	return change, err
}

func (r *userRepository) setBalanceAndCancelGrantLedger(
	ctx context.Context,
	userID int64,
	value float64,
) (service.BalanceChange, error) {
	var change service.BalanceChange
	err := r.withBalanceGrantTransaction(ctx, func(exec sqlQueryExecutor) error {
		current, err := lockUserBalance(ctx, exec, userID)
		if err != nil {
			return err
		}
		expired, _, err := settleExpiredBalanceGrantsLocked(ctx, exec, userID)
		if err != nil {
			return err
		}
		current -= expired
		if _, err := exec.ExecContext(ctx, `
			UPDATE user_balance_grants
			SET remaining_amount = 0,
				exhausted_at = COALESCE(exhausted_at, NOW()),
				updated_at = NOW()
			WHERE user_id = $1
			  AND remaining_amount > 0
			  AND expired_at IS NULL
		`, userID); err != nil {
			return err
		}
		result, err := exec.ExecContext(ctx, `
			UPDATE users
			SET balance = $1, updated_at = NOW()
			WHERE id = $2 AND deleted_at IS NULL
		`, value, userID)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return service.ErrUserNotFound
		}
		change = service.BalanceChange{Old: current, New: value}
		return nil
	})
	return change, err
}

func (r *userRepository) deductAvailableBalanceWithGrantLedger(
	ctx context.Context,
	userID int64,
	amount float64,
) (float64, error) {
	var deducted float64
	err := r.withBalanceGrantTransaction(ctx, func(exec sqlQueryExecutor) error {
		current, err := lockUserBalance(ctx, exec, userID)
		if err != nil {
			return err
		}
		expired, _, err := settleExpiredBalanceGrantsLocked(ctx, exec, userID)
		if err != nil {
			return err
		}
		current -= expired
		if current > 0 && amount > 0 {
			deducted = math.Min(amount, current)
		}
		if deducted > 0 {
			if _, err := consumeExpiringBalanceGrantsLocked(ctx, exec, userID, deducted); err != nil {
				return err
			}
		}
		result, err := exec.ExecContext(ctx, `
			UPDATE users
			SET balance = $1, updated_at = NOW()
			WHERE id = $2 AND deleted_at IS NULL
		`, current-deducted, userID)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return service.ErrUserNotFound
		}
		return nil
	})
	return deducted, err
}
