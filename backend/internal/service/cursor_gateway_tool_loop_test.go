package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/stretchr/testify/require"
)

func TestCursorToolLoopExceeded(t *testing.T) {
	conversation := &cursor.Conversation{Turns: []cursor.Turn{
		{Role: cursor.RoleUser, Text: "inspect the project"},
		{Role: cursor.RoleAssistant, ToolCalls: []cursor.ToolCall{{ID: "call_1", Name: "Read"}}},
		{Role: cursor.RoleTool, ToolCallID: "call_1", Text: "first"},
		{Role: cursor.RoleAssistant, ToolCalls: []cursor.ToolCall{{ID: "call_2", Name: "Read"}}},
		{Role: cursor.RoleTool, ToolCallID: "call_2", Text: "second"},
	}}

	service := &CursorGatewayService{maxToolContinuations: 2}
	depth, limit, exceeded := service.toolLoopExceeded(conversation)
	require.Equal(t, 2, depth)
	require.Equal(t, 2, limit)
	require.True(t, exceeded)
	require.Contains(t, cursorToolLoopMessage(depth, limit), "2 consecutive tool continuations")
}

func TestCursorToolLoopCanBeDisabled(t *testing.T) {
	conversation := &cursor.Conversation{Turns: []cursor.Turn{
		{Role: cursor.RoleUser, Text: "inspect the project"},
		{Role: cursor.RoleAssistant, ToolCalls: []cursor.ToolCall{{ID: "call_1", Name: "Read"}}},
		{Role: cursor.RoleTool, ToolCallID: "call_1", Text: "result"},
	}}

	service := &CursorGatewayService{maxToolContinuations: 0}
	depth, limit, exceeded := service.toolLoopExceeded(conversation)
	require.Zero(t, depth)
	require.Zero(t, limit)
	require.False(t, exceeded)
}

func TestCursorToolLoopProgressResetsGuard(t *testing.T) {
	conversation := &cursor.Conversation{Turns: []cursor.Turn{
		{Role: cursor.RoleUser, Text: "start the backend"},
		{Role: cursor.RoleAssistant, ToolCalls: []cursor.ToolCall{{ID: "call_1", Name: "Read"}}},
		{Role: cursor.RoleTool, ToolCallID: "call_1", Text: "package.json"},
		{Role: cursor.RoleAssistant, ToolCalls: []cursor.ToolCall{{ID: "call_2", Name: "PowerShell"}}},
		{Role: cursor.RoleTool, ToolCallID: "call_2", Text: "server started"},
		{Role: cursor.RoleAssistant, ToolCalls: []cursor.ToolCall{{ID: "call_3", Name: "Read"}}},
		{Role: cursor.RoleTool, ToolCallID: "call_3", Text: "health output"},
	}}

	service := &CursorGatewayService{maxToolContinuations: 2}
	depth, limit, exceeded := service.toolLoopExceeded(conversation)
	require.Equal(t, 1, depth)
	require.Equal(t, 2, limit)
	require.False(t, exceeded)
}

func TestApplyReadOnlyRecoverySuppressesObservationToolsAndKeepsActions(t *testing.T) {
	conversation := &cursor.Conversation{Turns: []cursor.Turn{
		{Role: cursor.RoleUser, Text: "start the backend"},
		{Role: cursor.RoleAssistant, ToolCalls: []cursor.ToolCall{{ID: "call_1", Name: "Glob"}}},
		{Role: cursor.RoleTool, ToolCallID: "call_1", Text: "package.json"},
		{Role: cursor.RoleAssistant, ToolCalls: []cursor.ToolCall{{ID: "call_2", Name: "Grep"}}},
		{Role: cursor.RoleTool, ToolCallID: "call_2", Text: "npm run dev"},
		{Role: cursor.RoleAssistant, ToolCalls: []cursor.ToolCall{{ID: "call_3", Name: "Read"}}},
		{Role: cursor.RoleTool, ToolCallID: "call_3", Text: "scripts"},
	}}
	nativeBridge := cursor.NativeToolBridge{
		"read":  {Name: "Read"},
		"grep":  {Name: "Grep"},
		"glob":  {Name: "Glob"},
		"shell": {Name: "Bash"},
		"write": {Name: "Write"},
	}
	mcpTools := []cursor.McpTool{
		{Name: "ReadFile"},
		{Name: "PowerShell"},
		{Name: "Edit"},
		{Name: "TaskUpdate"},
	}

	service := &CursorGatewayService{
		repeatedReadRecoveryThreshold: 2,
		readOnlyRecoveryThreshold:     3,
	}
	updatedBridge, updatedTools, recovery := service.applyRepeatedReadRecovery(
		conversation,
		nativeBridge,
		mcpTools,
	)

	require.NotNil(t, recovery)
	require.Equal(t, "read_only_exploration", recovery.Reason)
	require.Equal(t, 3, recovery.Repeats)
	require.True(t, recovery.NativeSuppressed)
	require.True(t, recovery.MCPSuppressed)
	require.Empty(t, updatedBridge.ClientName("read"))
	require.Empty(t, updatedBridge.ClientName("grep"))
	require.Empty(t, updatedBridge.ClientName("glob"))
	require.Equal(t, "Bash", updatedBridge.ClientName("shell"))
	require.Equal(t, "Write", updatedBridge.ClientName("write"))
	require.Equal(t, []string{"PowerShell", "Edit", "TaskUpdate"}, cursorMCPToolNames(updatedTools))
	require.Equal(t, cursor.RoleSystem, conversation.Turns[len(conversation.Turns)-1].Role)
	require.Contains(t, conversation.Turns[len(conversation.Turns)-1].Text, "Do not continue planning")
}

func TestApplyMissingReadRecoverySwitchesToMutationToolOnFirstResult(t *testing.T) {
	conversation := &cursor.Conversation{Turns: []cursor.Turn{
		{Role: cursor.RoleUser, Text: "create a summary"},
		{Role: cursor.RoleAssistant, ToolCalls: []cursor.ToolCall{{
			ID: "read_1", Name: "Read", Arguments: `{"file_path":"summary.md"}`,
		}}},
		{
			Role:       cursor.RoleTool,
			ToolCallID: "read_1",
			Text:       "[tool error] File does not exist.",
			ToolError:  true,
		},
	}}
	nativeBridge := cursor.NativeToolBridge{
		"read":  {Name: "Read"},
		"write": {Name: "Write"},
	}
	mcpTools := []cursor.McpTool{
		{Name: "Edit"},
		{Name: "TaskList"},
	}

	service := &CursorGatewayService{repeatedReadRecoveryThreshold: 2}
	updatedBridge, updatedTools, recovery := service.applyRepeatedReadRecovery(
		conversation,
		nativeBridge,
		mcpTools,
	)

	require.NotNil(t, recovery)
	require.Equal(t, "Read", recovery.ToolName)
	require.Equal(t, 1, recovery.Repeats)
	require.Equal(t, "missing_file_preflight", recovery.Reason)
	require.Equal(t, "summary.md", recovery.Path)
	require.Equal(t, []string{"write", cursor.McpToolNamespacePrefix + "Edit"}, recovery.MutationTools)
	require.True(t, recovery.ResultNormalized)
	require.True(t, recovery.NativeSuppressed)
	require.False(t, recovery.MCPSuppressed)
	require.Empty(t, updatedBridge.ClientName("read"))
	require.Equal(t, "Write", updatedBridge.ClientName("write"))
	require.Equal(t, []string{"Edit", "TaskList"}, cursorMCPToolNames(updatedTools))
	require.NotContains(t, conversation.Turns[len(conversation.Turns)-1].Text, "does not exist")
	require.Contains(t, conversation.Turns[len(conversation.Turns)-1].Text,
		"Available file mutation tools: write, "+cursor.McpToolNamespacePrefix+"Edit")
	require.False(t, conversation.Turns[len(conversation.Turns)-1].ToolError)
}

func TestApplyMissingReadRecoveryUsesExactMCPToolName(t *testing.T) {
	conversation := &cursor.Conversation{Turns: []cursor.Turn{
		{Role: cursor.RoleUser, Text: "create a summary"},
		{Role: cursor.RoleAssistant, ToolCalls: []cursor.ToolCall{{
			ID: "read_1", Name: "Read", Arguments: `{"file_path":"summary.md"}`,
		}}},
		{Role: cursor.RoleTool, ToolCallID: "read_1", Text: "file not found"},
	}}
	mcpTools := []cursor.McpTool{
		{Name: "Read"},
		{Name: "Write"},
		{Name: "TaskList"},
	}

	service := &CursorGatewayService{repeatedReadRecoveryThreshold: 2}
	updatedBridge, updatedTools, recovery := service.applyRepeatedReadRecovery(
		conversation,
		nil,
		mcpTools,
	)

	require.NotNil(t, recovery)
	require.Empty(t, updatedBridge)
	require.False(t, recovery.NativeSuppressed)
	require.True(t, recovery.MCPSuppressed)
	require.Equal(t, []string{cursor.McpToolNamespacePrefix + "Write"}, recovery.MutationTools)
	require.Equal(t, []string{"Write", "TaskList"}, cursorMCPToolNames(updatedTools))
	require.Contains(t, conversation.Turns[len(conversation.Turns)-1].Text,
		"Available file mutation tools: "+cursor.McpToolNamespacePrefix+"Write")
	require.NotContains(t, conversation.Turns[len(conversation.Turns)-1].Text, "file not found")
}

func TestApplyRepeatedReadRecoveryStillHandlesGenericNoProgressLoop(t *testing.T) {
	conversation := &cursor.Conversation{Turns: []cursor.Turn{
		{Role: cursor.RoleUser, Text: "inspect the summary"},
		{Role: cursor.RoleAssistant, ToolCalls: []cursor.ToolCall{{
			ID: "read_1", Name: "Read", Arguments: `{"file_path":"summary.md"}`,
		}}},
		{Role: cursor.RoleTool, ToolCallID: "read_1", Text: "same stale contents"},
		{Role: cursor.RoleAssistant, ToolCalls: []cursor.ToolCall{{
			ID: "read_2", Name: "Read", Arguments: `{"file_path":"summary.md"}`,
		}}},
		{Role: cursor.RoleTool, ToolCallID: "read_2", Text: "same stale contents"},
	}}
	nativeBridge := cursor.NativeToolBridge{
		"read":  {Name: "Read"},
		"write": {Name: "Write"},
	}

	service := &CursorGatewayService{repeatedReadRecoveryThreshold: 2}
	updatedBridge, _, recovery := service.applyRepeatedReadRecovery(
		conversation,
		nativeBridge,
		nil,
	)

	require.NotNil(t, recovery)
	require.Equal(t, 2, recovery.Repeats)
	require.Equal(t, "repeated_identical_result", recovery.Reason)
	require.False(t, recovery.ResultNormalized)
	require.Empty(t, updatedBridge.ClientName("read"))
	require.Equal(t, cursor.RoleSystem, conversation.Turns[len(conversation.Turns)-1].Role)
	require.Contains(t, conversation.Turns[len(conversation.Turns)-1].Text, "use Write or Edit")
}

func TestApplyMissingReadRecoveryRequiresMutationTool(t *testing.T) {
	conversation := &cursor.Conversation{Turns: []cursor.Turn{
		{Role: cursor.RoleUser, Text: "check whether it exists"},
		{Role: cursor.RoleAssistant, ToolCalls: []cursor.ToolCall{{
			ID: "read_1", Name: "Read", Arguments: `{"file_path":"summary.md"}`,
		}}},
		{Role: cursor.RoleTool, ToolCallID: "read_1", Text: "file not found"},
	}}
	nativeBridge := cursor.NativeToolBridge{"read": {Name: "Read"}}

	service := &CursorGatewayService{repeatedReadRecoveryThreshold: 2}
	updatedBridge, _, recovery := service.applyRepeatedReadRecovery(
		conversation,
		nativeBridge,
		nil,
	)

	require.Nil(t, recovery)
	require.Equal(t, "Read", updatedBridge.ClientName("read"))
	require.Equal(t, "file not found", conversation.Turns[len(conversation.Turns)-1].Text)
}

func TestApplyRepeatedReadRecoveryCanBeDisabled(t *testing.T) {
	conversation := &cursor.Conversation{Turns: []cursor.Turn{
		{Role: cursor.RoleUser, Text: "inspect"},
		{Role: cursor.RoleAssistant, ToolCalls: []cursor.ToolCall{{
			ID: "read_1", Name: "Read", Arguments: `{"file_path":"a"}`,
		}}},
		{Role: cursor.RoleTool, ToolCallID: "read_1", Text: "file not found"},
	}}
	nativeBridge := cursor.NativeToolBridge{
		"read":  {Name: "Read"},
		"write": {Name: "Write"},
	}

	service := &CursorGatewayService{repeatedReadRecoveryThreshold: 0}
	updatedBridge, _, recovery := service.applyRepeatedReadRecovery(
		conversation,
		nativeBridge,
		nil,
	)

	require.Nil(t, recovery)
	require.Equal(t, "Read", updatedBridge.ClientName("read"))
	require.Equal(t, "file not found", conversation.Turns[len(conversation.Turns)-1].Text)
}

func cursorMCPToolNames(tools []cursor.McpTool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}
