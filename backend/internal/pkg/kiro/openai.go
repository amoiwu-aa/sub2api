package kiro

import (
	"encoding/json"
	"strings"
)

// OpenAI Chat Completions 兼容层。
//
// 与 Anthropic 路径的区别（对齐 kiro-proxy server.js）：这条路只透出文本，
// thinking 与 tool_use 都不外露，因此也不把 tools 发给上游——发了却不回传
// tool_calls 会让客户端一直等一个永远不来的工具调用。

// OpenAIRequest 是 /v1/chat/completions 的请求体（只取转换需要的字段）。
type OpenAIRequest struct {
	Model    string          `json:"model"`
	Messages []OpenAIMessage `json:"messages"`
	Stream   bool            `json:"stream,omitempty"`
}

type OpenAIMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content,omitempty"`
	Name       string          `json:"name,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	ToolCalls  []struct {
		ID       string `json:"id"`
		Type     string `json:"type"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls,omitempty"`
}

// ToAnthropicRequest 把 OpenAI 请求折成 Anthropic 形状，从而复用
// BuildConversationState 那一套会话组装规则（system 注入、末尾补 user 等）。
func (r *OpenAIRequest) ToAnthropicRequest() *AnthropicRequest {
	if r == nil {
		return &AnthropicRequest{}
	}

	converted := &AnthropicRequest{Model: r.Model, Stream: r.Stream}
	systemParts := make([]string, 0, 1)

	for i := range r.Messages {
		message := &r.Messages[i]
		switch strings.ToLower(strings.TrimSpace(message.Role)) {
		case "system", "developer":
			if text := openAIMessageText(message.Content); text != "" {
				systemParts = append(systemParts, text)
			}
		case "tool":
			// 工具结果折成 user 文本。整条丢弃会打断 user/assistant 交替，
			// 让上游看到一段前后不接的对话。
			text := openAIMessageText(message.Content)
			if text == "" {
				continue
			}
			converted.Messages = append(converted.Messages, AnthropicMessage{
				Role:    "user",
				Content: textContentBlock("Tool result: " + text),
			})
		case "assistant":
			text := openAIMessageText(message.Content)
			// tool_calls 折成文本，保留「助手在这一轮调用了工具」这个事实。
			for _, call := range message.ToolCalls {
				summary := "Called tool " + call.Function.Name
				if args := strings.TrimSpace(call.Function.Arguments); args != "" {
					summary += " with " + args
				}
				if text != "" {
					text += "\n"
				}
				text += summary
			}
			if text == "" {
				continue
			}
			converted.Messages = append(converted.Messages, AnthropicMessage{
				Role:    "assistant",
				Content: textContentBlock(text),
			})
		default:
			converted.Messages = append(converted.Messages, AnthropicMessage{
				Role:    "user",
				Content: message.Content,
			})
		}
	}

	if len(systemParts) > 0 {
		converted.System = textJSON(strings.Join(systemParts, "\n"))
	}
	return converted
}

// openAIMessageText 把 string 与 [{type:text,text}] 两种 content 归一为文本。
func openAIMessageText(raw json.RawMessage) string {
	blocks, err := parseContentBlocks(raw)
	if err != nil {
		return ""
	}
	return extractText(blocks)
}

func textContentBlock(text string) json.RawMessage {
	return textJSON(text)
}

func textJSON(text string) json.RawMessage {
	encoded, err := json.Marshal(text)
	if err != nil {
		return json.RawMessage(`""`)
	}
	return encoded
}

// OpenAIChunk 是一条 chat.completion.chunk。
type OpenAIChunk struct {
	ID      string              `json:"id"`
	Object  string              `json:"object"`
	Created int64               `json:"created"`
	Model   string              `json:"model"`
	Choices []OpenAIChunkChoice `json:"choices"`
	Usage   *OpenAIUsage        `json:"usage,omitempty"`
}

type OpenAIChunkChoice struct {
	Index        int              `json:"index"`
	Delta        OpenAIDelta      `json:"delta"`
	FinishReason *string          `json:"finish_reason"`
	Logprobs     *json.RawMessage `json:"logprobs,omitempty"`
}

type OpenAIDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

type OpenAIUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

// OpenAIResponse 是非流式 chat.completion 响应。
type OpenAIResponse struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []OpenAIChoice `json:"choices"`
	Usage   OpenAIUsage    `json:"usage"`
}

type OpenAIChoice struct {
	Index        int                 `json:"index"`
	Message      OpenAIChoiceMessage `json:"message"`
	FinishReason string              `json:"finish_reason"`
}

type OpenAIChoiceMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// NewOpenAIChunk 构造一条内容增量。
func NewOpenAIChunk(id, model string, created int64, content string) OpenAIChunk {
	return OpenAIChunk{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []OpenAIChunkChoice{{Index: 0, Delta: OpenAIDelta{Content: content}}},
	}
}

// NewOpenAIFinalChunk 构造收尾的空增量（带 finish_reason）。
func NewOpenAIFinalChunk(id, model string, created int64, finishReason string, usage *OpenAIUsage) OpenAIChunk {
	reason := finishReason
	return OpenAIChunk{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []OpenAIChunkChoice{{Index: 0, Delta: OpenAIDelta{}, FinishReason: &reason}},
		Usage:   usage,
	}
}
