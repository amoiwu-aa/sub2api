package cursor

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEstimateAgentUsageIncludesOutOfBandToolsImagesThinkingAndCalls(t *testing.T) {
	baseInput := EstimateAgentInputTokens("prompt", AgentUsageDetails{})
	fullInput := EstimateAgentInputTokens("prompt", AgentUsageDetails{
		Tools: []McpTool{{
			Name:        "Read",
			Description: "read a file",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
		}},
		Images: []AttachedImage{{Data: make([]byte, 400), MIMEType: "image/png"}},
	})
	require.Greater(t, fullInput, baseInput)

	baseOutput := EstimateAgentOutputTokens("answer", AgentUsageDetails{})
	fullOutput := EstimateAgentOutputTokens("answer", AgentUsageDetails{
		Thinking: "reasoning",
		ToolCalls: []ToolCall{{
			Name: "Read", Arguments: `{"path":"a.md"}`,
		}},
	})
	require.Greater(t, fullOutput, baseOutput)
}
