package admin

import (
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// KiroHandler 暴露 kiro 账号的粘贴式导入与手动刷新。
//
// Kiro 没有可由服务端拉起的 OAuth 流程，所以这里没有 auth-url / exchange-code，
// 只有「贴 JSON」和「探测刷新」两个动作。
type KiroHandler struct {
	kiroOAuthService      *service.KiroOAuthService
	adminService          service.AdminService
	tokenCacheInvalidator service.TokenCacheInvalidator
}

func NewKiroHandler(
	kiroOAuthService *service.KiroOAuthService,
	adminService service.AdminService,
	tokenCacheInvalidator service.TokenCacheInvalidator,
) *KiroHandler {
	return &KiroHandler{
		kiroOAuthService:      kiroOAuthService,
		adminService:          adminService,
		tokenCacheInvalidator: tokenCacheInvalidator,
	}
}

type KiroImportRequest struct {
	// TokenJSON 是 ~/.aws/sso/cache/kiro-auth-token.json 的原文。
	TokenJSON string `json:"token_json"`
	// ClientRegistrationJSON 是 IdC 账号的 {clientId, clientSecret}。
	ClientRegistrationJSON string `json:"client_registration_json"`
	ProxyID                *int64 `json:"proxy_id"`
}

type KiroImportResponse struct {
	TokenInfo   *service.KiroTokenInfo `json:"token_info"`
	Credentials map[string]any         `json:"credentials"`
}

// Import 校验粘贴的凭证并返回可直接用于建号的 credentials。
// 前端拿到后走通用的 accounts.create，与 Grok 的主路径一致。
func (h *KiroHandler) Import(c *gin.Context) {
	var req KiroImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if strings.TrimSpace(req.TokenJSON) == "" {
		response.BadRequest(c, "token_json is required")
		return
	}

	tokenInfo, err := h.kiroOAuthService.Import(c.Request.Context(), service.KiroImportInput{
		TokenJSON:              req.TokenJSON,
		ClientRegistrationJSON: req.ClientRegistrationJSON,
		ProxyID:                req.ProxyID,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, KiroImportResponse{
		TokenInfo:   tokenInfo,
		Credentials: h.kiroOAuthService.BuildAccountCredentials(tokenInfo),
	})
}

// RefreshAccountToken 对已有账号执行一次真实刷新并落库。
func (h *KiroHandler) RefreshAccountToken(c *gin.Context) {
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
	if account.Platform != service.PlatformKiro {
		response.BadRequest(c, "Account platform does not match Kiro endpoint")
		return
	}
	if !account.IsOAuth() {
		response.BadRequest(c, "Cannot refresh non-OAuth account credentials")
		return
	}

	tokenInfo, err := h.kiroOAuthService.RefreshAccountToken(c.Request.Context(), account)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	newCredentials := service.MergeCredentials(
		account.Credentials,
		h.kiroOAuthService.BuildAccountCredentials(tokenInfo),
	)
	// 这条路径绕开了 OAuthRefreshAPI.RefreshIfNeeded，_token_version 不会被自动推进。
	// 不推进的话，请求路径上的 CheckTokenVersion 认不出凭证已经换过，
	// 并发的 provider 还会把旧 token 覆盖回缓存。
	newCredentials["_token_version"] = time.Now().UnixMilli()

	updatedAccount, err := h.adminService.UpdateAccount(c.Request.Context(), accountID, &service.UpdateAccountInput{
		Credentials: newCredentials,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	// 缓存里那份旧 access token 必须立刻作废，否则「后台刷新成功」之后
	// 网关仍会拿旧 token 打上游，直到 TTL 自然过期。
	if h.tokenCacheInvalidator != nil {
		if err := h.tokenCacheInvalidator.InvalidateToken(c.Request.Context(), updatedAccount); err != nil {
			slog.Warn("kiro_refresh_token_cache_invalidate_failed", "account_id", accountID, "error", err)
		}
	}
	response.Success(c, dto.AccountFromService(updatedAccount))
}
