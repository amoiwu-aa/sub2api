package kiro

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// 本文件把 Anthropic Messages API 的请求转换为 Q 的 conversationState。
// 转换规则对照 kiro-proxy q-client.js 的 convertMessages。

// AnthropicRequest 是 /v1/messages 的请求体（只取转换需要的字段）。
type AnthropicRequest struct {
	Model     string             `json:"model"`
	Messages  []AnthropicMessage `json:"messages"`
	System    json.RawMessage    `json:"system,omitempty"`
	Stream    bool               `json:"stream,omitempty"`
	MaxTokens int                `json:"max_tokens,omitempty"`
	Tools     []AnthropicTool    `json:"tools,omitempty"`
}

type AnthropicMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// AnthropicTool 同时兼容标准格式与 MCP 的 custom 格式。
type AnthropicTool struct {
	Type        string          `json:"type,omitempty"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
	Custom      *struct {
		Description string          `json:"description,omitempty"`
		InputSchema json.RawMessage `json:"input_schema,omitempty"`
	} `json:"custom,omitempty"`
}

type anthropicContentBlock struct {
	Type string `json:"type"`

	Text string `json:"text,omitempty"`

	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`

	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`

	Source *anthropicImageSource `json:"source,omitempty"`

	// OpenAI / LangChain 风格的图片块，反代也接受。
	ImageURL json.RawMessage `json:"image_url,omitempty"`
}

type anthropicImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

// maxToolDescriptionLen 对齐反代的 slice(0, 10000)。
const maxToolDescriptionLen = 10000

// emptyEditorState 是所有 userInputMessageContext 共用的空 editorState。
var emptyEditorState = &struct{}{}

// BuildConversationState 把 Anthropic 请求转换成 Q 的 conversationState。
//
// conversationID 由调用方给出（每请求一个新 UUID，上游没有会话粘性）。
func BuildConversationState(req *AnthropicRequest, conversationID, upstreamModelID string) (*ConversationState, error) {
	if req == nil {
		return nil, fmt.Errorf("kiro: anthropic request is nil")
	}

	history := make([]ChatMessage, 0, len(req.Messages)+2)

	// system 没有对应的上游角色，按反代的做法伪造成第一轮 user/assistant 对。
	if systemText := extractSystemText(req.System); systemText != "" {
		history = append(history,
			ChatMessage{UserInputMessage: newUserInputMessage(systemText, upstreamModelID, nil, nil)},
			ChatMessage{AssistantResponseMessage: &AssistantResponseMessage{Content: "OK"}},
		)
	}

	for i := range req.Messages {
		message := &req.Messages[i]
		blocks, err := parseContentBlocks(message.Content)
		if err != nil {
			return nil, fmt.Errorf("kiro: parse message %d content: %w", i, err)
		}

		switch strings.ToLower(strings.TrimSpace(message.Role)) {
		case "user":
			toolResults := extractToolResults(blocks)
			images := extractImages(blocks)
			text := extractText(blocks)
			if len(toolResults) > 0 {
				// tool_result 轮：content 必须为空，结果挂在 context 上。
				text = ""
			}
			history = append(history, ChatMessage{
				UserInputMessage: newUserInputMessage(text, upstreamModelID, toolResults, images),
			})
		case "assistant":
			history = append(history, ChatMessage{
				AssistantResponseMessage: &AssistantResponseMessage{
					Content:  extractText(blocks),
					ToolUses: extractToolUses(blocks),
				},
			})
		default:
			// 其它角色（含被单独传进 messages 的 system）忽略，与反代一致。
		}
	}

	// 上游要求最后一条必须是 user。末尾是 assistant 时补一句延续指令。
	if len(history) == 0 || history[len(history)-1].UserInputMessage == nil {
		history = append(history, ChatMessage{
			UserInputMessage: newUserInputMessage("Continue.", upstreamModelID, nil, nil),
		})
	}

	current := history[len(history)-1]
	// tools 只挂在 currentMessage 上；挂进 history 会被上游拒绝。
	if tools := convertTools(req.Tools); len(tools) > 0 && current.UserInputMessage != nil {
		if current.UserInputMessage.UserInputMessageContext == nil {
			current.UserInputMessage.UserInputMessageContext = &UserInputMessageContext{EditorState: emptyEditorState}
		}
		current.UserInputMessage.UserInputMessageContext.Tools = tools
	}

	return &ConversationState{
		ConversationID:  conversationID,
		ChatTriggerType: ChatTriggerTypeManual,
		CurrentMessage:  current,
		History:         history[:len(history)-1],
	}, nil
}

func newUserInputMessage(content, modelID string, toolResults []ToolResult, images []ImageBlock) *UserInputMessage {
	return &UserInputMessage{
		Content: content,
		Origin:  OriginAIEditor,
		ModelID: modelID,
		UserInputMessageContext: &UserInputMessageContext{
			EditorState: emptyEditorState,
			ToolResults: toolResults,
		},
		Images: images,
	}
}

// parseContentBlocks 接受 string 与 []block 两种 content 形态。
func parseContentBlocks(raw json.RawMessage) ([]anthropicContentBlock, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, err
		}
		return []anthropicContentBlock{{Type: "text", Text: text}}, nil
	}
	var blocks []anthropicContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, err
	}
	return blocks, nil
}

func extractSystemText(raw json.RawMessage) string {
	blocks, err := parseContentBlocks(raw)
	if err != nil {
		return ""
	}
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if text := strings.TrimSpace(block.Text); text != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func extractText(blocks []anthropicContentBlock) string {
	var builder strings.Builder
	for _, block := range blocks {
		if block.Type == "text" {
			builder.WriteString(block.Text)
		}
	}
	return builder.String()
}

func extractToolUses(blocks []anthropicContentBlock) []ToolUse {
	var uses []ToolUse
	for _, block := range blocks {
		if block.Type != "tool_use" {
			continue
		}
		input := block.Input
		if len(strings.TrimSpace(string(input))) == 0 {
			input = json.RawMessage("{}")
		}
		uses = append(uses, ToolUse{ToolUseID: block.ID, Name: block.Name, Input: input})
	}
	return uses
}

func extractToolResults(blocks []anthropicContentBlock) []ToolResult {
	var results []ToolResult
	for _, block := range blocks {
		if block.Type != "tool_result" {
			continue
		}
		status := ToolResultStatusSuccess
		if block.IsError {
			status = ToolResultStatusError
		}
		results = append(results, ToolResult{
			ToolUseID: block.ToolUseID,
			Content:   toolResultContent(block.Content),
			Status:    status,
		})
	}
	return results
}

// toolResultContent 把 tool_result 的 content 归一为文本块。
// 上游只认 {text} / {json}，字符串、块数组、以及非文本块都折成文本。
func toolResultContent(raw json.RawMessage) []ToolResultContentBlock {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return []ToolResultContentBlock{{Text: ""}}
	}
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err == nil {
			return []ToolResultContentBlock{{Text: text}}
		}
		return []ToolResultContentBlock{{Text: trimmed}}
	}

	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return []ToolResultContentBlock{{Text: trimmed}}
	}
	blocks := make([]ToolResultContentBlock, 0, len(items))
	for _, item := range items {
		itemText := strings.TrimSpace(string(item))
		if itemText != "" && itemText[0] == '"' {
			var text string
			if err := json.Unmarshal(item, &text); err == nil {
				blocks = append(blocks, ToolResultContentBlock{Text: text})
				continue
			}
		}
		var typed struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(item, &typed); err == nil && typed.Type == "text" {
			blocks = append(blocks, ToolResultContentBlock{Text: typed.Text})
			continue
		}
		blocks = append(blocks, ToolResultContentBlock{Text: itemText})
	}
	if len(blocks) == 0 {
		return []ToolResultContentBlock{{Text: ""}}
	}
	return blocks
}

var imageFormatByMediaType = map[string]string{
	"image/png":  "png",
	"image/jpeg": "jpeg",
	"image/gif":  "gif",
	"image/webp": "webp",
}

func extractImages(blocks []anthropicContentBlock) []ImageBlock {
	var images []ImageBlock
	for _, block := range blocks {
		switch block.Type {
		case "image":
			if block.Source == nil {
				continue
			}
			switch block.Source.Type {
			case "base64":
				if decoded, ok := decodeBase64(block.Source.Data); ok {
					images = append(images, ImageBlock{
						Format: imageFormat(block.Source.MediaType),
						Source: ImageSource{Bytes: decoded},
					})
				}
			case "url":
				if image, ok := imageFromDataURL(block.Source.URL); ok {
					images = append(images, image)
				}
			}
		case "image_url":
			if image, ok := imageFromDataURL(imageURLString(block.ImageURL)); ok {
				images = append(images, image)
			}
		}
	}
	return images
}

// imageURLString 接受 {"url":"..."} 与裸字符串两种形态。
func imageURLString(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	if trimmed[0] == '"' {
		var url string
		if err := json.Unmarshal(raw, &url); err == nil {
			return url
		}
		return ""
	}
	var wrapper struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &wrapper); err == nil {
		return wrapper.URL
	}
	return ""
}

// imageFromDataURL 只接受 data: URL。远程 URL 会让服务端代客户端发起出站请求
// （SSRF），而上游本身也只收 bytes，所以直接丢弃。
func imageFromDataURL(url string) (ImageBlock, bool) {
	url = strings.TrimSpace(url)
	if !strings.HasPrefix(url, "data:") {
		return ImageBlock{}, false
	}
	comma := strings.Index(url, ",")
	if comma < 0 {
		return ImageBlock{}, false
	}
	mediaType := ""
	header := url[len("data:"):comma]
	if semicolon := strings.Index(header, ";"); semicolon >= 0 {
		mediaType = header[:semicolon]
	} else {
		mediaType = header
	}
	decoded, ok := decodeBase64(url[comma+1:])
	if !ok {
		return ImageBlock{}, false
	}
	return ImageBlock{Format: imageFormat(mediaType), Source: ImageSource{Bytes: decoded}}, true
}

func decodeBase64(data string) ([]byte, bool) {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" {
		return nil, false
	}
	decoded, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		return nil, false
	}
	return decoded, true
}

func imageFormat(mediaType string) string {
	if format, ok := imageFormatByMediaType[strings.ToLower(strings.TrimSpace(mediaType))]; ok {
		return format
	}
	return "jpeg"
}

// convertTools 过滤掉 web_search（上游不支持，带上会整体 400）并裁剪描述长度。
func convertTools(tools []AnthropicTool) []Tool {
	converted := make([]Tool, 0, len(tools))
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			continue
		}
		switch strings.ToLower(name) {
		case "web_search", "websearch":
			continue
		}

		description := tool.Description
		schema := tool.InputSchema
		if tool.Custom != nil {
			if description == "" {
				description = tool.Custom.Description
			}
			if len(schema) == 0 {
				schema = tool.Custom.InputSchema
			}
		}
		if len(description) > maxToolDescriptionLen {
			description = description[:maxToolDescriptionLen]
		}
		if len(strings.TrimSpace(string(schema))) == 0 {
			schema = json.RawMessage("{}")
		}

		converted = append(converted, Tool{ToolSpecification: ToolSpecification{
			Name:        name,
			Description: description,
			InputSchema: ToolInputSchema{JSON: schema},
		}})
	}
	return converted
}
