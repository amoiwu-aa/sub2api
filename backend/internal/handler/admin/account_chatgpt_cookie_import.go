package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/chatgptcookie"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const (
	chatGPTCookieImportBodyLimit = chatgptcookie.MaxInputBytes*2 + 1<<20
	chatGPTCookieSource          = "chatgpt_cookie"
)

type ChatGPTCookieExchangeRequest struct {
	Content   string `json:"content"`
	UserAgent string `json:"user_agent"`
	ProxyID   *int64 `json:"proxy_id"`
}

type ChatGPTCookiePreviewResult struct {
	InputFormat  string `json:"input_format"`
	CookieCount  int    `json:"cookie_count"`
	EndpointHost string `json:"endpoint_host"`
	Email        string `json:"email,omitempty"`
	AccountID    string `json:"account_id,omitempty"`
	PlanType     string `json:"plan_type,omitempty"`
	ExpiresAt    string `json:"expires_at"`
}

type ChatGPTCookieReimportRequest struct {
	Content   string `json:"content"`
	UserAgent string `json:"user_agent"`
}

// ChatGPTCookieImportRequest reuses all account-creation controls from the
// Codex session importer and adds only the browser fingerprint input.
type ChatGPTCookieImportRequest struct {
	CodexSessionImportRequest
	UserAgent string `json:"user_agent"`
}

// PreviewChatGPTCookie validates a browser session and returns only non-secret
// metadata. The access token and raw cookies never leave the backend.
func (h *AccountHandler) PreviewChatGPTCookie(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, chatGPTCookieImportBodyLimit)

	var req ChatGPTCookieExchangeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := validateChatGPTCookieExchangeRequest(req.Content, req.UserAgent); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	exchanged, err := h.exchangeChatGPTCookie(c.Request.Context(), req.Content, req.UserAgent, req.ProxyID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, buildChatGPTCookiePreview(exchanged))
}

// ImportChatGPTCookie performs a one-shot browser session exchange and then
// feeds the sanitized credential into the existing Codex account importer.
// The raw cookie is neither persisted nor returned.
func (h *AccountHandler) ImportChatGPTCookie(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, chatGPTCookieImportBodyLimit)

	var req ChatGPTCookieImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := validateChatGPTCookieImportRequest(req); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	executeAdminIdempotentJSON(
		c,
		"admin.accounts.import_chatgpt_cookie",
		req,
		service.DefaultWriteIdempotencyTTL(),
		func(ctx context.Context) (any, error) {
			exchanged, err := h.exchangeChatGPTCookie(ctx, req.Content, req.UserAgent, req.ProxyID)
			if err != nil {
				return nil, err
			}

			credentialJSON, err := marshalChatGPTCookieCredential(exchanged)
			if err != nil {
				return nil, err
			}
			importReq := req.CodexSessionImportRequest
			importReq.Content = credentialJSON
			importReq.Contents = nil
			importReq.Extra = markChatGPTCookieSource(importReq.Extra, time.Now())

			entries, err := parseCodexSessionImportEntries(importReq)
			if err != nil {
				return nil, infraerrors.Newf(
					http.StatusBadRequest,
					"CHATGPT_COOKIE_CREDENTIAL_INVALID",
					"converted credential is not importable: %v",
					err,
				)
			}
			return h.importCodexSessions(ctx, importReq, entries)
		},
	)
}

// ReimportChatGPTCookie replaces only the credentials of one explicitly
// selected OpenAI OAuth account. The converted identity must match the target,
// so a Cookie from another account can never create or overwrite an account.
func (h *AccountHandler) ReimportChatGPTCookie(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, chatGPTCookieImportBodyLimit)

	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	var req ChatGPTCookieReimportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := validateChatGPTCookieExchangeRequest(req.Content, req.UserAgent); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	executeAdminIdempotentJSON(
		c,
		"admin.accounts.reimport_chatgpt_cookie",
		req,
		service.DefaultWriteIdempotencyTTL(),
		func(ctx context.Context) (any, error) {
			target, err := h.adminService.GetAccount(ctx, accountID)
			if err != nil {
				return nil, err
			}
			if target.Platform != service.PlatformOpenAI ||
				target.Type != service.AccountTypeOAuth ||
				target.ParentAccountID != nil {
				return nil, infraerrors.BadRequest(
					"CHATGPT_COOKIE_TARGET_INVALID",
					"ChatGPT Cookie re-import requires a non-shadow OpenAI OAuth account",
				)
			}

			exchanged, err := h.exchangeChatGPTCookie(
				ctx,
				req.Content,
				req.UserAgent,
				target.ProxyID,
			)
			if err != nil {
				return nil, err
			}
			credentialJSON, err := marshalChatGPTCookieCredential(exchanged)
			if err != nil {
				return nil, err
			}

			updateExisting := true
			importReq := CodexSessionImportRequest{
				Content:         credentialJSON,
				Extra:           markChatGPTCookieSource(nil, time.Now()),
				UpdateExisting:  &updateExisting,
				targetAccountID: &accountID,
			}
			entries, err := parseCodexSessionImportEntries(importReq)
			if err != nil {
				return nil, infraerrors.Newf(
					http.StatusBadRequest,
					"CHATGPT_COOKIE_CREDENTIAL_INVALID",
					"converted credential is not importable: %v",
					err,
				)
			}
			return h.importCodexSessions(ctx, importReq, entries)
		},
	)
}

func validateChatGPTCookieImportRequest(req ChatGPTCookieImportRequest) error {
	if err := validateChatGPTCookieExchangeRequest(req.Content, req.UserAgent); err != nil {
		return err
	}
	if len(req.Contents) > 0 {
		return infraerrors.BadRequest("CHATGPT_COOKIE_BATCH_UNSUPPORTED", "use one Cookie-Editor export per import")
	}
	if err := service.ValidateOpenAILongContextBillingExtra(service.PlatformOpenAI, req.Extra); err != nil {
		return err
	}
	if req.Concurrency != nil && *req.Concurrency < 0 {
		return infraerrors.BadRequest("CHATGPT_COOKIE_CONCURRENCY_INVALID", "concurrency must be >= 0")
	}
	if req.Priority != nil && *req.Priority < 0 {
		return infraerrors.BadRequest("CHATGPT_COOKIE_PRIORITY_INVALID", "priority must be >= 0")
	}
	if req.RateMultiplier != nil && *req.RateMultiplier < 0 {
		return infraerrors.BadRequest("CHATGPT_COOKIE_RATE_MULTIPLIER_INVALID", "rate_multiplier must be >= 0")
	}
	if req.LoadFactor != nil && *req.LoadFactor > 10000 {
		return infraerrors.BadRequest("CHATGPT_COOKIE_LOAD_FACTOR_INVALID", "load_factor must be <= 10000")
	}
	return nil
}

func validateChatGPTCookieExchangeRequest(content, userAgent string) error {
	if strings.TrimSpace(content) == "" {
		return infraerrors.BadRequest("CHATGPT_COOKIE_REQUIRED", "ChatGPT Cookie-Editor content is required")
	}
	if len(content) > chatgptcookie.MaxInputBytes {
		return infraerrors.BadRequest("CHATGPT_COOKIE_TOO_LARGE", "ChatGPT Cookie-Editor content exceeds the size limit")
	}
	if len(userAgent) > chatgptcookie.MaxUserAgentBytes {
		return infraerrors.BadRequest("CHATGPT_COOKIE_USER_AGENT_TOO_LARGE", "User-Agent exceeds the size limit")
	}
	if strings.ContainsAny(userAgent, "\x00\r\n") {
		return infraerrors.BadRequest("CHATGPT_COOKIE_USER_AGENT_INVALID", "User-Agent contains invalid characters")
	}
	return nil
}

func (h *AccountHandler) exchangeChatGPTCookie(
	ctx context.Context,
	content string,
	userAgent string,
	proxyID *int64,
) (*chatgptcookie.Result, error) {
	if h == nil || h.openaiOAuthService == nil {
		return nil, infraerrors.New(
			http.StatusServiceUnavailable,
			"CHATGPT_COOKIE_SERVICE_UNAVAILABLE",
			"ChatGPT Cookie import service is unavailable",
		)
	}
	return h.openaiOAuthService.ExchangeChatGPTCookie(ctx, &service.ChatGPTCookieExchangeInput{
		Content:   content,
		UserAgent: userAgent,
		ProxyID:   proxyID,
	})
}

func marshalChatGPTCookieCredential(exchanged *chatgptcookie.Result) (string, error) {
	if exchanged == nil {
		return "", infraerrors.New(
			http.StatusBadGateway,
			"CHATGPT_COOKIE_CREDENTIAL_INVALID",
			"ChatGPT session conversion returned no credential",
		)
	}
	credentialJSON, err := json.Marshal(exchanged.Credential)
	if err != nil {
		return "", infraerrors.Newf(
			http.StatusInternalServerError,
			"CHATGPT_COOKIE_SERIALIZE_FAILED",
			"failed to serialize converted credential: %v",
			err,
		)
	}
	return string(credentialJSON), nil
}

func buildChatGPTCookiePreview(exchanged *chatgptcookie.Result) ChatGPTCookiePreviewResult {
	preview := ChatGPTCookiePreviewResult{}
	if exchanged == nil {
		return preview
	}
	preview.InputFormat = exchanged.InputFormat
	preview.CookieCount = exchanged.CookieCount
	preview.EndpointHost = exchanged.EndpointHost
	preview.ExpiresAt = exchanged.TokenExpiresAt.UTC().Format(time.RFC3339)
	preview.Email = chatGPTCookieMapString(exchanged.Credential.User, "email")
	preview.AccountID = chatGPTCookieMapString(exchanged.Credential.Account, "id", "account_id")
	preview.PlanType = chatGPTCookieMapString(exchanged.Credential.Account, "planType", "plan_type")
	return preview
}

func chatGPTCookieMapString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	return ""
}

func markChatGPTCookieSource(extra map[string]any, now time.Time) map[string]any {
	out := make(map[string]any, len(extra)+2)
	for key, value := range extra {
		out[key] = value
	}
	out["openai_credential_source"] = chatGPTCookieSource
	out["chatgpt_cookie_imported_at"] = now.UTC().Format(time.RFC3339)
	return out
}
