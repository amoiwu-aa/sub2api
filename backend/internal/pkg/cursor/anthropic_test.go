package cursor

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func parseAnthropicRequest(t *testing.T, body string) *AnthropicRequest {
	t.Helper()
	var req AnthropicRequest
	require.NoError(t, json.Unmarshal([]byte(body), &req))
	return &req
}

func TestAnthropicRequestParsesOutputEffort(t *testing.T) {
	req := parseAnthropicRequest(t, `{
		"model":"cursor/grok-4.6",
		"output_config":{"effort":"max"},
		"messages":[]
	}`)
	require.NotNil(t, req.OutputConfig)
	require.NotNil(t, req.OutputConfig.Effort)
	require.Equal(t, "max", *req.OutputConfig.Effort)
}

func TestAnthropicRequestSystemBecomesSystemTurn(t *testing.T) {
	req := parseAnthropicRequest(t, `{
		"model":"cursor/default",
		"system":[{"type":"text","text":"be brief"}],
		"messages":[{"role":"user","content":"hi"}]
	}`)
	conversation := req.Conversation()
	require.Len(t, conversation.Turns, 2)
	require.Equal(t, RoleSystem, conversation.Turns[0].Role)
	require.Equal(t, "be brief", conversation.Turns[0].Text)
}

func TestAnthropicRequestSplitsToolResultOutOfUserMessage(t *testing.T) {
	// Anthropic 把工具结果塞在 user 消息的内容块里。不拆开的话，
	// 重放出来的历史看不出哪段是工具输出，模型会把工具再调一遍。
	req := parseAnthropicRequest(t, `{
		"model":"cursor/default",
		"messages":[
			{"role":"user","content":"run echo hi"},
			{"role":"assistant","content":[
				{"type":"text","text":"sure"},
				{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"echo hi"}}
			]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"toolu_1","content":"hi\n"},
				{"type":"text","text":"继续"}
			]}
		]
	}`)

	conversation := req.Conversation()
	require.Len(t, conversation.Turns, 4)

	require.Equal(t, RoleAssistant, conversation.Turns[1].Role)
	require.Equal(t, "sure", conversation.Turns[1].Text)
	require.Len(t, conversation.Turns[1].ToolCalls, 1)
	require.Equal(t, "toolu_1", conversation.Turns[1].ToolCalls[0].ID)
	require.JSONEq(t, `{"command":"echo hi"}`, conversation.Turns[1].ToolCalls[0].Arguments)

	// 工具结果必须排在同一条消息的文本之前。
	require.Equal(t, RoleTool, conversation.Turns[2].Role)
	require.Equal(t, "toolu_1", conversation.Turns[2].ToolCallID)
	require.Equal(t, "hi\n", conversation.Turns[2].Text)
	require.Equal(t, RoleUser, conversation.Turns[3].Role)
	require.Equal(t, "继续", conversation.Turns[3].Text)
}

func TestAnthropicToolResultErrorIsMarked(t *testing.T) {
	req := parseAnthropicRequest(t, `{
		"model":"cursor/default",
		"messages":[{"role":"user","content":[
			{"type":"tool_result","tool_use_id":"toolu_1","is_error":true,"content":"boom"}
		]}]
	}`)
	conversation := req.Conversation()
	require.Len(t, conversation.Turns, 1)
	require.Equal(t, "[tool error] boom", conversation.Turns[0].Text)
}

func TestAnthropicStructuredToolResultIsReplayedNotDropped(t *testing.T) {
	// 完整历史重放的要求：客户端发来的结构化工具结果（带 type 但没有 text 字段）
	// 不能被压成空串，否则模型看到的是一次"没有输出"的工具调用。
	req := parseAnthropicRequest(t, `{
		"model":"cursor/default",
		"messages":[{"role":"user","content":[
			{"type":"tool_result","tool_use_id":"toolu_1","content":[
				{"type":"json","json":{"temperature":21.5,"unit":"C"}}
			]},
			{"type":"tool_result","tool_use_id":"toolu_2","content":{"type":"structured","data":{"rows":3}}}
		]}]
	}`)
	conversation := req.Conversation()
	require.NoError(t, conversation.Err)
	require.Len(t, conversation.Turns, 2)

	require.Equal(t, RoleTool, conversation.Turns[0].Role)
	require.Equal(t, "toolu_1", conversation.Turns[0].ToolCallID)
	require.Contains(t, conversation.Turns[0].Text, `"temperature"`)
	require.Contains(t, conversation.Turns[0].Text, "21.5")

	require.Equal(t, RoleTool, conversation.Turns[1].Role)
	require.Equal(t, "toolu_2", conversation.Turns[1].ToolCallID)
	require.Contains(t, conversation.Turns[1].Text, `"rows"`)
}

func TestAnthropicEmptyTextBlocksStayDropped(t *testing.T) {
	// 空 text 块没有信息量，不应该触发原文 JSON 回退。
	req := parseAnthropicRequest(t, `{
		"model":"cursor/default",
		"messages":[{"role":"user","content":[
			{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"text","text":""}]}
		]}]
	}`)
	conversation := req.Conversation()
	require.Len(t, conversation.Turns, 1)
	require.Equal(t, "", conversation.Turns[0].Text)
}

func TestAnthropicRequestExtractsTools(t *testing.T) {
	req := parseAnthropicRequest(t, `{
		"model":"cursor/default",
		"messages":[{"role":"user","content":"hi"}],
		"tools":[
			{"name":"Bash","description":"run","input_schema":{"type":"object"}},
			{"name":"computer","type":"computer_20241022"}
		]
	}`)
	tools := req.McpTools()
	// 带 type 的是 Anthropic 自家的服务端工具，Cursor 执行不了。
	require.Len(t, tools, 1)
	require.Equal(t, "Bash", tools[0].Name)
}

func TestAnthropicToolChoiceNoneDisablesTools(t *testing.T) {
	req := parseAnthropicRequest(t, `{
		"model":"cursor/default",
		"messages":[{"role":"user","content":"hi"}],
		"tool_choice":{"type":"none"},
		"tools":[{"name":"Bash","input_schema":{"type":"object"}}]
	}`)
	require.Empty(t, req.McpTools())
}

func TestNewAnthropicToolUseID(t *testing.T) {
	require.Equal(t, "toolu_abc", NewAnthropicToolUseID("toolu_abc"))
	require.Equal(t, "toolu_041a48e1241e", NewAnthropicToolUseID("call_041a48e1-241e"))
	require.Equal(t, "toolu_unknown", NewAnthropicToolUseID(""))
}

func TestNewAnthropicContentNeverEmpty(t *testing.T) {
	// 空 content 数组会被部分客户端当成协议错误。
	blocks := NewAnthropicContent("", nil)
	require.Len(t, blocks, 1)
	require.Equal(t, "text", blocks[0].Type)
}

func TestNewAnthropicContentEmitsToolUseBlocks(t *testing.T) {
	blocks := NewAnthropicContent("working on it", []ToolCall{
		{ID: "call_1", Name: "Bash", Arguments: `{"command":"ls"}`},
		{ID: "call_2", Name: "Noop"},
	})
	require.Len(t, blocks, 3)
	require.Equal(t, "text", blocks[0].Type)
	require.Equal(t, "tool_use", blocks[1].Type)
	require.Equal(t, "Bash", blocks[1].Name)
	require.JSONEq(t, `{"command":"ls"}`, string(blocks[1].Input))
	// 入参为空时补 {}，否则 input 会序列化成 null。
	require.JSONEq(t, `{}`, string(blocks[2].Input))
}

func TestAnthropicStreamEventShapes(t *testing.T) {
	start := NewAnthropicToolUseBlockStart(1, ToolCall{ID: "call_1", Name: "Bash"})
	require.Equal(t, "content_block_start", start.Event)
	encoded, err := json.Marshal(start.Data)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"type":"content_block_start","index":1,
		"content_block":{"type":"tool_use","id":"toolu_1","name":"Bash","input":{}}
	}`, string(encoded))

	delta := NewAnthropicToolUseDelta(1, "")
	encoded, err = json.Marshal(delta.Data)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"type":"content_block_delta","index":1,
		"delta":{"type":"input_json_delta","partial_json":"{}"}
	}`, string(encoded))
}
