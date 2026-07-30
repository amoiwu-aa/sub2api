package cursor

import (
	"encoding/json"
	"strings"
)

// Anthropic Messages 兼容层。
//
// Claude Code 只说 Anthropic 协议，而 Cursor Agent 收的是一段纯文本 prompt。
// 这里把 Messages 请求归一化成 Conversation（与 chat/completions、responses
// 三方共用），再把 Agent 的输出翻译回 Messages 的 SSE 事件序列。

// AnthropicRequest 是 /v1/messages 的请求体（只取需要的字段）。
type AnthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens,omitempty"`
	System    json.RawMessage    `json:"system,omitempty"`
	Messages  []AnthropicMessage `json:"messages"`
	Tools     []AnthropicTool    `json:"tools,omitempty"`
	// ToolChoice 只用来识别 {"type":"none"}；Cursor 没有强制调某个工具的开关。
	ToolChoice json.RawMessage `json:"tool_choice,omitempty"`
	Stream     bool            `json:"stream,omitempty"`
	Metadata   *struct {
		ConversationID string `json:"conversation_id,omitempty"`
		UserID         string `json:"user_id,omitempty"`
	} `json:"metadata,omitempty"`
}

type AnthropicMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type AnthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
	// Type 出现在 Anthropic 的内置工具上（如 computer_20241022）。
	// 那些工具由 Anthropic 侧执行，转给 Cursor 没有意义，直接跳过。
	Type string `json:"type,omitempty"`
}

// anthropicContentBlock 覆盖入站请求里会出现的全部块类型。
type anthropicContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// tool_result
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

// ResolveConversationID 取 metadata 里的会话 id。
func (r *AnthropicRequest) ResolveConversationID() string {
	if r == nil || r.Metadata == nil {
		return ""
	}
	return strings.TrimSpace(r.Metadata.ConversationID)
}

// McpTools 把 Anthropic 的工具声明转成 MCP 工具定义。
func (r *AnthropicRequest) McpTools() []McpTool {
	if r == nil || len(r.Tools) == 0 || r.toolsDisabled() {
		return nil
	}
	tools := make([]McpTool, 0, len(r.Tools))
	for _, tool := range r.Tools {
		// 带 type 的是 Anthropic 自家的服务端工具，Cursor 执行不了。
		if strings.TrimSpace(tool.Type) != "" {
			continue
		}
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			continue
		}
		tools = append(tools, McpTool{
			Name:        name,
			Description: tool.Description,
			InputSchema: tool.InputSchema,
		})
	}
	return tools
}

func (r *AnthropicRequest) toolsDisabled() bool {
	trimmed := strings.TrimSpace(string(r.ToolChoice))
	if trimmed == "" {
		return false
	}
	var choice struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(r.ToolChoice, &choice); err != nil {
		return false
	}
	return choice.Type == "none"
}

// Conversation 把 Messages 请求归一化。
//
// Anthropic 把工具结果塞在 user 消息的内容块里，而不是单独的 role。
// 这里要把它拆成独立的 RoleTool，否则重放出来的历史看不出哪段是工具输出。
func (r *AnthropicRequest) Conversation() *Conversation {
	if r == nil {
		return &Conversation{}
	}
	turns := make([]Turn, 0, len(r.Messages)+1)
	if system := anthropicText(r.System); system != "" {
		turns = append(turns, Turn{Role: RoleSystem, Text: system})
	}
	for i := range r.Messages {
		turns = append(turns, anthropicMessageToTurns(r.Messages[i])...)
	}
	return &Conversation{Turns: turns, Tools: r.McpTools()}
}

func anthropicMessageToTurns(message AnthropicMessage) []Turn {
	role := RoleUser
	if strings.EqualFold(strings.TrimSpace(message.Role), "assistant") {
		role = RoleAssistant
	}

	blocks, ok := anthropicBlocks(message.Content)
	if !ok {
		return []Turn{{Role: role, Text: anthropicText(message.Content)}}
	}

	turns := make([]Turn, 0, 2)
	main := Turn{Role: role}
	texts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if block.Text != "" {
				texts = append(texts, block.Text)
			}
		case "tool_use":
			main.ToolCalls = append(main.ToolCalls, ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: string(block.Input),
			})
		case "tool_result":
			// 工具结果先独立成一条，顺序要排在同消息的文本之前：
			// 客户端把结果和后续追问塞在同一条 user 消息里，
			// 反过来渲染会让模型以为先有追问再有结果。
			result := Turn{
				Role:       RoleTool,
				ToolCallID: block.ToolUseID,
				Text:       anthropicText(block.Content),
			}
			if block.IsError {
				result.Text = "[tool error] " + result.Text
			}
			turns = append(turns, result)
		}
	}

	main.Text = strings.Join(texts, "\n")
	if main.Text != "" || len(main.ToolCalls) > 0 {
		turns = append(turns, main)
	}
	return turns
}

// anthropicBlocks 解析内容块数组；content 是裸字符串时返回 false。
func anthropicBlocks(raw json.RawMessage) ([]anthropicContentBlock, bool) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed[0] != '[' {
		return nil, false
	}
	var blocks []anthropicContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, false
	}
	return blocks, true
}

// anthropicText 从 string / 块数组 / 单个块里提取纯文本。
func anthropicText(raw json.RawMessage) string {
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
	if blocks, ok := anthropicBlocks(raw); ok {
		parts := make([]string, 0, len(blocks))
		for _, block := range blocks {
			if block.Text != "" {
				parts = append(parts, block.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	// 单个对象：可能是 {"type":"text","text":"..."}，也可能是任意 JSON
	// （tool_result 的 content 允许是结构化数据）。后者原样交给模型，
	// 丢掉比给一段它能读懂的 JSON 更糟。
	var block anthropicContentBlock
	if err := json.Unmarshal(raw, &block); err == nil && block.Text != "" {
		return block.Text
	}
	return trimmed
}

// ---------------------------------------------------------------------------
// 出站：Messages 响应与 SSE 事件
// ---------------------------------------------------------------------------

// AnthropicResponse 是非流式 /v1/messages 响应。
type AnthropicResponse struct {
	ID           string                   `json:"id"`
	Type         string                   `json:"type"`
	Role         string                   `json:"role"`
	Model        string                   `json:"model"`
	Content      []AnthropicResponseBlock `json:"content"`
	StopReason   string                   `json:"stop_reason"`
	StopSequence *string                  `json:"stop_sequence"`
	Usage        AnthropicUsage           `json:"usage"`
}

type AnthropicResponseBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type AnthropicUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

// StopReasonToolUse 是 Anthropic 侧「停在工具调用上」的标记。
const StopReasonToolUse = "tool_use"

// NewAnthropicToolUseID 把上游调用标识规整成 Anthropic 风格的 tool_use id。
func NewAnthropicToolUseID(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "toolu_unknown"
	}
	if strings.HasPrefix(trimmed, "toolu_") {
		return trimmed
	}
	return "toolu_" + strings.ReplaceAll(strings.TrimPrefix(trimmed, "call_"), "-", "")
}

// NewAnthropicContent 把一轮结果组装成 Messages 的内容块。
//
// 刻意不输出 thinking 块：Anthropic 的 thinking 需要配一个上游签名，
// 我们给不出。伪造一个签名会让 Claude Code 在下一轮把它回传时被自己的
// 校验拦下，那比不显示思考过程严重得多。
func NewAnthropicContent(text string, calls []ToolCall) []AnthropicResponseBlock {
	blocks := make([]AnthropicResponseBlock, 0, len(calls)+1)
	if strings.TrimSpace(text) != "" {
		blocks = append(blocks, AnthropicResponseBlock{Type: "text", Text: text})
	}
	for _, call := range calls {
		blocks = append(blocks, AnthropicResponseBlock{
			Type:  "tool_use",
			ID:    NewAnthropicToolUseID(call.ID),
			Name:  call.Name,
			Input: json.RawMessage(defaultToolArguments(call.Arguments)),
		})
	}
	// content 不能是空数组：部分客户端会当成协议错误。
	if len(blocks) == 0 {
		blocks = append(blocks, AnthropicResponseBlock{Type: "text", Text: ""})
	}
	return blocks
}

// AnthropicEvent 是一条待写出的 SSE 事件。
type AnthropicEvent struct {
	Event string
	Data  any
}

// NewAnthropicMessageStart 构造 message_start。
func NewAnthropicMessageStart(id, model string, inputTokens int64) AnthropicEvent {
	return AnthropicEvent{
		Event: "message_start",
		Data: map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":            id,
				"type":          "message",
				"role":          "assistant",
				"model":         model,
				"content":       []any{},
				"stop_reason":   nil,
				"stop_sequence": nil,
				"usage":         map[string]any{"input_tokens": inputTokens, "output_tokens": 0},
			},
		},
	}
}

// NewAnthropicTextBlockStart 构造一个文本块的开头。
func NewAnthropicTextBlockStart(index int) AnthropicEvent {
	return AnthropicEvent{
		Event: "content_block_start",
		Data: map[string]any{
			"type":          "content_block_start",
			"index":         index,
			"content_block": map[string]any{"type": "text", "text": ""},
		},
	}
}

// NewAnthropicTextDelta 构造一条文本增量。
func NewAnthropicTextDelta(index int, text string) AnthropicEvent {
	return AnthropicEvent{
		Event: "content_block_delta",
		Data: map[string]any{
			"type":  "content_block_delta",
			"index": index,
			"delta": map[string]any{"type": "text_delta", "text": text},
		},
	}
}

// NewAnthropicToolUseBlockStart 构造一个 tool_use 块的开头。
func NewAnthropicToolUseBlockStart(index int, call ToolCall) AnthropicEvent {
	return AnthropicEvent{
		Event: "content_block_start",
		Data: map[string]any{
			"type":  "content_block_start",
			"index": index,
			"content_block": map[string]any{
				"type":  "tool_use",
				"id":    NewAnthropicToolUseID(call.ID),
				"name":  call.Name,
				"input": map[string]any{},
			},
		},
	}
}

// NewAnthropicToolUseDelta 把入参作为 input_json_delta 一次性发出。
// 上游给的是完整入参，没有逐字符的流，所以一帧发完即可。
func NewAnthropicToolUseDelta(index int, arguments string) AnthropicEvent {
	return AnthropicEvent{
		Event: "content_block_delta",
		Data: map[string]any{
			"type":  "content_block_delta",
			"index": index,
			"delta": map[string]any{
				"type":         "input_json_delta",
				"partial_json": defaultToolArguments(arguments),
			},
		},
	}
}

// NewAnthropicBlockStop 构造 content_block_stop。
func NewAnthropicBlockStop(index int) AnthropicEvent {
	return AnthropicEvent{
		Event: "content_block_stop",
		Data:  map[string]any{"type": "content_block_stop", "index": index},
	}
}

// NewAnthropicMessageDelta 构造收尾的 message_delta（带 stop_reason 与出参用量）。
func NewAnthropicMessageDelta(stopReason string, outputTokens int64) AnthropicEvent {
	return AnthropicEvent{
		Event: "message_delta",
		Data: map[string]any{
			"type":  "message_delta",
			"delta": map[string]any{"stop_reason": stopReason, "stop_sequence": nil},
			"usage": map[string]any{"output_tokens": outputTokens},
		},
	}
}

// NewAnthropicMessageStop 构造 message_stop。
func NewAnthropicMessageStop() AnthropicEvent {
	return AnthropicEvent{
		Event: "message_stop",
		Data:  map[string]any{"type": "message_stop"},
	}
}

// NewAnthropicPing 构造心跳事件，避免长时间静默被中间代理掐断。
func NewAnthropicPing() AnthropicEvent {
	return AnthropicEvent{Event: "ping", Data: map[string]any{"type": "ping"}}
}
