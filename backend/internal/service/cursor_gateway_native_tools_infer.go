package service

import (
	"encoding/json"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
)

// 原生工具桥的自动推断。
//
// cursor_options.native_tools 是 RingStar 的私有扩展，只有改造过的客户端会发。
// Codex、Claude Code 这类第三方客户端不知道它，不推断的话原生桥对它们完全
// 不生效——模型继续被 <tool_policy> 赶去走 MCP 通道，长上下文里照样把调用
// 写成正文里的伪 XML。
//
// 推断只认客户端自己声明的东西：先按名字找候选工具，再拿它的 JSON Schema
// 逐个绑定我们会发出去的入参。绑不上就不映射，回落 MCP + stub——那是原来
// 就有的行为，不会更糟。
//
// 光按名字匹配不够，因为同一个语义各家叫法不同：Claude Code 的 Read 收
// file_path 而不是 path。所以绑定的产物除了工具名，还有一张「规范入参名 →
// 客户端属性名」的改写表，翻译时按它写出入参。

// nativeToolNameAliases 是按名字寻找候选客户端工具的顺序表（小写比较）。
// 靠前的先试；同一个客户端工具不会被两个内置工具同时占用。
var nativeToolNameAliases = map[string][]string{
	"read":   {"read", "read_file", "readfile", "viewfile", "view_file", "open_file"},
	"glob":   {"glob", "globtool", "glob_file_search", "file_search", "find_files", "findfiles"},
	"grep":   {"grep", "search", "ripgrep", "rg", "grep_search", "search_files", "searchtext"},
	"ls":     {"listdir", "ls", "list_dir", "list_directory", "list_files", "dir"},
	"shell":  {"bash", "shell", "run_terminal_cmd", "terminal", "execute_command", "run_command", "exec", "powershell"},
	"write":  {"write", "write_file", "writefile", "create_file", "save_file"},
	"delete": {"delete", "delete_file", "deletefile", "remove_file", "rm"},
	"fetch":  {"webfetch", "web_fetch", "fetch", "fetch_url", "url_fetch", "read_url"},
	// Cursor 的 ReadLints 由服务端自己消化，实测不会下发 diagnostics exec。
	// 把它摘出 MCP 只会让客户端自己的 ReadLints 失联。
	"diagnostics": {"lspdiagnostics", "diagnostics", "get_diagnostics"},
}

// nativeToolArgAliases 是把规范入参绑到客户端 schema 属性上的候选顺序表。
// 规范名本身总是最先尝试，这里只列它之外的别名。
var nativeToolArgAliases = map[string][]string{
	"path":              {"file_path", "filepath", "absolute_path", "target_file", "filename", "file", "dir_path", "directory_path"},
	"content":           {"contents", "file_text", "text", "new_content", "body", "data"},
	"command":           {"cmd", "script", "shell_command", "command_line"},
	"url":               {"uri", "link", "target_url", "address"},
	"pattern":           {"query", "regex", "search_term", "search_pattern"},
	"glob":              {"include", "include_pattern", "file_pattern", "filepattern", "filter"},
	"output_mode":       {"outputmode", "mode"},
	"case_insensitive":  {"caseinsensitive", "ignore_case", "ignorecase", "-i"},
	"head_limit":        {"headlimit", "max_results", "maxresults", "limit", "top_k"},
	"offset":            {"start_line", "startline", "start", "from_line", "skip"},
	"limit":             {"max_lines", "maxlines", "line_count", "count", "num_lines"},
	"timeout":           {"timeout_ms", "timeoutms", "timeout_seconds", "timeoutseconds"},
	"run_in_background": {"runinbackground", "background", "is_background", "detach"},
	"cwd":               {"working_directory", "workingdirectory", "workdir", "working_dir", "directory"},
	"description":       {"desc", "explanation", "reason", "purpose"},
}

// inferNativeToolBridge 从客户端声明的工具里推断出原生工具桥映射。
func inferNativeToolBridge(clientTools []cursor.McpTool) cursor.NativeToolBridge {
	if len(clientTools) == 0 {
		return nil
	}
	// 同名工具以先声明的为准，与 MCP 注册的取舍保持一致。
	declared := make(map[string]cursor.McpTool, len(clientTools))
	for _, tool := range clientTools {
		key := strings.ToLower(strings.TrimSpace(tool.Name))
		if key == "" {
			continue
		}
		if _, exists := declared[key]; !exists {
			declared[key] = tool
		}
	}

	bridge := make(cursor.NativeToolBridge, len(nativeToolNameAliases))
	// 一个客户端工具只能被一个内置工具占用：否则模型对着两个内置入口调同
	// 一个工具，重放与统计都会看到自相矛盾的历史。
	taken := make(map[string]struct{}, len(nativeToolNameAliases))
	for _, key := range cursor.NativeToolBridgeKeys() {
		for _, alias := range nativeToolNameAliases[key] {
			tool, ok := declared[alias]
			if !ok {
				continue
			}
			if _, used := taken[tool.Name]; used {
				continue
			}
			target, bound := bindNativeToolTarget(key, tool)
			if !bound {
				continue
			}
			bridge[key] = target
			taken[tool.Name] = struct{}{}
			break
		}
	}
	if len(bridge) == 0 {
		return nil
	}
	return bridge
}

// explicitNativeToolTarget 为客户端显式指定的映射构造桥接目标。
//
// 显式映射是客户端自己的断言，绑定失败也照样映射，只是不做参数改写——
// 按本包契约实现的客户端本来就用规范名，无需改写。
func explicitNativeToolTarget(key string, tool cursor.McpTool) cursor.NativeToolTarget {
	if target, ok := bindNativeToolTarget(key, tool); ok {
		// 显式映射是客户端对规范契约的断言：保留参数重命名，但不套推断的
		// 严格白名单，否则客户端 schema 未公开的兼容别名会被意外丢掉。
		target.ArgBindings = nil
		return target
	}
	return cursor.NativeToolTarget{Name: tool.Name}
}

// bindNativeToolTarget 校验某个内置工具能否安全桥到这个客户端工具上，
// 并给出规范入参到客户端属性名的改写表。
//
// 两个方向都要成立才算通过：
//   - 我们必发的入参，客户端 schema 里要有类型兼容的属性可落；
//   - 客户端 schema 声明的必填属性，都要能被我们发出的入参覆盖。
//
// 第二条拦的是 Claude Code 的 WebFetch 这类工具：它必填一个我们永远不会
// 发的 prompt，映过去每次调用都会被客户端判成缺参数。
func bindNativeToolTarget(key string, tool cursor.McpTool) (cursor.NativeToolTarget, bool) {
	spec := cursor.NativeToolArgSpec(key)
	if len(spec) == 0 {
		return cursor.NativeToolTarget{}, false
	}
	properties, required, ok := parseToolInputSchema(tool.InputSchema)
	if !ok {
		// schema 缺失或没有声明任何属性时无从校验。此时映射是在赌客户端
		// 认得我们的规范名，赌输就是每次调用都失败，不如回落 MCP。
		return cursor.NativeToolTarget{}, false
	}

	argNames := make(map[string]string, len(spec))
	argBindings := make(map[string]cursor.NativeArgBinding, len(spec))
	bound := make(map[string]struct{}, len(spec))
	requiredSet := make(map[string]struct{}, len(required))
	for _, name := range required {
		requiredSet[name] = struct{}{}
	}
	for _, arg := range spec {
		property, found := matchSchemaProperty(arg, properties, bound)
		if !found {
			if arg.Required {
				return cursor.NativeToolTarget{}, false
			}
			continue
		}
		if _, clientRequires := requiredSet[property]; clientRequires && !arg.Required {
			// 客户端每次都要求这个属性，但原生帧可能省略它。映射后会随机
			// 产生缺参数调用，宁可整项回落 MCP。
			return cursor.NativeToolTarget{}, false
		}
		bound[property] = struct{}{}
		if property != arg.Name {
			argNames[arg.Name] = property
		}
		argBindings[arg.Name] = cursor.NativeArgBinding{
			Name:      property,
			Transform: nativeArgTransform(arg.Name, property),
		}
	}
	for _, name := range required {
		if _, ok := bound[name]; !ok {
			return cursor.NativeToolTarget{}, false
		}
	}

	target := cursor.NativeToolTarget{
		Name:        tool.Name,
		ArgBindings: argBindings,
	}
	if len(argNames) > 0 {
		target.ArgNames = argNames
	}
	return target, true
}

func nativeArgTransform(argName, propertyName string) cursor.NativeArgTransform {
	if argName == "timeout" {
		switch strings.ToLower(strings.TrimSpace(propertyName)) {
		case "timeout_seconds", "timeoutseconds":
			return cursor.NativeArgTransformMillisecondsToSeconds
		}
	}
	return cursor.NativeArgTransformIdentity
}

// matchSchemaProperty 为一个规范入参找到类型兼容且尚未被占用的 schema 属性。
func matchSchemaProperty(
	arg cursor.NativeToolArg,
	properties map[string][]string,
	bound map[string]struct{},
) (string, bool) {
	candidates := make([]string, 0, 1+len(nativeToolArgAliases[arg.Name]))
	candidates = append(candidates, arg.Name)
	candidates = append(candidates, nativeToolArgAliases[arg.Name]...)

	for _, candidate := range candidates {
		for name, types := range properties {
			if !strings.EqualFold(name, candidate) {
				continue
			}
			if _, used := bound[name]; used {
				continue
			}
			if !nativeArgTypeCompatible(arg.Type, types) {
				continue
			}
			return name, true
		}
	}
	return "", false
}

// nativeArgTypeCompatible 报告客户端属性的声明类型能否接住我们发的值。
//
// 没声明类型时按兼容处理：anyOf / oneOf 这类写法读不出 type，一律拒绝会把
// 大量正常客户端挡在外面。显式冲突才拒——Codex 的 shell 收
// command: string[]，我们发的是字符串，映过去必炸。
func nativeArgTypeCompatible(want string, declared []string) bool {
	if len(declared) == 0 {
		return true
	}
	for _, got := range declared {
		switch want {
		case cursor.NativeArgString:
			if got == "string" {
				return true
			}
		case cursor.NativeArgInteger:
			if got == "integer" || got == "number" {
				return true
			}
		case cursor.NativeArgBoolean:
			if got == "boolean" {
				return true
			}
		}
	}
	return false
}

// parseToolInputSchema 读出对象 schema 的属性类型与必填列表。
// ok 为 false 表示这份 schema 没法用来做兼容校验。
func parseToolInputSchema(raw json.RawMessage) (properties map[string][]string, required []string, ok bool) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, nil, false
	}
	var schema struct {
		Properties map[string]struct {
			Type json.RawMessage `json:"type"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, nil, false
	}
	if len(schema.Properties) == 0 {
		return nil, nil, false
	}
	properties = make(map[string][]string, len(schema.Properties))
	for name, property := range schema.Properties {
		properties[name] = parseSchemaTypes(property.Type)
	}
	return properties, schema.Required, true
}

// parseSchemaTypes 归一 JSON Schema 的 type：既可能是 "string"，
// 也可能是 ["string","null"] 这样的联合。
func parseSchemaTypes(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		if single == "" {
			return nil
		}
		return []string{single}
	}
	var multiple []string
	if err := json.Unmarshal(raw, &multiple); err == nil {
		return multiple
	}
	return nil
}
