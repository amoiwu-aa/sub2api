package kiro

import (
	"encoding/json"
	"strings"
)

// 本文件把 Q 的事件流翻译成 Anthropic Messages API 的响应形状，
// 流式（SSE 序列）与非流式（单个 JSON）共用同一个累积器。

const (
	stopReasonEndTurn = "end_turn"
	stopReasonToolUse = "tool_use"
)

// SSEEvent 是一条待写出的 Anthropic SSE 事件。
type SSEEvent struct {
	Event string
	Data  any
}

// AnthropicUsage 是 Anthropic 响应里的 usage 段。
type AnthropicUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens,omitempty"`
}

// AnthropicContentItem 是非流式响应里的一个 content 块。
type AnthropicContentItem struct {
	Type string `json:"type"`

	Text string `json:"text,omitempty"`

	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`

	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

// AnthropicResponse 是非流式 /v1/messages 的响应体。
type AnthropicResponse struct {
	ID           string                 `json:"id"`
	Type         string                 `json:"type"`
	Role         string                 `json:"role"`
	Model        string                 `json:"model"`
	Content      []AnthropicContentItem `json:"content"`
	StopReason   string                 `json:"stop_reason"`
	StopSequence *string                `json:"stop_sequence"`
	Usage        AnthropicUsage         `json:"usage"`
}

// blockKind 标记当前打开的 content block 类型。
type blockKind int

const (
	blockNone blockKind = iota
	blockThinking
	blockText
	blockToolUse
)

// ResponseTranslator 把 Q 事件累积成 Anthropic 的内容块序列。
//
// 上游的文本、thinking 与 tool_use 是交错分片投递的，客户端要求同一类内容
// 连续成块，所以这里维护「当前打开的块」，类型切换时才关闭并开新块。
type ResponseTranslator struct {
	messageID string
	model     string

	emit func(SSEEvent) error

	current    blockKind
	blockIndex int
	blockOpen  bool

	text      strings.Builder
	thinking  strings.Builder
	signature string

	toolUseID    string
	toolName     string
	toolInput    strings.Builder
	sawToolUse   bool
	contentItems []AnthropicContentItem

	usage        AnthropicUsage
	sawUsage     bool
	meteringUnit float64

	started bool
}

// NewResponseTranslator 创建累积器。emit 为 nil 时只累积不产出 SSE（非流式路径）。
func NewResponseTranslator(messageID, model string, emit func(SSEEvent) error) *ResponseTranslator {
	return &ResponseTranslator{
		messageID:  messageID,
		model:      model,
		emit:       emit,
		blockIndex: -1,
	}
}

// Start 产出 message_start 与 ping。非流式路径无需调用。
//
// 幂等且**必须晚发**：写出 message_start 就等于把响应头交给了客户端，此后再也不能
// 换账号重试。调用方应该在收到上游第一个事件时才调它（Handle / Finish 内部会自动
// 触发），这样「上游还没吐第一个字节就失败」仍然落在可 failover 的窗口里。
func (t *ResponseTranslator) Start() error {
	if t.emit == nil || t.started {
		return nil
	}
	t.started = true
	if err := t.emit(SSEEvent{Event: "message_start", Data: map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            t.messageID,
			"type":          "message",
			"role":          "assistant",
			"model":         t.model,
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage":         AnthropicUsage{},
		},
	}}); err != nil {
		return err
	}
	return t.emit(SSEEvent{Event: "ping", Data: map[string]any{"type": "ping"}})
}

// Handle 消费一个上游事件。
func (t *ResponseTranslator) Handle(event StreamEvent) error {
	switch {
	case event.ReasoningContent != nil:
		if err := t.Start(); err != nil {
			return err
		}
		return t.handleReasoning(event.ReasoningContent)
	case event.AssistantResponse != nil:
		if err := t.Start(); err != nil {
			return err
		}
		return t.handleText(event.AssistantResponse)
	case event.ToolUse != nil:
		if err := t.Start(); err != nil {
			return err
		}
		return t.handleToolUse(event.ToolUse)
	case event.Metadata != nil && event.Metadata.TokenUsage != nil:
		t.applyTokenUsage(event.Metadata.TokenUsage)
	case event.Metering != nil:
		t.meteringUnit = event.Metering.Usage
	}
	return nil
}

func (t *ResponseTranslator) handleReasoning(event *ReasoningContentEvent) error {
	// signature 可能在 thinking 块已关闭后才到；此时只记录，最终块里带上。
	if event.Signature != "" {
		t.signature = event.Signature
		if t.current == blockThinking && t.blockOpen {
			if err := t.emitDelta(map[string]any{"type": "signature_delta", "signature": event.Signature}); err != nil {
				return err
			}
		}
	}
	if event.Text == "" {
		return nil
	}
	if err := t.switchBlock(blockThinking); err != nil {
		return err
	}
	t.thinking.WriteString(event.Text)
	return t.emitDelta(map[string]any{"type": "thinking_delta", "thinking": event.Text})
}

func (t *ResponseTranslator) handleText(event *AssistantResponseEvent) error {
	if event.Content == "" {
		return nil
	}
	if err := t.switchBlock(blockText); err != nil {
		return err
	}
	t.text.WriteString(event.Content)
	return t.emitDelta(map[string]any{"type": "text_delta", "text": event.Content})
}

func (t *ResponseTranslator) handleToolUse(event *ToolUseEvent) error {
	// 新的 toolUseId 意味着上一个工具调用结束（上游不保证每个都带 stop）。
	if event.ToolUseID != "" && event.ToolUseID != t.toolUseID {
		if err := t.closeBlock(); err != nil {
			return err
		}
		t.toolUseID = event.ToolUseID
		t.toolName = event.Name
		t.toolInput.Reset()
		if err := t.switchBlock(blockToolUse); err != nil {
			return err
		}
	} else if event.Name != "" && t.toolName == "" {
		t.toolName = event.Name
	}

	if event.Input != "" {
		t.toolInput.WriteString(event.Input)
		if err := t.emitDelta(map[string]any{"type": "input_json_delta", "partial_json": event.Input}); err != nil {
			return err
		}
	}
	if event.Stop {
		return t.closeBlock()
	}
	return nil
}

// switchBlock 在类型变化时关闭旧块、打开新块。
func (t *ResponseTranslator) switchBlock(kind blockKind) error {
	if t.blockOpen && t.current == kind {
		return nil
	}
	if err := t.closeBlock(); err != nil {
		return err
	}
	t.current = kind
	t.blockIndex++
	t.blockOpen = true

	if t.emit == nil {
		return nil
	}
	var block map[string]any
	switch kind {
	case blockThinking:
		block = map[string]any{"type": "thinking", "thinking": "", "signature": ""}
	case blockText:
		block = map[string]any{"type": "text", "text": ""}
	case blockToolUse:
		block = map[string]any{"type": "tool_use", "id": t.toolUseID, "name": t.toolName, "input": map[string]any{}}
	default:
		return nil
	}
	return t.emit(SSEEvent{Event: "content_block_start", Data: map[string]any{
		"type":          "content_block_start",
		"index":         t.blockIndex,
		"content_block": block,
	}})
}

func (t *ResponseTranslator) closeBlock() error {
	if !t.blockOpen {
		return nil
	}
	switch t.current {
	case blockThinking:
		t.contentItems = append(t.contentItems, AnthropicContentItem{
			Type: "thinking", Thinking: t.thinking.String(), Signature: t.signature,
		})
		t.thinking.Reset()
	case blockText:
		t.contentItems = append(t.contentItems, AnthropicContentItem{Type: "text", Text: t.text.String()})
		t.text.Reset()
	case blockToolUse:
		t.sawToolUse = true
		t.contentItems = append(t.contentItems, AnthropicContentItem{
			Type: "tool_use", ID: t.toolUseID, Name: t.toolName, Input: parseToolInput(t.toolInput.String()),
		})
		t.toolInput.Reset()
		t.toolUseID = ""
		t.toolName = ""
	}

	t.blockOpen = false
	t.current = blockNone
	if t.emit == nil {
		return nil
	}
	return t.emit(SSEEvent{Event: "content_block_stop", Data: map[string]any{
		"type": "content_block_stop", "index": t.blockIndex,
	}})
}

// Finish 关闭未完的块并产出 message_delta / message_stop。
func (t *ResponseTranslator) Finish() error {
	// 上游一个事件都没给（合法的空回复）时 Start 还没被触发过，
	// 这里补上，保证流出去的永远是一段完整的 message_start..message_stop。
	if err := t.Start(); err != nil {
		return err
	}
	if err := t.closeBlock(); err != nil {
		return err
	}
	if t.emit == nil {
		return nil
	}
	if err := t.emit(SSEEvent{Event: "message_delta", Data: map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": t.StopReason(), "stop_sequence": nil},
		"usage": t.usage,
	}}); err != nil {
		return err
	}
	return t.emit(SSEEvent{Event: "message_stop", Data: map[string]any{"type": "message_stop"}})
}

func (t *ResponseTranslator) emitDelta(delta map[string]any) error {
	if t.emit == nil {
		return nil
	}
	return t.emit(SSEEvent{Event: "content_block_delta", Data: map[string]any{
		"type": "content_block_delta", "index": t.blockIndex, "delta": delta,
	}})
}

func (t *ResponseTranslator) applyTokenUsage(usage *TokenUsage) {
	t.sawUsage = true
	t.usage = AnthropicUsage{
		InputTokens:              usage.UncachedInputTokens,
		OutputTokens:             usage.OutputTokens,
		CacheReadInputTokens:     usage.CacheReadInputTokens,
		CacheCreationInputTokens: usage.CacheWriteInputTokens,
	}
}

// StopReason 只有 end_turn 与 tool_use 两种：上游不返回 max_tokens 之类的信号。
func (t *ResponseTranslator) StopReason() string {
	if t.sawToolUse {
		return stopReasonToolUse
	}
	return stopReasonEndTurn
}

// Usage 返回上游报告的 token 用量。HasUpstreamUsage 为 false 时调用方需自行估算。
func (t *ResponseTranslator) Usage() AnthropicUsage { return t.usage }

// HasUpstreamUsage 报告上游是否给出了 tokenUsage。
func (t *ResponseTranslator) HasUpstreamUsage() bool { return t.sawUsage }

// MeteringUsage 是上游在 meteringEvent 里下发的计费单位数（credit）。
//
// 它和 token 不是一个量纲，**不能**拿来替代 token 计费，所以不参与
// ForwardResult.Usage 的计算。它的用处是对账：这是上游对本次请求的权威
// 扣费口径，与账号额度接口（GetUsageLimits）里的 currentUsage 同源，
// 可以用来验证本地的 token 估算有没有跑偏。
func (t *ResponseTranslator) MeteringUsage() float64 { return t.meteringUnit }

// Response 组装非流式响应。必须在 Finish 之后调用。
func (t *ResponseTranslator) Response() *AnthropicResponse {
	content := t.contentItems
	if content == nil {
		content = []AnthropicContentItem{}
	}
	return &AnthropicResponse{
		ID:         t.messageID,
		Type:       "message",
		Role:       "assistant",
		Model:      t.model,
		Content:    content,
		StopReason: t.StopReason(),
		Usage:      t.usage,
	}
}

// TextContent 返回累计的纯文本，供 OpenAI 兼容路径与本地 token 估算使用。
func (t *ResponseTranslator) TextContent() string {
	var builder strings.Builder
	for _, item := range t.contentItems {
		if item.Type == "text" {
			builder.WriteString(item.Text)
		}
	}
	return builder.String()
}

// parseToolInput 把累积的 input 分片解析成 JSON；解析不了就包成 {"raw": ...}，
// 与反代一致——宁可给客户端一个可见的畸形入参，也不要丢掉整个 tool_use。
func parseToolInput(raw string) json.RawMessage {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return json.RawMessage("{}")
	}
	if json.Valid([]byte(trimmed)) {
		return json.RawMessage(trimmed)
	}
	wrapped, err := json.Marshal(map[string]string{"raw": trimmed})
	if err != nil {
		return json.RawMessage("{}")
	}
	return wrapped
}
