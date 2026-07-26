package admin

import (
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// CursorHandler 暴露 cursor 账号的浏览器登录与凭证导入。
type CursorHandler struct {
	cursorOAuthService    *service.CursorOAuthService
	adminService          service.AdminService
	tokenCacheInvalidator service.TokenCacheInvalidator
}

func NewCursorHandler(
	cursorOAuthService *service.CursorOAuthService,
	adminService service.AdminService,
	tokenCacheInvalidator service.TokenCacheInvalidator,
) *CursorHandler {
	return &CursorHandler{
		cursorOAuthService:    cursorOAuthService,
		adminService:          adminService,
		tokenCacheInvalidator: tokenCacheInvalidator,
	}
}

type CursorLoginStartRequest struct {
	ProxyID *int64 `json:"proxy_id"`
}

// StartLogin 生成 PKCE 与浏览器登录 URL。
func (h *CursorHandler) StartLogin(c *gin.Context) {
	var req CursorLoginStartRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		req = CursorLoginStartRequest{}
	}
	result, err := h.cursorOAuthService.StartLogin(c.Request.Context(), req.ProxyID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

type CursorLoginPollRequest struct {
	SessionID string `json:"session_id" binding:"required"`
}

type CursorLoginPollResponse struct {
	// Pending 为 true 时前端应继续轮询。
	Pending     bool                     `json:"pending"`
	TokenInfo   *service.CursorTokenInfo `json:"token_info,omitempty"`
	Credentials map[string]any           `json:"credentials,omitempty"`
}

// PollLogin 查询一次登录状态。
//
// 未完成时返回 200 + pending=true，而不是错误状态码：前端每秒轮询一次，
// 用 4xx 表示「还在等」会把正常流程刷成一片红色的失败请求。
func (h *CursorHandler) PollLogin(c *gin.Context) {
	var req CursorLoginPollRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	tokenInfo, err := h.cursorOAuthService.PollLogin(c.Request.Context(), req.SessionID)
	if errors.Is(err, service.ErrCursorLoginPending) {
		response.Success(c, CursorLoginPollResponse{Pending: true})
		return
	}
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, CursorLoginPollResponse{
		TokenInfo:   tokenInfo,
		Credentials: h.cursorOAuthService.BuildAccountCredentials(tokenInfo),
	})
}

type CursorImportRequest struct {
	// Token 是 WorkosCursorSessionToken cookie 或裸 JWT。
	Token          string `json:"token" binding:"required"`
	ProxyID        *int64 `json:"proxy_id"`
	SelectedTeamID string `json:"selected_team_id"`
}

type CursorImportResponse struct {
	TokenInfo   *service.CursorTokenInfo `json:"token_info"`
	Credentials map[string]any           `json:"credentials"`
}

// Import 把粘贴的 cookie/JWT 换成 session 令牌并返回可建号的 credentials。
func (h *CursorHandler) Import(c *gin.Context) {
	var req CursorImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	tokenInfo, err := h.cursorOAuthService.ImportToken(
		c.Request.Context(), req.Token, req.ProxyID, req.SelectedTeamID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, CursorImportResponse{
		TokenInfo:   tokenInfo,
		Credentials: h.cursorOAuthService.BuildAccountCredentials(tokenInfo),
	})
}

// RefreshAccountToken 对已有账号执行一次真实刷新并落库。
func (h *CursorHandler) RefreshAccountToken(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	account, err := h.adminService.GetAccount(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if account.Platform != service.PlatformCursor {
		response.BadRequest(c, "Account platform does not match Cursor endpoint")
		return
	}
	if !account.IsOAuth() {
		response.BadRequest(c, "Cannot refresh non-OAuth account credentials")
		return
	}

	tokenInfo, err := h.cursorOAuthService.RefreshAccountToken(c.Request.Context(), account)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	newCredentials := service.MergeCredentials(
		account.Credentials,
		h.cursorOAuthService.BuildAccountCredentials(tokenInfo),
	)
	// 见 kiro_handler：这条路径绕开 OAuthRefreshAPI，必须自己推进 _token_version，
	// 否则请求路径上的 CheckTokenVersion 认不出凭证已经换过。
	newCredentials["_token_version"] = time.Now().UnixMilli()

	updatedAccount, err := h.adminService.UpdateAccount(c.Request.Context(), accountID, &service.UpdateAccountInput{
		Credentials: newCredentials,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if h.tokenCacheInvalidator != nil {
		if err := h.tokenCacheInvalidator.InvalidateToken(c.Request.Context(), updatedAccount); err != nil {
			slog.Warn("cursor_refresh_token_cache_invalidate_failed", "account_id", accountID, "error", err)
		}
	}
	response.Success(c, dto.AccountFromService(updatedAccount))
}
