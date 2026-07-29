package cursor

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func parseOpenAIRequest(t *testing.T, body string) *OpenAIRequest {
	t.Helper()
	var req OpenAIRequest
	require.NoError(t, json.Unmarshal([]byte(body), &req))
	return &req
}

func TestMessagesToAgentTextAddsRolePrefixes(t *testing.T) {
	req := parseOpenAIRequest(t, `{"messages":[
		{"role":"system","content":"be brief"},
		{"role":"user","content":"hi"},
		{"role":"assistant","content":"hello"},
		{"role":"user","content":"bye"}
	]}`)

	require.Equal(t, "[system]\nbe brief\n\nhi\n\n[assistant]\nhello\n\nbye",
		MessagesToAgentText(req.Messages))
}

func TestMessagesToAgentTextSingleSystemMessageDropsPrefix(t *testing.T) {
	// 只有一条 system 时保留前缀会把一句纯指令包装成看起来像转录的东西。
	req := parseOpenAIRequest(t, `{"messages":[{"role":"system","content":"just do it"}]}`)
	require.Equal(t, "just do it", MessagesToAgentText(req.Messages))
}

func TestMessagesToAgentTextHandlesContentArrays(t *testing.T) {
	req := parseOpenAIRequest(t, `{"messages":[{"role":"user","content":[
		{"type":"text","text":"line one"},
		{"type":"image_url","image_url":{"url":"data:..."}},
		{"type":"text","text":"line two"}
	]}]}`)
	// 非文本块被跳过，文本块按行拼接。
	require.Equal(t, "line one\nline two", MessagesToAgentText(req.Messages))
}

func TestMessagesToAgentTextSkipsEmptyMessages(t *testing.T) {
	req := parseOpenAIRequest(t, `{"messages":[
		{"role":"user","content":""},
		{"role":"assistant","content":null},
		{"role":"user","content":"only this"}
	]}`)
	require.Equal(t, "only this", MessagesToAgentText(req.Messages))
}

func TestResolveConversationIDPrefersTopLevel(t *testing.T) {
	withBoth := parseOpenAIRequest(t, `{"conversation_id":"top","metadata":{"conversation_id":"meta"},"messages":[]}`)
	require.Equal(t, "top", withBoth.ResolveConversationID())

	metaOnly := parseOpenAIRequest(t, `{"metadata":{"conversation_id":"meta"},"messages":[]}`)
	require.Equal(t, "meta", metaOnly.ResolveConversationID())

	neither := parseOpenAIRequest(t, `{"messages":[]}`)
	require.Empty(t, neither.ResolveConversationID())
}

func TestOpenAIChunkShape(t *testing.T) {
	raw, err := json.Marshal(NewOpenAIChunk("chatcmpl-1", "cursor/default", 1700000000, "hello"))
	require.NoError(t, err)
	require.JSONEq(t, `{
		"id": "chatcmpl-1",
		"object": "chat.completion.chunk",
		"created": 1700000000,
		"model": "cursor/default",
		"choices": [{"index": 0, "delta": {"content": "hello"}, "finish_reason": null}]
	}`, string(raw))
}

func TestOpenAIFinalChunkCarriesFinishReason(t *testing.T) {
	raw, err := json.Marshal(NewOpenAIFinalChunk("chatcmpl-1", "m", 1, "stop"))
	require.NoError(t, err)
	require.JSONEq(t, `{
		"id": "chatcmpl-1",
		"object": "chat.completion.chunk",
		"created": 1,
		"model": "m",
		"choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}]
	}`, string(raw))
}

func TestOpenAIReasoningChunkShape(t *testing.T) {
	raw, err := json.Marshal(NewOpenAIReasoningChunk("chatcmpl-1", "cursor/default", 1700000000, "think"))
	require.NoError(t, err)
	require.JSONEq(t, `{
		"id": "chatcmpl-1",
		"object": "chat.completion.chunk",
		"created": 1700000000,
		"model": "cursor/default",
		"choices": [{"index": 0, "delta": {"reasoning_content": "think"}, "finish_reason": null}]
	}`, string(raw))
}

func TestEstimateTokensCountsRunesNotBytes(t *testing.T) {
	// 按字节算会把中文对话的 token 数抬高约三倍。
	chinese := "你好世界你好世界"
	require.Equal(t, int64(2), EstimateTokens(chinese))

	require.Equal(t, int64(0), EstimateTokens(""))
	// 非空输入至少记 1，否则短请求的成本会是 0，平台配额形同虚设。
	require.Equal(t, int64(1), EstimateTokens("hi"))
	require.Equal(t, int64(4), EstimateTokens("0123456789abcdef"))
}

func TestUpstreamAndPublicModelIDRoundTrip(t *testing.T) {
	require.Equal(t, "claude-opus-4-8", UpstreamModelID("cursor/claude-opus-4-8"))
	// 专用 cursor 分组里客户端直接写原名也应该能用。
	require.Equal(t, "claude-opus-4-8", UpstreamModelID("claude-opus-4-8"))
	// 空值与只有前缀的输入回退到 Auto，避免把空模型名打给上游。
	require.Equal(t, AutoModelID, UpstreamModelID(""))
	require.Equal(t, AutoModelID, UpstreamModelID("cursor/"))
	require.Equal(t, "cursor/default", PublicModelID(AutoModelID))
	require.Equal(t, "cursor/default", PublicModelID("cursor/default"))
}
