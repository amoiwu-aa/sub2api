package cursor

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEncodeMcpToolsEmptyMatchesTextOnlyPlaceholder(t *testing.T) {
	// RunRequest 的 field 4 是观测到的必填字段。空工具列表必须编成零字节，
	// 与不带工具时的占位符逐字节相同，否则纯对话请求的指纹会因为这次改动而变。
	require.Empty(t, EncodeMcpTools(nil))
	require.Empty(t, EncodeMcpTools([]McpTool{}))
	require.Equal(t, EncodeBytesField(4, nil), EncodeBytesField(4, EncodeMcpTools(nil)))
}

func TestEncodeMcpToolsSkipsUnnamedTools(t *testing.T) {
	// 一个坏条目会连累整份声明，所以没名字的直接丢掉而不是编个空名进去。
	encoded := EncodeMcpTools([]McpTool{
		{Name: "  ", Description: "no name"},
		{Name: "Bash", Description: "run a command"},
	})
	fields, err := ReadFields(encoded)
	require.NoError(t, err)
	require.Len(t, fields, 1)

	definition, err := ReadFields(fields[0].Bytes)
	require.NoError(t, err)
	require.Equal(t, "Bash", FieldString(definition, 1))
	require.Equal(t, "run a command", FieldString(definition, 2))
	require.Equal(t, McpProviderIdentifier, FieldString(definition, 4))
	require.Equal(t, "Bash", FieldString(definition, 5))
}

func TestEncodeMcpToolDefinitionFillsMissingSchema(t *testing.T) {
	// 上游会忽略没有 input_schema 的工具，模型也就永远不会调它。
	encoded := encodeMcpToolDefinition(McpTool{Name: "Ping"})
	fields, err := ReadFields(encoded)
	require.NoError(t, err)

	schemaBytes, ok := FieldBytes(fields, 3)
	require.True(t, ok)
	decoded, err := DecodeProtobufValue(schemaBytes)
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}, decoded)
}

func TestEncodeMcpToolsIsDeterministic(t *testing.T) {
	// 同一份声明每次必须编出相同字节，否则请求指纹会在每次请求间抖动。
	tool := McpTool{
		Name:        "Read",
		Description: "read a file",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"b":{"type":"string"},"a":{"type":"number"}}}`),
	}
	first := EncodeMcpTools([]McpTool{tool})
	for i := 0; i < 20; i++ {
		require.Equal(t, first, EncodeMcpTools([]McpTool{tool}))
	}
}

func TestValidateMcpToolsRejectsMalformedDeclarations(t *testing.T) {
	tests := []struct {
		name  string
		tools []McpTool
		want  string
	}{
		{
			name:  "empty name",
			tools: []McpTool{{Name: " ", InputSchema: json.RawMessage(`{"type":"object"}`)}},
			want:  "name must not be empty",
		},
		{
			name: "duplicate name",
			tools: []McpTool{
				{Name: "Read", InputSchema: json.RawMessage(`{"type":"object"}`)},
				{Name: "Read", InputSchema: json.RawMessage(`{"type":"object"}`)},
			},
			want: "duplicate tool name",
		},
		{
			name:  "invalid json",
			tools: []McpTool{{Name: "Read", InputSchema: json.RawMessage(`{`)}},
			want:  "invalid JSON schema",
		},
		{
			name:  "non object root",
			tools: []McpTool{{Name: "Read", InputSchema: json.RawMessage(`{"type":"array"}`)}},
			want:  "root type must be object",
		},
		{
			name:  "invalid properties",
			tools: []McpTool{{Name: "Read", InputSchema: json.RawMessage(`{"type":"object","properties":[]}`)}},
			want:  "properties must be an object",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.ErrorContains(t, ValidateMcpTools(tc.tools), tc.want)
		})
	}
}

func TestEncodeRunRequestRejectsInvalidToolSchema(t *testing.T) {
	_, err := EncodeRunRequest(RunRequestInput{
		Text:           "hi",
		ConversationID: "conv-invalid-tools",
		Tools: []McpTool{{
			Name:        "Read",
			InputSchema: json.RawMessage(`not-json`),
		}},
	})
	require.ErrorContains(t, err, "invalid JSON schema")
}

// buildMcpArgs 拼一条 McpArgs：{5: tool_name, 2: {1: key, 2: Value}...}
func buildMcpArgs(toolName string, args map[string]any) []byte {
	out := EncodeStringField(mcpToolNameField, toolName)
	for key, value := range args {
		entry := concat(EncodeStringField(1, key), EncodeBytesField(2, EncodeProtobufValue(value)))
		out = append(out, EncodeBytesField(mcpToolArgsField, entry)...)
	}
	return out
}

func TestParseMcpArgsDecodesNameAndArguments(t *testing.T) {
	payload := buildMcpArgs("Bash", map[string]any{"command": "echo hi", "timeout": float64(5)})
	call, err := parseMcpArgs(payload)
	require.NoError(t, err)
	require.NotNil(t, call)
	require.Equal(t, "Bash", call.Name)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(call.Arguments, &decoded))
	require.Equal(t, "echo hi", decoded["command"])
	require.InDelta(t, 5.0, decoded["timeout"], 0.0001)
}

func TestParseMcpArgsPrefersToolNameOverLegacyName(t *testing.T) {
	// 上游两个字段都可能出现，只有 tool_name(5) 是客户端声明时用的那个名字。
	payload := concat(
		EncodeStringField(mcpToolLegacyName, "fallback"),
		EncodeStringField(mcpToolNameField, "Preferred"),
	)
	call, err := parseMcpArgs(payload)
	require.NoError(t, err)
	require.Equal(t, "Preferred", call.Name)
}

func TestParseMcpArgsStripsNamespacePrefix(t *testing.T) {
	// 模型侧看到的是 mcp_<provider>_<name>，客户端认的是裸名字。
	payload := EncodeStringField(mcpToolNameField, McpToolNamespacePrefix+"Bash")
	call, err := parseMcpArgs(payload)
	require.NoError(t, err)
	require.Equal(t, "Bash", call.Name)
}

func TestParseMcpArgsEmptyArgumentsBecomeEmptyObject(t *testing.T) {
	// 客户端普遍直接对 arguments 做 JSON.parse，null 会让它们炸掉。
	call, err := parseMcpArgs(EncodeStringField(mcpToolNameField, "NoArgs"))
	require.NoError(t, err)
	require.Equal(t, json.RawMessage("{}"), call.Arguments)
}

func TestParseMcpArgsWithoutNameReturnsNil(t *testing.T) {
	call, err := parseMcpArgs(EncodeBytesField(mcpToolArgsField, nil))
	require.NoError(t, err)
	require.Nil(t, call)
}

func TestParseServerMessageSurfacesMcpToolCall(t *testing.T) {
	// exec_server_message(2) → mcp_args(11)。这一条绝不能落进 stub 回执分支：
	// 回 stub 等于告诉模型「你的工具坏了」。
	payload := EncodeBytesField(2, concat(
		EncodeBytesField(mcpArgsField, buildMcpArgs("Bash", map[string]any{"command": "ls"})),
		EncodeStringField(15, "exec-42"),
	))

	message, err := ParseServerMessage(payload)
	require.NoError(t, err)
	require.Equal(t, KindToolCall, message.Kind)
	require.NotNil(t, message.ToolCall)
	require.Equal(t, "Bash", message.ToolCall.Name)
	require.Equal(t, "exec-42", message.ToolCall.CallID)
}

func TestParseServerMessageKeepsPlainExecSeparate(t *testing.T) {
	// 没有 mcp_args 的还是普通 exec，仍旧走 stub 回执。
	message, err := ParseServerMessage(execServerMessage(7, "exec-7", 8))
	require.NoError(t, err)
	require.Equal(t, KindExec, message.Kind)
	require.Nil(t, message.ToolCall)
	require.Equal(t, "ls", message.Exec.Kind)
}

func TestToolPolicyPreambleIsEmptyWithoutTools(t *testing.T) {
	// 纯对话请求不该被工具约束污染。
	require.Empty(t, ToolPolicyPreamble(nil))
	require.Empty(t, ToolPolicyPreamble([]McpTool{{Name: "   "}}))
}

func TestToolPolicyPreambleListsNamespacedNames(t *testing.T) {
	// 模型看到的是带命名空间的名字，前言里必须写这个，否则它对不上号。
	preamble := ToolPolicyPreamble([]McpTool{{Name: "Bash"}, {Name: "Read"}})
	require.Contains(t, preamble, McpToolNamespacePrefix+"Bash")
	require.Contains(t, preamble, McpToolNamespacePrefix+"Read")
	require.Contains(t, preamble, "unavailable")
}

func TestToolPolicyPreambleEnforcesNoneAndNoParallel(t *testing.T) {
	none := ToolPolicyPreambleWithControl(nil, nil, true, false)
	require.Contains(t, none, "Tool use is disabled")
	require.Contains(t, none, "do not invoke any tool")

	single := ToolPolicyPreambleWithControl([]McpTool{{Name: "Read"}}, nil, false, true)
	require.Contains(t, single, "Call at most one tool")
	require.Contains(t, single, McpToolNamespacePrefix+"Read")
}

func TestNormalizeToolName(t *testing.T) {
	require.Equal(t, "Bash", NormalizeToolName(McpToolNamespacePrefix+"Bash"))
	require.Equal(t, "Bash", NormalizeToolName("  Bash  "))
	require.Equal(t, "", NormalizeToolName(""))
}
