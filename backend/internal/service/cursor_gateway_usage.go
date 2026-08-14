package service

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
)

func cursorAgentUsageDetails(
	input cursor.AgentTurnInput,
	result *cursor.AgentTurnResult,
) cursor.AgentUsageDetails {
	details := cursor.AgentUsageDetails{
		Tools:  input.Tools,
		Images: input.Images,
	}
	if result != nil {
		details.Thinking = result.Thinking
		details.ToolCalls = result.ToolCalls
	}
	return details
}

func cursorEstimatedChatUsageChunk(
	id string,
	model string,
	created int64,
	prompt string,
	completion string,
	details ...cursor.AgentUsageDetails,
) cursor.OpenAIChunk {
	usageDetails := cursor.AgentUsageDetails{}
	if len(details) > 0 {
		usageDetails = details[0]
	}
	return cursor.NewOpenAIUsageChunk(
		id,
		model,
		created,
		cursor.EstimateAgentInputTokens(prompt, usageDetails),
		cursor.EstimateAgentOutputTokens(completion, usageDetails),
	)
}

func cursorEstimatedResponsesUsage(
	prompt string,
	completion string,
	details ...cursor.AgentUsageDetails,
) *apicompat.ResponsesUsage {
	usageDetails := cursor.AgentUsageDetails{}
	if len(details) > 0 {
		usageDetails = details[0]
	}
	inputTokens := int(cursor.EstimateAgentInputTokens(prompt, usageDetails))
	outputTokens := int(cursor.EstimateAgentOutputTokens(completion, usageDetails))
	return &apicompat.ResponsesUsage{
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		TotalTokens:  inputTokens + outputTokens,
	}
}
