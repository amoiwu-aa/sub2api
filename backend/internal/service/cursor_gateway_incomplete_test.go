package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

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
