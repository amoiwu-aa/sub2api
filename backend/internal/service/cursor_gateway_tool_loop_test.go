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
