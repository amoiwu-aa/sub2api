package cursor

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIRequestExtractsMcpTools(t *testing.T) {
	req := parseOpenAIRequest(t, `{
		"model":"cursor/default",
		"messages":[{"role":"user","content":"hi"}],
		"tools":[
			{"type":"function","function":{"name":"Bash","description":"run","parameters":{"type":"object"}}},
			{"type":"function","function":{"name":"","description":"nameless"}},
			{"type":"retrieval"}
		]
	}`)

	tools := req.McpTools()
	require.Len(t, tools, 1)
	require.Equal(t, "Bash", tools[0].Name)
	require.Equal(t, "run", tools[0].Description)
	require.JSONEq(t, `{"type":"object"}`, string(tools[0].InputSchema))
}

func TestOpenAIRequestToolChoiceNoneDisablesTools(t *testing.T) {
	// 照常声明的话模型多半还是会调，最后卡在一个客户端不打算执行的调用上。
	req := parseOpenAIRequest(t, `{
		"model":"cursor/default",
		"messages":[{"role":"user","content":"hi"}],
		"tool_choice":"none",
		"tools":[{"type":"function","function":{"name":"Bash"}}]
	}`)
	require.Empty(t, req.McpTools())
}

func TestOpenAIRequestConversationCarriesToolRoundTrip(t *testing.T) {
	req := parseOpenAIRequest(t, `{
		"model":"cursor/default",
		"messages":[
			{"role":"system","content":"be brief"},
			{"role":"user","content":"run echo hi"},
			{"role":"assistant","content":null,"tool_calls":[
				{"id":"call_1","type":"function","function":{"name":"Bash","arguments":"{\"command\":\"echo hi\"}"}}
			]},
			{"role":"tool","tool_call_id":"call_1","name":"Bash","content":"hi\n"}
		],
		"tools":[{"type":"function","function":{"name":"Bash","parameters":{"type":"object"}}}]
	}`)

	conversation := req.Conversation()
	require.Len(t, conversation.Turns, 4)
	require.Equal(t, RoleSystem, conversation.Turns[0].Role)
	require.Equal(t, RoleAssistant, conversation.Turns[2].Role)
	require.Len(t, conversation.Turns[2].ToolCalls, 1)
	require.Equal(t, "Bash", conversation.Turns[2].ToolCalls[0].Name)
	require.Equal(t, RoleTool, conversation.Turns[3].Role)
	require.Equal(t, "call_1", conversation.Turns[3].ToolCallID)

	rendered := conversation.Render()
	require.Contains(t, rendered, `<tool_call id="call_1" name="Bash">`)
	require.Contains(t, rendered, `<tool_result id="call_1" name="Bash">`)
	require.Contains(t, rendered, "<continue>")
}

func TestNewOpenAIToolCallID(t *testing.T) {
	// 不少客户端假定 id 形如 call_xxx，裸 uuid 会被它们的校验拦下。
	require.Equal(t, "call_abc123", NewOpenAIToolCallID("call_abc123"))
	require.Equal(t, "call_041a48e1241e4224aa3256a277bacfb2",
		NewOpenAIToolCallID("041a48e1-241e-4224-aa32-56a277bacfb2"))

	// 缺 id 的兜底必须唯一：同一轮多个缺 id 的调用如果同名，
	// Anthropic 客户端会拒绝重复的 tool_use.id。
	first := NewOpenAIToolCallID("  ")
	second := NewOpenAIToolCallID("")
	require.True(t, strings.HasPrefix(first, "call_"))
	require.True(t, strings.HasPrefix(second, "call_"))
	require.NotEqual(t, first, second)
}

func TestNewOpenAIToolCallChunkShape(t *testing.T) {
	chunk := NewOpenAIToolCallChunk("chatcmpl-1", "cursor/default", 100, 2, ToolCall{
		ID: "call_1", Name: "Bash", Arguments: `{"command":"ls"}`,
	})
	encoded, err := json.Marshal(chunk)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"id":"chatcmpl-1","object":"chat.completion.chunk","created":100,"model":"cursor/default",
		"choices":[{"index":0,"finish_reason":null,"delta":{"tool_calls":[
			{"index":2,"id":"call_1","type":"function","function":{"name":"Bash","arguments":"{\"command\":\"ls\"}"}}
		]}}]
	}`, string(encoded))
}

func TestNewOpenAIToolCallsFillsEmptyArguments(t *testing.T) {
	// 客户端直接 JSON.parse(arguments)，空串会炸。
	calls := NewOpenAIToolCalls([]ToolCall{{ID: "call_1", Name: "Noop"}})
	require.Len(t, calls, 1)
	require.Equal(t, "{}", calls[0].Function.Arguments)
	require.NotNil(t, calls[0].Index)
	require.Equal(t, 0, *calls[0].Index)
}
