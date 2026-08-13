package cursor

import (
	"encoding/json"
	"strings"
)

// 原生工具桥：把模型对 Cursor 内置工具的 exec 调用翻译成客户端协议的
// tool_calls，交回客户端执行。
//
// 动机：模型是照着「内置工具存在」训练的。MCP 通道是逆着这个习惯改道，
// 长上下文里模型会把调用写成正文文本（格式漂移），而内置工具调用天然走
// protobuf exec 帧，不会混进正文。让模型用它熟悉的工具，网关只做翻译。
//
// 安全语义：网关只做翻译，不执行任何东西。shell / write / delete 这类写
// 操作最终由客户端声明的工具执行，走客户端自己的审批/沙箱流程——与同一个
// 工具经 MCP 通道被调用时的风险等级完全相同。映射是请求级显式配置，
// 不配置的工具一律回 stub。
//
// 入参字段号取自反代 parseExecServerMessage / parseExtArgs
//（cursor-agent-exec.js / cursor-agent-exec-tools.js）：
//
//	read / redacted_read: path=1, tool_call_id=2, offset=4, limit=5
//	grep:  pattern=1, path=2, glob=3, output_mode=4, case_insensitive=8,
//	       head_limit=10, tool_call_id=14
//	ls:    path=1, tool_call_id=3
//	shell / shell_stream: command=1, working_directory=2, timeout=3,
//	       tool_call_id=4, is_background=11, description=15
//	background_shell_spawn: command=1, working_directory=2, tool_call_id=3
//	write: path=1, file_text=2, tool_call_id=3
//	delete: path=1, tool_call_id=2
//	fetch: url=1, tool_call_id=2
//	diagnostics: path=1, tool_call_id=2

// nativeBridgeableKinds 把 exec 帧里的工具名归一成桥接键。
// redacted_read 是敏感路径读取变体，入参与 read 相同；shell_stream 与
// background_shell_spawn 都是 shell 的变体（后者入参字段不同、语义是
// 强制后台）——都归并到同一个映射键。
var nativeBridgeableKinds = map[string]string{
	"read":                   "read",
	"redacted_read":          "read",
	"grep":                   "grep",
	"ls":                     "ls",
	"shell":                  "shell",
	"shell_stream":           "shell",
	"background_shell_spawn": "shell",
	"write":                  "write",
	"delete":                 "delete",
	"fetch":                  "fetch",
	"diagnostics":            "diagnostics",
}

// NativeToolBridgeKeys 返回 native_tools 映射里允许出现的键（升序）。
func NativeToolBridgeKeys() []string {
	return []string{"delete", "diagnostics", "fetch", "grep", "ls", "read", "shell", "write"}
}

// IsNativeToolBridgeKey 报告 key 是否是可桥接的内置工具名。
func IsNativeToolBridgeKey(key string) bool {
	switch key {
	case "delete", "diagnostics", "fetch", "grep", "ls", "read", "shell", "write":
		return true
	default:
		return false
	}
}

// TranslateNativeExec 把一次内置工具 exec 翻译成客户端工具调用。
//
// 返回 nil 表示不归桥接管（未配置映射、工具不在白名单、或入参解析失败），
// 调用方应回落到 stub 回执——宁可让模型收到「工具不可用」，也不能让流挂死。
func TranslateNativeExec(bridge map[string]string, exec *ExecRequest) *McpToolCall {
	// ArgBytes 为空不拦：proto3 对全默认值的消息就是零字节（如列默认目录的 ls），
	// 解析空字节得到全空参数，转发给客户端好过回 stub。
	if len(bridge) == 0 || exec == nil {
		return nil
	}
	canonical, ok := nativeBridgeableKinds[exec.Kind]
	if !ok {
		return nil
	}
	clientName, ok := bridge[canonical]
	if !ok || strings.TrimSpace(clientName) == "" {
		return nil
	}
	// 解析按原始 kind 走：同一个映射键下的变体（如 background_shell_spawn
	// 与 shell）字段布局不同。
	arguments, callID, err := parseNativeExecArgs(exec.Kind, exec.ArgBytes)
	if err != nil {
		return nil
	}
	if callID == "" {
		callID = exec.ExecID
	}
	return &McpToolCall{
		Name:      clientName,
		Arguments: arguments,
		CallID:    callID,
	}
}

// nativeReadArgs / nativeGrepArgs / nativeLsArgs 是发给客户端的入参形态。
// 字段名是对外契约的一部分（客户端工具的 schema 要认它们），见
// docs/CURSOR_TOOL_CALLING.md。
type nativeReadArgs struct {
	Path   string `json:"path"`
	Offset int64  `json:"offset,omitempty"`
	Limit  int64  `json:"limit,omitempty"`
}

type nativeGrepArgs struct {
	Pattern         string `json:"pattern"`
	Path            string `json:"path,omitempty"`
	Glob            string `json:"glob,omitempty"`
	OutputMode      string `json:"output_mode,omitempty"`
	CaseInsensitive bool   `json:"case_insensitive,omitempty"`
	HeadLimit       int64  `json:"head_limit,omitempty"`
}

type nativeLsArgs struct {
	Path string `json:"path"`
}

// nativeShellArgs 的字段名对齐常见编码客户端（Claude Code / AutoClaw 的
// Bash 工具）：cwd / run_in_background 比 wire 的 workingDirectory /
// isBackground 更容易零适配。timeout 单位以上游为准（未实证，按毫秒对待）。
type nativeShellArgs struct {
	Command         string `json:"command"`
	Cwd             string `json:"cwd,omitempty"`
	Timeout         int64  `json:"timeout,omitempty"`
	RunInBackground bool   `json:"run_in_background,omitempty"`
	Description     string `json:"description,omitempty"`
}

type nativeWriteArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type nativeDeleteArgs struct {
	Path string `json:"path"`
}

type nativeFetchArgs struct {
	URL string `json:"url"`
}

type nativeDiagnosticsArgs struct {
	Path string `json:"path"`
}

func parseNativeExecArgs(kind string, argBytes []byte) (json.RawMessage, string, error) {
	fields, err := ReadFields(argBytes)
	if err != nil {
		return nil, "", err
	}

	var payload any
	callID := ""
	switch kind {
	case "read", "redacted_read":
		payload = nativeReadArgs{
			Path:   protoStringField(fields, 1),
			Offset: int64(protoVarintField(fields, 4)),
			Limit:  int64(protoVarintField(fields, 5)),
		}
		callID = protoStringField(fields, 2)
	case "grep":
		payload = nativeGrepArgs{
			Pattern:         protoStringField(fields, 1),
			Path:            protoStringField(fields, 2),
			Glob:            protoStringField(fields, 3),
			OutputMode:      protoStringField(fields, 4),
			CaseInsensitive: protoVarintField(fields, 8) != 0,
			HeadLimit:       int64(protoVarintField(fields, 10)),
		}
		callID = protoStringField(fields, 14)
	case "ls":
		payload = nativeLsArgs{Path: protoStringField(fields, 1)}
		callID = protoStringField(fields, 3)
	case "shell", "shell_stream":
		payload = nativeShellArgs{
			Command:         protoStringField(fields, 1),
			Cwd:             protoStringField(fields, 2),
			Timeout:         int64(protoVarintField(fields, 3)),
			RunInBackground: protoVarintField(fields, 11) != 0,
			Description:     protoStringField(fields, 15),
		}
		callID = protoStringField(fields, 4)
	case "background_shell_spawn":
		// 字段布局与 shell 不同（tool_call_id 在 3），语义是强制后台。
		payload = nativeShellArgs{
			Command:         protoStringField(fields, 1),
			Cwd:             protoStringField(fields, 2),
			RunInBackground: true,
		}
		callID = protoStringField(fields, 3)
	case "write":
		payload = nativeWriteArgs{
			Path:    protoStringField(fields, 1),
			Content: protoStringField(fields, 2),
		}
		callID = protoStringField(fields, 3)
	case "delete":
		payload = nativeDeleteArgs{Path: protoStringField(fields, 1)}
		callID = protoStringField(fields, 2)
	case "fetch":
		payload = nativeFetchArgs{URL: protoStringField(fields, 1)}
		callID = protoStringField(fields, 2)
	case "diagnostics":
		payload = nativeDiagnosticsArgs{Path: protoStringField(fields, 1)}
		callID = protoStringField(fields, 2)
	default:
		return nil, "", nil
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, "", err
	}
	return encoded, callID, nil
}

func protoStringField(fields []Field, number int) string {
	for _, field := range fields {
		if field.Number == number && field.WireType == wireBytes {
			return string(field.Bytes)
		}
	}
	return ""
}

func protoVarintField(fields []Field, number int) uint64 {
	for _, field := range fields {
		if field.Number == number && field.WireType == wireVarint {
			return field.Varint
		}
	}
	return 0
}
