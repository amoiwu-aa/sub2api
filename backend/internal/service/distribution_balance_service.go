package service

import (
	"context"
	"errors"
	"math"
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/domain"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

const maxDistributionTransferIdempotencyKeyLen = 64

var errDistributionTransferIdempotencyRace = errors.New("distribution transfer idempotency insert race")

// DistributionBalanceService atomically transfers affiliate-admin balance to a
// managed user. It does not call AdminService.UpdateUserBalance (no invite rebate).
type DistributionBalanceService struct {
	entClient            *dbent.Client
	users                DistributionBalanceUserStore
	transfers            DistributionBalanceRepository
	authCacheInvalidator APIKeyAuthCacheInvalidator
	billingCache         DistributionUserBalanceCache
	runInTx              func(ctx context.Context, fn func(ctx context.Context) error) error
}

// NewDistributionBalanceService constructs a transfer service.
func NewDistributionBalanceService(
	entClient *dbent.Client,
	users DistributionBalanceUserStore,
	transfers DistributionBalanceRepository,
	authCacheInvalidator APIKeyAuthCacheInvalidator,
	billingCache DistributionUserBalanceCache,
) *DistributionBalanceService {
	return &DistributionBalanceService{
		entClient:            entClient,
		users:                users,
		transfers:            transfers,
		authCacheInvalidator: authCacheInvalidator,
		billingCache:         billingCache,
	}
}

func errInvalidTransferAmount() error {
	return infraerrors.BadRequest(domain.ErrReasonInvalidTransferAmount, "transfer amount must be a positive finite number")
}

func errManagedUserNotFound() error {
	return infraerrors.NotFound(domain.ErrReasonManagedUserNotFound, "managed user not found")
}

func errInsufficientDistributionBalance() error {
	return infraerrors.Forbidden(domain.ErrReasonInsufficientDistributionBalance, "insufficient distribution balance")
}

func errDistributionTransferConflict() error {
	return infraerrors.Conflict(domain.ErrReasonDistributionTransferConflict, "distribution transfer idempotency conflict")
}

func errDistributionActorForbidden() error {
	return infraerrors.Forbidden("INSUFFICIENT_PERMISSIONS", "actor is not an active affiliate admin")
}

// Transfer moves Amount from the affiliate admin to a managed user.
func (s *DistributionBalanceService) Transfer(ctx context.Context, input DistributionBalanceTransferInput) (*DistributionBalanceTransfer, error) {
	if s == nil || s.users == nil || s.transfers == nil {
		return nil, infraerrors.ServiceUnavailable("SERVICE_UNAVAILABLE", "distribution balance service unavailable")
	}
	if !isPositiveFiniteAmount(input.Amount) {
		return nil, errInvalidTransferAmount()
	}
	if input.AffiliateAdminID <= 0 || input.TargetUserID <= 0 || input.AffiliateAdminID == input.TargetUserID {
		return nil, errManagedUserNotFound()
	}
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.IdempotencyKey == "" || len(input.IdempotencyKey) > maxDistributionTransferIdempotencyKeyLen {
		return nil, infraerrors.BadRequest("INVALID_IDEMPOTENCY_KEY", "idempotency key is required")
	}

	existing, err := s.transfers.GetTransferByIdempotency(ctx, input.AffiliateAdminID, input.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return replayOrConflict(existing, input)
	}

	var committed *DistributionBalanceTransfer
	txErr := s.withTx(ctx, func(txCtx context.Context) error {
		locked, err := s.transfers.LockUsersForUpdate(txCtx, input.AffiliateAdminID, input.TargetUserID)
		if err != nil {
			return err
		}
		actor, ok := locked[input.AffiliateAdminID]
		if !ok || actor.Role != RoleAffiliateAdmin || actor.Status != StatusActive {
			return errDistributionActorForbidden()
		}
		if _, ok := locked[input.TargetUserID]; !ok {
			return errManagedUserNotFound()
		}

		managed, err := s.users.UserIsManagedBy(txCtx, input.TargetUserID, input.AffiliateAdminID)
		if err != nil {
			return err
		}
		if !managed {
			return errManagedUserNotFound()
		}

		// frozen_balance is display-only; available funds are actor.Balance.
		if actor.Balance < input.Amount {
			return errInsufficientDistributionBalance()
		}

		sourceChange, err := s.users.AdjustBalance(txCtx, input.AffiliateAdminID, -input.Amount)
		if err != nil {
			if errors.Is(err, ErrBalanceNegative) {
				return errInsufficientDistributionBalance()
			}
			return err
		}
		targetChange, err := s.users.AdjustBalance(txCtx, input.TargetUserID, input.Amount)
		if err != nil {
			return err
		}

		row := &DistributionBalanceTransfer{
			AffiliateAdminID:    input.AffiliateAdminID,
			TargetUserID:        input.TargetUserID,
			Amount:              input.Amount,
			SourceBalanceBefore: sourceChange.Old,
			SourceBalanceAfter:  sourceChange.New,
			TargetBalanceBefore: targetChange.Old,
			TargetBalanceAfter:  targetChange.New,
			IdempotencyKey:      input.IdempotencyKey,
			Notes:               input.Notes,
		}
		if err := s.transfers.InsertTransfer(txCtx, row); err != nil {
			if isDistributionTransferUniqueConflict(err) {
				return errDistributionTransferIdempotencyRace
			}
			return err
		}
		committed = row
		return nil
	})
	if errors.Is(txErr, errDistributionTransferIdempotencyRace) {
		existing, err := s.transfers.GetTransferByIdempotency(ctx, input.AffiliateAdminID, input.IdempotencyKey)
		if err != nil {
			return nil, err
		}
		return replayOrConflict(existing, input)
	}
	if txErr != nil {
		return nil, txErr
	}
	if committed == nil {
		return nil, infraerrors.InternalServer("INTERNAL_ERROR", "distribution transfer did not commit")
	}

	s.invalidateCaches(ctx, input.AffiliateAdminID, input.TargetUserID)
	logger.LegacyPrintf("service.distribution_balance",
		"distribution balance transfer: actor=%d target=%d amount=%.8f transfer_id=%d",
		input.AffiliateAdminID, input.TargetUserID, input.Amount, committed.ID)
	return committed, nil
}

// BalanceSummary returns the affiliate admin's wallet plus allocated outflow.
func (s *DistributionBalanceService) BalanceSummary(ctx context.Context, adminID int64) (*DistributionBalanceSummary, error) {
	if s == nil || s.users == nil || s.transfers == nil {
		return nil, infraerrors.ServiceUnavailable("SERVICE_UNAVAILABLE", "distribution balance service unavailable")
	}
	user, err := s.users.GetByID(ctx, adminID)
	if err != nil {
		return nil, err
	}
	allocated, err := s.transfers.SumSuccessfulAllocated(ctx, adminID)
	if err != nil {
		return nil, err
	}
	return &DistributionBalanceSummary{
		Available: user.Balance,
		Frozen:    user.FrozenBalance,
		Total:     user.Balance + user.FrozenBalance,
		Allocated: allocated,
	}, nil
}

// ListTransfers returns the affiliate admin's transfer ledger.
func (s *DistributionBalanceService) ListTransfers(ctx context.Context, adminID int64, page, pageSize int) ([]DistributionBalanceTransfer, int64, error) {
	if s == nil || s.transfers == nil {
		return nil, 0, infraerrors.ServiceUnavailable("SERVICE_UNAVAILABLE", "distribution balance service unavailable")
	}
	return s.transfers.ListTransfers(ctx, adminID, page, pageSize)
}

func (s *DistributionBalanceService) withTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if s.runInTx != nil {
		return s.runInTx(ctx, fn)
	}
	if s.entClient == nil {
		return infraerrors.ServiceUnavailable("SERVICE_UNAVAILABLE", "distribution balance transaction runner is required")
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(dbent.NewTxContext(ctx, tx)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (s *DistributionBalanceService) invalidateCaches(ctx context.Context, actorID, targetID int64) {
	if s.authCacheInvalidator != nil {
		s.authCacheInvalidator.InvalidateAuthCacheByUserID(ctx, actorID)
		s.authCacheInvalidator.InvalidateAuthCacheByUserID(ctx, targetID)
	}
	if s.billingCache == nil {
		return
	}
	for _, userID := range []int64{actorID, targetID} {
		if err := s.billingCache.InvalidateUserBalance(ctx, userID); err != nil {
			logger.LegacyPrintf("service.distribution_balance", "invalidate user balance cache failed: user_id=%d err=%v", userID, err)
		}
	}
}

func isPositiveFiniteAmount(amount float64) bool {
	return !math.IsNaN(amount) && !math.IsInf(amount, 0) && amount > 0
}

func replayOrConflict(existing *DistributionBalanceTransfer, input DistributionBalanceTransferInput) (*DistributionBalanceTransfer, error) {
	if existing == nil {
		return nil, errDistributionTransferConflict()
	}
	if existing.TargetUserID == input.TargetUserID && existing.Amount == input.Amount {
		return existing, nil
	}
	return nil, errDistributionTransferConflict()
}

func isDistributionTransferUniqueConflict(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrDistributionTransferUnique) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate key") ||
		strings.Contains(msg, "unique constraint") ||
		strings.Contains(msg, "duplicate entry")
}
