package kiro

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func mustBuild(t *testing.T, body string, model string) *ConversationState {
	t.Helper()
	var req AnthropicRequest
	require.NoError(t, json.Unmarshal([]byte(body), &req))
	state, err := BuildConversationState(&req, "conv-1", model)
	require.NoError(t, err)
	return state
}

func TestBuildConversationStateInjectsSystemAsUserAssistantPair(t *testing.T) {
	state := mustBuild(t, `{
		"model": "kiro/claude-sonnet-4.6",
		"system": "You are helpful.",
		"messages": [{"role": "user", "content": "hi"}]
	}`, "claude-sonnet-4.6")

	require.Len(t, state.History, 2)
	require.Equal(t, "You are helpful.", state.History[0].UserInputMessage.Content)
	require.Equal(t, "OK", state.History[1].AssistantResponseMessage.Content)

	require.Equal(t, "hi", state.CurrentMessage.UserInputMessage.Content)
	require.Equal(t, OriginAIEditor, state.CurrentMessage.UserInputMessage.Origin)
	require.Equal(t, "claude-sonnet-4.6", state.CurrentMessage.UserInputMessage.ModelID)
	require.Equal(t, ChatTriggerTypeManual, state.ChatTriggerType)
}

func TestBuildConversationStateAcceptsSystemBlockArray(t *testing.T) {
	state := mustBuild(t, `{
		"system": [{"type":"text","text":"line one"},{"type":"text","text":"line two"}],
		"messages": [{"role": "user", "content": "hi"}]
	}`, "m")
	require.Equal(t, "line one\nline two", state.History[0].UserInputMessage.Content)
}

func TestBuildConversationStateAppendsContinueWhenLastIsAssistant(t *testing.T) {
	// 上游要求最后一条是 user；末尾是 assistant 时必须补一句，否则 400。
	state := mustBuild(t, `{
		"messages": [
			{"role": "user", "content": "hi"},
			{"role": "assistant", "content": "hello"}
		]
	}`, "m")

	require.Equal(t, "Continue.", state.CurrentMessage.UserInputMessage.Content)
	require.Len(t, state.History, 2)
	require.Equal(t, "hello", state.History[1].AssistantResponseMessage.Content)
}

func TestBuildConversationStateWithEmptyMessages(t *testing.T) {
	state := mustBuild(t, `{"messages": []}`, "m")
	require.NotNil(t, state.CurrentMessage.UserInputMessage)
	require.Empty(t, state.History)
}

func TestBuildConversationStateToolResultRoundTrip(t *testing.T) {
	state := mustBuild(t, `{
		"messages": [
			{"role": "user", "content": "weather?"},
			{"role": "assistant", "content": [
				{"type": "text", "text": "checking"},
				{"type": "tool_use", "id": "tu-1", "name": "get_weather", "input": {"city": "Paris"}}
			]},
			{"role": "user", "content": [
				{"type": "tool_result", "tool_use_id": "tu-1", "content": "18C"}
			]}
		]
	}`, "m")

	assistant := state.History[1].AssistantResponseMessage
	require.Equal(t, "checking", assistant.Content)
	require.Len(t, assistant.ToolUses, 1)
	require.Equal(t, "tu-1", assistant.ToolUses[0].ToolUseID)
	require.JSONEq(t, `{"city":"Paris"}`, string(assistant.ToolUses[0].Input))

	current := state.CurrentMessage.UserInputMessage
	// tool_result 轮的 content 必须为空，结果挂在 context.toolResults 上。
	require.Equal(t, "", current.Content)
	require.Len(t, current.UserInputMessageContext.ToolResults, 1)
	require.Equal(t, "tu-1", current.UserInputMessageContext.ToolResults[0].ToolUseID)
	require.Equal(t, ToolResultStatusSuccess, current.UserInputMessageContext.ToolResults[0].Status)
	require.Equal(t, "18C", current.UserInputMessageContext.ToolResults[0].Content[0].Text)
}

func TestBuildConversationStateToolResultErrorStatusAndBlockArray(t *testing.T) {
	state := mustBuild(t, `{
		"messages": [{"role": "user", "content": [
			{"type": "tool_result", "tool_use_id": "tu-9", "is_error": true,
			 "content": [{"type":"text","text":"boom"},{"type":"other","k":1}]}
		]}]
	}`, "m")

	result := state.CurrentMessage.UserInputMessage.UserInputMessageContext.ToolResults[0]
	require.Equal(t, ToolResultStatusError, result.Status)
	require.Len(t, result.Content, 2)
	require.Equal(t, "boom", result.Content[0].Text)
	// 非文本块折成文本而不是被丢弃。
	require.Contains(t, result.Content[1].Text, `"other"`)
}

func TestBuildConversationStateAttachesToolsOnlyToCurrentMessage(t *testing.T) {
	state := mustBuild(t, `{
		"messages": [
			{"role": "user", "content": "a"},
			{"role": "assistant", "content": "b"},
			{"role": "user", "content": "c"}
		],
		"tools": [
			{"name": "get_weather", "description": "d", "input_schema": {"type": "object"}},
			{"name": "web_search", "description": "should be dropped"}
		]
	}`, "m")

	tools := state.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools
	require.Len(t, tools, 1)
	require.Equal(t, "get_weather", tools[0].ToolSpecification.Name)
	require.JSONEq(t, `{"type":"object"}`, string(tools[0].ToolSpecification.InputSchema.JSON))

	// history 里的 user 消息不能带 tools。
	require.Empty(t, state.History[0].UserInputMessage.UserInputMessageContext.Tools)
}

func TestConvertToolsAcceptsMCPCustomFormat(t *testing.T) {
	tools := convertTools([]AnthropicTool{{
		Type: "custom",
		Name: "mcp_tool",
		Custom: &struct {
			Description string          `json:"description,omitempty"`
			InputSchema json.RawMessage `json:"input_schema,omitempty"`
		}{Description: "from custom", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}})

	require.Len(t, tools, 1)
	require.Equal(t, "from custom", tools[0].ToolSpecification.Description)
	require.JSONEq(t, `{"type":"object"}`, string(tools[0].ToolSpecification.InputSchema.JSON))
}

func TestBuildConversationStateExtractsImages(t *testing.T) {
	png := base64.StdEncoding.EncodeToString([]byte{0x89, 0x50, 0x4e, 0x47})
	state := mustBuild(t, `{
		"messages": [{"role": "user", "content": [
			{"type": "text", "text": "what is this"},
			{"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": "`+png+`"}},
			{"type": "image_url", "image_url": {"url": "data:image/webp;base64,`+png+`"}},
			{"type": "image", "source": {"type": "url", "url": "https://example.com/remote.png"}}
		]}]
	}`, "m")

	images := state.CurrentMessage.UserInputMessage.Images
	// 远程 URL 被丢弃：让服务端代客户端抓取是个 SSRF 入口，上游也只收 bytes。
	require.Len(t, images, 2)
	require.Equal(t, "png", images[0].Format)
	require.Equal(t, []byte{0x89, 0x50, 0x4e, 0x47}, images[0].Source.Bytes)
	require.Equal(t, "webp", images[1].Format)
}

func TestBuildConversationStateStringAndBlockContentAreEquivalent(t *testing.T) {
	fromString := mustBuild(t, `{"messages":[{"role":"user","content":"hello"}]}`, "m")
	fromBlocks := mustBuild(t, `{"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`, "m")
	require.Equal(t, fromString.CurrentMessage.UserInputMessage.Content, fromBlocks.CurrentMessage.UserInputMessage.Content)
}

// ---- 响应翻译 ----

func collectSSE(t *testing.T, events ...StreamEvent) ([]SSEEvent, *ResponseTranslator) {
	t.Helper()
	var out []SSEEvent
	translator := NewResponseTranslator("msg_1", "kiro/claude-sonnet-4.6", func(e SSEEvent) error {
		out = append(out, e)
		return nil
	})
	require.NoError(t, translator.Start())
	for _, event := range events {
		require.NoError(t, translator.Handle(event))
	}
	require.NoError(t, translator.Finish())
	return out, translator
}

func sseEventNames(events []SSEEvent) []string {
	names := make([]string, 0, len(events))
	for _, event := range events {
		names = append(names, event.Event)
	}
	return names
}

func TestResponseTranslatorEmitsFullAnthropicSSESequence(t *testing.T) {
	events, translator := collectSSE(t,
		StreamEvent{ReasoningContent: &ReasoningContentEvent{Text: "let me think"}},
		StreamEvent{ReasoningContent: &ReasoningContentEvent{Signature: "sig"}},
		StreamEvent{AssistantResponse: &AssistantResponseEvent{Content: "Hello"}},
		StreamEvent{AssistantResponse: &AssistantResponseEvent{Content: " there"}},
		StreamEvent{Metadata: &MetadataEvent{TokenUsage: &TokenUsage{UncachedInputTokens: 10, OutputTokens: 5}}},
	)

	require.Equal(t, []string{
		"message_start", "ping",
		"content_block_start", "content_block_delta", "content_block_delta", "content_block_stop",
		"content_block_start", "content_block_delta", "content_block_delta", "content_block_stop",
		"message_delta", "message_stop",
	}, sseEventNames(events))

	// thinking 与 text 是两个独立的块，索引递增。
	require.Equal(t, 0, events[2].Data.(map[string]any)["index"])
	require.Equal(t, 1, events[6].Data.(map[string]any)["index"])
	require.Equal(t, "thinking", events[2].Data.(map[string]any)["content_block"].(map[string]any)["type"])
	require.Equal(t, "text", events[6].Data.(map[string]any)["content_block"].(map[string]any)["type"])

	require.Equal(t, stopReasonEndTurn, translator.StopReason())
	require.Equal(t, int64(10), translator.Usage().InputTokens)
	require.Equal(t, int64(5), translator.Usage().OutputTokens)
	require.True(t, translator.HasUpstreamUsage())
}

func TestResponseTranslatorEmitsSignatureDeltaWhileThinkingBlockOpen(t *testing.T) {
	events, _ := collectSSE(t,
		StreamEvent{ReasoningContent: &ReasoningContentEvent{Text: "step"}},
		StreamEvent{ReasoningContent: &ReasoningContentEvent{Signature: "sig-1"}},
	)
	delta := events[4].Data.(map[string]any)["delta"].(map[string]any)
	require.Equal(t, "signature_delta", delta["type"])
	require.Equal(t, "sig-1", delta["signature"])
}

func TestResponseTranslatorToolUseStopReasonAndInputAssembly(t *testing.T) {
	events, translator := collectSSE(t,
		StreamEvent{AssistantResponse: &AssistantResponseEvent{Content: "calling"}},
		StreamEvent{ToolUse: &ToolUseEvent{ToolUseID: "tu-1", Name: "get_weather", Input: `{"city":`}},
		StreamEvent{ToolUse: &ToolUseEvent{ToolUseID: "tu-1", Input: `"Paris"}`, Stop: true}},
	)

	require.Equal(t, stopReasonToolUse, translator.StopReason())

	response := translator.Response()
	require.Len(t, response.Content, 2)
	require.Equal(t, "text", response.Content[0].Type)
	require.Equal(t, "tool_use", response.Content[1].Type)
	require.Equal(t, "get_weather", response.Content[1].Name)
	require.JSONEq(t, `{"city":"Paris"}`, string(response.Content[1].Input))

	require.Contains(t, sseEventNames(events), "content_block_delta")
}

func TestResponseTranslatorClosesPreviousToolWhenNewToolStartsWithoutStop(t *testing.T) {
	// 上游不保证每个 tool_use 都带 stop；新 id 出现就必须收束上一个。
	_, translator := collectSSE(t,
		StreamEvent{ToolUse: &ToolUseEvent{ToolUseID: "tu-1", Name: "a", Input: `{"x":1}`}},
		StreamEvent{ToolUse: &ToolUseEvent{ToolUseID: "tu-2", Name: "b", Input: `{"y":2}`}},
	)

	response := translator.Response()
	require.Len(t, response.Content, 2)
	require.Equal(t, "a", response.Content[0].Name)
	require.Equal(t, "b", response.Content[1].Name)
	require.JSONEq(t, `{"x":1}`, string(response.Content[0].Input))
}

func TestResponseTranslatorWrapsUnparsableToolInput(t *testing.T) {
	_, translator := collectSSE(t,
		StreamEvent{ToolUse: &ToolUseEvent{ToolUseID: "tu-1", Name: "a", Input: `not json`, Stop: true}},
	)
	require.JSONEq(t, `{"raw":"not json"}`, string(translator.Response().Content[0].Input))
}

func TestResponseTranslatorNonStreamingSkipsSSE(t *testing.T) {
	translator := NewResponseTranslator("msg_2", "kiro/claude-opus-4.6", nil)
	require.NoError(t, translator.Handle(StreamEvent{AssistantResponse: &AssistantResponseEvent{Content: "hi"}}))
	require.NoError(t, translator.Finish())

	response := translator.Response()
	require.Equal(t, "message", response.Type)
	require.Equal(t, "assistant", response.Role)
	require.Equal(t, "kiro/claude-opus-4.6", response.Model)
	require.Equal(t, "hi", translator.TextContent())
	require.False(t, translator.HasUpstreamUsage())
}

func TestResponseTranslatorEmptyResponseStillWellFormed(t *testing.T) {
	events, translator := collectSSE(t)
	require.Equal(t, []string{"message_start", "ping", "message_delta", "message_stop"}, sseEventNames(events))
	require.Equal(t, []AnthropicContentItem{}, translator.Response().Content)
}
