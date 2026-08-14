package cursor

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// shrinkToolCallGrace 把工具调用后的收齐窗口压到毫秒级。
func shrinkToolCallGrace(t *testing.T, grace time.Duration) {
	t.Helper()
	original := toolCallGrace
	toolCallGrace = grace
	t.Cleanup(func() { toolCallGrace = original })
}

func shrinkToolCallCollectionCap(t *testing.T, cap time.Duration) {
	t.Helper()
	original := toolCallCollectionCap
	toolCallCollectionCap = cap
	t.Cleanup(func() { toolCallCollectionCap = original })
}

// mcpToolCallMessage 构造一条 MCP 工具调用：exec_server_message(2) → mcp_args(11)。
func mcpToolCallMessage(execID, toolName string, args map[string]any) []byte {
	mcpArgs := EncodeStringField(mcpToolNameField, toolName)
	for key, value := range args {
		entry := concat(EncodeStringField(1, key), EncodeBytesField(2, EncodeProtobufValue(value)))
		mcpArgs = append(mcpArgs, EncodeBytesField(mcpToolArgsField, entry)...)
	}
	return EncodeBytesField(2, concat(
		EncodeBytesField(mcpArgsField, mcpArgs),
		EncodeStringField(15, execID),
	))
}

func TestRunAgentTurnEndsOnMcpToolCall(t *testing.T) {
	// 工具调用是双方约好的暂停点：上游此后在等一个我们不打算给的 exec 回执，
	// 所以收齐调用后要主动收尾，把控制权还给客户端。
	shrinkToolCallGrace(t, 100*time.Millisecond)

	server := &agentTestServer{t: t, hangAfterScript: true, script: [][]byte{
		thinkingDeltaMessage("准备执行"),
		mcpToolCallMessage("exec-1", "Bash", map[string]any{"command": "echo hi"}),
	}}
	client, host := startAgentTestServer(t, server)

	var deltas []AgentDelta
	result, err := RunAgentTurn(context.Background(), testAgentOptions(t, client, host),
		AgentTurnInput{Text: "run echo", ConversationID: "conv-1"},
		func(delta AgentDelta) error {
			deltas = append(deltas, delta)
			return nil
		})
	require.NoError(t, err)

	require.True(t, result.EndedWithToolCalls())
	require.Len(t, result.ToolCalls, 1)
	require.Equal(t, 1, result.MCPToolCalls)
	require.Zero(t, result.NativeToolCalls)
	require.Equal(t, "Bash", result.ToolCalls[0].Name)
	// id 会被规整成 OpenAI 风格：加 call_ 前缀、去掉连字符。
	require.Equal(t, "call_exec1", result.ToolCalls[0].ID)
	var args map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.ToolCalls[0].Arguments), &args))
	require.Equal(t, "echo hi", args["command"])

	// 工具调用也要作为增量交给调用方，流式响应才能边收边发。
	require.Len(t, deltas, 2)
	require.Equal(t, "准备执行", deltas[0].Thinking)
	require.NotNil(t, deltas[1].ToolCall)
	require.Equal(t, "Bash", deltas[1].ToolCall.Name)

	// 停在工具调用上不算「不完整」，不该被记成账号故障。
	require.False(t, result.Incomplete())
	require.False(t, result.Stalled)
	require.Empty(t, result.IncompleteSummary())

	// 绝不能在流上回 exec 结果：只有首帧 RunRequest 与心跳。
	for _, frame := range server.receivedFrames()[1:] {
		fields, err := ReadFields(frame)
		require.NoError(t, err)
		for _, field := range fields {
			require.NotEqual(t, 2, field.Number, "MCP 工具调用不该被 stub 回执")
		}
	}
}

func TestRunAgentTurnCollectsParallelToolCalls(t *testing.T) {
	// 同一轮里可能连发多个调用，收齐窗口就是为它们留的。
	shrinkToolCallGrace(t, 300*time.Millisecond)

	server := &agentTestServer{t: t, hangAfterScript: true, script: [][]byte{
		mcpToolCallMessage("exec-1", "Bash", map[string]any{"command": "ls"}),
		mcpToolCallMessage("exec-2", "Read", map[string]any{"file_path": "/tmp/x"}),
	}}
	client, host := startAgentTestServer(t, server)

	result, err := RunAgentTurn(context.Background(), testAgentOptions(t, client, host),
		AgentTurnInput{Text: "do two things", ConversationID: "conv-1"}, nil)
	require.NoError(t, err)
	require.Len(t, result.ToolCalls, 2)
	require.Equal(t, "Bash", result.ToolCalls[0].Name)
	require.Equal(t, "Read", result.ToolCalls[1].Name)
}

func TestRunAgentTurnHonorsDisableParallelToolCalls(t *testing.T) {
	shrinkToolCallGrace(t, 300*time.Millisecond)

	server := &agentTestServer{t: t, hangAfterScript: true, frameDelay: 50 * time.Millisecond, script: [][]byte{
		mcpToolCallMessage("exec-1", "Read", map[string]any{"file_path": "/tmp/a"}),
		mcpToolCallMessage("exec-2", "Read", map[string]any{"file_path": "/tmp/b"}),
	}}
	client, host := startAgentTestServer(t, server)

	result, err := RunAgentTurn(context.Background(), testAgentOptions(t, client, host),
		AgentTurnInput{
			Text:                     "read one",
			ConversationID:           "conv-no-parallel",
			DisableParallelToolCalls: true,
		}, nil)
	require.NoError(t, err)
	require.Len(t, result.ToolCalls, 1)
	require.Equal(t, "/tmp/a", func() string {
		var args map[string]string
		require.NoError(t, json.Unmarshal([]byte(result.ToolCalls[0].Arguments), &args))
		return args["file_path"]
	}())
	require.False(t, result.Incomplete())
}

func TestRunAgentTurnWaitsOutSlowlyGeneratedToolCalls(t *testing.T) {
	// 上游不是一批发完并行调用，而是逐个生成、生成一个发一个，帧间隔就是一次
	// 生成的耗时。收齐窗口比这个间隔短的话，后面的调用会在生成途中被关流丢掉，
	// 客户端只拿到第一个——线上表现为其余调用的参数漏成正文。
	//
	// 这里让帧间隔（120ms）明显大于旧的 500ms 窗口所能容忍的比例，再把窗口设成
	// 间隔的数倍，验证四个调用都能收齐。
	shrinkToolCallGrace(t, 400*time.Millisecond)

	server := &agentTestServer{t: t, hangAfterScript: true, frameDelay: 120 * time.Millisecond, script: [][]byte{
		textDeltaMessage("我并行看几个文件"),
		mcpToolCallMessage("exec-1", "read", map[string]any{"filePath": "/srv/app/src/server.ts"}),
		mcpToolCallMessage("exec-2", "read", map[string]any{"filePath": "/srv/app/src/db.ts"}),
		mcpToolCallMessage("exec-3", "grep", map[string]any{"pattern": "app.get"}),
		mcpToolCallMessage("exec-4", "bash", map[string]any{"command": "ls docs"}),
	}}
	client, host := startAgentTestServer(t, server)

	result, err := RunAgentTurn(context.Background(), testAgentOptions(t, client, host),
		AgentTurnInput{Text: "explore", ConversationID: "conv-1"}, nil)
	require.NoError(t, err)

	require.Len(t, result.ToolCalls, 4, "慢速生成的并行调用被截断了")
	require.Equal(t, []string{"read", "read", "grep", "bash"}, []string{
		result.ToolCalls[0].Name, result.ToolCalls[1].Name,
		result.ToolCalls[2].Name, result.ToolCalls[3].Name,
	})
	// 正文只该有那句开场白，工具参数不能混进来。
	require.Equal(t, "我并行看几个文件", result.Text)
	require.False(t, result.Incomplete())
}

func TestRunAgentTurnDropsToolCallsWhenGraceIsTooShort(t *testing.T) {
	// 反向固定住这个 bug 的形态：窗口短于生成间隔时确实只剩第一个调用。
	// 这条用例是为了让「把窗口改小」这种回退在测试里立刻现形。
	shrinkToolCallGrace(t, 30*time.Millisecond)

	server := &agentTestServer{t: t, hangAfterScript: true, frameDelay: 150 * time.Millisecond, script: [][]byte{
		mcpToolCallMessage("exec-1", "read", map[string]any{"filePath": "/a"}),
		mcpToolCallMessage("exec-2", "read", map[string]any{"filePath": "/b"}),
	}}
	client, host := startAgentTestServer(t, server)

	result, err := RunAgentTurn(context.Background(), testAgentOptions(t, client, host),
		AgentTurnInput{Text: "explore", ConversationID: "conv-1"}, nil)
	require.NoError(t, err)
	require.Len(t, result.ToolCalls, 1)
}

func TestRunAgentTurnMarksAbsoluteToolCollectionCapIncomplete(t *testing.T) {
	shrinkToolCallGrace(t, 100*time.Millisecond)
	shrinkToolCallCollectionCap(t, 80*time.Millisecond)

	server := &agentTestServer{
		t: t, hangAfterScript: true, frameDelay: 30 * time.Millisecond,
		script: [][]byte{
			mcpToolCallMessage("exec-1", "read", map[string]any{"filePath": "/a"}),
			mcpToolCallMessage("exec-2", "read", map[string]any{"filePath": "/b"}),
			mcpToolCallMessage("exec-3", "read", map[string]any{"filePath": "/c"}),
			mcpToolCallMessage("exec-4", "read", map[string]any{"filePath": "/d"}),
			mcpToolCallMessage("exec-5", "read", map[string]any{"filePath": "/e"}),
		},
	}
	client, host := startAgentTestServer(t, server)

	result, err := RunAgentTurn(context.Background(), testAgentOptions(t, client, host),
		AgentTurnInput{Text: "explore", ConversationID: "conv-cap"}, nil)
	require.NoError(t, err)
	require.True(t, result.ToolCallCollectionTimedOut)
	require.True(t, result.Incomplete())
	require.Contains(t, result.IncompleteSummary(), "tool_call_collection_timeout")
	require.Less(t, len(result.ToolCalls), 5, "绝对上限没有截停持续生成的调用")
}

func TestRunAgentTurnDeduplicatesIdenticalToolCallID(t *testing.T) {
	shrinkToolCallGrace(t, 100*time.Millisecond)

	call := mcpToolCallMessage("exec-dup", "Read", map[string]any{"file_path": "/tmp/x"})
	server := &agentTestServer{t: t, hangAfterScript: true, script: [][]byte{call, call}}
	client, host := startAgentTestServer(t, server)

	result, err := RunAgentTurn(context.Background(), testAgentOptions(t, client, host),
		AgentTurnInput{Text: "read", ConversationID: "conv-dup"}, nil)
	require.NoError(t, err)
	require.Len(t, result.ToolCalls, 1)
	require.Equal(t, 1, result.DuplicateToolCalls)
	require.Zero(t, result.ConflictingToolCalls)
	require.False(t, result.Incomplete())
}

func TestRunAgentTurnMarksConflictingDuplicateToolCallIncomplete(t *testing.T) {
	shrinkToolCallGrace(t, 100*time.Millisecond)

	server := &agentTestServer{t: t, hangAfterScript: true, script: [][]byte{
		mcpToolCallMessage("exec-conflict", "Read", map[string]any{"file_path": "/tmp/a"}),
		mcpToolCallMessage("exec-conflict", "Read", map[string]any{"file_path": "/tmp/b"}),
	}}
	client, host := startAgentTestServer(t, server)

	result, err := RunAgentTurn(context.Background(), testAgentOptions(t, client, host),
		AgentTurnInput{Text: "read", ConversationID: "conv-conflict"}, nil)
	require.NoError(t, err)
	require.Len(t, result.ToolCalls, 1)
	require.Equal(t, 1, result.ConflictingToolCalls)
	require.True(t, result.Incomplete())
	require.Contains(t, result.IncompleteSummary(), "tool_call_conflicts=1")
}

func TestRunAgentTurnKeepsTextBeforeToolCall(t *testing.T) {
	// 模型常常先说一句再调工具，那段正文要留给客户端。
	shrinkToolCallGrace(t, 100*time.Millisecond)

	server := &agentTestServer{t: t, hangAfterScript: true, script: [][]byte{
		textDeltaMessage("我来查一下"),
		mcpToolCallMessage("exec-1", "Bash", map[string]any{"command": "ls"}),
	}}
	client, host := startAgentTestServer(t, server)

	result, err := RunAgentTurn(context.Background(), testAgentOptions(t, client, host),
		AgentTurnInput{Text: "look", ConversationID: "conv-1"}, nil)
	require.NoError(t, err)
	require.Equal(t, "我来查一下", result.Text)
	require.True(t, result.EndedWithToolCalls())
}

func TestEncodeRunRequestCarriesDeclaredTools(t *testing.T) {
	body, err := EncodeRunRequest(RunRequestInput{
		Text:           "hi",
		ConversationID: "conv-1",
		MessageID:      "msg-1",
		Tools: []McpTool{{
			Name:        "Bash",
			Description: "run a command",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}},
	})
	require.NoError(t, err)

	outer, err := ReadFields(body)
	require.NoError(t, err)
	runRequest, ok := FieldBytes(outer, 1)
	require.True(t, ok)
	fields, err := ReadFields(runRequest)
	require.NoError(t, err)

	mcpTools, ok := FieldBytes(fields, mcpToolsField)
	require.True(t, ok)
	entries, err := ReadFields(mcpTools)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	definition, err := ReadFields(entries[0].Bytes)
	require.NoError(t, err)
	require.Equal(t, "Bash", FieldString(definition, 1))
	require.Equal(t, "run a command", FieldString(definition, 2))
}

func TestEncodeRunRequestWithoutToolsIsByteIdenticalToBefore(t *testing.T) {
	// 不带工具的请求字节必须与改动前一致，否则纯对话请求的指纹会变。
	input := RunRequestInput{Text: "hi", ConversationID: "conv-1", MessageID: "msg-1"}
	withoutTools, err := EncodeRunRequest(input)
	require.NoError(t, err)

	input.Tools = []McpTool{}
	withEmptyTools, err := EncodeRunRequest(input)
	require.NoError(t, err)
	require.Equal(t, withoutTools, withEmptyTools)
}
