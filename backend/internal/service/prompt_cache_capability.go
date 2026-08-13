package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/tidwall/gjson"
)

// openAIExplicitPromptCacheSupported gates request-level prompt_cache_options
// and block-level breakpoints. OpenAI documents these fields for GPT-5.6 and
// later families; older models reject them instead of ignoring them.
func openAIExplicitPromptCacheSupported(account *Account, model string) bool {
	if account == nil || account.Platform != PlatformOpenAI {
		return false
	}
	major, minor, ok := openAIGPTModelVersion(model)
	if !ok {
		return false
	}
	return major > 5 || (major == 5 && minor >= 6)
}

func openAIGPTModelVersion(model string) (int, int, bool) {
	model = strings.ToLower(strings.TrimSpace(model))
	if index := strings.LastIndexByte(model, '/'); index >= 0 {
		model = model[index+1:]
	}
	if !strings.HasPrefix(model, "gpt-") {
		return 0, 0, false
	}
	version := model[len("gpt-"):]
	majorText, version := leadingDigits(version)
	if majorText == "" {
		return 0, 0, false
	}
	major, err := strconv.Atoi(majorText)
	if err != nil {
		return 0, 0, false
	}
	if version == "" || version[0] != '.' {
		return major, 0, true
	}
	minorText, _ := leadingDigits(version[1:])
	if minorText == "" {
		return 0, 0, false
	}
	minor, err := strconv.Atoi(minorText)
	if err != nil {
		return 0, 0, false
	}
	return major, minor, true
}

func leadingDigits(value string) (string, string) {
	for index, r := range value {
		if !unicode.IsDigit(r) {
			return value[:index], value[index:]
		}
	}
	return value, ""
}

// stripPromptCacheBreakpoints removes only block-level explicit-cache markers.
// It deliberately leaves prompt_cache_key, prompt_cache_options and
// cache_control untouched so the caller can apply each field's own capability
// policy without rewriting or inventing cache semantics.
func stripPromptCacheBreakpoints(value any) bool {
	return stripPromptCacheProtocolKeys(value, []string{"prompt_cache_breakpoint", "breakpoint"}, true)
}

// stripAnthropicCacheControls removes Anthropic-vocabulary cache_control
// annotations from protocol objects, with the same schema/tool-input safety
// guards as stripPromptCacheBreakpoints.
func stripAnthropicCacheControls(value any) bool {
	return stripPromptCacheProtocolKeys(value, []string{"cache_control"}, true)
}

func stripPromptCacheProtocolKeys(value any, keys []string, root bool) bool {
	changed := false
	switch typed := value.(type) {
	case map[string]any:
		if root || isPromptCacheProtocolObject(typed) {
			for _, key := range keys {
				if _, ok := typed[key]; ok {
					delete(typed, key)
					changed = true
				}
			}
		}
		objectType, _ := typed["type"].(string)
		for key, child := range typed {
			if isOpaquePromptCachePayload(key, objectType) {
				continue
			}
			if stripPromptCacheProtocolKeys(child, keys, false) {
				changed = true
			}
		}
	case []any:
		for _, child := range typed {
			if stripPromptCacheProtocolKeys(child, keys, false) {
				changed = true
			}
		}
	}
	return changed
}

// applyOpenAIPromptCachePolicyToBody is the single capability gate for every
// OpenAI-bound forwarding path that does not run through the /v1/responses
// modify pipeline (chat-completions fallbacks, /v1/messages conversion).
// Upstream models without explicit prompt-cache support must not receive any
// explicit cache vocabulary: prompt_cache_options, breakpoint markers, or
// Anthropic cache_control annotations. prompt_cache_key stays — every model
// generation accepts it. Supported models receive the body unchanged
// (preserve, don't falsify).
func applyOpenAIPromptCachePolicyToBody(account *Account, upstreamModel string, body []byte) ([]byte, error) {
	if openAIExplicitPromptCacheSupported(account, upstreamModel) {
		return body, nil
	}
	hasOptions := gjson.GetBytes(body, "prompt_cache_options").Exists()
	mayHaveMarkers := bytes.Contains(body, []byte(`"prompt_cache_breakpoint"`)) ||
		bytes.Contains(body, []byte(`"breakpoint"`)) ||
		bytes.Contains(body, []byte(`"cache_control"`))
	if !hasOptions && !mayHaveMarkers {
		return body, nil
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("apply prompt cache policy: %w", err)
	}
	changed := false
	if _, ok := decoded["prompt_cache_options"]; ok {
		delete(decoded, "prompt_cache_options")
		changed = true
	}
	if stripPromptCacheBreakpoints(decoded) {
		changed = true
	}
	if stripAnthropicCacheControls(decoded) {
		changed = true
	}
	if !changed {
		return body, nil
	}
	out, err := json.Marshal(decoded)
	if err != nil {
		return nil, fmt.Errorf("apply prompt cache policy: %w", err)
	}
	return out, nil
}

func isPromptCacheProtocolObject(value map[string]any) bool {
	if _, ok := value["role"]; ok {
		return true
	}
	if _, ok := value["input_schema"]; ok {
		return true
	}
	objectType, _ := value["type"].(string)
	switch objectType {
	case "message",
		"text", "input_text", "output_text",
		"image", "input_image", "image_url", "document",
		"tool_use", "tool_result",
		"function", "custom", "namespace", "tool_search",
		"web_search", "local_shell", "computer", "mcp":
		return true
	default:
		return false
	}
}

func isOpaquePromptCachePayload(key string, objectType string) bool {
	switch key {
	case "parameters", "input_schema", "schema", "json_schema", "arguments":
		return true
	case "input":
		switch objectType {
		case "tool_use", "function_call", "custom_tool_call":
			return true
		}
	}
	return false
}
