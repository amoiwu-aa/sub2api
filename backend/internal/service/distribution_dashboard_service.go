package service

import (
	"context"
	"errors"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

// DistributionDashboardSnapshot composes scoped usage totals with the
// administrator's customer counts and own balance. It is never sourced from
// the global admin dashboard or daily aggregation tables.
type DistributionDashboardSnapshot struct {
	DistributionUsageSnapshot
	CustomerCount         int64   `json:"customer_count"`
	ActiveCustomerCount   int64   `json:"active_customer_count"`
	DisabledCustomerCount int64   `json:"disabled_customer_count"`
	Available             float64 `json:"available"`
	Frozen                float64 `json:"frozen"`
	Allocated             float64 `json:"allocated"`
}

// distributionUserOwner is the ownership gate for a single-user drill-down.
type distributionUserOwner interface {
	UserIsManagedBy(ctx context.Context, userID, adminID int64) (bool, error)
}

// distributionUserReader loads the administrator's own balance row.
type distributionUserReader interface {
	GetByID(ctx context.Context, id int64) (*User, error)
}

// distributionUserCounter is a cheap managed-user tally. status is empty for
// all statuses, or StatusActive / StatusDisabled.
type distributionUserCounter interface {
	CountManagedUsers(ctx context.Context, adminID int64, status string) (int64, error)
}

// distributionUserLister is the ListWithFilters fallback used when the user
// store does not implement CountManagedUsers.
type distributionUserLister interface {
	ListWithFilters(ctx context.Context, params pagination.PaginationParams, filters UserListFilters) ([]User, *pagination.PaginationResult, error)
}

type listWithFiltersManagedUserCounter struct {
	lister distributionUserLister
}

func (c listWithFiltersManagedUserCounter) CountManagedUsers(ctx context.Context, adminID int64, status string) (int64, error) {
	if c.lister == nil || adminID <= 0 {
		return 0, nil
	}
	includeSubs := false
	_, result, err := c.lister.ListWithFilters(ctx, pagination.PaginationParams{Page: 1, PageSize: 1}, UserListFilters{
		Status:               status,
		Role:                 RoleUser,
		ManagedByAdminID:     adminID,
		IncludeSubscriptions: &includeSubs,
	})
	if err != nil {
		return 0, err
	}
	if result == nil {
		return 0, nil
	}
	return result.Total, nil
}

// DistributionDashboardService wraps DistributionUsageRepository with an
// ownership check and composes customer / balance fields for the snapshot.
// Callers must pass a single optional userID — never a client-provided []userID.
type DistributionDashboardService struct {
	usage    DistributionUsageRepository
	owner    distributionUserOwner
	reader   distributionUserReader
	counter  distributionUserCounter
	balances DistributionBalanceRepository
}

// NewDistributionDashboardService wires the scoped usage repo with optional
// user and balance stores. users may implement UserIsManagedBy, GetByID,
// CountManagedUsers, and/or ListWithFilters; capabilities are detected by
// type assertion. There is no cache.
func NewDistributionDashboardService(usage DistributionUsageRepository, users any, balances DistributionBalanceRepository) *DistributionDashboardService {
	s := &DistributionDashboardService{
		usage:    usage,
		balances: balances,
	}
	bindDistributionDashboardUsers(s, users)
	return s
}

func bindDistributionDashboardUsers(s *DistributionDashboardService, users any) {
	if s == nil || users == nil {
		return
	}
	if owner, ok := users.(distributionUserOwner); ok {
		s.owner = owner
	}
	if reader, ok := users.(distributionUserReader); ok {
		s.reader = reader
	}
	if counter, ok := users.(distributionUserCounter); ok {
		s.counter = counter
		return
	}
	if lister, ok := users.(distributionUserLister); ok {
		s.counter = listWithFiltersManagedUserCounter{lister: lister}
	}
}

func clampDistributionDashboardRange(start, end time.Time) (time.Time, time.Time, bool) {
	start = start.UTC()
	end = end.UTC()
	if !end.After(start) {
		return start, end, false
	}
	maxRange := time.Duration(DistributionUsageMaxRangeDays) * 24 * time.Hour
	if end.Sub(start) > maxRange {
		start = end.Add(-maxRange)
	}
	return start, end, true
}

func (s *DistributionDashboardService) requireManagedUser(ctx context.Context, adminID, userID int64) error {
	if userID <= 0 {
		return errManagedUserNotFound()
	}
	if s == nil || s.owner == nil {
		return errManagedUserNotFound()
	}
	ok, err := s.owner.UserIsManagedBy(ctx, userID, adminID)
	if err != nil {
		return err
	}
	if !ok {
		return errManagedUserNotFound()
	}
	return nil
}

// GetSnapshot composes scoped usage, managed-user counts, and the
// administrator's own balance. It does not read the global dashboard.
func (s *DistributionDashboardService) GetSnapshot(ctx context.Context, adminID int64, start, end time.Time) (*DistributionDashboardSnapshot, error) {
	out := &DistributionDashboardSnapshot{}
	if s == nil || adminID <= 0 {
		return out, nil
	}
	if s.usage != nil {
		if clampedStart, clampedEnd, ok := clampDistributionDashboardRange(start, end); ok {
			snap, err := s.usage.Snapshot(ctx, adminID, clampedStart, clampedEnd)
			if err != nil {
				return nil, err
			}
			if snap != nil {
				out.DistributionUsageSnapshot = *snap
			}
		}
	}
	if err := s.fillCustomerCounts(ctx, adminID, out); err != nil {
		return nil, err
	}
	if err := s.fillBalance(ctx, adminID, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *DistributionDashboardService) fillCustomerCounts(ctx context.Context, adminID int64, out *DistributionDashboardSnapshot) error {
	if s.counter == nil || out == nil {
		return nil
	}
	total, err := s.counter.CountManagedUsers(ctx, adminID, "")
	if err != nil {
		return err
	}
	out.CustomerCount = total

	active, err := s.counter.CountManagedUsers(ctx, adminID, StatusActive)
	if err != nil {
		return err
	}
	out.ActiveCustomerCount = active

	disabled, err := s.counter.CountManagedUsers(ctx, adminID, StatusDisabled)
	if err != nil {
		return err
	}
	out.DisabledCustomerCount = disabled
	return nil
}

func (s *DistributionDashboardService) fillBalance(ctx context.Context, adminID int64, out *DistributionDashboardSnapshot) error {
	if out == nil {
		return nil
	}
	if s.reader != nil {
		admin, err := s.reader.GetByID(ctx, adminID)
		if err != nil {
			if !errors.Is(err, ErrUserNotFound) && !infraerrors.IsNotFound(err) {
				return err
			}
		} else if admin != nil {
			out.Available = admin.Balance
			out.Frozen = admin.FrozenBalance
		}
	}
	if s.balances != nil {
		allocated, err := s.balances.SumSuccessfulAllocated(ctx, adminID)
		if err != nil {
			return err
		}
		out.Allocated = allocated
	}
	return nil
}

// GetTrend returns the scoped usage trend. A positive filter.UserID is
// rejected unless that user is managed by adminID.
func (s *DistributionDashboardService) GetTrend(ctx context.Context, adminID int64, start, end time.Time, granularity string, filter DistributionUsageFilter) ([]DistributionUsageTrendPoint, error) {
	empty := make([]DistributionUsageTrendPoint, 0)
	if s == nil || s.usage == nil || adminID <= 0 {
		return empty, nil
	}
	if filter.UserID > 0 {
		if err := s.requireManagedUser(ctx, adminID, filter.UserID); err != nil {
			return nil, err
		}
	}
	start, end, ok := clampDistributionDashboardRange(start, end)
	if !ok {
		return empty, nil
	}
	return s.usage.Trend(ctx, adminID, start, end, granularity, filter)
}

// GetModelStats returns scoped per-model totals. userID>0 requires ownership.
func (s *DistributionDashboardService) GetModelStats(ctx context.Context, adminID int64, start, end time.Time, userID int64) ([]DistributionUsageModelStat, error) {
	empty := make([]DistributionUsageModelStat, 0)
	if s == nil || s.usage == nil || adminID <= 0 {
		return empty, nil
	}
	if userID > 0 {
		if err := s.requireManagedUser(ctx, adminID, userID); err != nil {
			return nil, err
		}
	}
	start, end, ok := clampDistributionDashboardRange(start, end)
	if !ok {
		return empty, nil
	}
	return s.usage.ModelStats(ctx, adminID, start, end, userID)
}

// GetRanking returns the scoped user ranking. Scope is the managed-user CTE
// only; callers cannot pass a user-id list.
func (s *DistributionDashboardService) GetRanking(ctx context.Context, adminID int64, start, end time.Time, sort string, limit int) ([]DistributionUsageUserRankingItem, error) {
	empty := make([]DistributionUsageUserRankingItem, 0)
	if s == nil || s.usage == nil || adminID <= 0 {
		return empty, nil
	}
	start, end, ok := clampDistributionDashboardRange(start, end)
	if !ok {
		return empty, nil
	}
	return s.usage.UserRanking(ctx, adminID, start, end, sort, limit)
}

// GetErrorSummary returns billed vs failed/unbilled counts from usage_logs.
func (s *DistributionDashboardService) GetErrorSummary(ctx context.Context, adminID int64, start, end time.Time) (*DistributionUsageErrorSummary, error) {
	if s == nil || s.usage == nil || adminID <= 0 {
		return &DistributionUsageErrorSummary{}, nil
	}
	start, end, ok := clampDistributionDashboardRange(start, end)
	if !ok {
		return &DistributionUsageErrorSummary{}, nil
	}
	return s.usage.ErrorSummary(ctx, adminID, start, end)
}

// GetUserUsageSummary returns one managed user's usage. Unmanaged IDs are
// NotFound MANAGED_USER_NOT_FOUND and never reach the usage repo.
func (s *DistributionDashboardService) GetUserUsageSummary(ctx context.Context, adminID int64, userID int64, start, end time.Time) (*DistributionUsageUserSummary, error) {
	if s == nil || adminID <= 0 {
		return &DistributionUsageUserSummary{}, nil
	}
	if err := s.requireManagedUser(ctx, adminID, userID); err != nil {
		return nil, err
	}
	if s.usage == nil {
		return &DistributionUsageUserSummary{}, nil
	}
	start, end, ok := clampDistributionDashboardRange(start, end)
	if !ok {
		return &DistributionUsageUserSummary{}, nil
	}
	return s.usage.UserSummary(ctx, adminID, userID, start, end)
}
