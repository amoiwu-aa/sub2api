//go:build unit

package kiro

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEstimateTokens(t *testing.T) {
	require.Equal(t, 0, EstimateTokens(""))
	// 非空但很短的内容至少记 1，不能因为整除到 0 就变成"免费"。
	require.Equal(t, 1, EstimateTokens("ab"))
	require.Equal(t, 4, EstimateTokens("0123456789abcdef"))
	// 按 rune 计数：中文一个字符占 3 字节，按字节算会把估算抬高约三倍。
	require.Equal(t, 3, EstimateTokens("中文测试内容十二字符啊哈"))
}

func TestEstimateConversationTokensCoversHistoryToolsAndResults(t *testing.T) {
	require.Equal(t, 0, EstimateConversationTokens(nil))

	state := &ConversationState{
		History: []ChatMessage{
			{UserInputMessage: &UserInputMessage{Content: "0123456789abcdef"}},
			{AssistantResponseMessage: &AssistantResponseMessage{
				Content: "0123456789abcdef",
				ToolUses: []ToolUse{{
					Name:  "read_file",
					Input: json.RawMessage(`{"path":"/tmp/a"}`),
				}},
			}},
		},
		CurrentMessage: ChatMessage{UserInputMessage: &UserInputMessage{
			Content: "0123456789abcdef",
			UserInputMessageContext: &UserInputMessageContext{
				ToolResults: []ToolResult{{
					ToolUseID: "t1",
					Content:   []ToolResultContentBlock{{Text: "0123456789abcdef"}},
				}},
				Tools: []Tool{{ToolSpecification: ToolSpecification{
					Name:        "read_file",
					Description: "reads a file from disk",
					InputSchema: ToolInputSchema{JSON: json.RawMessage(`{"type":"object"}`)},
				}}},
			},
		}},
	}

	got := EstimateConversationTokens(state)

	// 三条消息各 +4 的固定开销，加上各段内容的 len/4。
	require.Greater(t, got, 3*perMessageOverhead,
		"per-message overhead alone must not be the whole estimate")

	// 关键不变式：工具定义、tool_result、历史 tool_use 都必须计入，
	// 否则一个纯工具轮会被估成几乎零输入。
	lean := &ConversationState{
		CurrentMessage: ChatMessage{UserInputMessage: &UserInputMessage{Content: "0123456789abcdef"}},
	}
	require.Greater(t, got, EstimateConversationTokens(lean)*2)
}

// metadataEvent 缺失是常态（工具轮、被中断的轮），此时估算必须给出非零输出，
// 否则整轮不计费、平台 USD 限额静默失效。
func TestEstimatedOutputTokensCountsThinkingAndToolUse(t *testing.T) {
	tr := NewResponseTranslator("msg", "kiro/claude-sonnet-4.6", nil)
	require.NoError(t, tr.Handle(StreamEvent{
		ReasoningContent: &ReasoningContentEvent{Text: "thinking hard about the problem here"},
	}))
	require.NoError(t, tr.Handle(StreamEvent{
		AssistantResponse: &AssistantResponseEvent{Content: "0123456789abcdef"},
	}))
	require.NoError(t, tr.Finish())

	require.False(t, tr.HasUpstreamUsage(), "no metadataEvent was delivered")
	require.Positive(t, tr.EstimatedOutputTokens(),
		"a turn with visible output must never estimate to zero tokens")

	// thinking 也是模型生成的 token，不能只算可见文本。
	textOnly := NewResponseTranslator("msg2", "kiro/claude-sonnet-4.6", nil)
	require.NoError(t, textOnly.Handle(StreamEvent{
		AssistantResponse: &AssistantResponseEvent{Content: "0123456789abcdef"},
	}))
	require.NoError(t, textOnly.Finish())
	require.Greater(t, tr.EstimatedOutputTokens(), textOnly.EstimatedOutputTokens())
}
