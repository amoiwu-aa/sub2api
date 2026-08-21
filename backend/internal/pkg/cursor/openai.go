package cursor

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// OpenAI Chat Completions 兼容层。
//
// Cursor Agent 收的是一段纯文本 prompt，不是消息数组，所以整段对话要压平。
// 压平规则对照反代 agent-client.js 的 messagesToAgentText。

// OpenAIRequest 是 /v1/chat/completions 的请求体（只取需要的字段）。
type OpenAIRequest struct {
	ID       string          `json:"id,omitempty"`
	Model    string          `json:"model"`
	Messages []OpenAIMessage `json:"messages"`
	Stream   bool            `json:"stream,omitempty"`
	// ReasoningEffort 是 Chat Completions 的标准推理档位字段。Cursor 通道把它
	// 映射到 RequestedModel.params["effort"]。
	ReasoningEffort *string `json:"reasoning_effort,omitempty"`
	ServiceTier     string  `json:"service_tier,omitempty"`
	StreamOptions   *struct {
		IncludeUsage bool `json:"include_usage,omitempty"`
	} `json:"stream_options,omitempty"`
	// Tools 是客户端声明的可调用工具。它们会作为 MCP 工具注册给 Cursor，
	// 模型调用后翻译成 tool_calls 交回客户端执行——opencode / Codex 这类
	// agent 客户端全靠这条通路。
	Tools []OpenAITool `json:"tools,omitempty"`
	// ToolChoice 目前只区分「禁用工具」与其它：Cursor 侧没有强制调用某个
	// 工具的开关，硬翻译过去只会给出一个做不到的承诺。
	ToolChoice        json.RawMessage `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool           `json:"parallel_tool_calls,omitempty"`
	// ConversationID 让客户端可以显式延续同一段 Agent 会话。
	ConversationID string `json:"conversation_id,omitempty"`
	Metadata       *struct {
		ConversationID string `json:"conversation_id,omitempty"`
	} `json:"metadata,omitempty"`
}

// OpenAITool 是 tools[] 里的一项。只支持 type=function，其余类型忽略。
type OpenAITool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type OpenAIMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content,omitempty"`
	Name    string          `json:"name,omitempty"`
	// ToolCalls 是助手上一轮发起的工具调用，重放时要原样带回给模型。
	ToolCalls []OpenAIToolCall `json:"tool_calls,omitempty"`
	// ToolCallID 出现在 role=tool 的消息上，标识这是哪次调用的结果。
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// OpenAIToolCall 是助手消息里的一次工具调用。
type OpenAIToolCall struct {
	Index    *int   `json:"index,omitempty"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// McpTools 把客户端声明的工具转成 MCP 工具定义。
//
// 丢掉没有名字的项：上游对空名工具的反应是整份声明失效，
// 一个坏条目会连累其余所有工具。
func (r *OpenAIRequest) McpTools() []McpTool {
	if r == nil || len(r.Tools) == 0 || r.toolsDisabled() {
		return nil
	}
	tools := make([]McpTool, 0, len(r.Tools))
	for _, tool := range r.Tools {
		if tool.Type != "" && tool.Type != "function" {
			continue
		}
		name := strings.TrimSpace(tool.Function.Name)
		tools = append(tools, McpTool{
			Name:        name,
			Description: tool.Function.Description,
			InputSchema: tool.Function.Parameters,
		})
	}
	return tools
}

// toolsDisabled 识别 tool_choice:"none"。客户端用它表示「这一轮别调工具」，
// 照常声明的话模型多半还是会调，最后卡在一个客户端不打算执行的调用上。
func (r *OpenAIRequest) toolsDisabled() bool {
	disableAll, _, _ := r.toolControl()
	return disableAll
}

func (r *OpenAIRequest) toolControl() (disableAll, disableParallel bool, err error) {
	if r == nil {
		return false, false, nil
	}
	disableParallel = r.ParallelToolCalls != nil && !*r.ParallelToolCalls
	trimmed := strings.TrimSpace(string(r.ToolChoice))
	if trimmed == "" || trimmed == `"auto"` {
		return false, disableParallel, nil
	}
	if trimmed == `"none"` {
		return true, disableParallel, nil
	}
	if trimmed == `"required"` {
		return false, disableParallel, errors.New("tool_choice=required is not supported by the Cursor bridge")
	}
	var choice map[string]any
	if json.Unmarshal(r.ToolChoice, &choice) == nil {
		return false, disableParallel, errors.New("named tool_choice is not supported by the Cursor bridge")
	}
	return false, disableParallel, errors.New("invalid tool_choice")
}

// Conversation 把请求归一化成与协议无关的对话表示。
func (r *OpenAIRequest) Conversation() *Conversation {
	if r == nil {
		return &Conversation{}
	}
	turns := make([]Turn, 0, len(r.Messages))
	var conversionErrors []error
	for i := range r.Messages {
		turn, err := openAIMessageToTurn(r.Messages[i])
		if err != nil {
			conversionErrors = append(conversionErrors, fmt.Errorf("messages[%d]: %w", i, err))
		}
		turns = append(turns, turn)
	}
	disableAll, disableParallel, controlErr := r.toolControl()
	return &Conversation{
		Turns:                    turns,
		Tools:                    r.McpTools(),
		DisableAllTools:          disableAll,
		DisableParallelToolCalls: disableParallel,
		Err:                      errors.Join(append(conversionErrors, controlErr)...),
	}
}

func openAIMessageToTurn(message OpenAIMessage) (Turn, error) {
	text, images, err := openAIMessageContent(message.Content, true)
	turn := Turn{Text: text, Images: images}
	switch strings.ToLower(strings.TrimSpace(message.Role)) {
	case "system", "developer":
		turn.Role = RoleSystem
	case "assistant":
		turn.Role = RoleAssistant
		for _, call := range message.ToolCalls {
			turn.ToolCalls = append(turn.ToolCalls, ToolCall{
				ID:        call.ID,
				Name:      call.Function.Name,
				Arguments: call.Function.Arguments,
			})
		}
	case "tool", "function":
		turn.Role = RoleTool
		turn.ToolCallID = message.ToolCallID
		turn.ToolName = message.Name
	default:
		turn.Role = RoleUser
	}
	return turn, err
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
	text, _, _ := openAIMessageContent(raw, false)
	return text
}

type openAIContentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL json.RawMessage `json:"image_url,omitempty"`
}

func openAIMessageContent(raw json.RawMessage, includeImageMarkers bool) (string, []AttachedImage, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return "", nil, nil
	}
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err == nil {
			return text, nil, nil
		}
		return "", nil, nil
	}

	var blocks []openAIContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", nil, nil
	}

	parts := make([]string, 0, len(blocks))
	images := make([]AttachedImage, 0, len(blocks))
	var conversionErrors []error
	for i, block := range blocks {
		switch strings.ToLower(strings.TrimSpace(block.Type)) {
		case "image_url", "input_image":
			imageURL := decodeOpenAIImageURL(block.ImageURL)
			if imageURL == "" {
				conversionErrors = append(conversionErrors, fmt.Errorf("content[%d]: image_url is required", i))
				continue
			}
			image, err := parseImageDataURI(imageURL)
			if err != nil {
				conversionErrors = append(conversionErrors, fmt.Errorf("content[%d]: %w", i, err))
				continue
			}
			images = append(images, image)
			if includeImageMarkers {
				parts = append(parts, attachedImageMarker)
			}
		default:
			if block.Text != "" {
				parts = append(parts, block.Text)
			}
		}
	}
	return strings.Join(parts, "\n"), images, errors.Join(conversionErrors...)
}

func decodeOpenAIImageURL(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	if trimmed[0] == '"' {
		var value string
		if err := json.Unmarshal(raw, &value); err == nil {
			return strings.TrimSpace(value)
		}
		return ""
	}
	var value struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return strings.TrimSpace(value.URL)
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
	Index        int         `json:"index"`
	Delta        OpenAIDelta `json:"delta"`
	FinishReason *string     `json:"finish_reason"`
}

type OpenAIDelta struct {
	Role             string           `json:"role,omitempty"`
	Content          string           `json:"content,omitempty"`
	ReasoningContent string           `json:"reasoning_content,omitempty"`
	ToolCalls        []OpenAIToolCall `json:"tool_calls,omitempty"`
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
	Role             string           `json:"role"`
	Content          string           `json:"content"`
	ReasoningContent string           `json:"reasoning_content,omitempty"`
	ToolCalls        []OpenAIToolCall `json:"tool_calls,omitempty"`
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

// NewOpenAIUsageChunk 构造 include_usage 请求的终态基础用量帧。
// Cursor 上游不返回缓存用量，因此只输出本地估算的基础 token 字段。
func NewOpenAIUsageChunk(id, model string, created, promptTokens, completionTokens int64) OpenAIChunk {
	return OpenAIChunk{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []OpenAIChunkChoice{},
		Usage: &OpenAIUsage{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      promptTokens + completionTokens,
		},
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

// FinishReasonToolCalls 是「这一轮以工具调用收尾」的标记。
// 客户端见到它才知道该去执行工具、再带着结果发起下一次请求。
const FinishReasonToolCalls = "tool_calls"

// NewOpenAIToolCallID 把上游的调用标识规整成 OpenAI 风格的 tool_call_id。
//
// 上游给的是裸 uuid，而不少客户端（以及一些严格的 SDK）假定 id 形如 call_xxx。
// 加个前缀既不影响我们自己的对账，也避开了那类校验。
func NewOpenAIToolCallID(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		// 兜底必须唯一：同一轮多个缺 id 的调用如果同名，Anthropic 客户端
		// 会拒绝重复的 tool_use.id（原生桥的 ls 调用实测常缺 id 字段）。
		return "call_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	if strings.HasPrefix(trimmed, "call_") {
		return trimmed
	}
	return "call_" + strings.ReplaceAll(trimmed, "-", "")
}

// NewOpenAIToolCallChunk 构造一条工具调用增量。
//
// 上游是一次性给出完整调用的，没有逐字符的 arguments 流，所以这里一帧发完。
// index 必须写：客户端按它把同一轮里的多个调用拼装起来。
func NewOpenAIToolCallChunk(id, model string, created int64, index int, call ToolCall) OpenAIChunk {
	toolCall := OpenAIToolCall{Index: &index, ID: call.ID, Type: "function"}
	toolCall.Function.Name = call.Name
	toolCall.Function.Arguments = defaultToolArguments(call.Arguments)
	return OpenAIChunk{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []OpenAIChunkChoice{{Index: 0, Delta: OpenAIDelta{ToolCalls: []OpenAIToolCall{toolCall}}}},
	}
}

// NewOpenAIToolCalls 把一轮里的全部调用转成非流式响应用的 tool_calls。
func NewOpenAIToolCalls(calls []ToolCall) []OpenAIToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]OpenAIToolCall, 0, len(calls))
	for i, call := range calls {
		index := i
		toolCall := OpenAIToolCall{Index: &index, ID: call.ID, Type: "function"}
		toolCall.Function.Name = call.Name
		toolCall.Function.Arguments = defaultToolArguments(call.Arguments)
		out = append(out, toolCall)
	}
	return out
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
