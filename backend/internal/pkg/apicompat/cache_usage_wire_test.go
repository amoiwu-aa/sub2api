package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func requireJSONField(t *testing.T, body []byte, path string, want int64) {
	t.Helper()
	field := gjson.GetBytes(body, path)
	require.Truef(t, field.Exists(), "expected %s to be present in %s", path, body)
	require.Equal(t, want, field.Int(), path)
}

func TestIncludeUsageStreamOptionSurvivesChatResponsesConversions(t *testing.T) {
	chat := &ChatCompletionsRequest{
		Model:         "gpt-5.6",
		Messages:      []ChatMessage{{Role: "user", Content: json.RawMessage(`"hello"`)}},
		Stream:        true,
		StreamOptions: &ChatStreamOptions{IncludeUsage: true},
	}
	responses, err := ChatCompletionsToResponses(chat)
	require.NoError(t, err)
	require.NotNil(t, responses.StreamOptions)
	require.True(t, responses.StreamOptions.IncludeUsage)

	roundTrip, err := ResponsesToChatCompletionsRequest(responses)
	require.NoError(t, err)
	require.NotNil(t, roundTrip.StreamOptions)
	require.True(t, roundTrip.StreamOptions.IncludeUsage)
}

func TestAnthropicUsageWirePreservesExplicitZeroTTLBreakdown(t *testing.T) {
	var usage AnthropicUsage
	require.NoError(t, json.Unmarshal([]byte(`{
		"input_tokens":11,
		"output_tokens":3,
		"cache_creation_input_tokens":7,
		"cache_read_input_tokens":0,
		"cache_creation":{
			"ephemeral_5m_input_tokens":0,
			"ephemeral_1h_input_tokens":7
		}
	}`), &usage))

	wire, err := json.Marshal(usage)
	require.NoError(t, err)
	requireJSONField(t, wire, "cache_read_input_tokens", 0)
	requireJSONField(t, wire, "cache_creation.ephemeral_5m_input_tokens", 0)
	requireJSONField(t, wire, "cache_creation.ephemeral_1h_input_tokens", 7)
}

func TestAnthropicUsageWireOmitsCacheFieldsWhenUpstreamDidNotReportThem(t *testing.T) {
	var usage AnthropicUsage
	require.NoError(t, json.Unmarshal([]byte(`{
		"input_tokens":11,
		"output_tokens":3
	}`), &usage))

	wire, err := json.Marshal(usage)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(wire, "cache_creation_input_tokens").Exists())
	require.False(t, gjson.GetBytes(wire, "cache_read_input_tokens").Exists())
	require.False(t, gjson.GetBytes(wire, "cache_creation").Exists())
}

func TestAnthropicUsageWireDoesNotInventMissingTTLBreakdown(t *testing.T) {
	var usage AnthropicUsage
	require.NoError(t, json.Unmarshal([]byte(`{
		"input_tokens":11,
		"output_tokens":3,
		"cache_creation_input_tokens":7,
		"cache_creation":{"ephemeral_1h_input_tokens":7}
	}`), &usage))

	wire, err := json.Marshal(usage)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(wire, "cache_creation.ephemeral_5m_input_tokens").Exists())
	requireJSONField(t, wire, "cache_creation.ephemeral_1h_input_tokens", 7)
}

func TestChatUsageWirePreservesExplicitZeroCacheFieldsAndTTLBreakdown(t *testing.T) {
	var usage ChatUsage
	require.NoError(t, json.Unmarshal([]byte(`{
		"prompt_tokens":18,
		"completion_tokens":4,
		"total_tokens":22,
		"prompt_tokens_details":{
			"cached_tokens":0,
			"cache_write_tokens":7,
			"cache_creation_5m_tokens":0,
			"cache_creation_1h_tokens":7
		}
	}`), &usage))

	wire, err := json.Marshal(usage)
	require.NoError(t, err)
	requireJSONField(t, wire, "prompt_tokens_details.cached_tokens", 0)
	requireJSONField(t, wire, "prompt_tokens_details.cache_write_tokens", 7)
	requireJSONField(t, wire, "prompt_tokens_details.cache_creation_5m_tokens", 0)
	requireJSONField(t, wire, "prompt_tokens_details.cache_creation_1h_tokens", 7)
}

func TestResponsesUsageWirePreservesExplicitZeroCacheFieldsAndTTLBreakdown(t *testing.T) {
	var usage ResponsesUsage
	require.NoError(t, json.Unmarshal([]byte(`{
		"input_tokens":18,
		"output_tokens":4,
		"total_tokens":22,
		"input_tokens_details":{
			"cached_tokens":0,
			"cache_write_tokens":7,
			"cache_creation_5m_tokens":0,
			"cache_creation_1h_tokens":7
		}
	}`), &usage))

	wire, err := json.Marshal(usage)
	require.NoError(t, err)
	requireJSONField(t, wire, "input_tokens_details.cached_tokens", 0)
	requireJSONField(t, wire, "input_tokens_details.cache_write_tokens", 7)
	requireJSONField(t, wire, "input_tokens_details.cache_creation_5m_tokens", 0)
	requireJSONField(t, wire, "input_tokens_details.cache_creation_1h_tokens", 7)
}

func TestAnthropicToResponsesWireCarriesCacheReadWriteAndTTLNonStreaming(t *testing.T) {
	var upstream AnthropicResponse
	require.NoError(t, json.Unmarshal([]byte(`{
		"id":"msg_cache_wire",
		"type":"message",
		"role":"assistant",
		"model":"claude-sonnet-4-5",
		"content":[{"type":"text","text":"ok"}],
		"stop_reason":"end_turn",
		"usage":{
			"input_tokens":11,
			"output_tokens":3,
			"cache_creation_input_tokens":7,
			"cache_read_input_tokens":0,
			"cache_creation":{
				"ephemeral_5m_input_tokens":0,
				"ephemeral_1h_input_tokens":7
			}
		}
	}`), &upstream))

	wire, err := json.Marshal(AnthropicToResponsesResponse(&upstream))
	require.NoError(t, err)
	requireJSONField(t, wire, "usage.input_tokens_details.cached_tokens", 0)
	requireJSONField(t, wire, "usage.input_tokens_details.cache_write_tokens", 7)
	requireJSONField(t, wire, "usage.input_tokens_details.cache_creation_5m_tokens", 0)
	requireJSONField(t, wire, "usage.input_tokens_details.cache_creation_1h_tokens", 7)
}

func TestAnthropicToResponsesWireDoesNotInventMissingTTLNonStreaming(t *testing.T) {
	var upstream AnthropicResponse
	require.NoError(t, json.Unmarshal([]byte(`{
		"id":"msg_cache_wire",
		"type":"message",
		"role":"assistant",
		"model":"claude-sonnet-4-5",
		"content":[],
		"stop_reason":"end_turn",
		"usage":{
			"input_tokens":11,
			"output_tokens":3,
			"cache_creation_input_tokens":7,
			"cache_creation":{"ephemeral_1h_input_tokens":7}
		}
	}`), &upstream))

	wire, err := json.Marshal(AnthropicToResponsesResponse(&upstream))
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(wire, "usage.input_tokens_details.cache_creation_5m_tokens").Exists())
	requireJSONField(t, wire, "usage.input_tokens_details.cache_creation_1h_tokens", 7)
}

func TestAnthropicToResponsesWireCarriesCacheReadWriteAndTTLStreaming(t *testing.T) {
	state := NewAnthropicEventToResponsesState()
	var start AnthropicStreamEvent
	require.NoError(t, json.Unmarshal([]byte(`{
		"type":"message_start",
		"message":{
			"id":"msg_cache_stream_wire",
			"type":"message",
			"role":"assistant",
			"model":"claude-sonnet-4-5",
			"content":[],
			"stop_reason":null,
			"usage":{
				"input_tokens":11,
				"output_tokens":0,
				"cache_creation_input_tokens":7,
				"cache_read_input_tokens":0,
				"cache_creation":{
					"ephemeral_5m_input_tokens":0,
					"ephemeral_1h_input_tokens":7
				}
			}
		}
	}`), &start))
	AnthropicEventToResponsesEvents(&start, state)
	events := AnthropicEventToResponsesEvents(&AnthropicStreamEvent{Type: "message_stop"}, state)

	var terminal *ResponsesStreamEvent
	for i := range events {
		if events[i].Type == "response.completed" {
			terminal = &events[i]
		}
	}
	require.NotNil(t, terminal)
	wire, err := json.Marshal(terminal)
	require.NoError(t, err)
	requireJSONField(t, wire, "response.usage.input_tokens_details.cached_tokens", 0)
	requireJSONField(t, wire, "response.usage.input_tokens_details.cache_write_tokens", 7)
	requireJSONField(t, wire, "response.usage.input_tokens_details.cache_creation_5m_tokens", 0)
	requireJSONField(t, wire, "response.usage.input_tokens_details.cache_creation_1h_tokens", 7)
}

func TestAnthropicToResponsesStreamingDoesNotInventMissingTTL(t *testing.T) {
	state := NewAnthropicEventToResponsesState()
	var start AnthropicStreamEvent
	require.NoError(t, json.Unmarshal([]byte(`{
		"type":"message_start",
		"message":{
			"id":"msg_cache_stream_wire",
			"type":"message",
			"role":"assistant",
			"model":"claude-sonnet-4-5",
			"content":[],
			"usage":{
				"input_tokens":11,
				"output_tokens":0,
				"cache_creation_input_tokens":7,
				"cache_creation":{"ephemeral_1h_input_tokens":7}
			}
		}
	}`), &start))
	AnthropicEventToResponsesEvents(&start, state)
	events := AnthropicEventToResponsesEvents(&AnthropicStreamEvent{Type: "message_stop"}, state)

	var terminal *ResponsesStreamEvent
	for i := range events {
		if events[i].Type == "response.completed" {
			terminal = &events[i]
		}
	}
	require.NotNil(t, terminal)
	wire, err := json.Marshal(terminal)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(wire, "response.usage.input_tokens_details.cache_creation_5m_tokens").Exists())
	requireJSONField(t, wire, "response.usage.input_tokens_details.cache_creation_1h_tokens", 7)
}

func TestResponsesToAnthropicWireCarriesCacheReadWriteAndTTL(t *testing.T) {
	var upstream ResponsesResponse
	require.NoError(t, json.Unmarshal([]byte(`{
		"id":"resp_cache_wire",
		"object":"response",
		"model":"gpt-5.4",
		"status":"completed",
		"output":[],
		"usage":{
			"input_tokens":18,
			"output_tokens":4,
			"total_tokens":22,
			"input_tokens_details":{
				"cached_tokens":0,
				"cache_write_tokens":7,
				"cache_creation_5m_tokens":0,
				"cache_creation_1h_tokens":7
			}
		}
	}`), &upstream))

	wire, err := json.Marshal(ResponsesToAnthropic(&upstream, "claude-sonnet-4-5"))
	require.NoError(t, err)
	requireJSONField(t, wire, "usage.cache_read_input_tokens", 0)
	requireJSONField(t, wire, "usage.cache_creation_input_tokens", 7)
	requireJSONField(t, wire, "usage.cache_creation.ephemeral_5m_input_tokens", 0)
	requireJSONField(t, wire, "usage.cache_creation.ephemeral_1h_input_tokens", 7)
}

func TestResponsesToAnthropicWireDoesNotInventMissingTTL(t *testing.T) {
	var upstream ResponsesResponse
	require.NoError(t, json.Unmarshal([]byte(`{
		"id":"resp_cache_wire",
		"object":"response",
		"model":"gpt-5.4",
		"status":"completed",
		"output":[],
		"usage":{
			"input_tokens":18,
			"output_tokens":4,
			"total_tokens":22,
			"input_tokens_details":{"cache_creation_1h_tokens":7}
		}
	}`), &upstream))

	wire, err := json.Marshal(ResponsesToAnthropic(&upstream, "claude-sonnet-4-5"))
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(wire, "usage.cache_creation.ephemeral_5m_input_tokens").Exists())
	requireJSONField(t, wire, "usage.cache_creation.ephemeral_1h_input_tokens", 7)
}

func TestResponsesToAnthropicWireCarriesCacheReadWriteAndTTLStreaming(t *testing.T) {
	state := NewResponsesEventToAnthropicState()
	var terminal ResponsesStreamEvent
	require.NoError(t, json.Unmarshal([]byte(`{
		"type":"response.completed",
		"response":{
			"id":"resp_cache_stream_wire",
			"object":"response",
			"model":"gpt-5.4",
			"status":"completed",
			"output":[],
			"usage":{
				"input_tokens":18,
				"output_tokens":4,
				"total_tokens":22,
				"input_tokens_details":{
					"cached_tokens":0,
					"cache_write_tokens":7,
					"cache_creation_5m_tokens":0,
					"cache_creation_1h_tokens":7
				}
			}
		}
	}`), &terminal))
	events := ResponsesEventToAnthropicEvents(&terminal, state)

	var delta *AnthropicStreamEvent
	for i := range events {
		if events[i].Type == "message_delta" {
			delta = &events[i]
		}
	}
	require.NotNil(t, delta)
	wire, err := json.Marshal(delta)
	require.NoError(t, err)
	requireJSONField(t, wire, "usage.cache_read_input_tokens", 0)
	requireJSONField(t, wire, "usage.cache_creation_input_tokens", 7)
	requireJSONField(t, wire, "usage.cache_creation.ephemeral_5m_input_tokens", 0)
	requireJSONField(t, wire, "usage.cache_creation.ephemeral_1h_input_tokens", 7)
}

func TestResponsesToAnthropicStreamingDoesNotInventMissingTTL(t *testing.T) {
	state := NewResponsesEventToAnthropicState()
	var terminal ResponsesStreamEvent
	require.NoError(t, json.Unmarshal([]byte(`{
		"type":"response.completed",
		"response":{
			"id":"resp_cache_stream_wire",
			"object":"response",
			"model":"gpt-5.4",
			"status":"completed",
			"output":[],
			"usage":{
				"input_tokens":18,
				"output_tokens":4,
				"total_tokens":22,
				"input_tokens_details":{"cache_creation_1h_tokens":7}
			}
		}
	}`), &terminal))
	events := ResponsesEventToAnthropicEvents(&terminal, state)

	var delta *AnthropicStreamEvent
	for i := range events {
		if events[i].Type == "message_delta" {
			delta = &events[i]
		}
	}
	require.NotNil(t, delta)
	wire, err := json.Marshal(delta)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(wire, "usage.cache_creation.ephemeral_5m_input_tokens").Exists())
	requireJSONField(t, wire, "usage.cache_creation.ephemeral_1h_input_tokens", 7)
}

func TestChatCompletionsToAnthropicWireCarriesCacheReadWriteAndTTLNonStreaming(t *testing.T) {
	var upstream ChatCompletionsResponse
	require.NoError(t, json.Unmarshal([]byte(`{
		"id":"chatcmpl-cache-wire",
		"object":"chat.completion",
		"created":1,
		"model":"gpt-5.4",
		"choices":[{
			"index":0,
			"message":{"role":"assistant","content":"ok"},
			"finish_reason":"stop"
		}],
		"usage":{
			"prompt_tokens":18,
			"completion_tokens":4,
			"total_tokens":22,
			"prompt_tokens_details":{
				"cached_tokens":0,
				"cache_write_tokens":7,
				"cache_creation_5m_tokens":0,
				"cache_creation_1h_tokens":7
			}
		}
	}`), &upstream))

	wire, err := json.Marshal(ChatCompletionsResponseToAnthropic(&upstream, "claude-sonnet-4-5"))
	require.NoError(t, err)
	requireJSONField(t, wire, "usage.cache_read_input_tokens", 0)
	requireJSONField(t, wire, "usage.cache_creation_input_tokens", 7)
	requireJSONField(t, wire, "usage.cache_creation.ephemeral_5m_input_tokens", 0)
	requireJSONField(t, wire, "usage.cache_creation.ephemeral_1h_input_tokens", 7)
}

func TestChatCompletionsToAnthropicWireDoesNotInventMissingTTL(t *testing.T) {
	var upstream ChatCompletionsResponse
	require.NoError(t, json.Unmarshal([]byte(`{
		"id":"chatcmpl-cache-wire",
		"object":"chat.completion",
		"created":1,
		"model":"gpt-5.4",
		"choices":[],
		"usage":{
			"prompt_tokens":18,
			"completion_tokens":4,
			"total_tokens":22,
			"prompt_tokens_details":{"cache_creation_1h_tokens":7}
		}
	}`), &upstream))

	wire, err := json.Marshal(ChatCompletionsResponseToAnthropic(&upstream, "claude-sonnet-4-5"))
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(wire, "usage.cache_creation.ephemeral_5m_input_tokens").Exists())
	requireJSONField(t, wire, "usage.cache_creation.ephemeral_1h_input_tokens", 7)
}

func TestChatCompletionsToAnthropicWireCarriesCacheReadWriteAndTTLStreaming(t *testing.T) {
	state := NewChatCompletionsToAnthropicStreamState("claude-sonnet-4-5")
	var chunk ChatCompletionsChunk
	require.NoError(t, json.Unmarshal([]byte(`{
		"id":"chatcmpl-cache-wire",
		"object":"chat.completion.chunk",
		"model":"gpt-5.4",
		"choices":[],
		"usage":{
			"prompt_tokens":18,
			"completion_tokens":4,
			"total_tokens":22,
			"prompt_tokens_details":{
				"cached_tokens":0,
				"cache_write_tokens":7,
				"cache_creation_5m_tokens":0,
				"cache_creation_1h_tokens":7
			}
		}
	}`), &chunk))
	ChatCompletionsChunkToAnthropicEvents(&chunk, state)
	events := FinalizeChatCompletionsAnthropicStream(state)

	var delta *AnthropicStreamEvent
	for i := range events {
		if events[i].Type == "message_delta" {
			delta = &events[i]
		}
	}
	require.NotNil(t, delta)
	wire, err := json.Marshal(delta)
	require.NoError(t, err)
	requireJSONField(t, wire, "usage.cache_read_input_tokens", 0)
	requireJSONField(t, wire, "usage.cache_creation_input_tokens", 7)
	requireJSONField(t, wire, "usage.cache_creation.ephemeral_5m_input_tokens", 0)
	requireJSONField(t, wire, "usage.cache_creation.ephemeral_1h_input_tokens", 7)
}

func TestChatCompletionsToAnthropicStreamingDoesNotInventMissingTTL(t *testing.T) {
	state := NewChatCompletionsToAnthropicStreamState("claude-sonnet-4-5")
	var chunk ChatCompletionsChunk
	require.NoError(t, json.Unmarshal([]byte(`{
		"id":"chatcmpl-cache-wire",
		"object":"chat.completion.chunk",
		"model":"gpt-5.4",
		"choices":[],
		"usage":{
			"prompt_tokens":18,
			"completion_tokens":4,
			"total_tokens":22,
			"prompt_tokens_details":{"cache_creation_1h_tokens":7}
		}
	}`), &chunk))
	ChatCompletionsChunkToAnthropicEvents(&chunk, state)
	events := FinalizeChatCompletionsAnthropicStream(state)

	var delta *AnthropicStreamEvent
	for i := range events {
		if events[i].Type == "message_delta" {
			delta = &events[i]
		}
	}
	require.NotNil(t, delta)
	wire, err := json.Marshal(delta)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(wire, "usage.cache_creation.ephemeral_5m_input_tokens").Exists())
	requireJSONField(t, wire, "usage.cache_creation.ephemeral_1h_input_tokens", 7)
}
