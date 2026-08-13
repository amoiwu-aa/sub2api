package service

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/stretchr/testify/require"
)

func TestParseClaudeUsageWireMarksReportedOnlyWhenCacheFieldsExist(t *testing.T) {
	t.Run("ordinary usage is unavailable", func(t *testing.T) {
		usage := parseClaudeUsageFromResponseBody([]byte(`{
			"usage":{"input_tokens":11,"output_tokens":4}
		}`))

		require.Equal(t, CacheUsageSourceUnavailable, usage.CacheUsageSource)
	})

	t.Run("explicit aggregate zeros are reported", func(t *testing.T) {
		usage := parseClaudeUsageFromResponseBody([]byte(`{
			"usage":{
				"input_tokens":11,
				"output_tokens":4,
				"cache_creation_input_tokens":0,
				"cache_read_input_tokens":0
			}
		}`))

		require.Equal(t, CacheUsageSourceReported, usage.CacheUsageSource)
	})

	t.Run("explicit ttl zeros are reported", func(t *testing.T) {
		usage := parseClaudeUsageFromResponseBody([]byte(`{
			"usage":{
				"input_tokens":11,
				"output_tokens":4,
				"cache_creation":{
					"ephemeral_5m_input_tokens":0,
					"ephemeral_1h_input_tokens":0
				}
			}
		}`))

		require.Equal(t, CacheUsageSourceReported, usage.CacheUsageSource)
	})
}

func TestParseClaudeStreamingUsageWireMarksReportedOnlyWhenCacheFieldsExist(t *testing.T) {
	svc := &GatewayService{}

	t.Run("ordinary usage is unavailable", func(t *testing.T) {
		usage := &ClaudeUsage{}
		svc.parseSSEUsagePassthrough(`{
			"type":"message_delta",
			"usage":{"input_tokens":11,"output_tokens":4}
		}`, usage)

		require.Equal(t, CacheUsageSourceUnavailable, usage.CacheUsageSource)
	})

	t.Run("explicit cache zeros are reported", func(t *testing.T) {
		usage := &ClaudeUsage{}
		svc.parseSSEUsagePassthrough(`{
			"type":"message_delta",
			"usage":{
				"input_tokens":11,
				"output_tokens":4,
				"cache_creation_input_tokens":0,
				"cache_read_input_tokens":0,
				"cache_creation":{
					"ephemeral_5m_input_tokens":0,
					"ephemeral_1h_input_tokens":0
				}
			}
		}`, usage)

		require.Equal(t, CacheUsageSourceReported, usage.CacheUsageSource)
	})
}

func TestParseClaudeCompatStreamingUsageMarksReportedOnlyWhenCacheFieldsExist(t *testing.T) {
	svc := &GatewayService{}

	t.Run("ordinary usage is unavailable", func(t *testing.T) {
		usage := &ClaudeUsage{}
		svc.parseSSEUsage(`{
			"type":"message_delta",
			"usage":{"input_tokens":11,"output_tokens":4}
		}`, usage)

		require.Equal(t, CacheUsageSourceUnavailable, usage.CacheUsageSource)
	})

	t.Run("explicit cache zeros are reported", func(t *testing.T) {
		usage := &ClaudeUsage{}
		svc.parseSSEUsage(`{
			"type":"message_start",
			"message":{"usage":{
				"input_tokens":11,
				"cache_creation_input_tokens":0,
				"cache_read_input_tokens":0,
				"cache_creation":{
					"ephemeral_5m_input_tokens":0,
					"ephemeral_1h_input_tokens":0
				}
			}}
		}`, usage)

		require.Equal(t, CacheUsageSourceReported, usage.CacheUsageSource)
	})

	t.Run("later ordinary delta does not downgrade reported source", func(t *testing.T) {
		usage := &ClaudeUsage{}
		svc.parseSSEUsage(`{
			"type":"message_start",
			"message":{"usage":{"input_tokens":11,"cache_read_input_tokens":0}}
		}`, usage)
		svc.parseSSEUsage(`{
			"type":"message_delta",
			"usage":{"output_tokens":4}
		}`, usage)

		require.Equal(t, CacheUsageSourceReported, usage.CacheUsageSource)
	})
}

func TestParseOpenAIUsageMarksReportedOnlyWhenCacheFieldsExist(t *testing.T) {
	t.Run("ordinary usage is unavailable", func(t *testing.T) {
		usage, ok := extractOpenAIUsageFromJSONBytes([]byte(`{
			"usage":{"input_tokens":11,"output_tokens":4}
		}`))

		require.True(t, ok)
		require.Equal(t, CacheUsageSourceUnavailable, usage.CacheUsageSource)
	})

	t.Run("explicit cached zero is reported", func(t *testing.T) {
		usage, ok := extractOpenAIUsageFromJSONBytes([]byte(`{
			"usage":{
				"input_tokens":11,
				"output_tokens":4,
				"input_tokens_details":{"cached_tokens":0}
			}
		}`))

		require.True(t, ok)
		require.Equal(t, CacheUsageSourceReported, usage.CacheUsageSource)
		require.Zero(t, usage.CacheReadInputTokens)
	})
}

func TestClaudeUsageCacheAliasesAreDecodedNotJustDetected(t *testing.T) {
	// 检测（claudeUsageNodeHasCacheFields）认这些别名字段并标记 Reported，
	// 提取就必须能解码它们——否则"已上报"的非零用量会被记成 0。
	t.Run("non-streaming body decodes write alias and nested read", func(t *testing.T) {
		usage := parseClaudeUsageFromResponseBody([]byte(`{
			"usage":{
				"input_tokens":11,
				"output_tokens":4,
				"cache_write_tokens":120,
				"input_tokens_details":{"cached_tokens":64}
			}
		}`))

		require.Equal(t, CacheUsageSourceReported, usage.CacheUsageSource)
		require.Equal(t, 120, usage.CacheCreationInputTokens)
		require.Equal(t, 64, usage.CacheReadInputTokens)
	})

	t.Run("non-streaming body decodes nested ttl aliases", func(t *testing.T) {
		usage := parseClaudeUsageFromResponseBody([]byte(`{
			"usage":{
				"input_tokens":11,
				"output_tokens":4,
				"input_tokens_details":{
					"cache_creation_5m_tokens":80,
					"cache_creation_1h_tokens":40
				}
			}
		}`))

		require.Equal(t, CacheUsageSourceReported, usage.CacheUsageSource)
		require.Equal(t, 80, usage.CacheCreation5mTokens)
		require.Equal(t, 40, usage.CacheCreation1hTokens)
		require.Equal(t, 120, usage.CacheCreationInputTokens)
	})

	t.Run("passthrough stream decodes aliases", func(t *testing.T) {
		svc := &GatewayService{}
		usage := &ClaudeUsage{}
		svc.parseSSEUsagePassthrough(`{
			"type":"message_delta",
			"usage":{
				"output_tokens":4,
				"cache_write_input_tokens":96,
				"prompt_tokens_details":{"cached_tokens":32}
			}
		}`, usage)

		require.Equal(t, CacheUsageSourceReported, usage.CacheUsageSource)
		require.Equal(t, 96, usage.CacheCreationInputTokens)
		require.Equal(t, 32, usage.CacheReadInputTokens)
	})

	t.Run("compat stream decodes aliases", func(t *testing.T) {
		svc := &GatewayService{}
		usage := &ClaudeUsage{}
		svc.parseSSEUsage(`{
			"type":"message_start",
			"message":{"usage":{
				"input_tokens":11,
				"input_tokens_details":{"cached_tokens":64,"cache_creation_tokens":120}
			}}
		}`, usage)

		require.Equal(t, CacheUsageSourceReported, usage.CacheUsageSource)
		require.Equal(t, 64, usage.CacheReadInputTokens)
		require.Equal(t, 120, usage.CacheCreationInputTokens)
	})

	t.Run("canonical fields are never overridden by aliases", func(t *testing.T) {
		usage := parseClaudeUsageFromResponseBody([]byte(`{
			"usage":{
				"input_tokens":11,
				"output_tokens":4,
				"cache_creation_input_tokens":7,
				"cache_read_input_tokens":9,
				"cache_write_tokens":120,
				"cached_tokens":64
			}
		}`))

		require.Equal(t, 7, usage.CacheCreationInputTokens)
		require.Equal(t, 9, usage.CacheReadInputTokens)
	})
}

func TestMergeAnthropicUsageMarksReportedOnlyWhenCacheFieldsExist(t *testing.T) {
	var ordinary apicompat.AnthropicUsage
	require.NoError(t, json.Unmarshal([]byte(`{
		"input_tokens":11,
		"output_tokens":4
	}`), &ordinary))
	var ordinaryResult ClaudeUsage
	mergeAnthropicUsage(&ordinaryResult, ordinary)
	require.Equal(t, CacheUsageSourceUnavailable, ordinaryResult.CacheUsageSource)

	var explicitZero apicompat.AnthropicUsage
	require.NoError(t, json.Unmarshal([]byte(`{
		"input_tokens":11,
		"output_tokens":4,
		"cache_creation_input_tokens":0,
		"cache_read_input_tokens":0
	}`), &explicitZero))
	var reportedResult ClaudeUsage
	mergeAnthropicUsage(&reportedResult, explicitZero)
	require.Equal(t, CacheUsageSourceReported, reportedResult.CacheUsageSource)

	mergeAnthropicUsage(&reportedResult, ordinary)
	require.Equal(t, CacheUsageSourceReported, reportedResult.CacheUsageSource)
}
