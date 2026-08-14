package cursor

import (
	"encoding/json"
	"fmt"
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
	return []string{"delete", "diagnostics", "fetch", "glob", "grep", "ls", "read", "shell", "write"}
}

// IsNativeToolBridgeKey 报告 key 是否是可桥接的内置工具名。
func IsNativeToolBridgeKey(key string) bool {
	switch key {
	case "delete", "diagnostics", "fetch", "glob", "grep", "ls", "read", "shell", "write":
		return true
	default:
		return false
	}
}

// resolveNativeBridgeKey 决定一次 exec 该按哪个桥接键翻译。
//
// glob 在 wire 上不是独立工具：模型侧叫 Glob，实际复用 grep 的字段号（5），
// 不带 pattern，只带 glob 与 output_mode=files_with_matches。实测帧：
//
//	#5 { #3 str "**/*.py"  #4 str "files_with_matches"  #14 str <tool_call_id> }
//
// 照 grep 翻译会给客户端发一个空 pattern，而各家 Grep 工具普遍要求 pattern
// 非空，调用当场失败。所以这种形状要改投客户端的 Glob 工具。
func resolveNativeBridgeKey(exec *ExecRequest) string {
	canonical, ok := nativeBridgeableKinds[exec.Kind]
	if !ok {
		return ""
	}
	if canonical == "grep" && isGlobShapedGrep(exec.ArgBytes) {
		return "glob"
	}
	return canonical
}

func isGlobShapedGrep(argBytes []byte) bool {
	fields, err := ReadFields(argBytes)
	if err != nil {
		return false
	}
	return protoStringField(fields, 1) == "" && protoStringField(fields, 3) != ""
}

// NativeToolTarget 是一个内置工具桥接到的客户端工具。
type NativeToolTarget struct {
	// Name 是客户端声明的工具名，翻译出来的调用以它为准。
	Name string
	// ArgNames 把规范入参名改写成客户端 schema 里的属性名。
	//
	// 没有条目的入参按规范名原样写出，所以按本包契约实现的客户端零适配。
	// 需要它是因为同一个语义在各家客户端里叫法不同：Claude Code 的 Read
	// 收 file_path 而不是 path，原样发过去会被客户端当成缺参数直接报错。
	ArgNames map[string]string
	// ArgBindings 是 schema 推断后的精确参数白名单。非 nil 时只有列在这里的
	// 规范参数才会发给客户端；这避免未绑定的可选参数按规范名意外漏出。
	//
	// nil 表示客户端显式断言自己接受规范契约（兼容 cursor_options.native_tools）。
	ArgBindings map[string]NativeArgBinding
}

// NativeToolBridge 是一次请求的「内置工具名 → 客户端工具」映射。
type NativeToolBridge map[string]NativeToolTarget

// ClientName 返回某个内置工具桥接到的客户端工具名，未桥接时返回空串。
func (b NativeToolBridge) ClientName(key string) string {
	return b[key].Name
}

// NativeArgTransform 是规范参数写入客户端 schema 前的值转换。
type NativeArgTransform string

const (
	NativeArgTransformIdentity              NativeArgTransform = ""
	NativeArgTransformMillisecondsToSeconds NativeArgTransform = "milliseconds_to_seconds"
)

// NativeArgBinding 描述一个规范参数如何落到客户端工具属性。
type NativeArgBinding struct {
	Name      string
	Transform NativeArgTransform
}

// JSON Schema 的类型名，用于 NativeToolArg.Type。
const (
	NativeArgString  = "string"
	NativeArgInteger = "integer"
	NativeArgBoolean = "boolean"
)

// NativeToolArg 是网关翻译一次内置工具调用时会发给客户端的一个入参。
type NativeToolArg struct {
	// Name 是规范入参名，也是没有重命名时实际写出的属性名。
	Name string `json:"name"`
	// Type 是这个入参的 JSON Schema 类型。
	Type string `json:"type"`
	// Required 的入参恒被写出（含零值），可选入参只在非零时出现。
	Required bool `json:"required"`
}

// nativeToolArgSpecs 是每个桥接键翻译后可能出现的入参。
//
// 它是对外契约的一部分：调用方拿它与客户端声明的 JSON Schema 做绑定和
// 兼容校验，决定某个内置工具能不能安全地桥到某个客户端工具上。
var nativeToolArgSpecs = map[string][]NativeToolArg{
	"read": {
		{Name: "path", Type: NativeArgString, Required: true},
		{Name: "offset", Type: NativeArgInteger},
		{Name: "limit", Type: NativeArgInteger},
	},
	"grep": {
		{Name: "pattern", Type: NativeArgString, Required: true},
		{Name: "path", Type: NativeArgString},
		{Name: "glob", Type: NativeArgString},
		{Name: "output_mode", Type: NativeArgString},
		{Name: "case_insensitive", Type: NativeArgBoolean},
		{Name: "head_limit", Type: NativeArgInteger},
	},
	// glob 的 pattern 是文件名匹配式（wire 上 grep 的 #3），不是正则。
	// 客户端的 Glob 工具（AutoClaw / Claude Code）都把它叫 pattern。
	"glob": {
		{Name: "pattern", Type: NativeArgString, Required: true},
		{Name: "path", Type: NativeArgString},
	},
	"ls": {
		{Name: "path", Type: NativeArgString, Required: true},
	},
	"shell": {
		{Name: "command", Type: NativeArgString, Required: true},
		{Name: "cwd", Type: NativeArgString},
		{Name: "timeout", Type: NativeArgInteger},
		{Name: "run_in_background", Type: NativeArgBoolean},
		{Name: "description", Type: NativeArgString},
	},
	"write": {
		{Name: "path", Type: NativeArgString, Required: true},
		{Name: "content", Type: NativeArgString, Required: true},
	},
	"delete": {
		{Name: "path", Type: NativeArgString, Required: true},
	},
	"fetch": {
		{Name: "url", Type: NativeArgString, Required: true},
	},
	"diagnostics": {
		{Name: "path", Type: NativeArgString, Required: true},
	},
}

// NativeToolArgSpec 返回某个桥接键翻译后会出现的入参，未知键返回 nil。
// 返回的切片是共享的，调用方不得修改。
func NativeToolArgSpec(key string) []NativeToolArg {
	return nativeToolArgSpecs[key]
}

// TranslateNativeExec 把一次内置工具 exec 翻译成客户端工具调用。
//
// 返回 nil 表示不归桥接管（未配置映射、工具不在白名单、或入参解析失败），
// 调用方应回落到 stub 回执——宁可让模型收到「工具不可用」，也不能让流挂死。
func TranslateNativeExec(bridge NativeToolBridge, exec *ExecRequest) *McpToolCall {
	// ArgBytes 为空不拦：proto3 对全默认值的消息就是零字节（如列默认目录的 ls），
	// 解析空字节得到全空参数，转发给客户端好过回 stub。
	if len(bridge) == 0 || exec == nil {
		return nil
	}
	key := resolveNativeBridgeKey(exec)
	if key == "" {
		return nil
	}
	target, ok := bridge[key]
	if !ok || strings.TrimSpace(target.Name) == "" {
		return nil
	}
	// 解析按原始 kind 走：同一个映射键下的变体（如 background_shell_spawn
	// 与 shell）字段布局不同。
	values, callID, err := parseNativeExecArgs(exec.Kind, key, exec.ArgBytes)
	if err != nil {
		return nil
	}
	arguments, err := encodeNativeArgs(key, values, target)
	if err != nil {
		return nil
	}
	if callID == "" {
		callID = exec.ExecID
	}
	callID = normalizeNativeCallID(callID)
	return &McpToolCall{
		Name:      target.Name,
		Arguments: arguments,
		CallID:    callID,
	}
}

// encodeNativeArgs 按规范参数表写出入参：必填项恒出现（含零值），可选项
// 只在非零时出现。推断映射只输出 ArgBindings 白名单中的属性；显式映射
// 没有白名单时按规范名（或 ArgNames）输出，保持原有客户端契约。
func encodeNativeArgs(key string, values map[string]any, target NativeToolTarget) (json.RawMessage, error) {
	payload := make(map[string]any, len(values))
	for _, arg := range nativeToolArgSpecs[key] {
		value, ok := values[arg.Name]
		if !ok {
			continue
		}
		if !arg.Required && isZeroNativeArg(value) {
			continue
		}

		name := arg.Name
		transform := NativeArgTransformIdentity
		if target.ArgBindings != nil {
			binding, bound := target.ArgBindings[arg.Name]
			if !bound {
				continue
			}
			if strings.TrimSpace(binding.Name) != "" {
				name = strings.TrimSpace(binding.Name)
			}
			transform = binding.Transform
		} else if renamed := strings.TrimSpace(target.ArgNames[name]); renamed != "" {
			name = renamed
		}

		value, err := transformNativeArgValue(value, transform)
		if err != nil {
			return nil, err
		}
		payload[name] = value
	}
	// map 的键由 encoding/json 排序，同一次调用的入参始终编码成同一串字节。
	return json.Marshal(payload)
}

func transformNativeArgValue(value any, transform NativeArgTransform) (any, error) {
	switch transform {
	case NativeArgTransformIdentity:
		return value, nil
	case NativeArgTransformMillisecondsToSeconds:
		milliseconds, ok := value.(int64)
		if !ok {
			return nil, fmt.Errorf("native arg milliseconds_to_seconds expects int64, got %T", value)
		}
		if milliseconds <= 0 {
			return milliseconds, nil
		}
		return (milliseconds + 999) / 1000, nil
	default:
		return nil, fmt.Errorf("unsupported native arg transform %q", transform)
	}
}

// normalizeNativeCallID 取上游复合标识的第一段。Cursor 的 shell/grep 实测会
// 返回 "call-...\nfc_..."，原样塞进 OpenAI/Anthropic tool id 会产生非法换行。
func normalizeNativeCallID(raw string) string {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func isZeroNativeArg(value any) bool {
	switch typed := value.(type) {
	case string:
		return typed == ""
	case int64:
		return typed == 0
	case bool:
		return !typed
	default:
		return value == nil
	}
}

// parseNativeExecArgs 把一次 exec 的参数解成「规范入参名 → 值」。
//
// 这里只负责从 wire 取值，不决定哪些值最终会写出去——那由
// nativeToolArgSpecs 的必填/可选规则和 encodeNativeArgs 处理。
func parseNativeExecArgs(kind, bridgeKey string, argBytes []byte) (map[string]any, string, error) {
	fields, err := ReadFields(argBytes)
	if err != nil {
		return nil, "", err
	}

	values := make(map[string]any, 6)
	callID := ""
	// glob 复用 grep 的帧，但语义不同：客户端要的 pattern 是 wire 上的
	// glob（#3），不是 grep 的正则（#1，这种形状下恒为空）。
	if bridgeKey == "glob" {
		values["pattern"] = protoStringField(fields, 3)
		values["path"] = protoStringField(fields, 2)
		return values, protoStringField(fields, 14), nil
	}
	switch kind {
	case "read", "redacted_read":
		values["path"] = protoStringField(fields, 1)
		values["offset"] = int64(protoVarintField(fields, 4))
		values["limit"] = int64(protoVarintField(fields, 5))
		callID = protoStringField(fields, 2)
	case "grep":
		values["pattern"] = protoStringField(fields, 1)
		values["path"] = protoStringField(fields, 2)
		values["glob"] = protoStringField(fields, 3)
		values["output_mode"] = protoStringField(fields, 4)
		values["case_insensitive"] = protoVarintField(fields, 8) != 0
		values["head_limit"] = int64(protoVarintField(fields, 10))
		callID = protoStringField(fields, 14)
	case "ls":
		values["path"] = protoStringField(fields, 1)
		callID = protoStringField(fields, 3)
	case "shell", "shell_stream":
		values["command"] = protoStringField(fields, 1)
		values["cwd"] = protoStringField(fields, 2)
		values["timeout"] = int64(protoVarintField(fields, 3))
		values["run_in_background"] = protoVarintField(fields, 11) != 0
		values["description"] = protoStringField(fields, 15)
		callID = protoStringField(fields, 4)
	case "background_shell_spawn":
		// 字段布局与 shell 不同（tool_call_id 在 3），语义是强制后台。
		values["command"] = protoStringField(fields, 1)
		values["cwd"] = protoStringField(fields, 2)
		values["run_in_background"] = true
		callID = protoStringField(fields, 3)
	case "write":
		values["path"] = protoStringField(fields, 1)
		values["content"] = protoStringField(fields, 2)
		callID = protoStringField(fields, 3)
	case "delete":
		values["path"] = protoStringField(fields, 1)
		callID = protoStringField(fields, 2)
	case "fetch":
		values["url"] = protoStringField(fields, 1)
		callID = protoStringField(fields, 2)
	case "diagnostics":
		values["path"] = protoStringField(fields, 1)
		callID = protoStringField(fields, 2)
	default:
		return nil, "", nil
	}

	return values, callID, nil
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
