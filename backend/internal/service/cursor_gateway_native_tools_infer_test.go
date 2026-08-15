//go:build unit

package service

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/stretchr/testify/require"
)

func schemaTool(name, schema string) cursor.McpTool {
	return cursor.McpTool{Name: name, InputSchema: json.RawMessage(schema)}
}

// claudeCodeTools 复刻 Claude Code 的工具形状：路径叫 file_path 而不是 path，
// grep 的大小写开关叫 "-i"，WebFetch 还必填一个网关永远不会发的 prompt。
func claudeCodeTools() []cursor.McpTool {
	return []cursor.McpTool{
		schemaTool("Read", `{"type":"object","properties":{
			"file_path":{"type":"string"},"offset":{"type":"number"},"limit":{"type":"number"}
		},"required":["file_path"]}`),
		schemaTool("Grep", `{"type":"object","properties":{
			"pattern":{"type":"string"},"path":{"type":"string"},"glob":{"type":"string"},
			"output_mode":{"type":"string"},"-i":{"type":"boolean"},"head_limit":{"type":"number"}
		},"required":["pattern"]}`),
		schemaTool("Bash", `{"type":"object","properties":{
			"command":{"type":"string"},"timeout":{"type":"number"},
			"description":{"type":"string"},"run_in_background":{"type":"boolean"}
		},"required":["command"]}`),
		schemaTool("Write", `{"type":"object","properties":{
			"file_path":{"type":"string"},"content":{"type":"string"}
		},"required":["file_path","content"]}`),
		schemaTool("WebFetch", `{"type":"object","properties":{
			"url":{"type":"string"},"prompt":{"type":"string"}
		},"required":["url","prompt"]}`),
	}
}

func TestInferNativeToolBridgeAdaptsClaudeCodeArgNames(t *testing.T) {
	bridge := inferNativeToolBridge(claudeCodeTools())

	require.Equal(t, cursor.NativeToolTarget{
		Name:     "Read",
		ArgNames: map[string]string{"path": "file_path"},
		ArgBindings: map[string]cursor.NativeArgBinding{
			"path":   {Name: "file_path"},
			"offset": {Name: "offset"},
			"limit":  {Name: "limit"},
		},
	}, bridge["read"])
	require.Equal(t, cursor.NativeToolTarget{
		Name:     "Write",
		ArgNames: map[string]string{"path": "file_path"},
		ArgBindings: map[string]cursor.NativeArgBinding{
			"path":    {Name: "file_path"},
			"content": {Name: "content"},
		},
	}, bridge["write"])
	require.Equal(t, cursor.NativeToolTarget{
		Name:     "Grep",
		ArgNames: map[string]string{"case_insensitive": "-i"},
		ArgBindings: map[string]cursor.NativeArgBinding{
			"pattern":          {Name: "pattern"},
			"path":             {Name: "path"},
			"glob":             {Name: "glob"},
			"output_mode":      {Name: "output_mode"},
			"case_insensitive": {Name: "-i"},
			"head_limit":       {Name: "head_limit"},
		},
	}, bridge["grep"])
	// Bash 的属性名与规范名完全一致，不需要改写表。
	require.Equal(t, cursor.NativeToolTarget{
		Name: "Bash",
		ArgBindings: map[string]cursor.NativeArgBinding{
			"command":           {Name: "command"},
			"timeout":           {Name: "timeout"},
			"run_in_background": {Name: "run_in_background"},
			"description":       {Name: "description"},
		},
	}, bridge["shell"])

	// WebFetch 必填 prompt，网关发不出来，映过去每次调用都会缺参数。
	require.NotContains(t, bridge, "fetch")
	// 客户端没声明的内置工具不会凭空出现。
	require.NotContains(t, bridge, "ls")
	require.NotContains(t, bridge, "delete")
	require.NotContains(t, bridge, "diagnostics")
}

// Codex 的 shell 收 command: string[]，网关发的是字符串，类型对不上就必须
// 放弃映射而不是硬发过去。
func TestInferNativeToolBridgeRejectsIncompatibleArgType(t *testing.T) {
	tools := []cursor.McpTool{
		schemaTool("shell", `{"type":"object","properties":{
			"command":{"type":"array","items":{"type":"string"}},
			"workdir":{"type":"string"},"timeout_ms":{"type":"number"}
		},"required":["command"]}`),
	}
	require.Nil(t, inferNativeToolBridge(tools))
}

// 按本包契约实现的客户端（AutoClaw）属性名就是规范名，推断结果不带改写表。
func TestInferNativeToolBridgeCanonicalNamesNeedNoRewrite(t *testing.T) {
	tools := []cursor.McpTool{
		schemaTool("Read", `{"type":"object","properties":{
			"path":{"type":"string"},"offset":{"type":"integer"},"limit":{"type":"integer"}
		},"required":["path"]}`),
		schemaTool("ListDir", `{"type":"object","properties":{"path":{"type":"string"}}}`),
		schemaTool("LspDiagnostics", `{"type":"object","properties":{"path":{"type":"string"}}}`),
	}

	bridge := inferNativeToolBridge(tools)
	require.Equal(t, cursor.NativeToolBridge{
		"read": {Name: "Read", ArgBindings: map[string]cursor.NativeArgBinding{
			"path": {Name: "path"}, "offset": {Name: "offset"}, "limit": {Name: "limit"},
		}},
		"ls": {Name: "ListDir", ArgBindings: map[string]cursor.NativeArgBinding{
			"path": {Name: "path"},
		}},
		"diagnostics": {Name: "LspDiagnostics", ArgBindings: map[string]cursor.NativeArgBinding{
			"path": {Name: "path"},
		}},
	}, bridge)
}

// 没有属性表的 schema 无从校验，宁可不映射也不赌客户端认得规范名。
func TestInferNativeToolBridgeSkipsUnverifiableSchema(t *testing.T) {
	require.Nil(t, inferNativeToolBridge([]cursor.McpTool{
		schemaTool("Read", `{"type":"object"}`),
		schemaTool("Grep", ``),
		schemaTool("Bash", `not json`),
	}))
}

// 一个客户端工具只能被一个内置工具占用；别名靠前的先拿。
func TestInferNativeToolBridgeClaimsEachClientToolOnce(t *testing.T) {
	readSchema := `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`
	tools := []cursor.McpTool{
		schemaTool("read_file", readSchema),
		schemaTool("Read", readSchema),
	}

	bridge := inferNativeToolBridge(tools)
	// "read" 别名排在 "read_file" 前面。
	require.Equal(t, cursor.NativeToolTarget{
		Name:        "Read",
		ArgBindings: map[string]cursor.NativeArgBinding{"path": {Name: "path"}},
	}, bridge["read"])
	require.Len(t, bridge, 1)
}

func TestResolveCursorNativeToolBridgeInfersWhenOptionsAbsent(t *testing.T) {
	tools := claudeCodeTools()
	bridge, mcpTools, err := resolveCursorNativeToolBridge(
		[]byte(`{"model":"cursor/grok-4.6"}`), tools, CursorNativeToolBridgeModeInferAll)
	require.NoError(t, err)
	require.Equal(t, "Read", bridge.ClientName("read"))
	require.Equal(t, "Write", bridge.ClientName("write"))

	// 被桥接的工具从 MCP 注册里摘掉，没桥上的 WebFetch 照常走 MCP。
	require.Len(t, mcpTools, 1)
	require.Equal(t, "WebFetch", mcpTools[0].Name)
}

func TestResolveCursorNativeToolBridgeDefaultModeInfersWrite(t *testing.T) {
	tools := claudeCodeTools()
	bridge, mcpTools, err := resolveCursorNativeToolBridge(
		[]byte(`{"model":"cursor/grok-4.6-max"}`), tools, "")
	require.NoError(t, err)
	require.Equal(t, "Write", bridge.ClientName("write"))
	require.NotContains(t, toolNames(mcpTools), "Write")
}

func TestNormalizeCursorNativeToolBridgeModeDefaultsToInferAll(t *testing.T) {
	require.Equal(t, CursorNativeToolBridgeModeInferAll, normalizeCursorNativeToolBridgeMode(""))
	require.Equal(t, CursorNativeToolBridgeModeShadow, normalizeCursorNativeToolBridgeMode("shadow"))
	require.Equal(t, CursorNativeToolBridgeModeExplicit, normalizeCursorNativeToolBridgeMode("typo"))
}

func TestResolveCursorNativeToolBridgeAutoCanBeDisabled(t *testing.T) {
	tools := claudeCodeTools()
	body := []byte(`{"cursor_options":{"native_tools_auto":false}}`)

	bridge, mcpTools, err := resolveCursorNativeToolBridge(body, tools, CursorNativeToolBridgeModeInferAll)
	require.NoError(t, err)
	require.Nil(t, bridge)
	require.Equal(t, tools, mcpTools)
}

func TestResolveCursorNativeToolBridgeInferAllFillsWriteOnPartialExplicitMap(t *testing.T) {
	body := []byte(`{"cursor_options":{"native_tools":{"read":"Read","grep":"Grep"}}}`)
	bridge, mcpTools, err := resolveCursorNativeToolBridge(body, claudeCodeTools(), CursorNativeToolBridgeModeInferAll)
	require.NoError(t, err)
	require.Equal(t, "Read", bridge.ClientName("read"))
	require.Equal(t, "Write", bridge.ClientName("write"))
	require.NotContains(t, toolNames(mcpTools), "Write")
}

// 显式映射是客户端的断言：只认它列出的键，不再自动补别的。
func TestResolveCursorNativeToolBridgeExplicitBeatsInference(t *testing.T) {
	body := []byte(`{"cursor_options":{"native_tools":{"read":"Read"}}}`)

	bridge, _, err := resolveCursorNativeToolBridge(body, claudeCodeTools(), CursorNativeToolBridgeModeShadow)
	require.NoError(t, err)
	require.Equal(t, cursor.NativeToolBridge{
		// 显式映射同样享受参数改写，否则 Claude Code 收不到 file_path。
		"read": {Name: "Read", ArgNames: map[string]string{"path": "file_path"}},
	}, bridge)
}

func TestResolveCursorNativeToolBridgeShadowKeepsAllToolsOnMCP(t *testing.T) {
	tools := claudeCodeTools()
	bridge, mcpTools, err := resolveCursorNativeToolBridge(
		[]byte(`{"model":"cursor/grok-4.6"}`), tools, CursorNativeToolBridgeModeShadow)
	require.NoError(t, err)
	require.Nil(t, bridge)
	require.Equal(t, tools, mcpTools)
}

func TestResolveCursorNativeToolBridgeRequestCanExplicitlyEnableAuto(t *testing.T) {
	tools := claudeCodeTools()
	body := []byte(`{"cursor_options":{"native_tools_auto":true}}`)
	bridge, mcpTools, err := resolveCursorNativeToolBridge(
		body, tools, CursorNativeToolBridgeModeShadow)
	require.NoError(t, err)
	require.NotNil(t, bridge)
	require.Less(t, len(mcpTools), len(tools))
}

func TestResolveCursorNativeToolBridgeInferReadOnlyKeepsMutatingToolsOnMCP(t *testing.T) {
	tools := claudeCodeTools()
	bridge, mcpTools, err := resolveCursorNativeToolBridge(
		[]byte(`{"model":"cursor/grok-4.6"}`), tools, CursorNativeToolBridgeModeInferReadOnly)
	require.NoError(t, err)
	require.Contains(t, bridge, "read")
	require.Contains(t, bridge, "grep")
	require.NotContains(t, bridge, "shell")
	require.NotContains(t, bridge, "write")
	require.Contains(t, toolNames(mcpTools), "Bash")
	require.Contains(t, toolNames(mcpTools), "Write")
}

func TestResolveCursorNativeToolBridgeOffDisablesExplicitMapping(t *testing.T) {
	tools := claudeCodeTools()
	body := []byte(`{"cursor_options":{"native_tools":{"read":"Read"}}}`)
	bridge, mcpTools, err := resolveCursorNativeToolBridge(
		body, tools, CursorNativeToolBridgeModeOff)
	require.NoError(t, err)
	require.Nil(t, bridge)
	require.Equal(t, tools, mcpTools)
}

func TestInferNativeToolBridgeRejectsClientRequiredOptionalNativeArg(t *testing.T) {
	tools := []cursor.McpTool{schemaTool("Bash", `{"type":"object","properties":{
		"command":{"type":"string"},"timeout_seconds":{"type":"integer"}
	},"required":["command","timeout_seconds"]}`)}
	require.Nil(t, inferNativeToolBridge(tools))
}

func TestInferNativeToolBridgeRecordsTimeoutUnitTransform(t *testing.T) {
	tools := []cursor.McpTool{schemaTool("Bash", `{"type":"object","properties":{
		"command":{"type":"string"},"timeout_seconds":{"type":"integer"}
	},"required":["command"]}`)}
	target := inferNativeToolBridge(tools)["shell"]
	require.Equal(t, cursor.NativeArgBinding{
		Name:      "timeout_seconds",
		Transform: cursor.NativeArgTransformMillisecondsToSeconds,
	}, target.ArgBindings["timeout"])
}

func TestInferNativeToolBridgeDoesNotClaimReadLints(t *testing.T) {
	tools := []cursor.McpTool{schemaTool("ReadLints", `{"type":"object","properties":{
		"path":{"type":"string"}
	},"required":["path"]}`)}
	require.Nil(t, inferNativeToolBridge(tools))
}

func toolNames(tools []cursor.McpTool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}
