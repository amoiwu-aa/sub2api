package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func mustCast[T any](t *testing.T, value any) T {
	t.Helper()
	typed, ok := value.(T)
	require.Truef(t, ok, "expected %T, got %T", typed, value)
	return typed
}

func TestOpenAIExplicitPromptCacheCapabilityUsesPlatformAndModel(t *testing.T) {
	tests := []struct {
		name     string
		account  *Account
		model    string
		expected bool
	}{
		{
			name:     "OpenAI gpt 5.6",
			account:  &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
			model:    "gpt-5.6",
			expected: true,
		},
		{
			name:     "OpenAI provider-prefixed gpt 5.6 variant",
			account:  &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
			model:    "openai/gpt-5.6-sol",
			expected: true,
		},
		{
			name:     "OpenAI future major",
			account:  &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
			model:    "gpt-6",
			expected: true,
		},
		{
			name:     "older OpenAI model",
			account:  &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
			model:    "gpt-5.5",
			expected: false,
		},
		{
			name:     "unrelated model containing gpt substring",
			account:  &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
			model:    "not-gpt-5.6",
			expected: false,
		},
		{
			name:     "Anthropic uses cache control instead",
			account:  &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey},
			model:    "claude-opus-4-6",
			expected: false,
		},
		{
			name:     "Cursor unsupported",
			account:  &Account{Platform: PlatformCursor},
			model:    "gpt-5.6",
			expected: false,
		},
		{
			name:     "Kiro unsupported",
			account:  &Account{Platform: PlatformKiro},
			model:    "gpt-5.6",
			expected: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.expected, openAIExplicitPromptCacheSupported(test.account, test.model))
		})
	}
}

func TestStripPromptCacheBreakpointsLeavesOtherCacheFieldsUntouched(t *testing.T) {
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(`{
		"prompt_cache_key":"stable-key",
		"prompt_cache_options":{"mode":"explicit","ttl":"30m"},
		"input":[{
			"type":"message",
			"content":[{
				"type":"input_text",
				"text":"prefix",
				"cache_control":{"type":"ephemeral","ttl":"1h"},
				"prompt_cache_breakpoint":{"mode":"explicit"},
				"breakpoint":{"mode":"explicit"}
			}]
		},{
			"type":"tool_use",
			"input":{"breakpoint":"user-supplied-value"}
		}],
		"tools":[{
			"type":"function",
			"function":{
				"name":"lookup",
				"parameters":{
					"type":"object",
					"properties":{"breakpoint":{"type":"string"}}
				}
			}
		}]
	}`), &payload))

	require.True(t, stripPromptCacheBreakpoints(payload))
	require.Equal(t, "stable-key", payload["prompt_cache_key"])
	require.NotNil(t, payload["prompt_cache_options"])
	inputs := mustCast[[]any](t, payload["input"])
	message := mustCast[map[string]any](t, inputs[0])
	content := mustCast[map[string]any](t, mustCast[[]any](t, message["content"])[0])
	require.NotNil(t, content["cache_control"])
	require.NotContains(t, content, "prompt_cache_breakpoint")
	require.NotContains(t, content, "breakpoint")
	toolInput := mustCast[map[string]any](t, mustCast[map[string]any](t, inputs[1])["input"])
	require.Equal(t, "user-supplied-value", toolInput["breakpoint"])
	tool := mustCast[map[string]any](t, mustCast[[]any](t, payload["tools"])[0])
	parameters := mustCast[map[string]any](t, mustCast[map[string]any](t, tool["function"])["parameters"])
	require.Contains(t, mustCast[map[string]any](t, parameters["properties"]), "breakpoint")
}

func TestApplyOpenAIPromptCachePolicyToBodyStripsAllExplicitCacheVocabulary(t *testing.T) {
	// 所有绕开 /v1/responses 主门的转发路径（chat 回退、messages 转换）都要走
	// 这个统一门：不支持显式缓存的上游收不到任何显式缓存词汇，也绝不误伤
	// schema 或工具入参里的同名业务字段。
	body := []byte(`{
		"model":"gpt-4o",
		"prompt_cache_key":"stable-key",
		"prompt_cache_options":{"mode":"explicit","ttl":"30m"},
		"messages":[{
			"role":"user",
			"cache_control":{"type":"ephemeral"},
			"prompt_cache_breakpoint":{"mode":"explicit"},
			"content":[{
				"type":"text",
				"text":"prefix",
				"cache_control":{"type":"ephemeral","ttl":"1h"},
				"breakpoint":{"mode":"explicit"}
			}]
		}],
		"tools":[{
			"type":"function",
			"cache_control":{"type":"ephemeral"},
			"function":{
				"name":"lookup",
				"parameters":{
					"type":"object",
					"properties":{
						"cache_control":{"type":"string"},
						"breakpoint":{"type":"string"}
					}
				}
			}
		}]
	}`)

	account := &Account{Platform: PlatformOpenAI}

	stripped, err := applyOpenAIPromptCachePolicyToBody(account, "gpt-4o", body)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(stripped, "prompt_cache_options").Exists())
	require.False(t, gjson.GetBytes(stripped, "messages.0.cache_control").Exists())
	require.False(t, gjson.GetBytes(stripped, "messages.0.prompt_cache_breakpoint").Exists())
	require.False(t, gjson.GetBytes(stripped, "messages.0.content.0.cache_control").Exists())
	require.False(t, gjson.GetBytes(stripped, "messages.0.content.0.breakpoint").Exists())
	require.False(t, gjson.GetBytes(stripped, "tools.0.cache_control").Exists())
	// prompt_cache_key 各代模型都接受，保留。
	require.Equal(t, "stable-key", gjson.GetBytes(stripped, "prompt_cache_key").String())
	// JSON Schema 里的同名属性是业务字段，不能剥。
	require.True(t, gjson.GetBytes(stripped, "tools.0.function.parameters.properties.cache_control").Exists())
	require.True(t, gjson.GetBytes(stripped, "tools.0.function.parameters.properties.breakpoint").Exists())

	kept, err := applyOpenAIPromptCachePolicyToBody(account, "gpt-5.6", body)
	require.NoError(t, err)
	require.True(t, gjson.GetBytes(kept, "prompt_cache_options").Exists())
	require.Equal(t, "1h", gjson.GetBytes(kept, "messages.0.content.0.cache_control.ttl").String())
	require.Equal(t, "explicit", gjson.GetBytes(kept, "messages.0.content.0.breakpoint.mode").String())
}
