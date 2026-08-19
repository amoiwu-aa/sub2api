package service

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

type dashboardUsageStub struct {
	snapshot      *DistributionUsageSnapshot
	trend         []DistributionUsageTrendPoint
	models        []DistributionUsageModelStat
	ranking       []DistributionUsageUserRankingItem
	errors        *DistributionUsageErrorSummary
	userSummary   *DistributionUsageUserSummary
	snapshotCalls int
	trendCalls    int
	modelCalls    int
	rankingCalls  int
	errorCalls    int
	summaryCalls  int
	globalCalls   int
	lastAdminID   int64
	lastUserID    int64
	lastStart     time.Time
	lastEnd       time.Time
	lastFilter    DistributionUsageFilter
}

func (s *dashboardUsageStub) Snapshot(_ context.Context, adminID int64, start, end time.Time) (*DistributionUsageSnapshot, error) {
	s.snapshotCalls++
	s.lastAdminID = adminID
	s.lastStart = start
	s.lastEnd = end
	if s.snapshot == nil {
		return &DistributionUsageSnapshot{}, nil
	}
	return s.snapshot, nil
}

func (s *dashboardUsageStub) Trend(_ context.Context, adminID int64, start, end time.Time, _ string, filter DistributionUsageFilter) ([]DistributionUsageTrendPoint, error) {
	s.trendCalls++
	s.lastAdminID = adminID
	s.lastStart = start
	s.lastEnd = end
	s.lastFilter = filter
	if s.trend == nil {
		return []DistributionUsageTrendPoint{}, nil
	}
	return s.trend, nil
}

func (s *dashboardUsageStub) ModelStats(_ context.Context, adminID int64, start, end time.Time, userID int64) ([]DistributionUsageModelStat, error) {
	s.modelCalls++
	s.lastAdminID = adminID
	s.lastStart = start
	s.lastEnd = end
	s.lastUserID = userID
	if s.models == nil {
		return []DistributionUsageModelStat{}, nil
	}
	return s.models, nil
}

func (s *dashboardUsageStub) UserRanking(_ context.Context, adminID int64, start, end time.Time, _ string, _ int) ([]DistributionUsageUserRankingItem, error) {
	s.rankingCalls++
	s.lastAdminID = adminID
	s.lastStart = start
	s.lastEnd = end
	if s.ranking == nil {
		return []DistributionUsageUserRankingItem{}, nil
	}
	return s.ranking, nil
}

func (s *dashboardUsageStub) ErrorSummary(_ context.Context, adminID int64, start, end time.Time) (*DistributionUsageErrorSummary, error) {
	s.errorCalls++
	s.lastAdminID = adminID
	s.lastStart = start
	s.lastEnd = end
	if s.errors == nil {
		return &DistributionUsageErrorSummary{}, nil
	}
	return s.errors, nil
}

func (s *dashboardUsageStub) UserSummary(_ context.Context, adminID, userID int64, start, end time.Time) (*DistributionUsageUserSummary, error) {
	s.summaryCalls++
	s.lastAdminID = adminID
	s.lastUserID = userID
	s.lastStart = start
	s.lastEnd = end
	if s.userSummary == nil {
		return &DistributionUsageUserSummary{UserID: userID}, nil
	}
	return s.userSummary, nil
}

func (s *dashboardUsageStub) GetDashboardStats(context.Context) (*usagestats.DashboardStats, error) {
	s.globalCalls++
	return &usagestats.DashboardStats{}, nil
}

func (s *dashboardUsageStub) GetDashboardStatsWithRange(context.Context, time.Time, time.Time) (*usagestats.DashboardStats, error) {
	s.globalCalls++
	return &usagestats.DashboardStats{}, nil
}

type dashboardUserStub struct {
	managed       bool
	managedErr    error
	ownerCalls    int
	admin         *User
	getByIDCalls  int
	getByIDLastID int64
	counts        map[string]int64
	countCalls    int
}

func (s *dashboardUserStub) UserIsManagedBy(_ context.Context, _, _ int64) (bool, error) {
	s.ownerCalls++
	return s.managed, s.managedErr
}

func (s *dashboardUserStub) GetByID(_ context.Context, id int64) (*User, error) {
	s.getByIDCalls++
	s.getByIDLastID = id
	if s.admin == nil {
		return nil, ErrUserNotFound
	}
	return s.admin, nil
}

func (s *dashboardUserStub) CountManagedUsers(_ context.Context, _ int64, status string) (int64, error) {
	s.countCalls++
	if s.counts == nil {
		return 0, nil
	}
	return s.counts[status], nil
}

type dashboardListOnlyUserStub struct {
	totals     map[string]int64
	listCalls  int
	filters    []UserListFilters
	admin      *User
	managed    bool
	ownerCalls int
}

func (s *dashboardListOnlyUserStub) UserIsManagedBy(_ context.Context, _, _ int64) (bool, error) {
	s.ownerCalls++
	return s.managed, nil
}

func (s *dashboardListOnlyUserStub) GetByID(_ context.Context, _ int64) (*User, error) {
	if s.admin == nil {
		return nil, ErrUserNotFound
	}
	return s.admin, nil
}

func (s *dashboardListOnlyUserStub) ListWithFilters(_ context.Context, params pagination.PaginationParams, filters UserListFilters) ([]User, *pagination.PaginationResult, error) {
	s.listCalls++
	s.filters = append(s.filters, filters)
	total := int64(0)
	if s.totals != nil {
		total = s.totals[filters.Status]
	}
	return []User{}, &pagination.PaginationResult{Total: total, Page: params.Page, PageSize: params.PageSize}, nil
}

type dashboardBalanceStub struct {
	allocated float64
	calls     int
	lastID    int64
}

func (s *dashboardBalanceStub) GetTransferByIdempotency(context.Context, int64, string) (*DistributionBalanceTransfer, error) {
	return nil, nil
}
func (s *dashboardBalanceStub) InsertTransfer(context.Context, *DistributionBalanceTransfer) error {
	return nil
}
func (s *dashboardBalanceStub) ListTransfers(context.Context, int64, int, int) ([]DistributionBalanceTransfer, int64, error) {
	return nil, 0, nil
}
func (s *dashboardBalanceStub) SumSuccessfulAllocated(_ context.Context, adminID int64) (float64, error) {
	s.calls++
	s.lastID = adminID
	return s.allocated, nil
}
func (s *dashboardBalanceStub) LockUsersForUpdate(context.Context, int64, int64) (map[int64]LockedDistributionUser, error) {
	return nil, nil
}

func dashboardRange() (time.Time, time.Time) {
	end := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	return end.Add(-24 * time.Hour), end
}

func TestDistributionDashboardService_UnmanagedUserIDDoesNotHitRepo(t *testing.T) {
	t.Parallel()

	usage := &dashboardUsageStub{
		trend:       []DistributionUsageTrendPoint{{Date: "2026-08-18", Requests: 9}},
		userSummary: &DistributionUsageUserSummary{UserID: 99, Email: "leak"},
	}
	users := &dashboardUserStub{managed: false}
	svc := NewDistributionDashboardService(usage, users, nil)
	start, end := dashboardRange()

	trend, err := svc.GetTrend(context.Background(), 7, start, end, DistributionUsageGranularityDay, DistributionUsageFilter{UserID: 99})
	require.Error(t, err)
	require.True(t, infraerrors.IsNotFound(err))
	require.Equal(t, domain.ErrReasonManagedUserNotFound, infraerrors.Reason(err))
	require.Nil(t, trend)
	require.Equal(t, 1, users.ownerCalls)
	require.Equal(t, 0, usage.trendCalls)

	summary, err := svc.GetUserUsageSummary(context.Background(), 7, 99, start, end)
	require.Error(t, err)
	require.True(t, infraerrors.IsNotFound(err))
	require.Equal(t, domain.ErrReasonManagedUserNotFound, infraerrors.Reason(err))
	require.Nil(t, summary)
	require.Equal(t, 2, users.ownerCalls)
	require.Equal(t, 0, usage.summaryCalls)

	models, err := svc.GetModelStats(context.Background(), 7, start, end, 99)
	require.Error(t, err)
	require.True(t, infraerrors.IsNotFound(err))
	require.Nil(t, models)
	require.Equal(t, 0, usage.modelCalls)
	require.Equal(t, 0, usage.globalCalls)
}

func TestDistributionDashboardService_AdminIDNonPositiveReturnsEmpty(t *testing.T) {
	t.Parallel()

	usage := &dashboardUsageStub{
		snapshot: &DistributionUsageSnapshot{Requests: 5},
		trend:    []DistributionUsageTrendPoint{{Date: "x"}},
	}
	users := &dashboardUserStub{
		managed: true,
		admin:   &User{ID: 7, Balance: 10, FrozenBalance: 2},
		counts:  map[string]int64{"": 4},
	}
	balances := &dashboardBalanceStub{allocated: 3}
	svc := NewDistributionDashboardService(usage, users, balances)
	start, end := dashboardRange()

	for _, adminID := range []int64{0, -1} {
		snap, err := svc.GetSnapshot(context.Background(), adminID, start, end)
		require.NoError(t, err)
		require.Equal(t, &DistributionDashboardSnapshot{}, snap)

		trend, err := svc.GetTrend(context.Background(), adminID, start, end, "day", DistributionUsageFilter{UserID: 11})
		require.NoError(t, err)
		require.Empty(t, trend)

		models, err := svc.GetModelStats(context.Background(), adminID, start, end, 11)
		require.NoError(t, err)
		require.Empty(t, models)

		ranking, err := svc.GetRanking(context.Background(), adminID, start, end, "actual", 10)
		require.NoError(t, err)
		require.Empty(t, ranking)

		errorsum, err := svc.GetErrorSummary(context.Background(), adminID, start, end)
		require.NoError(t, err)
		require.Equal(t, &DistributionUsageErrorSummary{}, errorsum)

		summary, err := svc.GetUserUsageSummary(context.Background(), adminID, 11, start, end)
		require.NoError(t, err)
		require.Equal(t, &DistributionUsageUserSummary{}, summary)
	}

	require.Equal(t, 0, usage.snapshotCalls)
	require.Equal(t, 0, usage.trendCalls)
	require.Equal(t, 0, usage.modelCalls)
	require.Equal(t, 0, usage.rankingCalls)
	require.Equal(t, 0, usage.errorCalls)
	require.Equal(t, 0, usage.summaryCalls)
	require.Equal(t, 0, usage.globalCalls)
	require.Equal(t, 0, users.ownerCalls)
	require.Equal(t, 0, users.getByIDCalls)
	require.Equal(t, 0, users.countCalls)
	require.Equal(t, 0, balances.calls)
}

func TestDistributionDashboardService_GetSnapshotComposesUsageAndBalance(t *testing.T) {
	t.Parallel()

	usage := &dashboardUsageStub{snapshot: &DistributionUsageSnapshot{
		Requests:    12,
		TotalTokens: 340,
		ActualCost:  1.25,
		ActiveUsers: 3,
	}}
	users := &dashboardUserStub{
		admin: &User{ID: 7, Balance: 88.5, FrozenBalance: 4.5},
		counts: map[string]int64{
			"":             6,
			StatusActive:   4,
			StatusDisabled: 2,
		},
	}
	balances := &dashboardBalanceStub{allocated: 15.75}
	svc := NewDistributionDashboardService(usage, users, balances)
	start, end := dashboardRange()

	snap, err := svc.GetSnapshot(context.Background(), 7, start, end)
	require.NoError(t, err)
	require.Equal(t, int64(12), snap.Requests)
	require.Equal(t, int64(340), snap.TotalTokens)
	require.InDelta(t, 1.25, snap.ActualCost, 1e-9)
	require.Equal(t, int64(3), snap.ActiveUsers)
	require.Equal(t, int64(6), snap.CustomerCount)
	require.Equal(t, int64(4), snap.ActiveCustomerCount)
	require.Equal(t, int64(2), snap.DisabledCustomerCount)
	require.InDelta(t, 88.5, snap.Available, 1e-9)
	require.InDelta(t, 4.5, snap.Frozen, 1e-9)
	require.InDelta(t, 15.75, snap.Allocated, 1e-9)
	require.Equal(t, 1, usage.snapshotCalls)
	require.Equal(t, int64(7), usage.lastAdminID)
	require.Equal(t, 1, users.getByIDCalls)
	require.Equal(t, int64(7), users.getByIDLastID)
	require.Equal(t, 3, users.countCalls)
	require.Equal(t, 1, balances.calls)
	require.Equal(t, int64(7), balances.lastID)
	require.Equal(t, 0, usage.globalCalls)
	require.Equal(t, 0, usage.trendCalls)
	require.Equal(t, 0, usage.rankingCalls)
}

func TestDistributionDashboardService_GetSnapshotFallsBackToListWithFilters(t *testing.T) {
	t.Parallel()

	usage := &dashboardUsageStub{snapshot: &DistributionUsageSnapshot{Requests: 1}}
	users := &dashboardListOnlyUserStub{
		totals: map[string]int64{
			"":             9,
			StatusActive:   7,
			StatusDisabled: 2,
		},
		admin: &User{ID: 7, Balance: 3, FrozenBalance: 1},
	}
	svc := NewDistributionDashboardService(usage, users, nil)
	start, end := dashboardRange()

	snap, err := svc.GetSnapshot(context.Background(), 7, start, end)
	require.NoError(t, err)
	require.Equal(t, int64(9), snap.CustomerCount)
	require.Equal(t, int64(7), snap.ActiveCustomerCount)
	require.Equal(t, int64(2), snap.DisabledCustomerCount)
	require.InDelta(t, 3.0, snap.Available, 1e-9)
	require.InDelta(t, 1.0, snap.Frozen, 1e-9)
	require.Equal(t, 3, users.listCalls)
	require.Len(t, users.filters, 3)
	for _, filters := range users.filters {
		require.Equal(t, int64(7), filters.ManagedByAdminID)
		require.Equal(t, RoleUser, filters.Role)
		require.NotNil(t, filters.IncludeSubscriptions)
		require.False(t, *filters.IncludeSubscriptions)
	}
}

func TestDistributionDashboardService_NeverCallsGlobalDashboard(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile("distribution_dashboard_service.go")
	require.NoError(t, err)
	text := string(src)
	require.NotContains(t, text, "usage.GetDashboardStats")
	require.NotContains(t, text, "usage_dashboard_daily")
	require.NotContains(t, text, "dashboard:stats")
	require.NotContains(t, text, "ops_error_logs")

	usage := &dashboardUsageStub{snapshot: &DistributionUsageSnapshot{Requests: 2}}
	users := &dashboardUserStub{
		managed: true,
		admin:   &User{ID: 7, Balance: 1},
		counts:  map[string]int64{"": 1, StatusActive: 1},
	}
	svc := NewDistributionDashboardService(usage, users, &dashboardBalanceStub{})
	start, end := dashboardRange()

	_, err = svc.GetSnapshot(context.Background(), 7, start, end)
	require.NoError(t, err)
	_, err = svc.GetTrend(context.Background(), 7, start, end, "day", DistributionUsageFilter{})
	require.NoError(t, err)
	_, err = svc.GetModelStats(context.Background(), 7, start, end, 0)
	require.NoError(t, err)
	_, err = svc.GetRanking(context.Background(), 7, start, end, "actual", 10)
	require.NoError(t, err)
	_, err = svc.GetErrorSummary(context.Background(), 7, start, end)
	require.NoError(t, err)
	_, err = svc.GetUserUsageSummary(context.Background(), 7, 11, start, end)
	require.NoError(t, err)
	require.Equal(t, 0, usage.globalCalls)
}

func TestDistributionDashboardService_GetTrendClampsRangeAndPassesManagedFilter(t *testing.T) {
	t.Parallel()

	usage := &dashboardUsageStub{trend: []DistributionUsageTrendPoint{{Date: "2026-08-18"}}}
	users := &dashboardUserStub{managed: true}
	svc := NewDistributionDashboardService(usage, users, nil)
	end := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	start := end.Add(-40 * 24 * time.Hour)

	got, err := svc.GetTrend(context.Background(), 7, start, end, DistributionUsageGranularityDay, DistributionUsageFilter{UserID: 11, Model: "claude"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, 1, users.ownerCalls)
	require.Equal(t, 1, usage.trendCalls)
	require.Equal(t, int64(7), usage.lastAdminID)
	require.Equal(t, int64(11), usage.lastFilter.UserID)
	require.Equal(t, "claude", usage.lastFilter.Model)
	require.Equal(t, end, usage.lastEnd)
	require.Equal(t, end.Add(-31*24*time.Hour), usage.lastStart)
}

func TestDistributionDashboardService_GetUserUsageSummaryManagedPassesThrough(t *testing.T) {
	t.Parallel()

	usage := &dashboardUsageStub{userSummary: &DistributionUsageUserSummary{
		UserID:                    11,
		Email:                     "u@example.com",
		DistributionUsageSnapshot: DistributionUsageSnapshot{Requests: 4},
	}}
	users := &dashboardUserStub{managed: true}
	svc := NewDistributionDashboardService(usage, users, nil)
	start, end := dashboardRange()

	got, err := svc.GetUserUsageSummary(context.Background(), 7, 11, start, end)
	require.NoError(t, err)
	require.Equal(t, int64(11), got.UserID)
	require.Equal(t, int64(4), got.Requests)
	require.Equal(t, 1, usage.summaryCalls)
	require.Equal(t, int64(7), usage.lastAdminID)
	require.Equal(t, int64(11), usage.lastUserID)
}
