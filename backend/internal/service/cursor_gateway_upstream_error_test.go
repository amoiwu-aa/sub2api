package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newCursorGatewayTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c, recorder
}

// 上游对欠费和「24 小时内设备过多」返回的顶层字段一模一样：
// code=resource_exhausted、message="Error"。只回这两个字段的话，面板上两种
// 故障长得完全一样，而它们一个要人去付账单、另一个等一天就自己好了。
func TestCursorGatewayUpstreamErrorSurfacesUpstreamReason(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "unpaid invoice",
			payload: `{"error":{"code":"resource_exhausted","message":"Error","details":[{"debug":{"error":"ERROR_RATE_LIMITED","details":{"title":"You have an unpaid invoice","detail":"Visit cursor.com/dashboard and pay your invoice in Stripe to resume requests.","isRetryable":false}}}]}}`,
			want:    "You have an unpaid invoice",
		},
		{
			name:    "too many computers",
			payload: `{"error":{"code":"resource_exhausted","message":"Error","details":[{"debug":{"error":"ERROR_CUSTOM_MESSAGE","details":{"title":"Too many computers.","detail":"Too many computers used within the last 24 hours for the same Cursor account."}}}]}}`,
			want:    "Too many computers",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &CursorGatewayService{}
			c, _ := newCursorGatewayTestContext()
			connectErr := cursor.ParseEndStreamError([]byte(tc.payload))
			require.NotNil(t, connectErr)

			err := svc.upstreamError(context.Background(), c, nil, "cursor/default", connectErr, false)

			var failoverErr *UpstreamFailoverError
			require.ErrorAs(t, err, &failoverErr)
			require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
			body := string(failoverErr.ResponseBody)
			require.Contains(t, body, tc.want)
			require.Contains(t, body, connectErr.UpstreamCode())
			// 那句什么也没说的通用文案不该再出现，否则真实原因等于没透出来。
			require.NotContains(t, body, "Cursor agent stream ended with an error")

			// failover 用尽账号后会覆盖客户端响应，ops 上下文是真实原因唯一活得下来的地方。
			require.Contains(t, c.GetString(OpsUpstreamErrorMessageKey), tc.want)
			raw, ok := c.Get(OpsUpstreamErrorsKey)
			require.True(t, ok, "必须留下一条 ops 上游错误事件")
			events, ok := raw.([]*OpsUpstreamErrorEvent)
			require.True(t, ok)
			require.Len(t, events, 1)
			require.Equal(t, PlatformCursor, events[0].Platform)
			require.Equal(t, http.StatusTooManyRequests, events[0].UpstreamStatusCode)
			require.Equal(t, connectErr.UpstreamCode(), events[0].Reason)
			require.Contains(t, events[0].Detail, tc.want)
		})
	}
}

// 上游没给 details 时必须退回原来的通用文案，不能变成一句空话。
func TestCursorGatewayUpstreamErrorFallsBackWithoutDetails(t *testing.T) {
	svc := &CursorGatewayService{}
	c, _ := newCursorGatewayTestContext()

	err := svc.upstreamError(context.Background(), c, nil, "cursor/default",
		&cursor.ConnectError{Code: "unavailable", Message: "upstream is down"}, false)

	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.Contains(t, string(failoverErr.ResponseBody), "Cursor agent stream ended with an error")
}

func TestCursorGatewayAnthropicStreamUsesProtocolErrorEvent(t *testing.T) {
	svc := &CursorGatewayService{}
	c, rec := newCursorGatewayTestContext()

	err := svc.upstreamAnthropicError(context.Background(), c, nil, "cursor/grok-4.6",
		errors.New("stream broke"), true)

	require.Error(t, err)
	body := rec.Body.String()
	require.Contains(t, body, "event: error")
	require.Contains(t, body, `"type":"error"`)
	require.NotContains(t, body, "data: [DONE]")
}

func TestCursorGatewayResponsesStreamUsesResponseFailed(t *testing.T) {
	svc := &CursorGatewayService{}
	c, rec := newCursorGatewayTestContext()
	state := apicompat.NewChatCompletionsToResponsesStreamState("cursor/grok-4.6")

	err := svc.upstreamResponsesError(context.Background(), c, nil, "cursor/grok-4.6",
		errors.New("stream broke"), true, state)

	require.Error(t, err)
	body := rec.Body.String()
	require.Contains(t, body, "event: response.failed")
	require.Contains(t, body, `"status":"failed"`)
	require.Contains(t, body, `"object":"response"`)
	require.NotContains(t, body, `data: {"error":`)
	require.True(t, state.CompletedSent)
}

func TestCursorGatewayProtocolErrorsPreserveFailoverBeforeStreamStarts(t *testing.T) {
	svc := &CursorGatewayService{}

	anthropicCtx, anthropicRec := newCursorGatewayTestContext()
	err := svc.upstreamAnthropicError(context.Background(), anthropicCtx, nil, "cursor/grok-4.6",
		errors.New("before first delta"), false)
	var anthropicFailover *UpstreamFailoverError
	require.ErrorAs(t, err, &anthropicFailover)
	require.Empty(t, anthropicRec.Body.String())

	responsesCtx, responsesRec := newCursorGatewayTestContext()
	err = svc.upstreamResponsesError(context.Background(), responsesCtx, nil, "cursor/grok-4.6",
		errors.New("before first delta"), false,
		apicompat.NewChatCompletionsToResponsesStreamState("cursor/grok-4.6"))
	var responsesFailover *UpstreamFailoverError
	require.ErrorAs(t, err, &responsesFailover)
	require.Empty(t, responsesRec.Body.String())
}
