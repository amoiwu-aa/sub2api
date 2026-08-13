package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestResolveCursorConversationIDPrecedence(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Request.Header.Set("session_id", "header-session")
	c.Set("api_key", &APIKey{ID: 41})

	account := &Account{ID: 73, Platform: PlatformCursor}
	conversation := &cursor.Conversation{Turns: []cursor.Turn{{
		Role: cursor.RoleUser,
		Text: "stable first user message",
	}}}

	require.Equal(t, "body-id", resolveCursorConversationID(
		c, account, conversation, " body-id "))
	require.Equal(t, "header-session", resolveCursorConversationID(
		c, account, conversation, ""))

	c.Request.Header.Del("session_id")
	derived := cursorConversationID(c, account, conversation)
	require.NotEmpty(t, derived)
	require.Equal(t, derived, resolveCursorConversationID(c, account, conversation, ""))
	require.Equal(t, derived, resolveCursorConversationID(c, account, conversation, ""))
}

func TestCursorConversationIDOnlyUsesRandomWhenNothingCanBeDerived(t *testing.T) {
	account := &Account{ID: 73, Platform: PlatformCursor}
	first := cursorConversationID(nil, account, nil)
	second := cursorConversationID(nil, account, nil)
	require.NotEmpty(t, first)
	require.Equal(t, first, second)

	randomFirst := resolveCursorConversationID(nil, nil, nil, "")
	randomSecond := resolveCursorConversationID(nil, nil, nil, "")
	require.NotEmpty(t, randomFirst)
	require.NotEqual(t, randomFirst, randomSecond)
	_, err := uuid.Parse(randomFirst)
	require.NoError(t, err)
}

func TestCursorBuildResultMarksLocalUsageEstimatedAndCacheUnavailable(t *testing.T) {
	result := (&CursorGatewayService{}).buildResult(
		"prompt text",
		"completion text",
		"cursor-auto",
		"cursor-small",
		false,
		nil,
		time.Now().Add(-time.Millisecond),
		false,
	)

	require.Equal(t, CacheUsageSourceEstimated, result.Usage.CacheUsageSource)
	require.Positive(t, result.Usage.InputTokens)
	require.Positive(t, result.Usage.OutputTokens)
	require.Zero(t, result.Usage.CacheCreationInputTokens)
	require.Zero(t, result.Usage.CacheReadInputTokens)
	require.Zero(t, result.Usage.CacheCreation5mTokens)
	require.Zero(t, result.Usage.CacheCreation1hTokens)
}

func TestCursorEstimatedChatStreamUsageContainsOnlyBasicTokens(t *testing.T) {
	chunk := cursorEstimatedChatUsageChunk(
		"chatcmpl-1",
		"cursor/default",
		1,
		"prompt text",
		"completion text",
	)
	raw, err := json.Marshal(chunk)
	require.NoError(t, err)

	require.Positive(t, gjson.GetBytes(raw, "usage.prompt_tokens").Int())
	require.Positive(t, gjson.GetBytes(raw, "usage.completion_tokens").Int())
	require.Equal(t,
		gjson.GetBytes(raw, "usage.prompt_tokens").Int()+gjson.GetBytes(raw, "usage.completion_tokens").Int(),
		gjson.GetBytes(raw, "usage.total_tokens").Int(),
	)
	require.False(t, gjson.GetBytes(raw, "usage.prompt_tokens_details").Exists())
	require.False(t, gjson.GetBytes(raw, "usage.cache_read_input_tokens").Exists())
	require.False(t, gjson.GetBytes(raw, "usage.cache_creation_input_tokens").Exists())
}

func TestCursorEstimatedResponsesStreamUsageContainsOnlyBasicTokens(t *testing.T) {
	usage := cursorEstimatedResponsesUsage("prompt text", "completion text")
	raw, err := json.Marshal(usage)
	require.NoError(t, err)

	require.Positive(t, gjson.GetBytes(raw, "input_tokens").Int())
	require.Positive(t, gjson.GetBytes(raw, "output_tokens").Int())
	require.Equal(t,
		gjson.GetBytes(raw, "input_tokens").Int()+gjson.GetBytes(raw, "output_tokens").Int(),
		gjson.GetBytes(raw, "total_tokens").Int(),
	)
	require.False(t, gjson.GetBytes(raw, "input_tokens_details").Exists())
	require.False(t, gjson.GetBytes(raw, "cache_creation_input_tokens").Exists())
}
