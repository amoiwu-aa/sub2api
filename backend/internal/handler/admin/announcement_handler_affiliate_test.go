package admin

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type affiliateAnnouncementPermissionRepoStub struct {
	allowed bool
}

func (s *affiliateAnnouncementPermissionRepoStub) HasPermission(context.Context, int64, string) (bool, error) {
	return s.allowed, nil
}

func (*affiliateAnnouncementPermissionRepoStub) SetPermission(context.Context, int64, string, bool) error {
	return nil
}

type affiliateAnnouncementRepoStub struct {
	service.AnnouncementRepository
	item        *service.Announcement
	created     *service.Announcement
	listFilters service.AnnouncementListFilters
}

func (s *affiliateAnnouncementRepoStub) Create(_ context.Context, item *service.Announcement) error {
	item.ID = 91
	item.CreatedAt = time.Now()
	item.UpdatedAt = item.CreatedAt
	s.created = item
	s.item = item
	return nil
}

func (s *affiliateAnnouncementRepoStub) GetByID(context.Context, int64) (*service.Announcement, error) {
	if s.item == nil {
		return nil, service.ErrAnnouncementNotFound
	}
	return s.item, nil
}

func (s *affiliateAnnouncementRepoStub) List(
	_ context.Context,
	params pagination.PaginationParams,
	filters service.AnnouncementListFilters,
) ([]service.Announcement, *pagination.PaginationResult, error) {
	s.listFilters = filters
	return []service.Announcement{}, &pagination.PaginationResult{
		Page:     params.Page,
		PageSize: params.PageSize,
	}, nil
}

type affiliateAnnouncementUserRepoStub struct {
	service.UserRepository
	actor       *service.User
	listFilters service.UserListFilters
}

func (s *affiliateAnnouncementUserRepoStub) GetByID(context.Context, int64) (*service.User, error) {
	return s.actor, nil
}

func (s *affiliateAnnouncementUserRepoStub) ListWithFilters(
	_ context.Context,
	params pagination.PaginationParams,
	filters service.UserListFilters,
) ([]service.User, *pagination.PaginationResult, error) {
	s.listFilters = filters
	return []service.User{}, &pagination.PaginationResult{
		Page:     params.Page,
		PageSize: params.PageSize,
	}, nil
}

type affiliateAnnouncementReadRepoStub struct {
	service.AnnouncementReadRepository
}

func (*affiliateAnnouncementReadRepoStub) GetReadMapByUsers(context.Context, int64, []int64) (map[int64]time.Time, error) {
	return map[int64]time.Time{}, nil
}

type affiliateAnnouncementSubscriptionRepoStub struct {
	service.UserSubscriptionRepository
}

func (*affiliateAnnouncementSubscriptionRepoStub) ListActiveByUserID(context.Context, int64) ([]service.UserSubscription, error) {
	return []service.UserSubscription{}, nil
}

func newAffiliateAnnouncementTestRouter(
	actorID int64,
	allowed bool,
	announcementRepo *affiliateAnnouncementRepoStub,
	userRepo *affiliateAnnouncementUserRepoStub,
) *gin.Engine {
	gin.SetMode(gin.TestMode)
	permissionService := service.NewAffiliateAdminPermissionService(
		&affiliateAnnouncementPermissionRepoStub{allowed: allowed},
		userRepo,
	)
	announcementService := service.NewAnnouncementService(
		announcementRepo,
		&affiliateAnnouncementReadRepoStub{},
		userRepo,
		&affiliateAnnouncementSubscriptionRepoStub{},
	)
	handler := NewAnnouncementHandler(announcementService, permissionService)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: actorID})
		c.Set(string(middleware.ContextKeyUserRole), service.RoleAffiliateAdmin)
		c.Next()
	})
	router.GET("/admin/announcements", handler.List)
	router.POST("/admin/announcements", handler.Create)
	router.GET("/admin/announcements/:id", handler.GetByID)
	router.GET("/admin/announcements/:id/read-status", handler.ListReadStatus)
	return router
}

func TestAffiliateAnnouncementHandlerRequiresDelegatedPermission(t *testing.T) {
	actorID := int64(31)
	repo := &affiliateAnnouncementRepoStub{}
	userRepo := &affiliateAnnouncementUserRepoStub{
		actor: &service.User{
			ID:     actorID,
			Role:   service.RoleAffiliateAdmin,
			Status: service.StatusActive,
		},
	}
	router := newAffiliateAnnouncementTestRouter(actorID, false, repo, userRepo)

	req := httptest.NewRequest(http.MethodGet, "/admin/announcements", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestAffiliateAnnouncementHandlerScopesListToOwnedAnnouncements(t *testing.T) {
	actorID := int64(31)
	repo := &affiliateAnnouncementRepoStub{}
	userRepo := &affiliateAnnouncementUserRepoStub{
		actor: &service.User{
			ID:     actorID,
			Role:   service.RoleAffiliateAdmin,
			Status: service.StatusActive,
		},
	}
	router := newAffiliateAnnouncementTestRouter(actorID, true, repo, userRepo)

	req := httptest.NewRequest(http.MethodGet, "/admin/announcements", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, repo.listFilters.CreatedBy)
	require.Equal(t, actorID, *repo.listFilters.CreatedBy)
	require.NotNil(t, repo.listFilters.AffiliateAdminID)
	require.Equal(t, actorID, *repo.listFilters.AffiliateAdminID)
}

func TestAffiliateAnnouncementHandlerForcesManagedUserAudience(t *testing.T) {
	actorID := int64(31)
	repo := &affiliateAnnouncementRepoStub{}
	userRepo := &affiliateAnnouncementUserRepoStub{
		actor: &service.User{
			ID:     actorID,
			Role:   service.RoleAffiliateAdmin,
			Status: service.StatusActive,
		},
	}
	router := newAffiliateAnnouncementTestRouter(actorID, true, repo, userRepo)
	body := []byte(`{
		"title":"通知",
		"content":"内容",
		"status":"active",
		"notify_mode":"popup",
		"targeting":{"any_of":[{"all_of":[{"type":"balance","operator":"gte","value":999}]}]}
	}`)

	req := httptest.NewRequest(http.MethodPost, "/admin/announcements", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, repo.created)
	require.NotNil(t, repo.created.CreatedBy)
	require.Equal(t, actorID, *repo.created.CreatedBy)
	require.NotNil(t, repo.created.Targeting.AffiliateAdminID)
	require.Equal(t, actorID, *repo.created.Targeting.AffiliateAdminID)
	require.Empty(t, repo.created.Targeting.AnyOf)
}

func TestAffiliateAnnouncementHandlerHidesGlobalAnnouncementID(t *testing.T) {
	actorID := int64(31)
	repo := &affiliateAnnouncementRepoStub{
		item: &service.Announcement{
			ID:        7,
			Title:     "全站公告",
			Content:   "内容",
			Status:    service.AnnouncementStatusActive,
			CreatedBy: &actorID,
		},
	}
	userRepo := &affiliateAnnouncementUserRepoStub{
		actor: &service.User{
			ID:     actorID,
			Role:   service.RoleAffiliateAdmin,
			Status: service.StatusActive,
		},
	}
	router := newAffiliateAnnouncementTestRouter(actorID, true, repo, userRepo)

	req := httptest.NewRequest(http.MethodGet, "/admin/announcements/7", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestAffiliateAnnouncementHandlerScopesReadStatusToManagedUsers(t *testing.T) {
	actorID := int64(31)
	repo := &affiliateAnnouncementRepoStub{
		item: &service.Announcement{
			ID:        7,
			Title:     "分销公告",
			Content:   "内容",
			Status:    service.AnnouncementStatusActive,
			CreatedBy: &actorID,
			Targeting: service.AnnouncementTargeting{AffiliateAdminID: &actorID},
		},
	}
	userRepo := &affiliateAnnouncementUserRepoStub{
		actor: &service.User{
			ID:     actorID,
			Role:   service.RoleAffiliateAdmin,
			Status: service.StatusActive,
		},
	}
	router := newAffiliateAnnouncementTestRouter(actorID, true, repo, userRepo)

	req := httptest.NewRequest(http.MethodGet, "/admin/announcements/7/read-status", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, actorID, userRepo.listFilters.ManagedByAdminID)
}
