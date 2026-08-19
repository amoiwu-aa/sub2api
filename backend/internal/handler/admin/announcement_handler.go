package admin

import (
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// AnnouncementHandler handles admin announcement management
type AnnouncementHandler struct {
	announcementService *service.AnnouncementService
	permissionService   *service.AffiliateAdminPermissionService
}

// NewAnnouncementHandler creates a new admin announcement handler
func NewAnnouncementHandler(
	announcementService *service.AnnouncementService,
	permissionService *service.AffiliateAdminPermissionService,
) *AnnouncementHandler {
	return &AnnouncementHandler{
		announcementService: announcementService,
		permissionService:   permissionService,
	}
}

type CreateAnnouncementRequest struct {
	Title      string                        `json:"title" binding:"required"`
	Content    string                        `json:"content" binding:"required"`
	Status     string                        `json:"status" binding:"omitempty,oneof=draft active archived"`
	NotifyMode string                        `json:"notify_mode" binding:"omitempty,oneof=silent popup"`
	Targeting  service.AnnouncementTargeting `json:"targeting"`
	StartsAt   *int64                        `json:"starts_at"` // Unix seconds, 0/empty = immediate
	EndsAt     *int64                        `json:"ends_at"`   // Unix seconds, 0/empty = never
}

type UpdateAnnouncementRequest struct {
	Title      *string                        `json:"title"`
	Content    *string                        `json:"content"`
	Status     *string                        `json:"status" binding:"omitempty,oneof=draft active archived"`
	NotifyMode *string                        `json:"notify_mode" binding:"omitempty,oneof=silent popup"`
	Targeting  *service.AnnouncementTargeting `json:"targeting"`
	StartsAt   *int64                         `json:"starts_at"` // Unix seconds, 0 = clear
	EndsAt     *int64                         `json:"ends_at"`   // Unix seconds, 0 = clear
}

func (h *AnnouncementHandler) requireAnnouncementActor(c *gin.Context) (int64, string, bool) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not found in context")
		return 0, "", false
	}
	role, ok := middleware2.GetUserRoleFromContext(c)
	if !ok {
		response.Unauthorized(c, "User role not found in context")
		return 0, "", false
	}
	switch role {
	case service.RoleAdmin:
		return subject.UserID, role, true
	case service.RoleAffiliateAdmin:
		if h.permissionService == nil {
			response.ErrorFrom(c, service.ErrAffiliateAdminPermissionDenied)
			return 0, "", false
		}
		if err := h.permissionService.RequirePublishAnnouncements(c.Request.Context(), subject.UserID); err != nil {
			response.ErrorFrom(c, err)
			return 0, "", false
		}
		return subject.UserID, role, true
	default:
		response.Forbidden(c, "Announcement management access required")
		return 0, "", false
	}
}

func affiliateOwnsAnnouncement(actorID int64, item *service.Announcement) bool {
	return item != nil &&
		item.CreatedBy != nil &&
		*item.CreatedBy == actorID &&
		item.Targeting.AffiliateAdminID != nil &&
		*item.Targeting.AffiliateAdminID == actorID
}

func (h *AnnouncementHandler) requireAnnouncementOwnership(
	c *gin.Context,
	actorID int64,
	role string,
	announcementID int64,
) (*service.Announcement, bool) {
	item, err := h.announcementService.GetByID(c.Request.Context(), announcementID)
	if err != nil {
		response.ErrorFrom(c, err)
		return nil, false
	}
	if role == service.RoleAffiliateAdmin && !affiliateOwnsAnnouncement(actorID, item) {
		// Hide global/other distributors' announcement IDs from affiliate admins.
		response.ErrorFrom(c, service.ErrAnnouncementNotFound)
		return nil, false
	}
	return item, true
}

// List handles listing announcements with filters
// GET /api/v1/admin/announcements
func (h *AnnouncementHandler) List(c *gin.Context) {
	actorID, role, ok := h.requireAnnouncementActor(c)
	if !ok {
		return
	}
	page, pageSize := response.ParsePagination(c)
	status := strings.TrimSpace(c.Query("status"))
	search := strings.TrimSpace(c.Query("search"))
	sortBy := c.DefaultQuery("sort_by", "created_at")
	sortOrder := c.DefaultQuery("sort_order", "desc")
	if len(search) > 200 {
		search = search[:200]
	}

	params := pagination.PaginationParams{
		Page:      page,
		PageSize:  pageSize,
		SortBy:    sortBy,
		SortOrder: sortOrder,
	}

	filters := service.AnnouncementListFilters{Status: status, Search: search}
	if role == service.RoleAffiliateAdmin {
		filters.CreatedBy = &actorID
		filters.AffiliateAdminID = &actorID
	}
	items, paginationResult, err := h.announcementService.List(
		c.Request.Context(),
		params,
		filters,
	)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	out := make([]dto.Announcement, 0, len(items))
	for i := range items {
		out = append(out, *dto.AnnouncementFromService(&items[i]))
	}
	response.Paginated(c, out, paginationResult.Total, page, pageSize)
}

// GetByID handles getting an announcement by ID
// GET /api/v1/admin/announcements/:id
func (h *AnnouncementHandler) GetByID(c *gin.Context) {
	actorID, role, ok := h.requireAnnouncementActor(c)
	if !ok {
		return
	}
	announcementID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || announcementID <= 0 {
		response.BadRequest(c, "Invalid announcement ID")
		return
	}

	item, ok := h.requireAnnouncementOwnership(c, actorID, role, announcementID)
	if !ok {
		return
	}

	response.Success(c, dto.AnnouncementFromService(item))
}

// Create handles creating a new announcement
// POST /api/v1/admin/announcements
func (h *AnnouncementHandler) Create(c *gin.Context) {
	actorID, role, ok := h.requireAnnouncementActor(c)
	if !ok {
		return
	}
	var req CreateAnnouncementRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	targeting := req.Targeting
	if role == service.RoleAffiliateAdmin {
		// Affiliate announcements always target every managed user. Advanced
		// global conditions remain a super-admin capability.
		targeting = service.AnnouncementTargeting{AffiliateAdminID: &actorID}
	} else {
		// Clients cannot manufacture an affiliate scope through the global API.
		targeting.AffiliateAdminID = nil
	}

	input := &service.CreateAnnouncementInput{
		Title:      req.Title,
		Content:    req.Content,
		Status:     req.Status,
		NotifyMode: req.NotifyMode,
		Targeting:  targeting,
		ActorID:    &actorID,
	}

	if req.StartsAt != nil && *req.StartsAt > 0 {
		t := time.Unix(*req.StartsAt, 0)
		input.StartsAt = &t
	}
	if req.EndsAt != nil && *req.EndsAt > 0 {
		t := time.Unix(*req.EndsAt, 0)
		input.EndsAt = &t
	}

	created, err := h.announcementService.Create(c.Request.Context(), input)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, dto.AnnouncementFromService(created))
}

// Update handles updating an announcement
// PUT /api/v1/admin/announcements/:id
func (h *AnnouncementHandler) Update(c *gin.Context) {
	actorID, role, ok := h.requireAnnouncementActor(c)
	if !ok {
		return
	}
	announcementID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || announcementID <= 0 {
		response.BadRequest(c, "Invalid announcement ID")
		return
	}

	var req UpdateAnnouncementRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	item, ok := h.requireAnnouncementOwnership(c, actorID, role, announcementID)
	if !ok {
		return
	}

	targeting := req.Targeting
	if role == service.RoleAffiliateAdmin {
		scoped := service.AnnouncementTargeting{AffiliateAdminID: &actorID}
		targeting = &scoped
	} else if targeting != nil && item.Targeting.AffiliateAdminID == nil {
		// A global announcement cannot be converted into a distributor-scoped
		// record via a client-supplied internal field.
		targeting.AffiliateAdminID = nil
	}

	input := &service.UpdateAnnouncementInput{
		Title:      req.Title,
		Content:    req.Content,
		Status:     req.Status,
		NotifyMode: req.NotifyMode,
		Targeting:  targeting,
		ActorID:    &actorID,
	}

	if req.StartsAt != nil {
		if *req.StartsAt == 0 {
			var cleared *time.Time = nil
			input.StartsAt = &cleared
		} else {
			t := time.Unix(*req.StartsAt, 0)
			ptr := &t
			input.StartsAt = &ptr
		}
	}

	if req.EndsAt != nil {
		if *req.EndsAt == 0 {
			var cleared *time.Time = nil
			input.EndsAt = &cleared
		} else {
			t := time.Unix(*req.EndsAt, 0)
			ptr := &t
			input.EndsAt = &ptr
		}
	}

	updated, err := h.announcementService.Update(c.Request.Context(), announcementID, input)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, dto.AnnouncementFromService(updated))
}

// Delete handles deleting an announcement
// DELETE /api/v1/admin/announcements/:id
func (h *AnnouncementHandler) Delete(c *gin.Context) {
	actorID, role, ok := h.requireAnnouncementActor(c)
	if !ok {
		return
	}
	announcementID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || announcementID <= 0 {
		response.BadRequest(c, "Invalid announcement ID")
		return
	}
	if _, ok := h.requireAnnouncementOwnership(c, actorID, role, announcementID); !ok {
		return
	}

	if err := h.announcementService.Delete(c.Request.Context(), announcementID); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, gin.H{"message": "Announcement deleted successfully"})
}

// ListReadStatus handles listing users read status for an announcement
// GET /api/v1/admin/announcements/:id/read-status
func (h *AnnouncementHandler) ListReadStatus(c *gin.Context) {
	actorID, role, ok := h.requireAnnouncementActor(c)
	if !ok {
		return
	}
	announcementID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || announcementID <= 0 {
		response.BadRequest(c, "Invalid announcement ID")
		return
	}
	item, ok := h.requireAnnouncementOwnership(c, actorID, role, announcementID)
	if !ok {
		return
	}

	page, pageSize := response.ParsePagination(c)
	params := pagination.PaginationParams{
		Page:      page,
		PageSize:  pageSize,
		SortBy:    c.DefaultQuery("sort_by", "email"),
		SortOrder: c.DefaultQuery("sort_order", "asc"),
	}
	search := strings.TrimSpace(c.Query("search"))
	if len(search) > 200 {
		search = search[:200]
	}

	var items []service.AnnouncementUserReadStatus
	var paginationResult *pagination.PaginationResult
	managedByAdminID := int64(0)
	if role == service.RoleAffiliateAdmin {
		managedByAdminID = actorID
	} else if item.Targeting.AffiliateAdminID != nil {
		managedByAdminID = *item.Targeting.AffiliateAdminID
	}
	if managedByAdminID > 0 {
		items, paginationResult, err = h.announcementService.ListUserReadStatusScoped(
			c.Request.Context(),
			announcementID,
			params,
			search,
			managedByAdminID,
		)
	} else {
		items, paginationResult, err = h.announcementService.ListUserReadStatus(
			c.Request.Context(),
			announcementID,
			params,
			search,
		)
	}
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Paginated(c, items, paginationResult.Total, page, pageSize)
}
