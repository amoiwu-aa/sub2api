package service

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
)

const (
	CursorNativeToolBridgeModeOff           = "off"
	CursorNativeToolBridgeModeShadow        = "shadow"
	CursorNativeToolBridgeModeExplicit      = "explicit"
	CursorNativeToolBridgeModeInferReadOnly = "infer_readonly"
	CursorNativeToolBridgeModeInferAll      = "infer_all"
)

func normalizeCursorNativeToolBridgeMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case CursorNativeToolBridgeModeOff:
		return CursorNativeToolBridgeModeOff
	case CursorNativeToolBridgeModeExplicit:
		return CursorNativeToolBridgeModeExplicit
	case CursorNativeToolBridgeModeInferReadOnly:
		return CursorNativeToolBridgeModeInferReadOnly
	case CursorNativeToolBridgeModeInferAll, "":
		return CursorNativeToolBridgeModeInferAll
	case CursorNativeToolBridgeModeShadow:
		return CursorNativeToolBridgeModeShadow
	default:
		// Unknown configuration must fail closed. Keep explicit client mappings
		// available, but never turn on inference because of a typo.
		return CursorNativeToolBridgeModeExplicit
	}
}

// 原生工具桥的请求级配置。
//
// 客户端通过 cursor_options.native_tools 声明「内置工具名 → 客户端工具名」的
// 映射，例如 {"read":"Read","grep":"Grep","ls":"LS"}。命中的客户端工具不再注册
// 为 MCP 工具；模型直接调它训练时熟悉的内置 read / grep / ls，网关把 exec 帧
// 翻译成标准 tool_calls 交回客户端执行。参数契约见 docs/CURSOR_TOOL_CALLING.md。

type cursorNativeToolsEnvelope struct {
	CursorOptions *struct {
		NativeTools map[string]string `json:"native_tools,omitempty"`
		// NativeToolsAuto 显式置 false 可关掉自动推断，让本次请求的全部
		// 客户端工具都走 MCP 通道。
		NativeToolsAuto *bool `json:"native_tools_auto,omitempty"`
	} `json:"cursor_options,omitempty"`
}

// resolveCursorNativeToolBridge 解析并校验 native_tools 映射，同时把客户端
// 工具列表切分成「仍走 MCP 注册的」与「由原生桥接管的」。
//
// 客户端没有显式配置时按声明的工具 schema 自动推断（见
// cursor_gateway_native_tools_infer.go）：Codex、Claude Code 这类第三方
// 客户端不认识 cursor_options，不推断的话原生桥对它们等于不存在。
//
// 显式配置的校验失败返回错误（应回 400）：静默忽略会让模型继续用错误的
// 通道，客户端毫无察觉——上次正文泄漏工具调用的教训。自动推断则相反，
// 绑不上就安静回落 MCP，不能让推断把本来能用的请求打成 400。
func resolveCursorNativeToolBridge(
	body []byte,
	clientTools []cursor.McpTool,
	configuredMode string,
) (bridge cursor.NativeToolBridge, mcpTools []cursor.McpTool, err error) {
	var envelope cursorNativeToolsEnvelope
	if unmarshalErr := json.Unmarshal(body, &envelope); unmarshalErr != nil {
		return nil, nil, fmt.Errorf("invalid cursor_options: %w", unmarshalErr)
	}

	mode := normalizeCursorNativeToolBridgeMode(configuredMode)
	options := envelope.CursorOptions
	if mode == CursorNativeToolBridgeModeOff {
		return nil, clientTools, nil
	}
	if options == nil || len(options.NativeTools) == 0 {
		if options != nil && options.NativeToolsAuto != nil && !*options.NativeToolsAuto {
			return nil, clientTools, nil
		}
		requestExplicitlyEnabledAuto := options != nil &&
			options.NativeToolsAuto != nil && *options.NativeToolsAuto
		if mode == CursorNativeToolBridgeModeExplicit && !requestExplicitlyEnabledAuto {
			return nil, clientTools, nil
		}

		bridge = inferNativeToolBridge(clientTools)
		if len(bridge) == 0 {
			return nil, clientTools, nil
		}
		if mode == CursorNativeToolBridgeModeShadow && !requestExplicitlyEnabledAuto {
			slog.Debug("cursor native tool bridge shadow proposal",
				"mappings", nativeToolBridgeSummary(bridge))
			return nil, clientTools, nil
		}
		if mode == CursorNativeToolBridgeModeInferReadOnly && !requestExplicitlyEnabledAuto {
			bridge = filterReadOnlyNativeToolBridge(bridge)
			if len(bridge) == 0 {
				return nil, clientTools, nil
			}
		}
		return bridge, splitBridgedMcpTools(bridge, clientTools), nil
	}

	declared := make(map[string]cursor.McpTool, len(clientTools))
	for _, tool := range clientTools {
		if _, exists := declared[tool.Name]; !exists {
			declared[tool.Name] = tool
		}
	}

	bridge = make(cursor.NativeToolBridge, len(options.NativeTools))
	for key, clientName := range options.NativeTools {
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
		tool, ok := declared[trimmedName]
		if !ok {
			return nil, nil, fmt.Errorf(
				"cursor_options.native_tools maps %q to client tool %q, which is not declared in tools",
				normalizedKey, trimmedName)
		}
		if _, dup := bridge[normalizedKey]; dup {
			return nil, nil, fmt.Errorf(
				"cursor_options.native_tools: duplicate mapping for %q", normalizedKey)
		}
		bridge[normalizedKey] = explicitNativeToolTarget(normalizedKey, tool)
	}
	return bridge, splitBridgedMcpTools(bridge, clientTools), nil
}

func filterReadOnlyNativeToolBridge(bridge cursor.NativeToolBridge) cursor.NativeToolBridge {
	filtered := make(cursor.NativeToolBridge)
	for _, key := range []string{"diagnostics", "fetch", "glob", "grep", "ls", "read"} {
		if target, ok := bridge[key]; ok {
			filtered[key] = target
		}
	}
	return filtered
}

func nativeToolBridgeSummary(bridge cursor.NativeToolBridge) []string {
	summary := make([]string, 0, len(bridge))
	for key, target := range bridge {
		summary = append(summary, key+"->"+target.Name)
	}
	sort.Strings(summary)
	return summary
}

// splitBridgedMcpTools 摘掉已被原生桥接管的客户端工具。
//
// 同一个工具走两条通道会让模型二选一，重放与统计也会出现两个名字。
func splitBridgedMcpTools(bridge cursor.NativeToolBridge, clientTools []cursor.McpTool) []cursor.McpTool {
	if len(bridge) == 0 {
		return clientTools
	}
	bridged := make(map[string]struct{}, len(bridge))
	for _, target := range bridge {
		bridged[target.Name] = struct{}{}
	}
	mcpTools := make([]cursor.McpTool, 0, len(clientTools))
	for _, tool := range clientTools {
		if _, ok := bridged[tool.Name]; ok {
			continue
		}
		mcpTools = append(mcpTools, tool)
	}
	return mcpTools
}
