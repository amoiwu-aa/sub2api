package service

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCursorToolLoopGuardBlocksAllProtocolsBeforeUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &CursorGatewayService{maxToolContinuations: 2}
	tests := []struct {
		name string
		body string
		call func(context.Context, *gin.Context, *Account, []byte) (*ForwardResult, error)
	}{
		{
			name: "anthropic messages",
			body: `{
				"model": "cursor/grok-4.6",
				"messages": [
					{"role": "user", "content": "inspect"},
					{"role": "assistant", "content": [{"type": "tool_use", "id": "toolu_1", "name": "Read", "input": {"file_path": "a"}}]},
					{"role": "user", "content": [{"type": "tool_result", "tool_use_id": "toolu_1", "content": "a"}]},
					{"role": "assistant", "content": [{"type": "tool_use", "id": "toolu_2", "name": "Read", "input": {"file_path": "b"}}]},
					{"role": "user", "content": [{"type": "tool_result", "tool_use_id": "toolu_2", "content": "b"}]}
				]
			}`,
			call: service.forwardMessagesOnce,
		},
		{
			name: "chat completions",
			body: `{
				"model": "cursor/grok-4.6",
				"messages": [
					{"role": "user", "content": "inspect"},
					{"role": "assistant", "content": "", "tool_calls": [{"id": "call_1", "type": "function", "function": {"name": "Read", "arguments": "{\"file_path\":\"a\"}"}}]},
					{"role": "tool", "tool_call_id": "call_1", "content": "a"},
					{"role": "assistant", "content": "", "tool_calls": [{"id": "call_2", "type": "function", "function": {"name": "Read", "arguments": "{\"file_path\":\"b\"}"}}]},
					{"role": "tool", "tool_call_id": "call_2", "content": "b"}
				]
			}`,
			call: service.forwardChatCompletionsOnce,
		},
		{
			name: "responses",
			body: `{
				"model": "cursor/grok-4.6",
				"input": [
					{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "inspect"}]},
					{"type": "function_call", "call_id": "call_1", "name": "Read", "arguments": "{\"file_path\":\"a\"}"},
					{"type": "function_call_output", "call_id": "call_1", "output": "a"},
					{"type": "function_call", "call_id": "call_2", "name": "Read", "arguments": "{\"file_path\":\"b\"}"},
					{"type": "function_call_output", "call_id": "call_2", "output": "b"}
				]
			}`,
			call: service.forwardResponsesOnce,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(test.body))

			result, err := test.call(context.Background(), ctx, nil, []byte(test.body))

			require.Nil(t, result)
			require.Error(t, err)
			require.Equal(t, http.StatusConflict, recorder.Code)
			require.Contains(t, recorder.Body.String(), "Cursor tool loop guard stopped")
		})
	}
}

func TestRepeatedReadDetectionAcrossProtocols(t *testing.T) {
	tests := []struct {
		name         string
		conversation func(*testing.T) *cursor.Conversation
	}{
		{
			name: "anthropic messages",
			conversation: func(t *testing.T) *cursor.Conversation {
				var req cursor.AnthropicRequest
				require.NoError(t, json.Unmarshal([]byte(`{
					"model": "cursor/grok-4.6",
					"messages": [
						{"role": "user", "content": "create a summary"},
						{"role": "assistant", "content": [{"type": "tool_use", "id": "call_1", "name": "Read", "input": {"file_path": "summary.md"}}]},
						{"role": "user", "content": [{"type": "tool_result", "tool_use_id": "call_1", "content": "file not found"}]},
						{"role": "assistant", "content": [{"type": "tool_use", "id": "call_2", "name": "Read", "input": {"file_path": "summary.md"}}]},
						{"role": "user", "content": [{"type": "tool_result", "tool_use_id": "call_2", "content": "file not found"}]}
					]
				}`), &req))
				return req.Conversation()
			},
		},
		{
			name: "chat completions",
			conversation: func(t *testing.T) *cursor.Conversation {
				var req cursor.OpenAIRequest
				require.NoError(t, json.Unmarshal([]byte(`{
					"model": "cursor/grok-4.6",
					"messages": [
						{"role": "user", "content": "create a summary"},
						{"role": "assistant", "tool_calls": [{"id": "call_1", "type": "function", "function": {"name": "Read", "arguments": "{\"file_path\":\"summary.md\"}"}}]},
						{"role": "tool", "tool_call_id": "call_1", "content": "file not found"},
						{"role": "assistant", "tool_calls": [{"id": "call_2", "type": "function", "function": {"name": "Read", "arguments": "{\"file_path\":\"summary.md\"}"}}]},
						{"role": "tool", "tool_call_id": "call_2", "content": "file not found"}
					]
				}`), &req))
				return req.Conversation()
			},
		},
		{
			name: "responses",
			conversation: func(t *testing.T) *cursor.Conversation {
				var req apicompat.ResponsesRequest
				require.NoError(t, json.Unmarshal([]byte(`{
					"model": "cursor/grok-4.6",
					"input": [
						{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "create a summary"}]},
						{"type": "function_call", "call_id": "call_1", "name": "Read", "arguments": "{\"file_path\":\"summary.md\"}"},
						{"type": "function_call_output", "call_id": "call_1", "output": "file not found"},
						{"type": "function_call", "call_id": "call_2", "name": "Read", "arguments": "{\"file_path\":\"summary.md\"}"},
						{"type": "function_call_output", "call_id": "call_2", "output": "file not found"}
					]
				}`), &req))
				chatReq, err := apicompat.ResponsesToChatCompletionsRequest(&req)
				require.NoError(t, err)
				chatBody, err := json.Marshal(chatReq)
				require.NoError(t, err)
				var openAIReq cursor.OpenAIRequest
				require.NoError(t, json.Unmarshal(chatBody, &openAIReq))
				return openAIReq.Conversation()
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			observation, repeated := test.conversation(t).RepeatedRead(2)
			require.True(t, repeated)
			require.Equal(t, "Read", observation.ToolName)
			require.Equal(t, 2, observation.Repeats)
		})
	}
}
