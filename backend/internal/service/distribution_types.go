package service

import (
	"context"
	"errors"
	"time"
)

// ErrDistributionTransferUnique is returned (possibly wrapped) when inserting a
// transfer would violate the per-admin idempotency unique index.
var ErrDistributionTransferUnique = errors.New("distribution transfer unique constraint")

// DistributionBalanceTransfer is a persisted affiliate-admin to managed-user
// balance transfer row (table affiliate_admin_balance_transfers).
type DistributionBalanceTransfer struct {
	ID                  int64
	AffiliateAdminID    int64
	TargetUserID        int64
	Amount              float64
	SourceBalanceBefore float64
	SourceBalanceAfter  float64
	TargetBalanceBefore float64
	TargetBalanceAfter  float64
	IdempotencyKey      string
	Notes               string
	CreatedAt           time.Time
}

// DistributionBalanceTransferInput is an affiliate-admin → managed-user transfer.
type DistributionBalanceTransferInput struct {
	AffiliateAdminID int64
	TargetUserID     int64
	Amount           float64
	Notes            string
	IdempotencyKey   string
}

// DistributionBalanceSummary is the actor's wallet plus allocated outflow.
type DistributionBalanceSummary struct {
	Available float64
	Frozen    float64
	Total     float64
	Allocated float64
}

// LockedDistributionUser is a FOR UPDATE snapshot of a users row.
type LockedDistributionUser struct {
	ID            int64
	Role          string
	Status        string
	Balance       float64
	FrozenBalance float64
}

// DistributionBalanceUserStore is the user persistence needed for transfers.
// *repository.userRepository satisfies this (GetByID, AdjustBalance, UserIsManagedBy).
type DistributionBalanceUserStore interface {
	GetByID(ctx context.Context, id int64) (*User, error)
	AdjustBalance(ctx context.Context, id int64, delta float64) (BalanceChange, error)
	UserIsManagedBy(ctx context.Context, userID, adminID int64) (bool, error)
}

// DistributionUserBalanceCache invalidates cached wallet balances after a transfer.
// BillingCache and *BillingCacheService both satisfy this.
type DistributionUserBalanceCache interface {
	InvalidateUserBalance(ctx context.Context, userID int64) error
}

// DistributionInviteRepository persists per-admin invite defaults.
type DistributionInviteRepository interface {
	GetOrCreateSettings(ctx context.Context, adminID int64) (enabled bool, err error)
	UpdateEnabled(ctx context.Context, adminID int64, enabled bool) error
	ListDefaultGroupIDs(ctx context.Context, adminID int64) ([]int64, error)
	ReplaceDefaultGroupIDs(ctx context.Context, adminID int64, groupIDs []int64) error
	RemoveDefaultGroupID(ctx context.Context, adminID, groupID int64) error
	RemoveDefaultGroupIDForAdmins(ctx context.Context, adminIDs []int64, groupID int64) error
}

// DistributionBalanceRepository persists affiliate-admin balance transfers.
type DistributionBalanceRepository interface {
	GetTransferByIdempotency(ctx context.Context, adminID int64, key string) (*DistributionBalanceTransfer, error)
	InsertTransfer(ctx context.Context, row *DistributionBalanceTransfer) error
	ListTransfers(ctx context.Context, adminID int64, page, pageSize int) ([]DistributionBalanceTransfer, int64, error)
	SumSuccessfulAllocated(ctx context.Context, adminID int64) (float64, error)
	// LockUsersForUpdate SELECT … FOR UPDATE both users in ascending ID order.
	LockUsersForUpdate(ctx context.Context, idA, idB int64) (map[int64]LockedDistributionUser, error)
}
