package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newKiroGatewayTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	return c, recorder
}

func TestKiroGatewayUpstreamErrorTriggersFailoverBeforeHeaders(t *testing.T) {
	svc := NewKiroGatewayService(nil, nil)
	c, _ := newKiroGatewayTestContext()

	err := svc.upstreamError(context.Background(), c, nil, "kiro/claude-sonnet-4.6", &kiro.APIError{Status: http.StatusTooManyRequests, Body: "slow down"}, false)

	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
	require.JSONEq(t,
		`{"type":"error","error":{"type":"rate_limit_error","message":"Kiro upstream request failed"}}`,
		string(failoverErr.ResponseBody),
	)
}

func TestKiroGatewayUpstreamErrorAfterHeadersWritesErrorEventInsteadOfFailover(t *testing.T) {
	svc := NewKiroGatewayService(nil, nil)
	c, recorder := newKiroGatewayTestContext()

	// 流已经开出去了，客户端收到了开头就不能再换账号重来。
	err := svc.upstreamError(context.Background(), c, nil, "kiro/claude-sonnet-4.6", &kiro.APIError{Status: http.StatusUnauthorized}, true)

	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	require.Contains(t, recorder.Body.String(), "event: error")
	require.Contains(t, recorder.Body.String(), "authentication_error")
}

func TestKiroErrorBodyMapsStatusToAnthropicErrorType(t *testing.T) {
	cases := map[int]string{
		http.StatusUnauthorized:        "authentication_error",
		http.StatusForbidden:           "authentication_error",
		http.StatusTooManyRequests:     "rate_limit_error",
		http.StatusBadRequest:          "invalid_request_error",
		http.StatusBadGateway:          "api_error",
		http.StatusInternalServerError: "api_error",
	}
	for status, expected := range cases {
		require.Contains(t, string(kiroErrorBody(status, "m")), `"type":"`+expected+`"`, "status=%d", status)
	}
}

func TestKiroGatewayBuildResultOmitsUpstreamModelWhenIdentical(t *testing.T) {
	svc := NewKiroGatewayService(nil, nil)
	translator := kiro.NewResponseTranslator("msg", "kiro/claude-sonnet-4.6", nil)
	require.NoError(t, translator.Handle(kiro.StreamEvent{
		Metadata: &kiro.MetadataEvent{TokenUsage: &kiro.TokenUsage{
			UncachedInputTokens: 11, OutputTokens: 22, CacheReadInputTokens: 3, CacheWriteInputTokens: 4,
		}},
	}))
	require.NoError(t, translator.Finish())

	mapped := svc.buildResult(translator, nil, "kiro/claude-sonnet-4.6", "claude-sonnet-4.6", true, nil, time.Now(), false)
	require.Equal(t, "kiro/claude-sonnet-4.6", mapped.Model)
	require.Equal(t, "claude-sonnet-4.6", mapped.UpstreamModel)
	require.Equal(t, 11, mapped.Usage.InputTokens)
	require.Equal(t, 22, mapped.Usage.OutputTokens)
	require.Equal(t, 3, mapped.Usage.CacheReadInputTokens)
	require.Equal(t, 4, mapped.Usage.CacheCreationInputTokens)

	// 模型名相同的情况下不写 UpstreamModel，避免落一条无意义的映射记录。
	same := svc.buildResult(translator, nil, "claude-sonnet-4.6", "claude-sonnet-4.6", false, nil, time.Now(), false)
	require.Empty(t, same.UpstreamModel)
}

// TestKiroGatewayBuildResultCarriesUpstreamCredits 锁定 credit 一定要落到
// ForwardResult 上。免费档一个周期只有 50 credit 且用尽即硬拒，而成本列走的是
// Anthropic 价目表（实测与真实消耗相差约 20 倍），丢了这个数就没有任何字段能
// 回答「上游额度还剩多少」。
func TestKiroGatewayBuildResultCarriesUpstreamCredits(t *testing.T) {
	svc := NewKiroGatewayService(nil, nil)
	translator := kiro.NewResponseTranslator("msg", "kiro/auto", nil)
	require.NoError(t, translator.Handle(kiro.StreamEvent{
		Metadata: &kiro.MetadataEvent{TokenUsage: &kiro.TokenUsage{
			UncachedInputTokens: 42366, OutputTokens: 32,
		}},
	}))
	require.NoError(t, translator.Handle(kiro.StreamEvent{
		Metering: &kiro.MeteringEvent{Usage: 0.148231, Unit: "Credit"},
	}))
	require.NoError(t, translator.Finish())

	result := svc.buildResult(translator, nil, "kiro/auto", "auto", true, nil, time.Now(), false)
	require.InDelta(t, 0.148231, result.UpstreamCredits, 1e-9)
	// credit 与 token 不同量纲，绝不能混进 token 计费。
	require.Equal(t, 42366, result.Usage.InputTokens)
	require.Equal(t, 32, result.Usage.OutputTokens)
}

// TestKiroGatewayBuildResultCarriesCreditsWhenTokensEstimated 覆盖上游没给
// tokenUsage 的那一轮：token 退化成本地估算，但 credit 仍是上游权威值，
// 恰恰是这种时候最需要它来校准估算。
func TestKiroGatewayBuildResultCarriesCreditsWhenTokensEstimated(t *testing.T) {
	svc := NewKiroGatewayService(nil, nil)
	translator := kiro.NewResponseTranslator("msg", "kiro/auto", nil)
	require.NoError(t, translator.Handle(kiro.StreamEvent{
		Metering: &kiro.MeteringEvent{Usage: 0.082131, Unit: "Credit"},
	}))
	require.NoError(t, translator.Finish())

	result := svc.buildResult(translator, &kiro.ConversationState{}, "kiro/auto", "auto", false, nil, time.Now(), false)
	require.False(t, translator.HasUpstreamUsage())
	require.InDelta(t, 0.082131, result.UpstreamCredits, 1e-9)
}

// TestKiroGatewayBuildResultWithoutMeteringLeavesCreditsZero 确认上游没报时是 0
// 而不是垃圾值——落库侧不判空直接求和，非 Kiro 平台也全走这个零值。
func TestKiroGatewayBuildResultWithoutMeteringLeavesCreditsZero(t *testing.T) {
	svc := NewKiroGatewayService(nil, nil)
	translator := kiro.NewResponseTranslator("msg", "kiro/auto", nil)
	require.NoError(t, translator.Handle(kiro.StreamEvent{
		Metadata: &kiro.MetadataEvent{TokenUsage: &kiro.TokenUsage{UncachedInputTokens: 7, OutputTokens: 1}},
	}))
	require.NoError(t, translator.Finish())

	result := svc.buildResult(translator, nil, "kiro/auto", "auto", false, nil, time.Now(), false)
	require.Zero(t, result.UpstreamCredits)
}

func TestKiroGatewayForwardRejectsMissingModel(t *testing.T) {
	svc := NewKiroGatewayService(nil, nil)
	c, recorder := newKiroGatewayTestContext()

	_, err := svc.Forward(c.Request.Context(), c, &Account{Platform: PlatformKiro}, []byte(`{"messages":[]}`))
	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "Missing model")
}

func TestKiroGatewayForwardWithoutTokenProviderFailsOver(t *testing.T) {
	svc := NewKiroGatewayService(nil, nil)
	c, _ := newKiroGatewayTestContext()

	_, err := svc.Forward(c.Request.Context(), c, &Account{Platform: PlatformKiro},
		[]byte(`{"model":"kiro/claude-sonnet-4.6","messages":[{"role":"user","content":"hi"}]}`))

	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Contains(t, string(failoverErr.ResponseBody), "token provider")
}

func TestKiroTokenProviderRejectsConfiguredProxyMiss(t *testing.T) {
	provider := NewKiroTokenProvider(nil, nil)
	proxyID := int64(7)
	account := &Account{
		ID: 1, Platform: PlatformKiro, Type: AccountTypeOAuth,
		ProxyID:     &proxyID,
		Credentials: map[string]any{"access_token": "a", "expires_at": time.Now().Add(time.Hour).Format(time.RFC3339)},
	}

	// 配置了代理却取不到代理对象时绝不能退化成直连。
	_, err := provider.GetAccessToken(t.Context(), account)
	require.ErrorIs(t, err, errKiroConfiguredProxyMiss)
}

func TestKiroTokenProviderReturnsValidToken(t *testing.T) {
	provider := NewKiroTokenProvider(nil, nil)
	account := &Account{
		ID: 1, Platform: PlatformKiro, Type: AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "valid-token",
			"expires_at":   time.Now().Add(time.Hour).Format(time.RFC3339),
		},
	}

	token, err := provider.GetAccessToken(t.Context(), account)
	require.NoError(t, err)
	require.Equal(t, "valid-token", token)
}
