package admin

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/chatgptcookie"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	reqv3 "github.com/imroc/req/v3"
	"github.com/stretchr/testify/require"
)

func TestImportChatGPTCookieConvertsAndCreatesWithoutPersistingBrowserSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousCoordinator := service.DefaultIdempotencyCoordinator()
	service.SetDefaultIdempotencyCoordinator(nil)
	t.Cleanup(func() { service.SetDefaultIdempotencyCoordinator(previousCoordinator) })

	const browserSession = "browser-session-must-not-be-stored"
	accessToken := buildCodexImportTestJWT(t, time.Now().Add(time.Hour), map[string]any{
		"email": "browser@example.com",
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "workspace-cookie",
			"chatgpt_user_id":    "user-cookie",
			"chatgpt_plan_type":  "plus",
		},
	})

	reqClient := reqv3.C()
	reqClient.GetClient().Transport = chatGPTCookieHandlerRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, "__Secure-next-auth.session-token="+browserSession, r.Header.Get("Cookie"))
		body, err := json.Marshal(map[string]any{
			"accessToken": accessToken,
			"user":        map[string]any{"email": "browser@example.com"},
			"account":     map[string]any{"id": "workspace-cookie", "planType": "plus"},
		})
		require.NoError(t, err)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(body)),
			Request:    r,
		}, nil
	})
	openAIService := service.NewOpenAIOAuthService(nil, nil)
	openAIService.SetPrivacyClientFactory(func(string) (*reqv3.Client, error) {
		return reqClient, nil
	})

	adminService := newCodexImportMemoryAdminService(nil)
	handler := NewAccountHandler(
		adminService,
		nil,
		openAIService,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
	router := gin.New()
	router.POST("/accounts/import/chatgpt-cookie", handler.ImportChatGPTCookie)

	requestBody, err := json.Marshal(map[string]any{
		"content":                 "__Secure-next-auth.session-token=" + browserSession,
		"user_agent":              "test-browser",
		"name":                    "Browser Cookie import",
		"skip_default_group_bind": true,
	})
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/accounts/import/chatgpt-cookie",
		bytes.NewReader(requestBody),
	)
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Len(t, adminService.createdAccounts, 1)
	created := adminService.createdAccounts[0]
	require.Equal(t, service.PlatformOpenAI, created.Platform)
	require.Equal(t, service.AccountTypeOAuth, created.Type)
	require.Equal(t, accessToken, created.Credentials["access_token"])
	require.Equal(t, "workspace-cookie", created.Credentials["chatgpt_account_id"])
	require.Equal(t, chatGPTCookieSource, created.Extra["openai_credential_source"])
	require.NotEmpty(t, created.Extra["chatgpt_cookie_imported_at"])
	require.NotContains(t, created.Credentials, "cookie")
	require.NotContains(t, created.Credentials, "session_token")

	storedJSON, err := json.Marshal(created)
	require.NoError(t, err)
	require.NotContains(t, string(storedJSON), browserSession)
	require.NotContains(t, recorder.Body.String(), browserSession)
}

func TestPreviewChatGPTCookieReturnsOnlyNonSecretMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const browserSession = "preview-browser-session-canary"
	accessToken := buildCodexImportTestJWT(t, time.Now().Add(time.Hour), map[string]any{
		"email": "preview@example.com",
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "workspace-preview",
			"chatgpt_plan_type":  "plus",
		},
	})

	openAIService := service.NewOpenAIOAuthService(nil, nil)
	openAIService.SetPrivacyClientFactory(func(string) (*reqv3.Client, error) {
		return newChatGPTCookieHandlerClient(t, accessToken, "preview@example.com", "workspace-preview", "plus"), nil
	})
	handler := NewAccountHandler(
		newCodexImportMemoryAdminService(nil),
		nil,
		openAIService,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
	router := gin.New()
	router.POST("/accounts/import/chatgpt-cookie/preview", handler.PreviewChatGPTCookie)

	requestBody, err := json.Marshal(map[string]any{
		"content":    "__Secure-next-auth.session-token=" + browserSession,
		"user_agent": "test-browser",
	})
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/accounts/import/chatgpt-cookie/preview",
		bytes.NewReader(requestBody),
	)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Contains(t, recorder.Body.String(), `"input_format":"Header String"`)
	require.Contains(t, recorder.Body.String(), `"email":"preview@example.com"`)
	require.Contains(t, recorder.Body.String(), `"account_id":"workspace-preview"`)
	require.NotContains(t, recorder.Body.String(), browserSession)
	require.NotContains(t, recorder.Body.String(), accessToken)
}

func TestReimportChatGPTCookieUpdatesOnlyMatchingTarget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousCoordinator := service.DefaultIdempotencyCoordinator()
	service.SetDefaultIdempotencyCoordinator(nil)
	t.Cleanup(func() { service.SetDefaultIdempotencyCoordinator(previousCoordinator) })

	oldToken := buildCodexImportTestJWT(t, time.Now().Add(30*time.Minute), map[string]any{
		"email": "target@example.com",
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "workspace-target",
			"chatgpt_user_id":    "user-target",
		},
	})
	newToken := buildCodexImportTestJWT(t, time.Now().Add(2*time.Hour), map[string]any{
		"email": "target@example.com",
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "workspace-target",
			"chatgpt_user_id":    "user-target",
			"chatgpt_plan_type":  "plus",
		},
	})
	adminService := newCodexImportMemoryAdminService([]service.Account{{
		ID:       42,
		Name:     "Target",
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":       oldToken,
			"email":              "target@example.com",
			"chatgpt_account_id": "workspace-target",
			"chatgpt_user_id":    "user-target",
		},
		Extra: map[string]any{"keep": "value"},
	}})
	openAIService := service.NewOpenAIOAuthService(nil, nil)
	openAIService.SetPrivacyClientFactory(func(string) (*reqv3.Client, error) {
		return newChatGPTCookieHandlerClient(t, newToken, "target@example.com", "workspace-target", "plus"), nil
	})
	handler := NewAccountHandler(
		adminService,
		nil,
		openAIService,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
	router := gin.New()
	router.POST("/accounts/:id/reimport/chatgpt-cookie", handler.ReimportChatGPTCookie)

	const browserSession = "reimport-browser-session-canary"
	requestBody, err := json.Marshal(map[string]any{
		"content": "__Secure-next-auth.session-token=" + browserSession,
	})
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/accounts/42/reimport/chatgpt-cookie",
		bytes.NewReader(requestBody),
	)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Empty(t, adminService.createdAccounts)
	require.Len(t, adminService.updatedAccounts, 1)
	update := adminService.updatedAccounts[0]
	require.EqualValues(t, 42, update.id)
	require.Equal(t, newToken, update.input.Credentials["access_token"])
	require.Equal(t, "value", update.input.Extra["keep"])
	require.Equal(t, chatGPTCookieSource, update.input.Extra["openai_credential_source"])
	require.NotContains(t, recorder.Body.String(), browserSession)
}

func TestReimportChatGPTCookieRejectsDifferentIdentityWithoutMutation(t *testing.T) {
	target := service.Account{
		ID:       42,
		Name:     "Target",
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeOAuth,
		Credentials: map[string]any{
			"email":              "target@example.com",
			"chatgpt_account_id": "workspace-target",
			"chatgpt_user_id":    "user-target",
		},
	}
	item := &codexImportAccount{
		Email:     "other@example.com",
		AccountID: "workspace-other",
		UserID:    "user-other",
	}
	require.False(t, chatGPTCookieTargetIdentityMatches(&target, item))

	item = &codexImportAccount{
		Email:     "target@example.com",
		AccountID: "workspace-target",
		UserID:    "different-user",
	}
	require.False(t, chatGPTCookieTargetIdentityMatches(&target, item))
}

func TestTargetedCookieImportCannotCreateOrUpdateDifferentAccount(t *testing.T) {
	adminService := newCodexImportMemoryAdminService([]service.Account{{
		ID:       42,
		Name:     "Target",
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeOAuth,
		Credentials: map[string]any{
			"email":              "target@example.com",
			"chatgpt_account_id": "workspace-target",
			"chatgpt_user_id":    "user-target",
		},
	}})
	handler := &AccountHandler{adminService: adminService}
	otherToken := buildCodexImportTestJWT(t, time.Now().Add(time.Hour), map[string]any{
		"email": "other@example.com",
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "workspace-other",
			"chatgpt_user_id":    "user-other",
		},
	})
	targetID := int64(42)
	updateExisting := true
	req := CodexSessionImportRequest{
		Content:         otherToken,
		UpdateExisting:  &updateExisting,
		targetAccountID: &targetID,
	}
	entries, err := parseCodexSessionImportEntries(req)
	require.NoError(t, err)

	result, err := handler.importCodexSessions(t.Context(), req, entries)

	require.NoError(t, err)
	require.Equal(t, 1, result.Failed)
	require.Empty(t, adminService.createdAccounts)
	require.Empty(t, adminService.updatedAccounts)
	require.NotContains(t, result.Errors[0].Message, otherToken)
}

func TestValidateChatGPTCookieImportRejectsBatchAndHeaderInjection(t *testing.T) {
	err := validateChatGPTCookieImportRequest(ChatGPTCookieImportRequest{
		CodexSessionImportRequest: CodexSessionImportRequest{
			Content:  "__Secure-next-auth.session-token=one",
			Contents: []string{"second"},
		},
	})
	require.Error(t, err)

	err = validateChatGPTCookieImportRequest(ChatGPTCookieImportRequest{
		CodexSessionImportRequest: CodexSessionImportRequest{Content: "cookie=value"},
		UserAgent:                 "browser\r\nX-Leak: yes",
	})
	require.Error(t, err)
	require.False(t, strings.Contains(err.Error(), "cookie=value"))

	err = validateChatGPTCookieImportRequest(ChatGPTCookieImportRequest{
		CodexSessionImportRequest: CodexSessionImportRequest{Content: "cookie=value"},
		UserAgent:                 strings.Repeat("x", chatgptcookie.MaxUserAgentBytes+1),
	})
	require.Error(t, err)
}

type chatGPTCookieHandlerRoundTripFunc func(*http.Request) (*http.Response, error)

func (f chatGPTCookieHandlerRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newChatGPTCookieHandlerClient(
	t *testing.T,
	accessToken string,
	email string,
	accountID string,
	planType string,
) *reqv3.Client {
	t.Helper()
	client := reqv3.C()
	client.GetClient().Transport = chatGPTCookieHandlerRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, err := json.Marshal(map[string]any{
			"accessToken": accessToken,
			"user":        map[string]any{"email": email},
			"account":     map[string]any{"id": accountID, "planType": planType},
		})
		require.NoError(t, err)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(body)),
			Request:    r,
		}, nil
	})
	return client
}
