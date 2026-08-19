package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func affiliateGroupRouter(svc service.AdminService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7})
		c.Set(string(middleware.ContextKeyUserRole), service.RoleAffiliateAdmin)
		c.Next()
	})
	r.GET("/admin/groups/all", NewGroupHandler(svc, nil, nil).GetAll)
	return r
}

func TestGroupHandlerGetAll_AffiliateAdminFiltersToAllowedActive(t *testing.T) {
	adminSvc := newStubAdminService()
	adminSvc.users = append(adminSvc.users, service.User{
		ID:            7,
		Email:         "aff@test.com",
		Role:          service.RoleAffiliateAdmin,
		Status:        service.StatusActive,
		AllowedGroups: []int64{2, 4},
	})
	adminSvc.groups = []service.Group{
		{ID: 2, Name: "allowed-active", Status: service.StatusActive, IsExclusive: true, SubscriptionType: service.SubscriptionTypeStandard},
		{ID: 3, Name: "not-allowed", Status: service.StatusActive, IsExclusive: false, SubscriptionType: service.SubscriptionTypeStandard},
		{ID: 4, Name: "allowed-inactive", Status: "inactive", IsExclusive: false, SubscriptionType: service.SubscriptionTypeStandard},
		{ID: 5, Name: "allowed-public", Status: service.StatusActive, IsExclusive: false, SubscriptionType: service.SubscriptionTypeStandard},
	}

	router := affiliateGroupRouter(adminSvc)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/admin/groups/all?include_inactive=true", nil)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var envelope struct {
		Data []map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	require.Len(t, envelope.Data, 1)
	require.Equal(t, float64(2), envelope.Data[0]["id"])
	require.Equal(t, true, envelope.Data[0]["is_exclusive"])
	require.Equal(t, service.SubscriptionTypeStandard, envelope.Data[0]["subscription_type"])
	require.NotContains(t, envelope.Data[0], "model_routing")
}

func TestGroupHandlerGetAll_AffiliateAdminGetUserFailureReturnsEmpty(t *testing.T) {
	adminSvc := newStubAdminService()
	adminSvc.getUserErr = errors.New("store down")
	adminSvc.groups = []service.Group{
		{ID: 2, Name: "secret", Status: service.StatusActive},
	}

	router := affiliateGroupRouter(adminSvc)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/admin/groups/all", nil)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var envelope struct {
		Data []map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	require.Empty(t, envelope.Data)
}
