package service

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
)

func cursorEstimatedChatUsageChunk(
	id string,
	model string,
	created int64,
	prompt string,
	completion string,
) cursor.OpenAIChunk {
	return cursor.NewOpenAIUsageChunk(
		id,
		model,
		created,
		cursor.EstimateTokens(prompt),
		cursor.EstimateTokens(completion),
	)
}

func cursorEstimatedResponsesUsage(prompt string, completion string) *apicompat.ResponsesUsage {
	inputTokens := int(cursor.EstimateTokens(prompt))
	outputTokens := int(cursor.EstimateTokens(completion))
	return &apicompat.ResponsesUsage{
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		TotalTokens:  inputTokens + outputTokens,
	}
}
