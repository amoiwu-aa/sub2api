package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRecordAgentIncompleteWritesOpsEvent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	account := &Account{ID: 42, Name: "cursor-probe", Platform: PlatformCursor}
	svc := &CursorGatewayService{}
	svc.recordAgentIncomplete(c, account, "cursor/default", "default", &cursor.AgentTurnResult{
		Stalled:        true,
		ExecUnanswered: 1,
		QueryIgnored:   1,
		Text:           "hi",
	})

	require.Equal(t, "stalled;no_turn_ended;exec_unanswered=1;query_ignored=1", rec.Header().Get("X-RingStar-Cursor-Agent"))
	require.Equal(t, "Cursor agent turn incomplete: stalled;no_turn_ended;exec_unanswered=1;query_ignored=1", c.GetString(OpsUpstreamErrorMessageKey))

	raw, ok := c.Get(OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := raw.([]*OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, events, 1)
	require.Equal(t, "cursor_agent_stall", events[0].Kind)
	require.Equal(t, string(GatewayFailureScopeRequest), events[0].Scope)
	require.Equal(t, int64(42), events[0].AccountID)
	require.Equal(t, http.StatusOK, events[0].UpstreamStatusCode)
}

func TestRecordAgentIncompleteSkipsHealthyTurn(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	svc := &CursorGatewayService{}
	svc.recordAgentIncomplete(c, &Account{ID: 1}, "cursor/default", "default", &cursor.AgentTurnResult{
		TurnEnded: true,
		Text:      "ok",
	})

	require.Empty(t, rec.Header().Get("X-RingStar-Cursor-Agent"))
	_, ok := c.Get(OpsUpstreamErrorsKey)
	require.False(t, ok)
}

func stalledCursorTurn() *cursor.AgentTurnResult {
	return &cursor.AgentTurnResult{Stalled: true, KVSeen: 7}
}

func TestCursorIncompleteDoesNotFailoverOrReportAccountError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &CursorGatewayService{}
	account := &Account{ID: 8, Name: "ultra", Platform: PlatformCursor}
	incomplete := cursorIncompleteTurnError(stalledCursorTurn())

	c, rec := newCursorGatewayTestContext()
	svc.recordAgentIncomplete(c, account, "cursor/grok-4.6-max", "grok-4.6-max", stalledCursorTurn())
	err := svc.upstreamError(context.Background(), c, account, "cursor/grok-4.6-max", incomplete, false)

	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr), "stall 不得排除账号去换号")
	require.True(t, IsResponseCommitted(c))
	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Contains(t, rec.Body.String(), "stalled")
	require.Contains(t, rec.Body.String(), "kv=7")

	raw, ok := c.Get(OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := raw.([]*OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, events, 1)
	require.Equal(t, "cursor_agent_stall", events[0].Kind)
	require.Equal(t, string(GatewayFailureScopeRequest), events[0].Scope)
}

func TestCursorIncompleteStreamingWritesProtocolTerminal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &CursorGatewayService{}
	incomplete := cursorIncompleteTurnError(stalledCursorTurn())

	chatCtx, chatRec := newCursorGatewayTestContext()
	err := svc.upstreamError(context.Background(), chatCtx, nil, "cursor/grok-4.6-max", incomplete, true)
	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	require.Contains(t, chatRec.Body.String(), `"message":"Cursor agent turn incomplete: stalled;no_turn_ended;kv=7"`)
	require.Contains(t, chatRec.Body.String(), "data: [DONE]")
	require.True(t, IsResponseCommitted(chatCtx))

	anthropicCtx, anthropicRec := newCursorGatewayTestContext()
	err = svc.upstreamAnthropicError(context.Background(), anthropicCtx, nil, "cursor/grok-4.6-max", incomplete, true)
	require.Error(t, err)
	require.False(t, errors.As(err, &failoverErr))
	require.Contains(t, anthropicRec.Body.String(), "event: error")
	require.Contains(t, anthropicRec.Body.String(), "stalled;no_turn_ended;kv=7")
	require.True(t, IsResponseCommitted(anthropicCtx))

	responsesCtx, responsesRec := newCursorGatewayTestContext()
	state := apicompat.NewChatCompletionsToResponsesStreamState("cursor/grok-4.6-max")
	err = svc.upstreamResponsesError(context.Background(), responsesCtx, nil, "cursor/grok-4.6-max", incomplete, true, state)
	require.Error(t, err)
	require.False(t, errors.As(err, &failoverErr))
	require.Contains(t, responsesRec.Body.String(), "event: response.failed")
	require.Contains(t, responsesRec.Body.String(), "stalled;no_turn_ended;kv=7")
	require.True(t, state.CompletedSent)
	require.True(t, IsResponseCommitted(responsesCtx))
}

func TestCursorIncompleteBufferedWritesJSONNotFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &CursorGatewayService{}
	incomplete := cursorIncompleteTurnError(stalledCursorTurn())

	responsesCtx, responsesRec := newCursorGatewayTestContext()
	err := svc.upstreamResponsesError(context.Background(), responsesCtx, nil, "cursor/grok-4.6-max",
		incomplete, false, apicompat.NewChatCompletionsToResponsesStreamState("cursor/grok-4.6-max"))
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	require.Equal(t, http.StatusBadGateway, responsesRec.Code)
	var parsed map[string]any
	require.NoError(t, json.Unmarshal(responsesRec.Body.Bytes(), &parsed))
	require.Contains(t, responsesRec.Body.String(), "Cursor agent turn incomplete")
}
