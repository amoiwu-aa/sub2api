package kiro

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func toAnthropic(t *testing.T, body string) *AnthropicRequest {
	t.Helper()
	var req OpenAIRequest
	require.NoError(t, json.Unmarshal([]byte(body), &req))
	return req.ToAnthropicRequest()
}

func TestOpenAIRequestExtractsSystemMessages(t *testing.T) {
	converted := toAnthropic(t, `{
		"model": "kiro/claude-sonnet-4.6",
		"messages": [
			{"role": "system", "content": "be brief"},
			{"role": "developer", "content": "and precise"},
			{"role": "user", "content": "hi"}
		]
	}`)

	require.Equal(t, `"be brief\nand precise"`, string(converted.System))
	require.Len(t, converted.Messages, 1)
	require.Equal(t, "user", converted.Messages[0].Role)
}

func TestOpenAIRequestFoldsToolMessagesIntoUserTurns(t *testing.T) {
	// 整条丢弃 tool 消息会打断 user/assistant 交替，上游会看到一段接不上的对话。
	converted := toAnthropic(t, `{
		"messages": [
			{"role": "user", "content": "weather?"},
			{"role": "assistant", "content": null, "tool_calls": [
				{"id": "c1", "type": "function", "function": {"name": "get_weather", "arguments": "{\"city\":\"Paris\"}"}}
			]},
			{"role": "tool", "tool_call_id": "c1", "content": "18C"}
		]
	}`)

	require.Len(t, converted.Messages, 3)
	require.Equal(t, "assistant", converted.Messages[1].Role)
	require.Contains(t, string(converted.Messages[1].Content), "Called tool get_weather")
	require.Equal(t, "user", converted.Messages[2].Role)
	require.Contains(t, string(converted.Messages[2].Content), "Tool result: 18C")
}

func TestOpenAIRequestSkipsEmptyAssistantTurns(t *testing.T) {
	converted := toAnthropic(t, `{
		"messages": [
			{"role": "user", "content": "hi"},
			{"role": "assistant", "content": ""}
		]
	}`)
	require.Len(t, converted.Messages, 1)
}

func TestOpenAIRequestHandlesMultimodalContentArray(t *testing.T) {
	converted := toAnthropic(t, `{
		"messages": [{"role": "user", "content": [
			{"type": "text", "text": "part one "},
			{"type": "text", "text": "part two"}
		]}]
	}`)

	state, err := BuildConversationState(converted, "conv", "m")
	require.NoError(t, err)
	require.Equal(t, "part one part two", state.CurrentMessage.UserInputMessage.Content)
}

func TestOpenAIRequestFeedsBuildConversationState(t *testing.T) {
	converted := toAnthropic(t, `{
		"model": "kiro/claude-sonnet-4.6",
		"stream": true,
		"messages": [
			{"role": "system", "content": "sys"},
			{"role": "user", "content": "hi"}
		]
	}`)
	require.True(t, converted.Stream)

	state, err := BuildConversationState(converted, "conv", "claude-sonnet-4.6")
	require.NoError(t, err)
	require.Equal(t, "sys", state.History[0].UserInputMessage.Content)
	require.Equal(t, "OK", state.History[1].AssistantResponseMessage.Content)
	require.Equal(t, "hi", state.CurrentMessage.UserInputMessage.Content)
	// OpenAI 路径不发 tools：不回传 tool_calls 却发工具定义会让客户端
	// 一直等一个永远不来的工具调用。
	require.Empty(t, state.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools)
}

func TestOpenAIChunkShape(t *testing.T) {
	chunk := NewOpenAIChunk("chatcmpl-1", "kiro/claude-sonnet-4.6", 1700000000, "hello")
	raw, err := json.Marshal(chunk)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"id": "chatcmpl-1",
		"object": "chat.completion.chunk",
		"created": 1700000000,
		"model": "kiro/claude-sonnet-4.6",
		"choices": [{"index": 0, "delta": {"content": "hello"}, "finish_reason": null}]
	}`, string(raw))
}

func TestOpenAIFinalChunkCarriesFinishReasonAndUsage(t *testing.T) {
	chunk := NewOpenAIFinalChunk("chatcmpl-1", "m", 1, "stop", &OpenAIUsage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7})
	raw, err := json.Marshal(chunk)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"id": "chatcmpl-1",
		"object": "chat.completion.chunk",
		"created": 1,
		"model": "m",
		"choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}],
		"usage": {"prompt_tokens": 3, "completion_tokens": 4, "total_tokens": 7}
	}`, string(raw))
}

func TestUpstreamAndPublicModelIDRoundTrip(t *testing.T) {
	require.Equal(t, "claude-sonnet-4.6", UpstreamModelID("kiro/claude-sonnet-4.6"))
	// 专用 kiro 分组里客户端直接写原名也应该能用。
	require.Equal(t, "claude-sonnet-4.6", UpstreamModelID("claude-sonnet-4.6"))
	require.Equal(t, "kiro/claude-sonnet-4.6", PublicModelID("claude-sonnet-4.6"))
	require.Equal(t, "kiro/claude-sonnet-4.6", PublicModelID("kiro/claude-sonnet-4.6"))
}
