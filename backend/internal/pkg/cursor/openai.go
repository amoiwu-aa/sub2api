package cursor

import (
	"encoding/json"
	"strings"
)

// OpenAI Chat Completions 兼容层。
//
// Cursor Agent 收的是一段纯文本 prompt，不是消息数组，所以整段对话要压平。
// 压平规则对照反代 agent-client.js 的 messagesToAgentText。

// OpenAIRequest 是 /v1/chat/completions 的请求体（只取需要的字段）。
type OpenAIRequest struct {
	Model    string          `json:"model"`
	Messages []OpenAIMessage `json:"messages"`
	Stream   bool            `json:"stream,omitempty"`
	// ConversationID 让客户端可以显式延续同一段 Agent 会话。
	ConversationID string `json:"conversation_id,omitempty"`
	Metadata       *struct {
		ConversationID string `json:"conversation_id,omitempty"`
	} `json:"metadata,omitempty"`
}

type OpenAIMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content,omitempty"`
}

// ResolveConversationID 取客户端指定的会话 id（顶层优先于 metadata）。
func (r *OpenAIRequest) ResolveConversationID() string {
	if r == nil {
		return ""
	}
	if id := strings.TrimSpace(r.ConversationID); id != "" {
		return id
	}
	if r.Metadata != nil {
		return strings.TrimSpace(r.Metadata.ConversationID)
	}
	return ""
}

// MessagesToAgentText 把消息数组压平成 Agent 要的单段 prompt。
//
// system 与 assistant 加角色前缀，让模型还能区分谁说的；只有一条消息且是
// system 时去掉前缀，避免把一句纯指令包装成看起来像转录的东西。
func MessagesToAgentText(messages []OpenAIMessage) string {
	parts := make([]string, 0, len(messages))
	for i := range messages {
		text := messageText(messages[i].Content)
		if text == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(messages[i].Role)) {
		case "system", "developer":
			parts = append(parts, "[system]\n"+text)
		case "assistant":
			parts = append(parts, "[assistant]\n"+text)
		default:
			parts = append(parts, text)
		}
	}
	if len(parts) == 1 {
		return strings.TrimPrefix(parts[0], "[system]\n")
	}
	return strings.Join(parts, "\n\n")
}

// messageText 接受 string 与 [{type:text,text}] 两种 content 形态。
func messageText(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err == nil {
			return text
		}
		return ""
	}

	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	var builder strings.Builder
	for _, block := range blocks {
		if block.Text != "" {
			if builder.Len() > 0 {
				builder.WriteString("\n")
			}
			builder.WriteString(block.Text)
		}
	}
	return builder.String()
}

// OpenAIChunk 是一条 chat.completion.chunk。
type OpenAIChunk struct {
	ID      string              `json:"id"`
	Object  string              `json:"object"`
	Created int64               `json:"created"`
	Model   string              `json:"model"`
	Choices []OpenAIChunkChoice `json:"choices"`
}

type OpenAIChunkChoice struct {
	Index        int         `json:"index"`
	Delta        OpenAIDelta `json:"delta"`
	FinishReason *string     `json:"finish_reason"`
}

type OpenAIDelta struct {
	Role             string `json:"role,omitempty"`
	Content          string `json:"content,omitempty"`
	ReasoningContent string `json:"reasoning_content,omitempty"`
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
	Role             string `json:"role"`
	Content          string `json:"content"`
	ReasoningContent string `json:"reasoning_content,omitempty"`
}

// OpenAIUsage 的数值是本地估算的：Cursor 上游不返回 token 用量。
type OpenAIUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
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

// NewOpenAIReasoningChunk 构造一条思考增量（DeepSeek 风格 reasoning_content）。
// Cursor Agent 的 thinking_delta 必须外露，否则客户端会一直停在「思考中」。
func NewOpenAIReasoningChunk(id, model string, created int64, reasoning string) OpenAIChunk {
	return OpenAIChunk{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []OpenAIChunkChoice{{Index: 0, Delta: OpenAIDelta{ReasoningContent: reasoning}}},
	}
}

// NewOpenAIRoleChunk 构造首帧 role=assistant，方便流式客户端尽早进入接收态。
func NewOpenAIRoleChunk(id, model string, created int64) OpenAIChunk {
	return OpenAIChunk{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []OpenAIChunkChoice{{Index: 0, Delta: OpenAIDelta{Role: "assistant"}}},
	}
}

// NewOpenAIFinalChunk 构造收尾的空增量（带 finish_reason）。
func NewOpenAIFinalChunk(id, model string, created int64, finishReason string) OpenAIChunk {
	reason := finishReason
	return OpenAIChunk{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []OpenAIChunkChoice{{Index: 0, Delta: OpenAIDelta{}, FinishReason: &reason}},
	}
}

// EstimateTokens 是一个粗略的 token 估算。
//
// Cursor 的 Agent 上游既不返回 token 用量也没有可查的计量接口，而计费与
// 平台配额都要求成本非 0（否则限额是个静默失效的开关）。这里用「字符数 / 4」
// 这个业界通行的近似——它会低估 CJK、高估代码，但对按量分摊够用了。
// 口径的最终决定见 Phase 9 的注释。
func EstimateTokens(text string) int64 {
	if text == "" {
		return 0
	}
	// 按 rune 计数而不是字节：UTF-8 下中文一个字符占 3 字节，
	// 按字节算会把中文对话的 token 数抬高约三倍。
	runes := int64(len([]rune(text)))
	estimated := runes / 4
	if estimated < 1 {
		return 1
	}
	return estimated
}
