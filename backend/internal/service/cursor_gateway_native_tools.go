package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
)

// 原生工具桥的请求级配置。
//
// 客户端通过 cursor_options.native_tools 声明「内置工具名 → 客户端工具名」的
// 映射，例如 {"read":"Read","grep":"Grep","ls":"LS"}。命中的客户端工具不再注册
// 为 MCP 工具；模型直接调它训练时熟悉的内置 read / grep / ls，网关把 exec 帧
// 翻译成标准 tool_calls 交回客户端执行。参数契约见 docs/CURSOR_TOOL_CALLING.md。

type cursorNativeToolsEnvelope struct {
	CursorOptions *struct {
		NativeTools map[string]string `json:"native_tools,omitempty"`
	} `json:"cursor_options,omitempty"`
}

// resolveCursorNativeToolBridge 解析并校验 native_tools 映射，同时把客户端
// 工具列表切分成「仍走 MCP 注册的」与「由原生桥接管的」。
//
// 校验失败返回错误（应回 400）：静默忽略会让模型继续用错误的通道，
// 客户端毫无察觉——上次正文泄漏工具调用的教训。
func resolveCursorNativeToolBridge(
	body []byte,
	clientTools []cursor.McpTool,
) (bridge map[string]string, mcpTools []cursor.McpTool, err error) {
	var envelope cursorNativeToolsEnvelope
	if unmarshalErr := json.Unmarshal(body, &envelope); unmarshalErr != nil {
		return nil, nil, fmt.Errorf("invalid cursor_options: %w", unmarshalErr)
	}
	if envelope.CursorOptions == nil || len(envelope.CursorOptions.NativeTools) == 0 {
		return nil, clientTools, nil
	}

	declared := make(map[string]struct{}, len(clientTools))
	for _, tool := range clientTools {
		declared[tool.Name] = struct{}{}
	}

	bridge = make(map[string]string, len(envelope.CursorOptions.NativeTools))
	bridged := make(map[string]struct{}, len(envelope.CursorOptions.NativeTools))
	for key, clientName := range envelope.CursorOptions.NativeTools {
		normalizedKey := strings.ToLower(strings.TrimSpace(key))
		if !cursor.IsNativeToolBridgeKey(normalizedKey) {
			return nil, nil, fmt.Errorf(
				"cursor_options.native_tools: unsupported built-in tool %q; supported: %s",
				key, strings.Join(cursor.NativeToolBridgeKeys(), ", "))
		}
		trimmedName := strings.TrimSpace(clientName)
		if trimmedName == "" {
			return nil, nil, fmt.Errorf(
				"cursor_options.native_tools: mapping for %q must name a client tool", normalizedKey)
		}
		if _, ok := declared[trimmedName]; !ok {
			return nil, nil, fmt.Errorf(
				"cursor_options.native_tools maps %q to client tool %q, which is not declared in tools",
				normalizedKey, trimmedName)
		}
		if _, dup := bridge[normalizedKey]; dup {
			return nil, nil, fmt.Errorf(
				"cursor_options.native_tools: duplicate mapping for %q", normalizedKey)
		}
		bridge[normalizedKey] = trimmedName
		bridged[trimmedName] = struct{}{}
	}

	// 被桥接的客户端工具不再注册 MCP：同一个工具走两条通道会让模型二选一，
	// 重放与统计也会出现两个名字。
	mcpTools = make([]cursor.McpTool, 0, len(clientTools))
	for _, tool := range clientTools {
		if _, ok := bridged[tool.Name]; ok {
			continue
		}
		mcpTools = append(mcpTools, tool)
	}
	return bridge, mcpTools, nil
}
