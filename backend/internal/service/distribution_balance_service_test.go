package service

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type distributionBalanceUserStub struct {
	users   map[int64]*User
	managed map[int64]int64
	adjusts []distributionBalanceAdjust
}

type distributionBalanceAdjust struct {
	ID    int64
	Delta float64
}

func (s *distributionBalanceUserStub) GetByID(_ context.Context, id int64) (*User, error) {
	u, ok := s.users[id]
	if !ok {
		return nil, ErrUserNotFound
	}
	clone := *u
	return &clone, nil
}

func (s *distributionBalanceUserStub) AdjustBalance(_ context.Context, id int64, delta float64) (BalanceChange, error) {
	u, ok := s.users[id]
	if !ok {
		return BalanceChange{}, ErrUserNotFound
	}
	change := BalanceChange{Old: u.Balance, New: u.Balance + delta}
	if change.New < 0 {
		return change, ErrBalanceNegative
	}
	u.Balance = change.New
	s.adjusts = append(s.adjusts, distributionBalanceAdjust{ID: id, Delta: delta})
	return change, nil
}

func (s *distributionBalanceUserStub) UserIsManagedBy(_ context.Context, userID, adminID int64) (bool, error) {
	return s.managed[userID] == adminID && adminID > 0, nil
}

func (s *distributionBalanceUserStub) snapshot() map[int64]float64 {
	out := make(map[int64]float64, len(s.users))
	for id, u := range s.users {
		out[id] = u.Balance
	}
	return out
}

func (s *distributionBalanceUserStub) restore(snap map[int64]float64) {
	for id, bal := range snap {
		if u, ok := s.users[id]; ok {
			u.Balance = bal
		}
	}
}

type distributionBalanceRepoStub struct {
	users     *distributionBalanceUserStub
	byKey     map[string]*DistributionBalanceTransfer
	insertErr error
	inserts   int
	allocated float64
	list      []DistributionBalanceTransfer
	listTotal int64
	nextID    int64
	// getMisses makes the next N GetTransferByIdempotency calls return nil,
	// simulating a concurrent insert that is not visible until after rollback.
	getMisses int
}

func (r *distributionBalanceRepoStub) GetTransferByIdempotency(_ context.Context, adminID int64, key string) (*DistributionBalanceTransfer, error) {
	if r.getMisses > 0 {
		r.getMisses--
		return nil, nil
	}
	row := r.byKey[key]
	if row == nil || row.AffiliateAdminID != adminID {
		return nil, nil
	}
	clone := *row
	return &clone, nil
}

func (r *distributionBalanceRepoStub) InsertTransfer(_ context.Context, row *DistributionBalanceTransfer) error {
	if r.insertErr != nil {
		return r.insertErr
	}
	if existing := r.byKey[row.IdempotencyKey]; existing != nil && existing.AffiliateAdminID == row.AffiliateAdminID {
		return ErrDistributionTransferUnique
	}
	if r.nextID == 0 {
		r.nextID = 1
	}
	row.ID = r.nextID
	r.nextID++
	row.CreatedAt = time.Date(2026, 8, 19, 3, 0, 0, 0, time.UTC)
	clone := *row
	if r.byKey == nil {
		r.byKey = map[string]*DistributionBalanceTransfer{}
	}
	r.byKey[row.IdempotencyKey] = &clone
	r.inserts++
	return nil
}

func (r *distributionBalanceRepoStub) ListTransfers(_ context.Context, _ int64, _, _ int) ([]DistributionBalanceTransfer, int64, error) {
	if r.list == nil {
		return []DistributionBalanceTransfer{}, r.listTotal, nil
	}
	return r.list, r.listTotal, nil
}

func (r *distributionBalanceRepoStub) SumSuccessfulAllocated(_ context.Context, _ int64) (float64, error) {
	return r.allocated, nil
}

func (r *distributionBalanceRepoStub) LockUsersForUpdate(_ context.Context, idA, idB int64) (map[int64]LockedDistributionUser, error) {
	out := make(map[int64]LockedDistributionUser, 2)
	if r.users == nil {
		return out, nil
	}
	for _, id := range []int64{idA, idB} {
		u, ok := r.users.users[id]
		if !ok {
			continue
		}
		out[id] = LockedDistributionUser{
			ID:            u.ID,
			Role:          u.Role,
			Status:        u.Status,
			Balance:       u.Balance,
			FrozenBalance: u.FrozenBalance,
		}
	}
	return out, nil
}

type distributionAuthCacheStub struct {
	userIDs []int64
}

func (s *distributionAuthCacheStub) InvalidateAuthCacheByKey(context.Context, string) {}
func (s *distributionAuthCacheStub) InvalidateAuthCacheByGroupID(context.Context, int64) {
}
func (s *distributionAuthCacheStub) InvalidateAuthCacheByUserID(_ context.Context, userID int64) {
	s.userIDs = append(s.userIDs, userID)
}

type distributionBillingCacheStub struct {
	userIDs []int64
}

func (s *distributionBillingCacheStub) InvalidateUserBalance(_ context.Context, userID int64) error {
	s.userIDs = append(s.userIDs, userID)
	return nil
}

type distributionBalanceHarness struct {
	users     *distributionBalanceUserStub
	transfers *distributionBalanceRepoStub
	auth      *distributionAuthCacheStub
	billing   *distributionBillingCacheStub
	svc       *DistributionBalanceService
}

func newDistributionBalanceHarness() *distributionBalanceHarness {
	users := &distributionBalanceUserStub{
		users: map[int64]*User{
			10: {ID: 10, Role: RoleAffiliateAdmin, Status: StatusActive, Balance: 100, FrozenBalance: 7},
			20: {ID: 20, Role: RoleUser, Status: StatusActive, Balance: 5},
		},
		managed: map[int64]int64{20: 10},
	}
	transfers := &distributionBalanceRepoStub{users: users, byKey: map[string]*DistributionBalanceTransfer{}}
	auth := &distributionAuthCacheStub{}
	billing := &distributionBillingCacheStub{}
	h := &distributionBalanceHarness{
		users:     users,
		transfers: transfers,
		auth:      auth,
		billing:   billing,
		svc:       NewDistributionBalanceService(nil, users, transfers, auth, billing),
	}
	h.svc.runInTx = func(ctx context.Context, fn func(context.Context) error) error {
		snap := users.snapshot()
		err := fn(ctx)
		if err != nil {
			users.restore(snap)
		}
		return err
	}
	return h
}

func validTransferInput() DistributionBalanceTransferInput {
	return DistributionBalanceTransferInput{
		AffiliateAdminID: 10,
		TargetUserID:     20,
		Amount:           10,
		Notes:            "top-up",
		IdempotencyKey:   "k-1",
	}
}

func TestDistributionBalanceTransfer_RejectsInvalidAmount(t *testing.T) {
	t.Parallel()
	h := newDistributionBalanceHarness()
	for _, amount := range []float64{0, -1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		input := validTransferInput()
		input.Amount = amount
		_, err := h.svc.Transfer(context.Background(), input)
		require.Error(t, err)
		require.Equal(t, domain.ErrReasonInvalidTransferAmount, infraerrors.Reason(err))
		require.True(t, infraerrors.IsBadRequest(err))
		require.Empty(t, h.users.adjusts)
	}
}

func TestDistributionBalanceTransfer_RejectsSelfTransfer(t *testing.T) {
	t.Parallel()
	h := newDistributionBalanceHarness()
	input := validTransferInput()
	input.TargetUserID = input.AffiliateAdminID
	_, err := h.svc.Transfer(context.Background(), input)
	require.Error(t, err)
	require.Equal(t, domain.ErrReasonManagedUserNotFound, infraerrors.Reason(err))
	require.True(t, infraerrors.IsNotFound(err))
	require.Empty(t, h.users.adjusts)
}

func TestDistributionBalanceTransfer_RejectsUnmanagedUser(t *testing.T) {
	t.Parallel()
	h := newDistributionBalanceHarness()
	h.users.users[21] = &User{ID: 21, Role: RoleUser, Status: StatusActive, Balance: 0}
	input := validTransferInput()
	input.TargetUserID = 21
	_, err := h.svc.Transfer(context.Background(), input)
	require.Error(t, err)
	require.Equal(t, domain.ErrReasonManagedUserNotFound, infraerrors.Reason(err))
	require.Empty(t, h.users.adjusts)
	require.Equal(t, 100.0, h.users.users[10].Balance)
}

func TestDistributionBalanceTransfer_RejectsInsufficientBalance(t *testing.T) {
	t.Parallel()
	h := newDistributionBalanceHarness()
	input := validTransferInput()
	input.Amount = 100.01
	_, err := h.svc.Transfer(context.Background(), input)
	require.Error(t, err)
	require.Equal(t, domain.ErrReasonInsufficientDistributionBalance, infraerrors.Reason(err))
	require.Empty(t, h.users.adjusts)
	require.Equal(t, 100.0, h.users.users[10].Balance)
	require.Equal(t, 5.0, h.users.users[20].Balance)
}

func TestDistributionBalanceTransfer_FrozenBalanceNotSubtractedFromAvailable(t *testing.T) {
	t.Parallel()
	h := newDistributionBalanceHarness()
	input := validTransferInput()
	input.Amount = 100
	got, err := h.svc.Transfer(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, 0.0, h.users.users[10].Balance)
	require.Equal(t, 7.0, h.users.users[10].FrozenBalance)
	require.Equal(t, 105.0, h.users.users[20].Balance)
	require.Equal(t, 0.0, got.SourceBalanceAfter)
}

func TestDistributionBalanceTransfer_RejectsNonAffiliateAdminActor(t *testing.T) {
	t.Parallel()
	h := newDistributionBalanceHarness()
	h.users.users[10].Role = RoleUser
	_, err := h.svc.Transfer(context.Background(), validTransferInput())
	require.Error(t, err)
	require.True(t, infraerrors.IsForbidden(err))
	require.Empty(t, h.users.adjusts)
}

func TestDistributionBalanceTransfer_SuccessInvalidatesCachesWithoutRebate(t *testing.T) {
	t.Parallel()
	h := newDistributionBalanceHarness()
	got, err := h.svc.Transfer(context.Background(), validTransferInput())
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, int64(1), got.ID)
	require.Equal(t, int64(10), got.AffiliateAdminID)
	require.Equal(t, int64(20), got.TargetUserID)
	require.Equal(t, 10.0, got.Amount)
	require.Equal(t, 100.0, got.SourceBalanceBefore)
	require.Equal(t, 90.0, got.SourceBalanceAfter)
	require.Equal(t, 5.0, got.TargetBalanceBefore)
	require.Equal(t, 15.0, got.TargetBalanceAfter)
	require.Equal(t, 90.0, h.users.users[10].Balance)
	require.Equal(t, 15.0, h.users.users[20].Balance)
	require.Equal(t, []distributionBalanceAdjust{{ID: 10, Delta: -10}, {ID: 20, Delta: 10}}, h.users.adjusts)
	require.Equal(t, 1, h.transfers.inserts)
	require.Equal(t, []int64{10, 20}, h.auth.userIDs)
	require.Equal(t, []int64{10, 20}, h.billing.userIDs)
	require.Nil(t, h.svc.entClient)
}

func TestDistributionBalanceTransfer_IdempotentReplayDoesNotDebitAgain(t *testing.T) {
	t.Parallel()
	h := newDistributionBalanceHarness()
	first, err := h.svc.Transfer(context.Background(), validTransferInput())
	require.NoError(t, err)
	second, err := h.svc.Transfer(context.Background(), validTransferInput())
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)
	require.Equal(t, 1, h.transfers.inserts)
	require.Equal(t, []distributionBalanceAdjust{{ID: 10, Delta: -10}, {ID: 20, Delta: 10}}, h.users.adjusts)
	require.Equal(t, 90.0, h.users.users[10].Balance)
	require.Equal(t, 15.0, h.users.users[20].Balance)
}

func TestDistributionBalanceTransfer_IdempotentConflictOnDifferentPayload(t *testing.T) {
	t.Parallel()
	h := newDistributionBalanceHarness()
	_, err := h.svc.Transfer(context.Background(), validTransferInput())
	require.NoError(t, err)
	input := validTransferInput()
	input.Amount = 11
	_, err = h.svc.Transfer(context.Background(), input)
	require.Error(t, err)
	require.Equal(t, domain.ErrReasonDistributionTransferConflict, infraerrors.Reason(err))
	require.Equal(t, 90.0, h.users.users[10].Balance)
}

func TestDistributionBalanceTransfer_InsertUniqueRaceReturnsExistingWithoutDoubleAdjust(t *testing.T) {
	t.Parallel()
	h := newDistributionBalanceHarness()
	existing := &DistributionBalanceTransfer{
		ID:               99,
		AffiliateAdminID: 10,
		TargetUserID:     20,
		Amount:           10,
		IdempotencyKey:   "k-1",
	}
	h.transfers.byKey["k-1"] = existing
	h.transfers.insertErr = ErrDistributionTransferUnique
	h.transfers.getMisses = 1

	got, err := h.svc.Transfer(context.Background(), validTransferInput())
	require.NoError(t, err)
	require.Equal(t, int64(99), got.ID)
	require.Equal(t, []distributionBalanceAdjust{{ID: 10, Delta: -10}, {ID: 20, Delta: 10}}, h.users.adjusts)
	require.Equal(t, 100.0, h.users.users[10].Balance, "unique conflict must roll back both AdjustBalance calls")
	require.Equal(t, 5.0, h.users.users[20].Balance)
	require.Empty(t, h.auth.userIDs)
	require.Empty(t, h.billing.userIDs)
}

func TestDistributionBalanceSummaryAndList(t *testing.T) {
	t.Parallel()
	h := newDistributionBalanceHarness()
	h.transfers.allocated = 42.5
	h.transfers.list = []DistributionBalanceTransfer{{ID: 3, Amount: 1}}
	h.transfers.listTotal = 1

	sum, err := h.svc.BalanceSummary(context.Background(), 10)
	require.NoError(t, err)
	require.Equal(t, 100.0, sum.Available)
	require.Equal(t, 7.0, sum.Frozen)
	require.Equal(t, 107.0, sum.Total)
	require.Equal(t, 42.5, sum.Allocated)

	rows, total, err := h.svc.ListTransfers(context.Background(), 10, 1, 20)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Equal(t, int64(3), rows[0].ID)
}
