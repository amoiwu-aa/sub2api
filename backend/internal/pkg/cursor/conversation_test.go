package cursor

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRenderPlainChatStaysUnwrapped(t *testing.T) {
	// 单条用户消息、无工具时不该被套上一堆标签：那会白白改变纯对话请求的形态。
	conversation := &Conversation{Turns: []Turn{{Role: RoleUser, Text: "1+1 等于几？"}}}
	require.Equal(t, "1+1 等于几？", conversation.Render())
}

func TestRenderPutsSystemInItsOwnBlock(t *testing.T) {
	conversation := &Conversation{Turns: []Turn{
		{Role: RoleSystem, Text: "be brief"},
		{Role: RoleUser, Text: "hi"},
	}}
	rendered := conversation.Render()
	require.Contains(t, rendered, "<system_instructions>\nbe brief\n</system_instructions>")
	require.True(t, strings.HasSuffix(rendered, "hi"))
}

func TestRenderIncludesToolPolicyWhenToolsDeclared(t *testing.T) {
	// 没有这段前言，模型会去用 Cursor 自带的 shell/read——那些工具网关执行不了，
	// 整轮会卡在一个回不出结果的 exec 上。实测验证过。
	conversation := &Conversation{
		Tools: []McpTool{{Name: "Bash"}},
		Turns: []Turn{{Role: RoleUser, Text: "list files"}},
	}
	rendered := conversation.Render()
	require.Contains(t, rendered, "<tool_policy>")
	require.Contains(t, rendered, McpToolNamespacePrefix+"Bash")
}

func TestRenderToolRoundTripHistory(t *testing.T) {
	conversation := &Conversation{
		Tools: []McpTool{{Name: "Bash"}},
		Turns: []Turn{
			{Role: RoleUser, Text: "run echo hi"},
			{Role: RoleAssistant, Text: "sure", ToolCalls: []ToolCall{{
				ID: "call_1", Name: "Bash", Arguments: `{"command":"echo hi"}`,
			}}},
			{Role: RoleTool, ToolCallID: "call_1", ToolName: "Bash", Text: "hi\n"},
		},
	}
	rendered := conversation.Render()

	require.Contains(t, rendered, "<conversation_history>")
	require.Contains(t, rendered, "<user>\nrun echo hi\n</user>")
	require.Contains(t, rendered, `<tool_call id="call_1" name="Bash">`)
	require.Contains(t, rendered, `{"command":"echo hi"}`)
	require.Contains(t, rendered, `<tool_result id="call_1" name="Bash">`)
	require.Contains(t, rendered, "hi")
	// 末尾是工具结果时要点破「继续，别重复调用」，否则模型会把刚跑完的工具再调一遍。
	require.Contains(t, rendered, "<continue>")
}

func TestRenderWithoutTrailingToolResultOmitsContinue(t *testing.T) {
	conversation := &Conversation{Turns: []Turn{
		{Role: RoleUser, Text: "first"},
		{Role: RoleAssistant, Text: "answer"},
		{Role: RoleUser, Text: "second"},
	}}
	rendered := conversation.Render()
	require.NotContains(t, rendered, "<continue>")
	// 末尾连续的用户消息算「本轮请求」，要放在历史块之外。
	require.Contains(t, rendered, "</conversation_history>\n\nsecond")
}

func TestRenderEmptyToolResultIsSpelledOut(t *testing.T) {
	// 留白会让模型以为工具还没跑完，转头把同一个调用再发一遍。
	conversation := &Conversation{Turns: []Turn{
		{Role: RoleUser, Text: "go"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "c1", Name: "Noop"}}},
		{Role: RoleTool, ToolCallID: "c1", ToolName: "Noop", Text: "  "},
	}}
	require.Contains(t, conversation.Render(), "(the tool returned no output)")
}

func TestRenderToolCallWithoutArgumentsEmitsEmptyObject(t *testing.T) {
	conversation := &Conversation{Turns: []Turn{
		{Role: RoleUser, Text: "go"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "c1", Name: "Noop"}}},
		{Role: RoleUser, Text: "and then?"},
	}}
	require.Contains(t, conversation.Render(), "<tool_call id=\"c1\" name=\"Noop\">\n{}\n</tool_call>")
}

func TestHasHistory(t *testing.T) {
	single := &Conversation{Turns: []Turn{
		{Role: RoleSystem, Text: "s"},
		{Role: RoleUser, Text: "u"},
	}}
	require.False(t, single.HasHistory())

	withTool := &Conversation{Turns: []Turn{
		{Role: RoleUser, Text: "u"},
		{Role: RoleTool, ToolCallID: "c1", Text: "r"},
	}}
	require.True(t, withTool.HasHistory())

	multiTurn := &Conversation{Turns: []Turn{
		{Role: RoleUser, Text: "a"},
		{Role: RoleUser, Text: "b"},
	}}
	require.True(t, multiTurn.HasHistory())
}

func TestRenderNilAndEmpty(t *testing.T) {
	var nilConversation *Conversation
	require.Empty(t, nilConversation.Render())
	require.Empty(t, (&Conversation{}).Render())
}
