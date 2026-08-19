package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type distDashboardStub struct {
	snapshot *service.DistributionDashboardSnapshot
}

func (s *distDashboardStub) GetSnapshot(context.Context, int64, time.Time, time.Time) (*service.DistributionDashboardSnapshot, error) {
	if s.snapshot != nil {
		return s.snapshot, nil
	}
	return &service.DistributionDashboardSnapshot{
		CustomerCount:       4,
		ActiveCustomerCount: 3,
		DistributionUsageSnapshot: service.DistributionUsageSnapshot{
			Requests:    12,
			TotalTokens: 340,
			ActualCost:  1.5,
		},
		Available: 88,
	}, nil
}

func (s *distDashboardStub) GetTrend(context.Context, int64, time.Time, time.Time, string, service.DistributionUsageFilter) ([]service.DistributionUsageTrendPoint, error) {
	return []service.DistributionUsageTrendPoint{}, nil
}

func (s *distDashboardStub) GetModelStats(context.Context, int64, time.Time, time.Time, int64) ([]service.DistributionUsageModelStat, error) {
	return []service.DistributionUsageModelStat{}, nil
}

func (s *distDashboardStub) GetRanking(context.Context, int64, time.Time, time.Time, string, int) ([]service.DistributionUsageUserRankingItem, error) {
	return []service.DistributionUsageUserRankingItem{}, nil
}

func (s *distDashboardStub) GetErrorSummary(context.Context, int64, time.Time, time.Time) (*service.DistributionUsageErrorSummary, error) {
	return &service.DistributionUsageErrorSummary{}, nil
}

func (s *distDashboardStub) GetUserUsageSummary(context.Context, int64, int64, time.Time, time.Time) (*service.DistributionUsageUserSummary, error) {
	return &service.DistributionUsageUserSummary{}, nil
}

type distBalanceStub struct{}

func (s *distBalanceStub) Transfer(_ context.Context, input service.DistributionBalanceTransferInput) (*service.DistributionBalanceTransfer, error) {
	return &service.DistributionBalanceTransfer{
		ID:           1,
		TargetUserID: input.TargetUserID,
		Amount:       input.Amount,
		Notes:        input.Notes,
		CreatedAt:    time.Unix(1, 0).UTC(),
	}, nil
}

func (s *distBalanceStub) BalanceSummary(context.Context, int64) (*service.DistributionBalanceSummary, error) {
	return &service.DistributionBalanceSummary{Available: 10}, nil
}

func (s *distBalanceStub) ListTransfers(context.Context, int64, int, int) ([]service.DistributionBalanceTransfer, int64, error) {
	return []service.DistributionBalanceTransfer{}, 0, nil
}

type distManagedAdminStub struct {
	service.AdminService
	managed map[int64]bool
}

func (s *distManagedAdminStub) UserIsManagedBy(_ context.Context, userID, _ int64) (bool, error) {
	return s.managed[userID], nil
}

func distributionRouter(role string, h *DistributionHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7})
		c.Set(string(middleware.ContextKeyUserRole), role)
		c.Next()
	})
	g := r.Group("/admin/distribution")
	g.Use(middleware.RequireAffiliateAdmin())
	g.GET("/dashboard/snapshot", h.GetDashboardSnapshot)
	g.GET("/groups", h.ListGroups)
	g.GET("/users/:id/usage/summary", h.GetUserUsageSummary)
	g.POST("/users/:id/balance-transfers", h.CreateBalanceTransfer)
	return r
}

func TestDistributionHandler_AffiliateSnapshotOK(t *testing.T) {
	h := &DistributionHandler{dashboard: &distDashboardStub{}}
	router := distributionRouter(service.RoleAffiliateAdmin, h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/admin/distribution/dashboard/snapshot", nil)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	require.Equal(t, float64(4), envelope.Data["customer_count"])
	require.Equal(t, float64(12), envelope.Data["today_requests"])
	require.Equal(t, float64(88), envelope.Data["available_balance"])
}

func TestDistributionHandler_RequireAffiliateAdminRejectsAdmin(t *testing.T) {
	h := &DistributionHandler{dashboard: &distDashboardStub{}}
	router := distributionRouter(service.RoleAdmin, h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/admin/distribution/dashboard/snapshot", nil)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code)
	require.Contains(t, w.Body.String(), "Affiliate admin access required")
}

func TestDistributionHandler_UnmanagedUserNotFound(t *testing.T) {
	h := &DistributionHandler{
		admin:     &distManagedAdminStub{managed: map[int64]bool{11: true}},
		dashboard: &distDashboardStub{},
	}
	router := distributionRouter(service.RoleAffiliateAdmin, h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/admin/distribution/users/99/usage/summary", nil)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code)
	var envelope struct {
		Reason string `json:"reason"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	require.Equal(t, "MANAGED_USER_NOT_FOUND", envelope.Reason)
}

func TestDistributionHandler_TransferRejectsInvalidAmount(t *testing.T) {
	h := &DistributionHandler{
		admin:    &distManagedAdminStub{managed: map[int64]bool{11: true}},
		balances: &distBalanceStub{},
	}
	router := distributionRouter(service.RoleAffiliateAdmin, h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(
		http.MethodPost,
		"/admin/distribution/users/11/balance-transfers",
		bytes.NewBufferString(`{"amount":0,"notes":"nope"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	var envelope struct {
		Reason string `json:"reason"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	require.Equal(t, "INVALID_TRANSFER_AMOUNT", envelope.Reason)
}

func TestDistributionHandler_ListGroupsFiltersCatalog(t *testing.T) {
	adminSvc := newStubAdminService()
	adminSvc.users = append(adminSvc.users, service.User{
		ID:            7,
		Email:         "aff@test.com",
		Role:          service.RoleAffiliateAdmin,
		Status:        service.StatusActive,
		AllowedGroups: []int64{2, 4},
	})
	adminSvc.groups = []service.Group{
		{ID: 2, Name: "allowed-active", Status: service.StatusActive},
		{ID: 3, Name: "not-allowed", Status: service.StatusActive},
		{ID: 4, Name: "allowed-inactive", Status: "inactive"},
	}

	h := &DistributionHandler{admin: adminSvc}
	router := distributionRouter(service.RoleAffiliateAdmin, h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/admin/distribution/groups", nil)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var envelope struct {
		Data []distributionGroupItem `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	require.Len(t, envelope.Data, 1)
	require.Equal(t, int64(2), envelope.Data[0].ID)
	require.Equal(t, "allowed-active", envelope.Data[0].Name)
}
