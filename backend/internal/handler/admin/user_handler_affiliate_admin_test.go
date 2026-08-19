package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type affiliateUserAdminStub struct {
	service.AdminService
	managed       map[int64]bool
	listFilters   service.UserListFilters
	listSortBy    string
	listSortOrder string
	createInput   *service.CreateUserInput
	updateInput   *service.UpdateUserInput
	deletedID     int64
}

func (s *affiliateUserAdminStub) ListUsers(_ context.Context, _, _ int, filters service.UserListFilters, sortBy, sortOrder string) ([]service.User, int64, error) {
	s.listFilters = filters
	s.listSortBy = sortBy
	s.listSortOrder = sortOrder
	return []service.User{{
		ID: 11, Email: "owned@test.com", Role: service.RoleUser, Status: service.StatusActive,
		Balance: 99, FrozenBalance: 4, Concurrency: 8, RPMLimit: 60,
		AllowedGroups: []int64{3}, GroupRates: map[int64]float64{3: 1.5},
		Subscriptions: []service.UserSubscription{{ID: 1}},
	}}, 1, nil
}

func (s *affiliateUserAdminStub) UserIsManagedBy(_ context.Context, userID, _ int64) (bool, error) {
	return s.managed[userID], nil
}

func (s *affiliateUserAdminStub) GetUser(_ context.Context, id int64) (*service.User, error) {
	return &service.User{
		ID: id, Email: "owned@test.com", Role: service.RoleUser, Status: service.StatusActive,
		Balance: 99, FrozenBalance: 4, Concurrency: 8, RPMLimit: 60,
		AllowedGroups: []int64{3}, GroupRates: map[int64]float64{3: 1.5},
		Subscriptions: []service.UserSubscription{{ID: 1}},
	}, nil
}

func (s *affiliateUserAdminStub) CreateUser(_ context.Context, input *service.CreateUserInput) (*service.User, error) {
	copied := *input
	s.createInput = &copied
	return &service.User{ID: 99, Email: input.Email, Role: input.Role, Status: service.StatusActive}, nil
}

func (s *affiliateUserAdminStub) UpdateUser(_ context.Context, id int64, input *service.UpdateUserInput) (*service.User, error) {
	copied := *input
	s.updateInput = &copied
	return &service.User{ID: id, Email: "owned@test.com", Role: service.RoleUser, Status: service.StatusActive}, nil
}

func (s *affiliateUserAdminStub) DeleteUser(_ context.Context, id int64) error {
	s.deletedID = id
	return nil
}

func affiliateAdminRouter(svc service.AdminService, method, path string, handler gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7})
		c.Set(string(middleware.ContextKeyUserRole), service.RoleAffiliateAdmin)
		c.Next()
	})
	r.Handle(method, path, handler)
	return r
}

func TestAffiliateAdminList_ForcesManagedScope(t *testing.T) {
	stub := &affiliateUserAdminStub{
		AdminService: newStubAdminService(),
		managed:      map[int64]bool{11: true},
	}
	h := NewUserHandler(stub, nil, nil, nil, nil, nil, nil)
	router := affiliateAdminRouter(stub, http.MethodGet, "/admin/users", h.List)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/admin/users?role=admin&group_name=secret&api_key_group_id=3&attr[1]=hidden&include_subscriptions=true&sort_by=concurrency&sort_order=asc", nil)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, int64(7), stub.listFilters.ManagedByAdminID)
	require.Equal(t, service.RoleUser, stub.listFilters.Role)
	require.Equal(t, "secret", stub.listFilters.GroupName)
	require.Zero(t, stub.listFilters.APIKeyGroupID)
	require.Empty(t, stub.listFilters.Attributes)
	require.NotNil(t, stub.listFilters.IncludeSubscriptions)
	require.False(t, *stub.listFilters.IncludeSubscriptions)
	require.Equal(t, "created_at", stub.listSortBy)
	require.Equal(t, "desc", stub.listSortOrder)

	var envelope struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	require.Len(t, envelope.Data.Items, 1)
	assertAffiliateAdminUserIsRedacted(t, envelope.Data.Items[0])
}

func TestAffiliateAdminGetUpdateDelete_RejectsUnmanagedUser(t *testing.T) {
	stub := &affiliateUserAdminStub{
		AdminService: newStubAdminService(),
		managed:      map[int64]bool{11: true},
	}
	h := NewUserHandler(stub, nil, nil, nil, nil, nil, nil)

	t.Run("get 403", func(t *testing.T) {
		router := affiliateAdminRouter(stub, http.MethodGet, "/admin/users/:id", h.GetByID)
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodGet, "/admin/users/99", nil)
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("update 403", func(t *testing.T) {
		router := affiliateAdminRouter(stub, http.MethodPut, "/admin/users/:id", h.Update)
		w := httptest.NewRecorder()
		body, _ := json.Marshal(map[string]any{"email": "x@test.com"})
		req, _ := http.NewRequest(http.MethodPut, "/admin/users/99", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusForbidden, w.Code)
		require.Nil(t, stub.updateInput)
	})

	t.Run("delete 403", func(t *testing.T) {
		router := affiliateAdminRouter(stub, http.MethodDelete, "/admin/users/:id", h.Delete)
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodDelete, "/admin/users/99", nil)
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusForbidden, w.Code)
		require.Zero(t, stub.deletedID)
	})
}

func TestAffiliateAdminGetUpdateDelete_AllowsManagedUser(t *testing.T) {
	stub := &affiliateUserAdminStub{
		AdminService: newStubAdminService(),
		managed:      map[int64]bool{11: true},
	}
	h := NewUserHandler(stub, nil, nil, nil, nil, nil, nil)

	router := affiliateAdminRouter(stub, http.MethodGet, "/admin/users/:id", h.GetByID)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/admin/users/11", nil)
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var envelope struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	assertAffiliateAdminUserIsRedacted(t, envelope.Data)
}

func TestAffiliateAdminCreate_ForcesUserRoleAndDropsPrivilegedFields(t *testing.T) {
	stub := &affiliateUserAdminStub{AdminService: newStubAdminService()}
	h := NewUserHandler(stub, nil, nil, nil, nil, nil, nil)
	router := affiliateAdminRouter(stub, http.MethodPost, "/admin/users", h.Create)

	body, _ := json.Marshal(map[string]any{
		"email":          "new@test.com",
		"password":       "secret1",
		"role":           "admin",
		"balance":        99.5,
		"concurrency":    9,
		"rpm_limit":      30,
		"allowed_groups": []int64{3},
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/admin/users", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, stub.createInput)
	require.Equal(t, service.RoleUser, stub.createInput.Role)
	require.Nil(t, stub.createInput.Balance)
	require.Equal(t, []int64{3}, stub.createInput.AllowedGroups)
	require.Zero(t, stub.createInput.Concurrency)
	require.Zero(t, stub.createInput.RPMLimit)
	require.Equal(t, int64(7), stub.createInput.ActorAdminID)
	require.Equal(t, service.RoleAffiliateAdmin, stub.createInput.ActorRole)

	var envelope struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	assertAffiliateAdminUserIsRedacted(t, envelope.Data)
}

func TestRequireSuperAdmin_BlocksAffiliateAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7})
		c.Set(string(middleware.ContextKeyUserRole), service.RoleAffiliateAdmin)
		c.Next()
	})
	r.GET("/admin/settings", middleware.RequireSuperAdmin(), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/admin/settings", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusForbidden, w.Code)
}

func TestRequireSuperAdmin_AllowsSuperAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
		c.Set(string(middleware.ContextKeyUserRole), service.RoleAdmin)
		c.Next()
	})
	r.GET("/admin/settings", middleware.RequireSuperAdmin(), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/admin/settings", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
}

func assertAffiliateAdminUserIsRedacted(t *testing.T, user map[string]any) {
	t.Helper()
	for _, field := range []string{
		"role",
		"frozen_balance",
		"concurrency",
		"current_concurrency",
		"rpm_limit",
		"group_rates",
		"api_keys",
		"subscriptions",
		"total_recharged",
		"balance_notify_enabled",
		"balance_notify_threshold",
		"balance_notify_extra_emails",
	} {
		require.NotContains(t, user, field)
	}
	require.NotEmpty(t, user["email"])
	require.Equal(t, service.StatusActive, user["status"])
	require.Contains(t, user, "allowed_groups")
	require.Contains(t, user, "balance")
}
