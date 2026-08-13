package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestChatCompletionsRequestWirePreservesPromptCacheControls(t *testing.T) {
	var request ChatCompletionsRequest
	require.NoError(t, json.Unmarshal([]byte(`{
		"model":"gpt-5.6",
		"prompt_cache_key":"tenant:conversation",
		"prompt_cache_options":{"mode":"explicit","ttl":"30m"},
		"messages":[{
			"role":"system",
			"prompt_cache_breakpoint":{"scope":"message"},
			"breakpoint":{"scope":"message"},
			"content":[{
				"type":"text",
				"text":"stable prefix",
				"cache_control":{"type":"ephemeral","ttl":"1h"},
				"prompt_cache_breakpoint":{"mode":"explicit"},
				"breakpoint":{"mode":"explicit"}
			}]
		}]
	}`), &request))

	wire, err := json.Marshal(request)
	require.NoError(t, err)
	require.Equal(t, "tenant:conversation", gjson.GetBytes(wire, "prompt_cache_key").String())
	require.Equal(t, "explicit", gjson.GetBytes(wire, "prompt_cache_options.mode").String())
	require.Equal(t, "30m", gjson.GetBytes(wire, "prompt_cache_options.ttl").String())
	require.Equal(t, "message", gjson.GetBytes(wire, "messages.0.prompt_cache_breakpoint.scope").String())
	require.Equal(t, "message", gjson.GetBytes(wire, "messages.0.breakpoint.scope").String())
	require.Equal(t, "ephemeral", gjson.GetBytes(wire, "messages.0.content.0.cache_control.type").String())
	require.Equal(t, "1h", gjson.GetBytes(wire, "messages.0.content.0.cache_control.ttl").String())
	require.Equal(t, "explicit", gjson.GetBytes(wire, "messages.0.content.0.prompt_cache_breakpoint.mode").String())
	require.Equal(t, "explicit", gjson.GetBytes(wire, "messages.0.content.0.breakpoint.mode").String())
}

func TestResponsesRequestWirePreservesPromptCacheControls(t *testing.T) {
	var request ResponsesRequest
	require.NoError(t, json.Unmarshal([]byte(`{
		"model":"gpt-5.6",
		"prompt_cache_key":"tenant:conversation",
		"prompt_cache_options":{"mode":"explicit","ttl":"30m"},
		"input":[{
			"type":"message",
			"role":"user",
			"content":[{
				"type":"input_text",
				"text":"stable prefix",
				"cache_control":{"type":"ephemeral","ttl":"1h"},
				"prompt_cache_breakpoint":{"mode":"explicit"},
				"breakpoint":{"mode":"explicit"}
			}]
		}]
	}`), &request))

	wire, err := json.Marshal(request)
	require.NoError(t, err)
	require.Equal(t, "tenant:conversation", gjson.GetBytes(wire, "prompt_cache_key").String())
	require.Equal(t, "explicit", gjson.GetBytes(wire, "prompt_cache_options.mode").String())
	require.Equal(t, "30m", gjson.GetBytes(wire, "prompt_cache_options.ttl").String())
	require.Equal(t, "ephemeral", gjson.GetBytes(wire, "input.0.content.0.cache_control.type").String())
	require.Equal(t, "1h", gjson.GetBytes(wire, "input.0.content.0.cache_control.ttl").String())
	require.Equal(t, "explicit", gjson.GetBytes(wire, "input.0.content.0.prompt_cache_breakpoint.mode").String())
	require.Equal(t, "explicit", gjson.GetBytes(wire, "input.0.content.0.breakpoint.mode").String())
}

func TestChatCompletionsToResponsesPreservesPromptCacheControls(t *testing.T) {
	var request ChatCompletionsRequest
	require.NoError(t, json.Unmarshal([]byte(`{
		"model":"gpt-5.6",
		"prompt_cache_key":"tenant:conversation",
		"prompt_cache_options":{"mode":"explicit","ttl":"30m"},
		"messages":[{
			"role":"system",
			"content":[{
				"type":"text",
				"text":"stable prefix",
				"cache_control":{"type":"ephemeral","ttl":"1h"},
				"prompt_cache_breakpoint":{"mode":"explicit"},
				"breakpoint":{"mode":"explicit"}
			}]
		}]
	}`), &request))

	converted, err := ChatCompletionsToResponses(&request)
	require.NoError(t, err)
	wire, err := json.Marshal(converted)
	require.NoError(t, err)
	require.Equal(t, "tenant:conversation", gjson.GetBytes(wire, "prompt_cache_key").String())
	require.Equal(t, "explicit", gjson.GetBytes(wire, "prompt_cache_options.mode").String())
	require.Equal(t, "ephemeral", gjson.GetBytes(wire, "input.0.content.0.cache_control.type").String())
	require.Equal(t, "explicit", gjson.GetBytes(wire, "input.0.content.0.prompt_cache_breakpoint.mode").String())
	require.Equal(t, "explicit", gjson.GetBytes(wire, "input.0.content.0.breakpoint.mode").String())
}

func TestResponsesToChatCompletionsPreservesPromptCacheControls(t *testing.T) {
	var request ResponsesRequest
	require.NoError(t, json.Unmarshal([]byte(`{
		"model":"gpt-5.6",
		"prompt_cache_key":"tenant:conversation",
		"prompt_cache_options":{"mode":"explicit","ttl":"30m"},
		"input":[{
			"type":"message",
			"role":"user",
			"content":[{
				"type":"input_text",
				"text":"stable prefix",
				"cache_control":{"type":"ephemeral","ttl":"1h"},
				"prompt_cache_breakpoint":{"mode":"explicit"},
				"breakpoint":{"mode":"explicit"}
			}]
		}]
	}`), &request))

	converted, err := ResponsesToChatCompletionsRequest(&request)
	require.NoError(t, err)
	wire, err := json.Marshal(converted)
	require.NoError(t, err)
	require.Equal(t, "tenant:conversation", gjson.GetBytes(wire, "prompt_cache_key").String())
	require.Equal(t, "explicit", gjson.GetBytes(wire, "prompt_cache_options.mode").String())
	require.Equal(t, "ephemeral", gjson.GetBytes(wire, "messages.0.content.0.cache_control.type").String())
	require.Equal(t, "explicit", gjson.GetBytes(wire, "messages.0.content.0.prompt_cache_breakpoint.mode").String())
	require.Equal(t, "explicit", gjson.GetBytes(wire, "messages.0.content.0.breakpoint.mode").String())
}

func TestAnthropicToResponsesPreservesCacheControlAndBreakpoints(t *testing.T) {
	var request AnthropicRequest
	require.NoError(t, json.Unmarshal([]byte(`{
		"model":"gpt-5.6",
		"max_tokens":128,
		"messages":[{
			"role":"user",
			"content":[{
				"type":"text",
				"text":"stable prefix",
				"cache_control":{"type":"ephemeral","ttl":"1h"},
				"prompt_cache_breakpoint":{"mode":"explicit"},
				"breakpoint":{"mode":"explicit"}
			}]
		}]
	}`), &request))

	converted, err := AnthropicToResponses(&request)
	require.NoError(t, err)
	wire, err := json.Marshal(converted)
	require.NoError(t, err)
	require.Equal(t, "ephemeral", gjson.GetBytes(wire, "input.0.content.0.cache_control.type").String())
	require.Equal(t, "1h", gjson.GetBytes(wire, "input.0.content.0.cache_control.ttl").String())
	require.Equal(t, "explicit", gjson.GetBytes(wire, "input.0.content.0.prompt_cache_breakpoint.mode").String())
	require.Equal(t, "explicit", gjson.GetBytes(wire, "input.0.content.0.breakpoint.mode").String())
}

func TestAnthropicToResponsesAssistantTextCarriesCacheControl(t *testing.T) {
	var request AnthropicRequest
	require.NoError(t, json.Unmarshal([]byte(`{
		"model":"gpt-5.6",
		"max_tokens":128,
		"messages":[
			{"role":"user","content":"hi"},
			{"role":"assistant","content":[
				{"type":"text","text":"first"},
				{"type":"text","text":"second",
					"cache_control":{"type":"ephemeral","ttl":"1h"},
					"prompt_cache_breakpoint":{"mode":"explicit"},
					"breakpoint":{"mode":"explicit"}}
			]}
		]
	}`), &request))

	converted, err := AnthropicToResponses(&request)
	require.NoError(t, err)
	wire, err := json.Marshal(converted)
	require.NoError(t, err)
	// 压平成单个 output_text 后，折叠边界上的缓存标记必须保留。
	require.Equal(t, "ephemeral", gjson.GetBytes(wire, "input.1.content.0.cache_control.type").String())
	require.Equal(t, "1h", gjson.GetBytes(wire, "input.1.content.0.cache_control.ttl").String())
	require.Equal(t, "explicit", gjson.GetBytes(wire, "input.1.content.0.prompt_cache_breakpoint.mode").String())
	require.Equal(t, "explicit", gjson.GetBytes(wire, "input.1.content.0.breakpoint.mode").String())
}

func TestChatToResponsesAssistantCarriesMessageCacheControl(t *testing.T) {
	var request ChatCompletionsRequest
	require.NoError(t, json.Unmarshal([]byte(`{
		"model":"gpt-5.6",
		"messages":[
			{"role":"user","content":"hi"},
			{"role":"assistant","content":"prior answer",
				"cache_control":{"type":"ephemeral","ttl":"1h"},
				"prompt_cache_breakpoint":{"mode":"explicit"},
				"breakpoint":{"mode":"explicit"}}
		]
	}`), &request))

	converted, err := ChatCompletionsToResponses(&request)
	require.NoError(t, err)
	wire, err := json.Marshal(converted)
	require.NoError(t, err)
	require.Equal(t, "ephemeral", gjson.GetBytes(wire, "input.1.content.0.cache_control.type").String())
	require.Equal(t, "explicit", gjson.GetBytes(wire, "input.1.content.0.prompt_cache_breakpoint.mode").String())
	require.Equal(t, "explicit", gjson.GetBytes(wire, "input.1.content.0.breakpoint.mode").String())
}

func TestAnthropicToChatCompletionsBridgePreservesPromptCacheControls(t *testing.T) {
	var request AnthropicRequest
	require.NoError(t, json.Unmarshal([]byte(`{
		"model":"gpt-5.6",
		"max_tokens":128,
		"system":[
			{"type":"text","text":"system prefix","cache_control":{"type":"ephemeral","ttl":"1h"}}
		],
		"messages":[
			{"role":"user","content":[
				{"type":"text","text":"stable prefix","cache_control":{"type":"ephemeral","ttl":"5m"}}
			]},
			{"role":"assistant","content":[
				{"type":"text","text":"prior answer","cache_control":{"type":"ephemeral"}},
				{"type":"tool_use","id":"toolu_1","name":"lookup","input":{"q":"x"}}
			]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"toolu_1","content":"result",
					"cache_control":{"type":"ephemeral","ttl":"1h"}}
			]}
		],
		"tools":[{
			"name":"lookup",
			"description":"look things up",
			"input_schema":{"type":"object"},
			"cache_control":{"type":"ephemeral","ttl":"1h"},
			"prompt_cache_breakpoint":{"mode":"explicit"},
			"breakpoint":{"mode":"explicit"}
		}]
	}`), &request))

	converted, err := AnthropicToChatCompletionsRequest(&request)
	require.NoError(t, err)
	wire, err := json.Marshal(converted)
	require.NoError(t, err)

	require.Equal(t, "1h", gjson.GetBytes(wire, "messages.0.cache_control.ttl").String(),
		"system message should keep the folded system block cache_control")
	require.Equal(t, "5m", gjson.GetBytes(wire, "messages.1.cache_control.ttl").String(),
		"user message should keep the folded text block cache_control")
	require.Equal(t, "ephemeral", gjson.GetBytes(wire, "messages.2.cache_control.type").String(),
		"assistant message should keep the folded text block cache_control")
	require.Equal(t, "1h", gjson.GetBytes(wire, "messages.3.cache_control.ttl").String(),
		"tool result message should keep the tool_result block cache_control")
	require.Equal(t, "1h", gjson.GetBytes(wire, "tools.0.cache_control.ttl").String())
	require.Equal(t, "explicit", gjson.GetBytes(wire, "tools.0.prompt_cache_breakpoint.mode").String())
	require.Equal(t, "explicit", gjson.GetBytes(wire, "tools.0.breakpoint.mode").String())
}

func TestResponsesToAnthropicRequestPreservesCacheControls(t *testing.T) {
	var request ResponsesRequest
	require.NoError(t, json.Unmarshal([]byte(`{
		"model":"claude-sonnet-4-5",
		"input":[{
			"type":"message",
			"role":"user",
			"content":[{
				"type":"input_text",
				"text":"stable prefix",
				"cache_control":{"type":"ephemeral","ttl":"1h"}
			}]
		}],
		"tools":[{
			"type":"function",
			"name":"lookup",
			"parameters":{"type":"object"},
			"cache_control":{"type":"ephemeral","ttl":"1h"},
			"prompt_cache_breakpoint":{"mode":"explicit"},
			"breakpoint":{"mode":"explicit"}
		}]
	}`), &request))

	converted, err := ResponsesToAnthropicRequest(&request)
	require.NoError(t, err)
	wire, err := json.Marshal(converted)
	require.NoError(t, err)
	// Anthropic 上游原生支持 cache_control，这个方向的转换是缓存收益的关键路径。
	require.Equal(t, "ephemeral", gjson.GetBytes(wire, "messages.0.content.0.cache_control.type").String())
	require.Equal(t, "1h", gjson.GetBytes(wire, "messages.0.content.0.cache_control.ttl").String())
	require.Equal(t, "1h", gjson.GetBytes(wire, "tools.0.cache_control.ttl").String())
	require.Equal(t, "explicit", gjson.GetBytes(wire, "tools.0.prompt_cache_breakpoint.mode").String())
	require.Equal(t, "explicit", gjson.GetBytes(wire, "tools.0.breakpoint.mode").String())
}

func TestToolPromptCacheControlsSurviveChatResponsesRoundTrip(t *testing.T) {
	var request ChatCompletionsRequest
	require.NoError(t, json.Unmarshal([]byte(`{
		"model":"gpt-5.6",
		"messages":[{"role":"user","content":"run tool"}],
		"tools":[{
			"type":"function",
			"function":{"name":"lookup","parameters":{"type":"object"}},
			"cache_control":{"type":"ephemeral","ttl":"1h"},
			"prompt_cache_breakpoint":{"mode":"explicit"},
			"breakpoint":{"mode":"explicit"}
		}]
	}`), &request))

	responsesRequest, err := ChatCompletionsToResponses(&request)
	require.NoError(t, err)
	responsesWire, err := json.Marshal(responsesRequest)
	require.NoError(t, err)
	require.Equal(t, "ephemeral", gjson.GetBytes(responsesWire, "tools.0.cache_control.type").String())
	require.Equal(t, "explicit", gjson.GetBytes(responsesWire, "tools.0.prompt_cache_breakpoint.mode").String())
	require.Equal(t, "explicit", gjson.GetBytes(responsesWire, "tools.0.breakpoint.mode").String())

	roundTrip, err := ResponsesToChatCompletionsRequest(responsesRequest)
	require.NoError(t, err)
	chatWire, err := json.Marshal(roundTrip)
	require.NoError(t, err)
	require.Equal(t, "ephemeral", gjson.GetBytes(chatWire, "tools.0.cache_control.type").String())
	require.Equal(t, "explicit", gjson.GetBytes(chatWire, "tools.0.prompt_cache_breakpoint.mode").String())
	require.Equal(t, "explicit", gjson.GetBytes(chatWire, "tools.0.breakpoint.mode").String())
}
