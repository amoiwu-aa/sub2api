package cursor

import "strings"

// AgentUsageDetails captures request/response material that is sent outside the
// rendered prompt string. Cursor does not expose authoritative usage, so this
// remains explicitly estimated.
type AgentUsageDetails struct {
	Tools     []McpTool
	Images    []AttachedImage
	Thinking  string
	ToolCalls []ToolCall
}

func EstimateAgentInputTokens(prompt string, details AgentUsageDetails) int64 {
	estimated := EstimateTokens(prompt)
	var toolText strings.Builder
	for _, tool := range details.Tools {
		toolText.WriteString(tool.Name)
		toolText.WriteByte('\n')
		toolText.WriteString(tool.Description)
		toolText.WriteByte('\n')
		toolText.Write(tool.InputSchema)
		toolText.WriteByte('\n')
	}
	estimated += EstimateTokens(toolText.String())
	for _, image := range details.Images {
		if len(image.Data) > 0 {
			// Conservative byte-based approximation. It is deliberately marked
			// estimated by the billing layer and never reported as provider usage.
			estimated += int64((len(image.Data) + 3) / 4)
		}
	}
	return estimated
}

func EstimateAgentOutputTokens(text string, details AgentUsageDetails) int64 {
	var output strings.Builder
	output.WriteString(text)
	output.WriteByte('\n')
	output.WriteString(details.Thinking)
	for _, call := range details.ToolCalls {
		output.WriteByte('\n')
		output.WriteString(call.Name)
		output.WriteByte('\n')
		output.WriteString(call.Arguments)
	}
	return EstimateTokens(output.String())
}
