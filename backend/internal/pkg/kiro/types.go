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

	// CachePoint 在此消息处打一个提示缓存点。
	//
	// ⚠️ 网关**故意不发**它：这条路在 Kiro 上没通，发了没有任何收益。
	// 详见 CachePoint 类型上的说明。
	CachePoint *CachePoint `json:"cachePoint,omitempty"`
}

// CachePointTypeDefault 是 CachePoint.Type 唯一的合法取值。
const CachePointTypeDefault = "default"

// CachePoint 是提示缓存的断点标记。
//
// # 结论：不要发它
//
// 上游 schema 允许 UserInputMessage / AssistantResponseMessage / Tool 挂
// cachePoint，ListAvailableModels 也按模型下发 promptCaching 能力
// （Claude 与 GPT 系 supportsPromptCaching=true，门槛 1024 或 4096 token，
// 单请求上限 4 个缓存点）。看起来完全可用，但实测和逆向都说明这条路没通：
//
//   - 计费无差异：同一段 4 万字符前缀在 claude-sonnet-4.5 上跑 5 次对照，
//     credit 全部是 0.082131，逐位相同。
//   - 延迟无差异：TTFT 中位数 1568ms（带）vs 1682ms（不带），区间大幅重叠，
//     -6.8% 属噪声。真命中的话这个量级的前缀应当成倍下降。
//   - 官方客户端零调用：整个 Kiro 安装目录里，设置 cachePoint 的代码 0 处、
//     设置 clientCacheConfig 的 0 处、读取 metadataEvent.tokenUsage 的 0 处，
//     也没有任何缓存相关配置开关。cachePoint 仅出现在 AWS SDK 自动生成的
//     schema 与字符串常量表里。
//
// 最后一条是决定性的：Kiro 自己从没用过提示缓存，所以服务端这条链路大概率
// 没有实现。这也解释了为什么上游一次都不下发 metadataEvent——没有客户端要它，
// 于是 cacheReadInputTokens / cacheWriteInputTokens 这个唯一的直接判据也拿不到。
//
// 保留类型定义是为了将来 Kiro 真接了缓存时能快速重测，不必从头逆向。
// 想省钱请改用 RateMultiplierByModel：实测模型间价差达 48 倍，那是确定收益。
type CachePoint struct {
	Type string `json:"type"`
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

	// CachePoint 标记「到这条历史消息为止的内容可缓存」。
	//
	// 挂在助手消息上是提示缓存的主用法：多轮对话里历史只增不改，
	// 在最后一条历史消息处打点，后续每轮都能复用前面全部前缀。
	// 挂在 UserInputMessage 上的那个只覆盖当前这条消息。
	CachePoint *CachePoint `json:"cachePoint,omitempty"`
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
