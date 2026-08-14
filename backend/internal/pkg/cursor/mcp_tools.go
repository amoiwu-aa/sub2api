package cursor

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// 客户端工具的声明与回调。
//
// Cursor Agent 自带一套 shell / read / ls 工具，但那套工具的执行发生在「IDE 本地」，
// 网关不可能替租户执行，回 stub 又会让 Agent 卡死（见 agent_client.go 的看门狗）。
// 真正能让 opencode / Claude Code / Codex 这类客户端用起来的通路是 MCP：把客户端
// 自己声明的工具当作 MCP 工具注册进 RunRequest，模型就会通过 ExecServerMessage
// 的 mcp_args 回调它们，网关只需把这次回调翻译成客户端协议里的 tool_calls。
//
// 字段号全部来自对 cursor-agent CLI 的逆向：
//
//	RunRequest.mcp_tools          = 4   McpTools { tools = 1 (repeated) }
//	McpToolDefinition.name        = 1
//	McpToolDefinition.description = 2
//	McpToolDefinition.input_schema= 3   google.protobuf.Value 的序列化字节
//	McpToolDefinition.provider    = 4
//	McpToolDefinition.tool_name   = 5
//	ExecServerMessage.mcp_args    = 11  McpArgs
//	McpArgs.name                  = 1
//	McpArgs.args                  = 2   repeated { 1: key, 2: Value }
//	McpArgs.tool_name             = 5   优先于 name

const (
	mcpToolsField     = 4
	mcpArgsField      = 11
	mcpToolNameField  = 5
	mcpToolLegacyName = 1
	mcpToolArgsField  = 2

	// McpProviderIdentifier 是声明工具时填的 MCP 服务标识。
	// 上游只把它当展示名，但空值会让部分客户端界面显示成未知来源。
	McpProviderIdentifier = "cursor-cli"
)

// McpTool 是一个要声明给 Cursor 的客户端工具。
type McpTool struct {
	Name        string
	Description string
	// InputSchema 是 JSON Schema 原文。为空时按「无参数对象」处理：
	// 上游对缺失 schema 的工具会直接忽略，模型也就永远不会调它。
	InputSchema json.RawMessage
}

const (
	// Codex 0.144 with bundled plugins declares roughly 273 tools. The limit must
	// cover real clients while still bounding protobuf and prompt amplification.
	MaxMcpTools                 = 512
	MaxMcpToolSchemaBytes       = 256 * 1024
	MaxMcpToolSchemasTotalBytes = 4 * 1024 * 1024
)

// ValidateMcpTools rejects declarations that would otherwise be silently
// skipped or encoded as protobuf null. Gateway adapters call it before any
// upstream request, so malformed client tools return a deterministic 400.
func ValidateMcpTools(tools []McpTool) error {
	if len(tools) > MaxMcpTools {
		return fmt.Errorf("too many tools: %d exceeds limit %d", len(tools), MaxMcpTools)
	}
	seen := make(map[string]struct{}, len(tools))
	totalSchemaBytes := 0
	for i, tool := range tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			return fmt.Errorf("tools[%d].name must not be empty", i)
		}
		if len(name) > 128 {
			return fmt.Errorf("tools[%d].name exceeds 128 bytes", i)
		}
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("duplicate tool name %q", name)
		}
		seen[name] = struct{}{}

		schema := bytesTrimSpace(tool.InputSchema)
		if len(schema) == 0 {
			continue
		}
		if len(schema) > MaxMcpToolSchemaBytes {
			return fmt.Errorf("tool %q schema exceeds %d bytes", name, MaxMcpToolSchemaBytes)
		}
		totalSchemaBytes += len(schema)
		if totalSchemaBytes > MaxMcpToolSchemasTotalBytes {
			return fmt.Errorf("tool schemas exceed aggregate limit %d bytes", MaxMcpToolSchemasTotalBytes)
		}
		var root map[string]any
		if err := json.Unmarshal(schema, &root); err != nil {
			return fmt.Errorf("tool %q has invalid JSON schema: %w", name, err)
		}
		if declaredType, ok := root["type"]; ok && declaredType != "object" {
			return fmt.Errorf("tool %q schema root type must be object", name)
		}
		if properties, ok := root["properties"]; ok {
			if _, ok := properties.(map[string]any); !ok {
				return fmt.Errorf("tool %q schema properties must be an object", name)
			}
		}
	}
	return nil
}

func bytesTrimSpace(raw []byte) []byte {
	return []byte(strings.TrimSpace(string(raw)))
}

// EncodeMcpTools 编码 McpTools 的消息体（不含外层 tag）。
//
// 空工具列表必须编成零字节：RunRequest 的 field 4 是观测到的必填字段，
// 而空 McpTools 与「纯文本请求」的占位符字节完全一致，这样不带工具的
// 请求指纹不会因为这次改动而变化。
func EncodeMcpTools(tools []McpTool) []byte {
	if len(tools) == 0 {
		return nil
	}
	out := make([]byte, 0, len(tools)*128)
	for _, tool := range tools {
		if strings.TrimSpace(tool.Name) == "" {
			continue
		}
		out = append(out, EncodeBytesField(1, encodeMcpToolDefinition(tool))...)
	}
	return out
}

func encodeMcpToolDefinition(tool McpTool) []byte {
	schema := tool.InputSchema
	if len(strings.TrimSpace(string(schema))) == 0 {
		schema = json.RawMessage(`{"type":"object","properties":{}}`)
	}
	return concat(
		EncodeStringField(1, tool.Name),
		EncodeStringField(2, tool.Description),
		EncodeBytesField(3, EncodeProtobufValueFromJSON(schema)),
		EncodeStringField(4, McpProviderIdentifier),
		EncodeStringField(5, tool.Name),
	)
}

// McpToolNamespacePrefix 是上游给 MCP 工具加的命名空间。
//
// 声明 name="Bash" 之后，模型侧看到的名字是 "mcp_cursor-cli_Bash"，
// 回调时也可能带着这个前缀。翻译回客户端协议前必须剥掉，否则客户端
// 认不出这个工具名，会直接报错。
var McpToolNamespacePrefix = "mcp_" + McpProviderIdentifier + "_"

// NormalizeToolName 把上游回调里的工具名还原成客户端声明时用的名字。
func NormalizeToolName(name string) string {
	trimmed := strings.TrimSpace(name)
	if stripped, found := strings.CutPrefix(trimmed, McpToolNamespacePrefix); found {
		return stripped
	}
	return trimmed
}

// ToolPolicyPreamble 生成一段约束模型只用客户端工具的前言。
//
// Cursor Agent 自带一整套 Shell / Read / Write / Grep 工具，且模型天然更偏好它们。
// 但那些工具的执行发生在「IDE 本地」，对网关而言无法履行——真让模型用了，这一轮
// 就会卡在一个我们回不出结果的 exec 上。所以每次带工具的请求都要显式把内置工具
// 划掉，把模型引导到 MCP 通道上来。
//
// 没有工具时返回空串：纯对话请求不该被这段约束污染。
func ToolPolicyPreamble(tools []McpTool) string {
	return ToolPolicyPreambleWithNative(tools, nil)
}

// ToolPolicyPreambleWithNative 是 ToolPolicyPreamble 的原生工具桥变体。
//
// nativeBridge 非空时（内置名 → 客户端工具名），对应的内置只读工具被放行：
// 模型直接用它训练时熟悉的 read / grep / ls，网关把 exec 调用翻译给客户端执行
// （见 native_tools.go）。这比逼模型改走 MCP 通道更顺——长上下文里 MCP 调用
// 容易发生格式漂移（把调用写成正文文本），内置工具调用天然走协议帧。
func ToolPolicyPreambleWithNative(tools []McpTool, nativeBridge NativeToolBridge) string {
	return ToolPolicyPreambleWithControl(tools, nativeBridge, false, false)
}

// ToolPolicyPreambleWithControl additionally enforces standard tool controls.
// tool_choice=none still needs a policy even though no client tools are declared,
// otherwise Cursor's own built-ins remain visible and can stall the turn.
func ToolPolicyPreambleWithControl(
	tools []McpTool,
	nativeBridge NativeToolBridge,
	disableAll bool,
	disableParallel bool,
) string {
	names := make([]string, 0, len(tools))
	if !disableAll {
		for _, tool := range tools {
			if name := strings.TrimSpace(tool.Name); name != "" {
				names = append(names, McpToolNamespacePrefix+name)
			}
		}
	}
	nativeAllowed := make([]string, 0, len(nativeBridge))
	if !disableAll {
		for _, key := range NativeToolBridgeKeys() {
			if strings.TrimSpace(nativeBridge.ClientName(key)) != "" {
				nativeAllowed = append(nativeAllowed, key)
			}
		}
	}
	if !disableAll && len(names) == 0 && len(nativeAllowed) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("<tool_policy>\n")
	sb.WriteString("This session runs outside an editor.\n")
	if disableAll {
		sb.WriteString("Tool use is disabled for this request. Every built-in and MCP tool is unavailable. ")
		sb.WriteString("Answer in plain text and do not invoke any tool.\n")
	} else if len(nativeAllowed) > 0 {
		sb.WriteString("You MAY use these built-in tools directly; the host executes them: ")
		sb.WriteString(strings.Join(nativeAllowed, ", "))
		sb.WriteString(".\n")
		// 未桥接的内置工具要显式点名禁用：它们只会得到 stub 的空回执，
		// 不点名的话模型误用时毫无线索。桥接键（read/grep/ls/shell/write/
		// delete/fetch）按配置动态进出禁用名单，其余内置工具恒在。
		unavailable := make([]string, 0, 16)
		for _, key := range NativeToolBridgeKeys() {
			if _, ok := nativeBridge[key]; !ok {
				unavailable = append(unavailable, key)
			}
		}
		unavailable = append(unavailable,
			"AwaitShell", "StrReplace", "EditNotebook", "ReadLints", "Task",
			"TodoWrite", "WebSearch", "GenerateImage", "SwitchMode")
		sb.WriteString("Every other built-in tool (")
		sb.WriteString(strings.Join(unavailable, ", "))
		sb.WriteString(" and any other non-MCP tool) is unavailable ")
		sb.WriteString("here and will fail if you call it.\n")
	} else {
		sb.WriteString("Every built-in tool ")
		sb.WriteString("(Shell, AwaitShell, Read, Write, StrReplace, Delete, Grep, Glob, ")
		sb.WriteString("EditNotebook, ReadLints, Task, TodoWrite, WebSearch, WebFetch, ")
		sb.WriteString("GenerateImage, SwitchMode and any other non-MCP tool) is unavailable ")
		sb.WriteString("here and will fail if you call it.\n")
	}
	if len(names) > 0 {
		if len(nativeAllowed) > 0 {
			sb.WriteString("The only other tools you may call are:\n")
		} else {
			sb.WriteString("The only tools you may call are:\n")
		}
		for _, name := range names {
			sb.WriteString("  - ")
			sb.WriteString(name)
			sb.WriteString("\n")
		}
	}
	if disableParallel && !disableAll {
		sb.WriteString("Call at most one tool in this turn; parallel tool calls are disabled by the client.\n")
	}
	if !disableAll {
		sb.WriteString("Use them for every action you need to take. ")
		sb.WriteString("If none of them fits, answer in plain text instead of reaching for an unavailable tool.\n")
	}
	// 长上下文里模型可能把调用写成正文标记（<tool_call>/<invoke> 之类的伪 XML），
	// 那样的调用没人执行，客户端只会看到一坨原始文本。显式点破这一条。
	sb.WriteString("Invoke tools only through the tool-calling channel. Never write tool-call ")
	sb.WriteString("markup such as <tool_call> or <invoke> blocks as plain text in your reply; ")
	sb.WriteString("text like that is not executed and will be shown to the user as-is.\n")
	sb.WriteString("</tool_policy>")
	return sb.String()
}

// McpToolCall 是模型发起的一次客户端工具调用。
type McpToolCall struct {
	// Name 是被调用的工具名，对应客户端 tools 里声明的那个。
	Name string
	// Arguments 是入参的 JSON 序列化结果，可直接塞进 OpenAI 的
	// tool_calls[].function.arguments 或 Anthropic 的 tool_use.input。
	Arguments json.RawMessage
	// CallID 是上游给这次调用的标识。缺失时由调用方补一个。
	CallID string
}

// parseMcpArgs 解析 ExecServerMessage.mcp_args。
//
// tool_name（5）优先于 name（1）：上游两个字段都可能出现，
// 但只有 tool_name 是客户端声明时用的那个名字。
func parseMcpArgs(data []byte) (*McpToolCall, error) {
	fields, err := ReadFields(data)
	if err != nil {
		return nil, err
	}

	call := &McpToolCall{}
	legacyName := ""
	args := make(map[string]any)

	for _, field := range fields {
		switch {
		case field.Number == mcpToolNameField && field.WireType == wireBytes:
			call.Name = string(field.Bytes)
		case field.Number == mcpToolLegacyName && field.WireType == wireBytes:
			legacyName = string(field.Bytes)
		case field.Number == mcpToolArgsField && field.WireType == wireBytes:
			key, value, err := decodeProtobufMapEntry(field.Bytes, 0)
			if err != nil {
				// 单个入参解不出来不该让整次调用作废：留空比整轮失败好，
				// 客户端至少能看到工具名并回一个「参数缺失」的结果。
				continue
			}
			if key != "" {
				args[key] = value
			}
		}
	}
	if call.Name == "" {
		call.Name = legacyName
	}
	if call.Name == "" {
		return nil, nil
	}
	call.Name = NormalizeToolName(call.Name)

	encoded, err := marshalToolArguments(args)
	if err != nil {
		return nil, err
	}
	call.Arguments = encoded
	return call, nil
}

// marshalToolArguments 把入参序列化成稳定的 JSON。
//
// encoding/json 对 map 已经按键排序，这里显式走一遍是为了对空值给出 "{}"
// 而不是 "null"——客户端普遍会对 arguments 直接做 JSON.parse，null 会炸。
func marshalToolArguments(args map[string]any) (json.RawMessage, error) {
	if len(args) == 0 {
		return json.RawMessage("{}"), nil
	}
	keys := make([]string, 0, len(args))
	for key := range args {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	encoded, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}
