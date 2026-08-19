package admin

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type distributionDashboardAPI interface {
	GetSnapshot(ctx context.Context, adminID int64, start, end time.Time) (*service.DistributionDashboardSnapshot, error)
	GetTrend(ctx context.Context, adminID int64, start, end time.Time, granularity string, filter service.DistributionUsageFilter) ([]service.DistributionUsageTrendPoint, error)
	GetModelStats(ctx context.Context, adminID int64, start, end time.Time, userID int64) ([]service.DistributionUsageModelStat, error)
	GetRanking(ctx context.Context, adminID int64, start, end time.Time, sort string, limit int) ([]service.DistributionUsageUserRankingItem, error)
	GetErrorSummary(ctx context.Context, adminID int64, start, end time.Time) (*service.DistributionUsageErrorSummary, error)
	GetUserUsageSummary(ctx context.Context, adminID int64, userID int64, start, end time.Time) (*service.DistributionUsageUserSummary, error)
}

type distributionBalanceAPI interface {
	Transfer(ctx context.Context, input service.DistributionBalanceTransferInput) (*service.DistributionBalanceTransfer, error)
	BalanceSummary(ctx context.Context, adminID int64) (*service.DistributionBalanceSummary, error)
	ListTransfers(ctx context.Context, adminID int64, page, pageSize int) ([]service.DistributionBalanceTransfer, int64, error)
}

type distributionInviteAPI interface {
	GetProfile(ctx context.Context, adminID int64) (*service.DistributionInviteProfile, error)
	UpdateSettings(ctx context.Context, adminID int64, enabled *bool, defaultGroupIDs *[]int64) error
	RotateCode(ctx context.Context, adminID int64) (string, error)
	ListRegistrations(ctx context.Context, adminID int64, page, pageSize int) ([]service.User, int64, error)
}

type distributionUsageLogAPI interface {
	ListByUser(ctx context.Context, userID int64, params pagination.PaginationParams) ([]service.UsageLog, *pagination.PaginationResult, error)
}

type distributionSubscriptionAPI interface {
	ListUserSubscriptions(ctx context.Context, userID int64) ([]service.UserSubscription, error)
}

type distributionPermissionAPI interface {
	GetPermissions(ctx context.Context, affiliateAdminID int64) (*service.AffiliateAdminPermissions, error)
	UpdatePermissions(ctx context.Context, affiliateAdminID int64, permissions service.AffiliateAdminPermissions) error
}

// DistributionHandler serves affiliate-admin distribution APIs scoped to the actor.
type DistributionHandler struct {
	dashboard     distributionDashboardAPI
	balances      distributionBalanceAPI
	invites       distributionInviteAPI
	permissions   distributionPermissionAPI
	admin         service.AdminService
	usage         distributionUsageLogAPI
	subscriptions distributionSubscriptionAPI
}

// NewDistributionHandler wires affiliate-admin distribution HTTP handlers.
func NewDistributionHandler(
	dashboard *service.DistributionDashboardService,
	balances *service.DistributionBalanceService,
	invites *service.DistributionInviteService,
	adminService service.AdminService,
	usageService *service.UsageService,
	subscriptionService *service.SubscriptionService,
	permissionService *service.AffiliateAdminPermissionService,
) *DistributionHandler {
	h := &DistributionHandler{admin: adminService}
	if dashboard != nil {
		h.dashboard = dashboard
	}
	if balances != nil {
		h.balances = balances
	}
	if invites != nil {
		h.invites = invites
	}
	if usageService != nil {
		h.usage = usageService
	}
	if subscriptionService != nil {
		h.subscriptions = subscriptionService
	}
	if permissionService != nil {
		h.permissions = permissionService
	}
	return h
}

type distributionGroupItem struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type updateDistributionUserGroupsRequest struct {
	GroupIDs []int64 `json:"group_ids"`
}

type createDistributionBalanceTransferRequest struct {
	Amount         float64 `json:"amount"`
	Notes          string  `json:"notes"`
	IdempotencyKey string  `json:"idempotency_key"`
}

type updateDistributionInviteSettingsRequest struct {
	Enabled         *bool    `json:"enabled"`
	DefaultGroupIDs *[]int64 `json:"default_group_ids"`
}

type updateDistributionPermissionsRequest struct {
	CanPublishAnnouncements *bool `json:"can_publish_announcements"`
}

type distributionInviteProfileResponse struct {
	InviteCode        string  `json:"invite_code"`
	InviteLink        string  `json:"invite_link"`
	RegistrationCount int64   `json:"registration_count"`
	Enabled           bool    `json:"enabled"`
	AffCode           string  `json:"aff_code"`
	RegisterPath      string  `json:"register_path"`
	DefaultGroupIDs   []int64 `json:"default_group_ids"`
}

type distributionRegistrationItem struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	Email     string    `json:"email"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"created_at"`
}

type distributionSubscriptionSummary struct {
	PlanName  string    `json:"plan_name"`
	Status    string    `json:"status"`
	ExpiresAt time.Time `json:"expires_at"`
}

type distributionBalanceTransferItem struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	Email     string    `json:"email"`
	Username  string    `json:"username"`
	Amount    float64   `json:"amount"`
	Notes     string    `json:"notes"`
	CreatedAt time.Time `json:"created_at"`
}

func firstNonEmptyQuery(c *gin.Context, keys ...string) string {
	for _, key := range keys {
		if raw := strings.TrimSpace(c.Query(key)); raw != "" {
			return raw
		}
	}
	return ""
}

func parseDistributionInstant(raw, userTZ string, isEndDate bool) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	if strings.Contains(raw, "T") {
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			return t, true
		}
		if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			return t, true
		}
	}
	if t, err := timezone.ParseInUserLocation("2006-01-02", raw, userTZ); err == nil {
		if isEndDate {
			return t.Add(24 * time.Hour), true
		}
		return t, true
	}
	return time.Time{}, false
}

func parseDistributionTimeRange(c *gin.Context) (time.Time, time.Time) {
	userTZ := c.Query("timezone")
	now := timezone.NowInUserLocation(userTZ)
	startRaw := firstNonEmptyQuery(c, "start", "start_date")
	endRaw := firstNonEmptyQuery(c, "end", "end_date")
	if startRaw == "" && endRaw == "" {
		return parseTimeRange(c)
	}

	startTime, startOK := parseDistributionInstant(startRaw, userTZ, false)
	if !startOK {
		startTime = timezone.StartOfDayInUserLocation(now.AddDate(0, 0, -7), userTZ)
	}
	endTime, endOK := parseDistributionInstant(endRaw, userTZ, true)
	if !endOK {
		endTime = timezone.StartOfDayInUserLocation(now.AddDate(0, 0, 1), userTZ)
	}
	return startTime, endTime
}

func distributionTodayRange(c *gin.Context) (time.Time, time.Time) {
	userTZ := c.Query("timezone")
	now := timezone.NowInUserLocation(userTZ)
	start := timezone.StartOfDayInUserLocation(now, userTZ)
	end := timezone.StartOfDayInUserLocation(now.AddDate(0, 0, 1), userTZ)
	return start, end
}

func parseOptionalQueryInt64(c *gin.Context, name string) int64 {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return 0
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0
	}
	return id
}

func (h *DistributionHandler) managedUserID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid user ID")
		return 0, false
	}
	if !requireManagedUser(c, h.admin, id) {
		return 0, false
	}
	return id, true
}

func inviteProfileResponse(profile *service.DistributionInviteProfile) distributionInviteProfileResponse {
	out := distributionInviteProfileResponse{DefaultGroupIDs: []int64{}}
	if profile == nil {
		return out
	}
	ids := profile.DefaultGroupIDs
	if ids == nil {
		ids = []int64{}
	}
	return distributionInviteProfileResponse{
		InviteCode:        profile.AffCode,
		InviteLink:        profile.RegisterPath,
		RegistrationCount: profile.RegistrationCount,
		Enabled:           profile.Enabled,
		AffCode:           profile.AffCode,
		RegisterPath:      profile.RegisterPath,
		DefaultGroupIDs:   ids,
	}
}

func (h *DistributionHandler) writeInviteProfile(c *gin.Context, adminID int64) {
	if h.invites == nil {
		response.Success(c, inviteProfileResponse(nil))
		return
	}
	profile, err := h.invites.GetProfile(c.Request.Context(), adminID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, inviteProfileResponse(profile))
}

func (h *DistributionHandler) lookupUserContact(ctx context.Context, userID int64) (email, username string) {
	if h.admin == nil || userID <= 0 {
		return "", ""
	}
	user, err := h.admin.GetUser(ctx, userID)
	if err != nil || user == nil {
		return "", ""
	}
	return user.Email, user.Username
}

func (h *DistributionHandler) mapTransfer(ctx context.Context, row *service.DistributionBalanceTransfer) distributionBalanceTransferItem {
	if row == nil {
		return distributionBalanceTransferItem{}
	}
	email, username := h.lookupUserContact(ctx, row.TargetUserID)
	return distributionBalanceTransferItem{
		ID:        row.ID,
		UserID:    row.TargetUserID,
		Email:     email,
		Username:  username,
		Amount:    row.Amount,
		Notes:     row.Notes,
		CreatedAt: row.CreatedAt,
	}
}

// GetDashboardSnapshot GET /api/v1/admin/distribution/dashboard/snapshot
func (h *DistributionHandler) GetDashboardSnapshot(c *gin.Context) {
	adminID := getAdminIDFromContext(c)
	todayStart, todayEnd := distributionTodayRange(c)
	out := gin.H{
		"customer_count":          int64(0),
		"active_customer_count":   int64(0),
		"today_requests":          int64(0),
		"today_tokens":            int64(0),
		"today_cost":              float64(0),
		"available_balance":       float64(0),
		"invite_count":            int64(0),
		"registration_count":      int64(0),
		"disabled_customer_count": int64(0),
		"frozen_balance":          float64(0),
		"total_transferred":       float64(0),
	}
	if h.dashboard != nil {
		snap, err := h.dashboard.GetSnapshot(c.Request.Context(), adminID, todayStart, todayEnd)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		if snap != nil {
			out["customer_count"] = snap.CustomerCount
			out["active_customer_count"] = snap.ActiveCustomerCount
			out["disabled_customer_count"] = snap.DisabledCustomerCount
			out["today_requests"] = snap.Requests
			out["today_tokens"] = snap.TotalTokens
			out["today_cost"] = snap.ActualCost
			out["available_balance"] = snap.Available
			out["frozen_balance"] = snap.Frozen
			out["total_transferred"] = snap.Allocated
		}
	}
	if h.invites != nil {
		profile, err := h.invites.GetProfile(c.Request.Context(), adminID)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		if profile != nil {
			out["registration_count"] = profile.RegistrationCount
			if profile.AffCode != "" {
				out["invite_count"] = int64(1)
			}
		}
	}
	response.Success(c, out)
}

func distributionDateBounds(start, end time.Time) (string, string) {
	startDate := start.Format("2006-01-02")
	endDate := end.Add(-24 * time.Hour).Format("2006-01-02")
	if !end.After(start) {
		endDate = startDate
	}
	return startDate, endDate
}

func mapTrendPoint(p service.DistributionUsageTrendPoint) gin.H {
	return gin.H{
		"date":         p.Date,
		"requests":     p.Requests,
		"tokens":       p.TotalTokens,
		"cost":         p.ActualCost,
		"actual_cost":  p.ActualCost,
		"total_tokens": p.TotalTokens,
	}
}

func mapModelStat(p service.DistributionUsageModelStat) gin.H {
	return gin.H{
		"model":    p.Model,
		"requests": p.Requests,
		"tokens":   p.TotalTokens,
		"cost":     p.ActualCost,
	}
}

func mapRankingItem(p service.DistributionUsageUserRankingItem) gin.H {
	return gin.H{
		"user_id":  p.UserID,
		"email":    p.Email,
		"username": p.Username,
		"requests": p.Requests,
		"tokens":   p.Tokens,
		"cost":     p.ActualCost,
	}
}

func (h *DistributionHandler) usageTrend(c *gin.Context, filter service.DistributionUsageFilter) {
	if h.dashboard == nil {
		start, end := parseDistributionTimeRange(c)
		startDate, endDate := distributionDateBounds(start, end)
		response.Success(c, gin.H{
			"trend":       []gin.H{},
			"start_date":  startDate,
			"end_date":    endDate,
			"granularity": c.DefaultQuery("granularity", service.DistributionUsageGranularityDay),
		})
		return
	}
	start, end := parseDistributionTimeRange(c)
	granularity := c.DefaultQuery("granularity", service.DistributionUsageGranularityDay)
	points, err := h.dashboard.GetTrend(c.Request.Context(), getAdminIDFromContext(c), start, end, granularity, filter)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]gin.H, 0, len(points))
	for i := range points {
		out = append(out, mapTrendPoint(points[i]))
	}
	startDate, endDate := distributionDateBounds(start, end)
	response.Success(c, gin.H{
		"trend":       out,
		"start_date":  startDate,
		"end_date":    endDate,
		"granularity": granularity,
	})
}

// GetUsageTrend GET /api/v1/admin/distribution/usage/trend
func (h *DistributionHandler) GetUsageTrend(c *gin.Context) {
	h.usageTrend(c, service.DistributionUsageFilter{UserID: parseOptionalQueryInt64(c, "user_id")})
}

// GetUserUsageTrend GET /api/v1/admin/distribution/users/:id/usage/trend
func (h *DistributionHandler) GetUserUsageTrend(c *gin.Context) {
	userID, ok := h.managedUserID(c)
	if !ok {
		return
	}
	h.usageTrend(c, service.DistributionUsageFilter{UserID: userID})
}

// GetUsageModels GET /api/v1/admin/distribution/usage/models
func (h *DistributionHandler) GetUsageModels(c *gin.Context) {
	if h.dashboard == nil {
		response.Success(c, gin.H{"models": []gin.H{}})
		return
	}
	start, end := parseDistributionTimeRange(c)
	models, err := h.dashboard.GetModelStats(c.Request.Context(), getAdminIDFromContext(c), start, end, parseOptionalQueryInt64(c, "user_id"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]gin.H, 0, len(models))
	for i := range models {
		out = append(out, mapModelStat(models[i]))
	}
	response.Success(c, gin.H{"models": out})
}

// GetUsageErrors GET /api/v1/admin/distribution/usage/errors
func (h *DistributionHandler) GetUsageErrors(c *gin.Context) {
	errors := make([]gin.H, 0)
	if h.dashboard == nil {
		response.Success(c, gin.H{"errors": errors})
		return
	}
	start, end := parseDistributionTimeRange(c)
	summary, err := h.dashboard.GetErrorSummary(c.Request.Context(), getAdminIDFromContext(c), start, end)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if summary != nil && summary.FailedOrUnbilledRequests > 0 {
		errors = append(errors, gin.H{
			"status_code":  0,
			"message":      "failed_or_unbilled",
			"count":        summary.FailedOrUnbilledRequests,
			"last_seen_at": nil,
		})
	}
	response.Success(c, gin.H{"errors": errors})
}

// GetUserRanking GET /api/v1/admin/distribution/users/ranking
func (h *DistributionHandler) GetUserRanking(c *gin.Context) {
	if h.dashboard == nil {
		response.Success(c, gin.H{"items": []gin.H{}})
		return
	}
	start, end := parseDistributionTimeRange(c)
	limit := service.DistributionUsageDefaultLimit
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	sort := c.DefaultQuery("sort", service.DistributionUsageSortActual)
	items, err := h.dashboard.GetRanking(c.Request.Context(), getAdminIDFromContext(c), start, end, sort, limit)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]gin.H, 0, len(items))
	for i := range items {
		out = append(out, mapRankingItem(items[i]))
	}
	response.Success(c, gin.H{"items": out})
}

// GetUserUsageSummary GET /api/v1/admin/distribution/users/:id/usage/summary
func (h *DistributionHandler) GetUserUsageSummary(c *gin.Context) {
	userID, ok := h.managedUserID(c)
	if !ok {
		return
	}
	out := gin.H{
		"user_id":        userID,
		"total_requests": int64(0),
		"total_tokens":   int64(0),
		"total_cost":     float64(0),
		"today_requests": int64(0),
		"today_tokens":   int64(0),
		"today_cost":     float64(0),
	}
	if h.dashboard == nil {
		response.Success(c, out)
		return
	}
	adminID := getAdminIDFromContext(c)
	start, end := parseDistributionTimeRange(c)
	summary, err := h.dashboard.GetUserUsageSummary(c.Request.Context(), adminID, userID, start, end)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if summary != nil {
		out["user_id"] = summary.UserID
		out["total_requests"] = summary.Requests
		out["total_tokens"] = summary.TotalTokens
		out["total_cost"] = summary.ActualCost
	}
	todayStart, todayEnd := distributionTodayRange(c)
	today, err := h.dashboard.GetUserUsageSummary(c.Request.Context(), adminID, userID, todayStart, todayEnd)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if today != nil {
		out["today_requests"] = today.Requests
		out["today_tokens"] = today.TotalTokens
		out["today_cost"] = today.ActualCost
	}
	response.Success(c, out)
}

// GetUserUsageLogs GET /api/v1/admin/distribution/users/:id/usage/logs
// Uses the user-facing UsageLog DTO (no AdminUsageLog internals).
func (h *DistributionHandler) GetUserUsageLogs(c *gin.Context) {
	userID, ok := h.managedUserID(c)
	if !ok {
		return
	}
	page, pageSize := response.ParsePagination(c)
	if h.usage == nil {
		// Safe empty list until a scoped usage-log reader is needed.
		response.Paginated(c, []dto.UsageLog{}, 0, page, pageSize)
		return
	}
	records, result, err := h.usage.ListByUser(c.Request.Context(), userID, pagination.PaginationParams{
		Page:      page,
		PageSize:  pageSize,
		SortBy:    c.DefaultQuery("sort_by", "created_at"),
		SortOrder: c.DefaultQuery("sort_order", pagination.SortOrderDesc),
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]dto.UsageLog, 0, len(records))
	for i := range records {
		if item := dto.UsageLogFromService(&records[i]); item != nil {
			out = append(out, *item)
		}
	}
	total := int64(len(out))
	if result != nil {
		total = result.Total
	}
	response.Paginated(c, out, total, page, pageSize)
}

// GetBalanceSummary GET /api/v1/admin/distribution/balance/summary
func (h *DistributionHandler) GetBalanceSummary(c *gin.Context) {
	out := gin.H{
		"available_balance":      float64(0),
		"frozen_balance":         float64(0),
		"total_transferred":      float64(0),
		"customer_balance_total": float64(0),
	}
	if h.balances == nil {
		response.Success(c, out)
		return
	}
	summary, err := h.balances.BalanceSummary(c.Request.Context(), getAdminIDFromContext(c))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if summary != nil {
		out["available_balance"] = summary.Available
		out["frozen_balance"] = summary.Frozen
		out["total_transferred"] = summary.Allocated
	}
	response.Success(c, out)
}

// ListBalanceTransfers GET /api/v1/admin/distribution/balance/transfers
func (h *DistributionHandler) ListBalanceTransfers(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)
	if h.balances == nil {
		response.Paginated(c, []distributionBalanceTransferItem{}, 0, page, pageSize)
		return
	}
	rows, total, err := h.balances.ListTransfers(c.Request.Context(), getAdminIDFromContext(c), page, pageSize)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	items := make([]distributionBalanceTransferItem, 0, len(rows))
	for i := range rows {
		items = append(items, h.mapTransfer(c.Request.Context(), &rows[i]))
	}
	response.Paginated(c, items, total, page, pageSize)
}

// CreateBalanceTransfer POST /api/v1/admin/distribution/users/:id/balance-transfers
func (h *DistributionHandler) CreateBalanceTransfer(c *gin.Context) {
	userID, ok := h.managedUserID(c)
	if !ok {
		return
	}
	var req createDistributionBalanceTransferRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := parsePositiveAmount(req.Amount); err != nil {
		response.ErrorFrom(c, infraerrors.BadRequest(domain.ErrReasonInvalidTransferAmount, err.Error()))
		return
	}
	if h.balances == nil {
		response.ErrorFrom(c, infraerrors.ServiceUnavailable("SERVICE_UNAVAILABLE", "distribution balance service unavailable"))
		return
	}
	key := strings.TrimSpace(req.IdempotencyKey)
	if key == "" {
		key = uuid.NewString()
	}
	row, err := h.balances.Transfer(c.Request.Context(), service.DistributionBalanceTransferInput{
		AffiliateAdminID: getAdminIDFromContext(c),
		TargetUserID:     userID,
		Amount:           req.Amount,
		Notes:            req.Notes,
		IdempotencyKey:   key,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, h.mapTransfer(c.Request.Context(), row))
}

// ListGroups GET /api/v1/admin/distribution/groups
func (h *DistributionHandler) ListGroups(c *gin.Context) {
	if h.admin == nil {
		response.Success(c, []distributionGroupItem{})
		return
	}
	actorID := getAdminIDFromContext(c)
	actor, err := h.admin.GetUser(c.Request.Context(), actorID)
	if actorID <= 0 || err != nil || actor == nil {
		response.Success(c, []distributionGroupItem{})
		return
	}
	groups, err := h.admin.GetAllGroups(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	filtered := service.FilterActiveGroupsByAllowedIDs(groups, actor.AllowedGroups)
	out := make([]distributionGroupItem, 0, len(filtered))
	for i := range filtered {
		out = append(out, distributionGroupItem{
			ID:     filtered[i].ID,
			Name:   filtered[i].Name,
			Status: filtered[i].Status,
		})
	}
	response.Success(c, out)
}

// UpdateUserGroups PUT /api/v1/admin/distribution/users/:id/groups
func (h *DistributionHandler) UpdateUserGroups(c *gin.Context) {
	userID, ok := h.managedUserID(c)
	if !ok {
		return
	}
	var req updateDistributionUserGroupsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if req.GroupIDs == nil {
		req.GroupIDs = []int64{}
	}
	if h.admin == nil {
		response.ErrorFrom(c, infraerrors.ServiceUnavailable("SERVICE_UNAVAILABLE", "admin service unavailable"))
		return
	}
	updated, err := h.admin.UpdateUser(c.Request.Context(), userID, &service.UpdateUserInput{
		AllowedGroups: &req.GroupIDs,
		ActorAdminID:  getAdminIDFromContext(c),
		ActorRole:     service.RoleAffiliateAdmin,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	groupIDs := req.GroupIDs
	if updated != nil && updated.AllowedGroups != nil {
		groupIDs = updated.AllowedGroups
	}
	response.Success(c, gin.H{"group_ids": groupIDs})
}

// GetInviteProfile GET /api/v1/admin/distribution/invites/profile
func (h *DistributionHandler) GetInviteProfile(c *gin.Context) {
	h.writeInviteProfile(c, getAdminIDFromContext(c))
}

// UpdateInviteSettings PUT /api/v1/admin/distribution/invites/settings
func (h *DistributionHandler) UpdateInviteSettings(c *gin.Context) {
	var req updateDistributionInviteSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	adminID := getAdminIDFromContext(c)
	if h.invites == nil {
		response.ErrorFrom(c, infraerrors.ServiceUnavailable("SERVICE_UNAVAILABLE", "distribution invite service unavailable"))
		return
	}
	if err := h.invites.UpdateSettings(c.Request.Context(), adminID, req.Enabled, req.DefaultGroupIDs); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.writeInviteProfile(c, adminID)
}

// RotateInviteCode POST /api/v1/admin/distribution/invites/rotate-code
func (h *DistributionHandler) RotateInviteCode(c *gin.Context) {
	adminID := getAdminIDFromContext(c)
	if h.invites == nil {
		response.ErrorFrom(c, infraerrors.ServiceUnavailable("SERVICE_UNAVAILABLE", "distribution invite service unavailable"))
		return
	}
	if _, err := h.invites.RotateCode(c.Request.Context(), adminID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.writeInviteProfile(c, adminID)
}

// ListInviteRegistrations GET /api/v1/admin/distribution/invites/registrations
func (h *DistributionHandler) ListInviteRegistrations(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)
	if h.invites == nil {
		response.Paginated(c, []distributionRegistrationItem{}, 0, page, pageSize)
		return
	}
	users, total, err := h.invites.ListRegistrations(c.Request.Context(), getAdminIDFromContext(c), page, pageSize)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	items := make([]distributionRegistrationItem, 0, len(users))
	for i := range users {
		items = append(items, distributionRegistrationItem{
			ID:        users[i].ID,
			UserID:    users[i].ID,
			Email:     users[i].Email,
			Username:  users[i].Username,
			CreatedAt: users[i].CreatedAt,
		})
	}
	response.Paginated(c, items, total, page, pageSize)
}

// ListUserSubscriptions GET /api/v1/admin/distribution/users/:id/subscriptions
func (h *DistributionHandler) ListUserSubscriptions(c *gin.Context) {
	userID, ok := h.managedUserID(c)
	if !ok {
		return
	}
	page, pageSize := response.ParsePagination(c)
	if h.subscriptions == nil {
		response.Paginated(c, []distributionSubscriptionSummary{}, 0, page, pageSize)
		return
	}
	subs, err := h.subscriptions.ListUserSubscriptions(c.Request.Context(), userID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	items := make([]distributionSubscriptionSummary, 0, len(subs))
	for i := range subs {
		planName := ""
		if subs[i].Group != nil {
			planName = subs[i].Group.Name
		}
		items = append(items, distributionSubscriptionSummary{
			PlanName:  planName,
			Status:    subs[i].Status,
			ExpiresAt: subs[i].ExpiresAt,
		})
	}
	total := int64(len(items))
	start := (page - 1) * pageSize
	if start < 0 {
		start = 0
	}
	if start > len(items) {
		start = len(items)
	}
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}
	response.Paginated(c, items[start:end], total, page, pageSize)
}

// GetMyPermissions GET /api/v1/admin/distribution/permissions
func (h *DistributionHandler) GetMyPermissions(c *gin.Context) {
	if h.permissions == nil {
		response.ErrorFrom(c, infraerrors.ServiceUnavailable(
			"SERVICE_UNAVAILABLE",
			"distribution permission service unavailable",
		))
		return
	}
	permissions, err := h.permissions.GetPermissions(c.Request.Context(), getAdminIDFromContext(c))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, permissions)
}

// GetUserPermissions GET /api/v1/admin/users/:id/distribution-permissions
func (h *DistributionHandler) GetUserPermissions(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || userID <= 0 {
		response.BadRequest(c, "Invalid user ID")
		return
	}
	if h.permissions == nil {
		response.ErrorFrom(c, infraerrors.ServiceUnavailable(
			"SERVICE_UNAVAILABLE",
			"distribution permission service unavailable",
		))
		return
	}
	permissions, err := h.permissions.GetPermissions(c.Request.Context(), userID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, permissions)
}

// UpdateUserPermissions PUT /api/v1/admin/users/:id/distribution-permissions
func (h *DistributionHandler) UpdateUserPermissions(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || userID <= 0 {
		response.BadRequest(c, "Invalid user ID")
		return
	}
	var req updateDistributionPermissionsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if req.CanPublishAnnouncements == nil {
		response.BadRequest(c, "can_publish_announcements is required")
		return
	}
	if h.permissions == nil {
		response.ErrorFrom(c, infraerrors.ServiceUnavailable(
			"SERVICE_UNAVAILABLE",
			"distribution permission service unavailable",
		))
		return
	}
	permissions := service.AffiliateAdminPermissions{
		CanPublishAnnouncements: *req.CanPublishAnnouncements,
	}
	if err := h.permissions.UpdatePermissions(c.Request.Context(), userID, permissions); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, permissions)
}
