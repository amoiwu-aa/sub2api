package kiro

import "encoding/json"

// 本文件的 JSON 字段名逐个对照 @aws/codewhisperer-streaming-client 的
// schemas_0.js（restJson1 协议的 wire 形状），而不是反代 q-client.js 的
// JS 对象——两者有一处关键分歧见 UserInputMessage.ModelID 的注释。

// ChatTriggerTypeManual 是 IDE 手动发起对话的触发类型。
const ChatTriggerTypeManual = "MANUAL"

// OriginAIEditor 是 Kiro IDE 使用的 origin。
const OriginAIEditor = "AI_EDITOR"

// GenerateAssistantResponseRequest 是 POST /generateAssistantResponse 的请求体。
type GenerateAssistantResponseRequest struct {
	ConversationState ConversationState `json:"conversationState"`
	ProfileARN        string            `json:"profileArn,omitempty"`
}

type ConversationState struct {
	ConversationID  string        `json:"conversationId,omitempty"`
	ChatTriggerType string        `json:"chatTriggerType,omitempty"`
	CurrentMessage  ChatMessage   `json:"currentMessage"`
	History         []ChatMessage `json:"history,omitempty"`
}

// ChatMessage 是 userInputMessage / assistantResponseMessage 的联合体。
// 序列化时恰好设置其中一个。
type ChatMessage struct {
	UserInputMessage         *UserInputMessage         `json:"userInputMessage,omitempty"`
	AssistantResponseMessage *AssistantResponseMessage `json:"assistantResponseMessage,omitempty"`
}

type UserInputMessage struct {
	Content string `json:"content"`
	Origin  string `json:"origin,omitempty"`

	// ModelID 在 schema 里是 userInputMessage 的成员，不是
	// userInputMessageContext 的。反代 q-client.js 把它写进了 context，
	// 而 smithy 序列化器只输出建模过的成员——也就是说反代实际上从没把
	// 模型选择发给上游，一直在用账号默认模型。这里放在正确位置。
	ModelID string `json:"modelId,omitempty"`

	UserInputMessageContext *UserInputMessageContext `json:"userInputMessageContext,omitempty"`
	Images                  []ImageBlock             `json:"images,omitempty"`
}

type UserInputMessageContext struct {
	// EditorState 恒为空对象：上游要求该字段存在，但服务端没有编辑器上下文可给。
	EditorState *struct{}    `json:"editorState,omitempty"`
	ToolResults []ToolResult `json:"toolResults,omitempty"`
	Tools       []Tool       `json:"tools,omitempty"`
}

type AssistantResponseMessage struct {
	Content  string    `json:"content"`
	ToolUses []ToolUse `json:"toolUses,omitempty"`
}

type ToolUse struct {
	ToolUseID string          `json:"toolUseId"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
}

// ToolResultStatus 取值 success / error。
const (
	ToolResultStatusSuccess = "success"
	ToolResultStatusError   = "error"
)

type ToolResult struct {
	ToolUseID string                   `json:"toolUseId"`
	Content   []ToolResultContentBlock `json:"content"`
	Status    string                   `json:"status,omitempty"`
}

type ToolResultContentBlock struct {
	Text string          `json:"text,omitempty"`
	JSON json.RawMessage `json:"json,omitempty"`
}

type Tool struct {
	ToolSpecification ToolSpecification `json:"toolSpecification"`
}

type ToolSpecification struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema ToolInputSchema `json:"inputSchema"`
}

type ToolInputSchema struct {
	JSON json.RawMessage `json:"json"`
}

type ImageBlock struct {
	Format string      `json:"format"`
	Source ImageSource `json:"source"`
}

// ImageSource.Bytes 是 blob，restJson1 用 base64 字符串承载。
// []byte 的默认 JSON 编码正是 base64，所以这里不需要手动转换。
type ImageSource struct {
	Bytes []byte `json:"bytes"`
}

// ---- 响应事件 ----
//
// 事件流的每一帧带 :event-type 头，值为 ChatResponseStream 联合体的成员名，
// payload 是该成员结构体的 JSON。

const (
	EventAssistantResponse     = "assistantResponseEvent"
	EventReasoningContent      = "reasoningContentEvent"
	EventToolUse               = "toolUseEvent"
	EventMetering              = "meteringEvent"
	EventMetadata              = "metadataEvent"
	EventContextUsage          = "contextUsageEvent"
	EventInvalidState          = "invalidStateEvent"
	EventSupplementaryWebLinks = "supplementaryWebLinksEvent"
	EventCodeReference         = "codeReferenceEvent"
	EventMessageMetadata       = "messageMetadataEvent"
)

type AssistantResponseEvent struct {
	Content string `json:"content"`
	ModelID string `json:"modelId,omitempty"`
}

type ReasoningContentEvent struct {
	Text      string `json:"text,omitempty"`
	Signature string `json:"signature,omitempty"`
}

// ToolUseEvent 是分片投递的：同一个 toolUseId 会收到多帧，input 需要拼接，
// 最后一帧带 stop=true。
type ToolUseEvent struct {
	ToolUseID string `json:"toolUseId"`
	Name      string `json:"name,omitempty"`
	Input     string `json:"input,omitempty"`
	Stop      bool   `json:"stop,omitempty"`
}

type MeteringEvent struct {
	Usage      float64 `json:"usage"`
	Unit       string  `json:"unit,omitempty"`
	UnitPlural string  `json:"unitPlural,omitempty"`
}

type MetadataEvent struct {
	TokenUsage *TokenUsage `json:"tokenUsage,omitempty"`
}

type TokenUsage struct {
	UncachedInputTokens   int64 `json:"uncachedInputTokens,omitempty"`
	OutputTokens          int64 `json:"outputTokens,omitempty"`
	TotalTokens           int64 `json:"totalTokens,omitempty"`
	CacheReadInputTokens  int64 `json:"cacheReadInputTokens,omitempty"`
	CacheWriteInputTokens int64 `json:"cacheWriteInputTokens,omitempty"`
}

type ContextUsageEvent struct {
	ContextUsagePercentage float64 `json:"contextUsagePercentage,omitempty"`
}

type InvalidStateEvent struct {
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}
