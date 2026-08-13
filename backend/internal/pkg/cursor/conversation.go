package cursor

import (
	"fmt"
	"strings"
)

// 归一化对话与重放渲染。
//
// # 为什么要把整段对话拍平成一条消息
//
// Cursor 的 AgentService/Run 只接受一条用户消息，没有「历史消息数组」这种入参；
// 而 OpenAI / Anthropic / Responses 三套协议都是无状态多轮：客户端每次带着完整
// 历史（含之前的工具调用与结果）重新发一遍。两者对接只能靠重放——把历史渲染成
// 一段结构化文本，随新一轮的请求一起发上去。
//
// # 为什么不用挂起流
//
// 实测过：工具结果在流上回执后，上游会收下并回显，但不会再发起下一次模型调用，
// 整条流就停在那里直到看门狗超时。上游的 agent 循环本就由客户端驱动，一轮
// HTTP 流对应一次模型调用。重放因此不是退而求其次，而是这个协议本来的用法，
// 而且天然无状态：不占上游连接、不怕重启、能水平扩展。

// ToolCall 是助手在某一轮里发起的一次工具调用。
type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// TurnRole 标记一条归一化消息的角色。
type TurnRole string

const (
	RoleSystem    TurnRole = "system"
	RoleUser      TurnRole = "user"
	RoleAssistant TurnRole = "assistant"
	RoleTool      TurnRole = "tool"
)

// Turn 是一条归一化后的对话消息。
//
// 三套入站协议（chat/completions、messages、responses）各自转成 Turn，
// 之后共用同一套渲染与工具映射，避免同样的逻辑写三遍、修一处漏两处。
type Turn struct {
	Role TurnRole
	Text string
	// Images are attached through Cursor's SelectedContext.
	Images []AttachedImage
	// ToolCalls 只在 Role == RoleAssistant 时有意义。
	ToolCalls []ToolCall
	// ToolCallID / ToolName 只在 Role == RoleTool 时有意义。
	ToolCallID string
	ToolName   string
}

// Conversation 是一次请求归一化后的全部输入。
type Conversation struct {
	Turns []Turn
	Tools []McpTool
	// NativeToolBridge 是原生工具桥映射（内置名 → 客户端工具名）。
	// 非空时 Render 的工具策略前言会放行对应的内置只读工具；
	// 这些客户端工具不该再出现在 Tools 里（由 service 层切分）。
	NativeToolBridge map[string]string
	// Err prevents malformed or unsupported images from being silently dropped.
	Err error
}

// ValidationError reports an input conversion error.
func (c *Conversation) ValidationError() error {
	if c == nil {
		return nil
	}
	return c.Err
}

// Images returns all client-supplied images in message order.
func (c *Conversation) Images() []AttachedImage {
	if c == nil {
		return nil
	}
	var images []AttachedImage
	for _, turn := range c.Turns {
		images = append(images, turn.Images...)
	}
	return images
}

// HasHistory 报告是否存在需要重放的历史（多于一条用户消息，或含工具往返）。
func (c *Conversation) HasHistory() bool {
	if c == nil {
		return false
	}
	nonSystem := 0
	for _, turn := range c.Turns {
		if turn.Role == RoleSystem {
			continue
		}
		if turn.Role != RoleUser || len(turn.ToolCalls) > 0 {
			return true
		}
		nonSystem++
	}
	return nonSystem > 1
}

// MaxPromptBytes 是渲染后 prompt 的上限，超过就不发给上游。
//
// 上游对过大的 prompt 不会报错，而是收下之后彻底沉默：实测 2.5 MB 的 prompt 只
// 回了 7 个 KV 帧就没了下文，网关只能等满 120 秒的看门狗，最后返回 HTTP 200 +
// 空正文。更糟的是这一轮照样按完整 prompt 计费——线上两次这样的请求各扣了 1.88
// 美元，占当天总成本的四成多。
//
// 1.25 MB 的 prompt 实测能正常返回，2.5 MB 稳定沉默，阈值取在中间偏保守的位置。
// 这是量出来的经验值，换上游模型可能要重调，所以留了环境变量。
var MaxPromptBytes = envBytes("CURSOR_MAX_PROMPT_BYTES", 1_500_000)

// PromptTooLarge 报告这段 prompt 是否超出上游能消化的体积。
func PromptTooLarge(prompt string) bool {
	return MaxPromptBytes > 0 && len(prompt) > MaxPromptBytes
}

// Render 把整段对话渲染成发给 Agent 的单条用户消息。
//
// 简单场景（一条系统提示 + 一条用户消息、无工具）会退化成近似原文的形式，
// 不给纯对话请求平白加上一堆标签。
func (c *Conversation) Render() string {
	if c == nil || len(c.Turns) == 0 {
		return ""
	}

	var sb strings.Builder
	if policy := ToolPolicyPreambleWithNative(c.Tools, c.NativeToolBridge); policy != "" {
		sb.WriteString(policy)
		sb.WriteString("\n\n")
	}

	system := collectSystemText(c.Turns)
	if system != "" {
		sb.WriteString("<system_instructions>\n")
		sb.WriteString(system)
		sb.WriteString("\n</system_instructions>\n\n")
	}

	body, endsWithToolResult := renderBody(c.Turns)
	sb.WriteString(body)

	// 最后一条是工具结果时，客户端在等模型基于结果继续，而不是等它再问一遍。
	// 不点破这一点，模型很可能把刚跑完的工具再调一次。
	if endsWithToolResult {
		sb.WriteString("\n\n<continue>\n")
		sb.WriteString("The tool results above are real output from tools you already called in this ")
		sb.WriteString("conversation. Continue the task from there: do not repeat a call whose result ")
		sb.WriteString("is already shown. Call another tool only if you still need information that ")
		sb.WriteString("is not present above.\n")
		sb.WriteString("</continue>")
	}
	return strings.TrimSpace(sb.String())
}

func collectSystemText(turns []Turn) string {
	parts := make([]string, 0, 2)
	for _, turn := range turns {
		if turn.Role != RoleSystem {
			continue
		}
		if text := strings.TrimSpace(turn.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// renderBody 渲染非系统消息，并报告最后一条是否为工具结果。
func renderBody(turns []Turn) (string, bool) {
	conversation := make([]Turn, 0, len(turns))
	for _, turn := range turns {
		if turn.Role == RoleSystem {
			continue
		}
		conversation = append(conversation, turn)
	}
	if len(conversation) == 0 {
		return "", false
	}

	// 只有一条用户消息时按原文发，保持纯对话请求的形态不变。
	if len(conversation) == 1 && conversation[0].Role == RoleUser && len(conversation[0].ToolCalls) == 0 {
		return strings.TrimSpace(conversation[0].Text), false
	}

	// 末尾连续的用户消息是「本轮真正的请求」，前面的都算历史。
	split := len(conversation)
	for split > 0 {
		previous := conversation[split-1]
		if previous.Role != RoleUser || len(previous.ToolCalls) > 0 {
			break
		}
		split--
	}

	var sb strings.Builder
	if split > 0 {
		sb.WriteString("<conversation_history>\n")
		for _, turn := range conversation[:split] {
			sb.WriteString(renderTurn(turn))
		}
		sb.WriteString("</conversation_history>")
	}

	if split < len(conversation) {
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		texts := make([]string, 0, len(conversation)-split)
		for _, turn := range conversation[split:] {
			if text := strings.TrimSpace(turn.Text); text != "" {
				texts = append(texts, text)
			}
		}
		sb.WriteString(strings.Join(texts, "\n\n"))
	}

	last := conversation[len(conversation)-1]
	return sb.String(), last.Role == RoleTool
}

func renderTurn(turn Turn) string {
	var sb strings.Builder
	switch turn.Role {
	case RoleUser:
		if text := strings.TrimSpace(turn.Text); text != "" {
			sb.WriteString("<user>\n")
			sb.WriteString(text)
			sb.WriteString("\n</user>\n")
		}
	case RoleAssistant:
		if text := strings.TrimSpace(turn.Text); text != "" {
			sb.WriteString("<assistant>\n")
			sb.WriteString(text)
			sb.WriteString("\n</assistant>\n")
		}
		for _, call := range turn.ToolCalls {
			sb.WriteString(fmt.Sprintf("<tool_call id=%q name=%q>\n", call.ID, call.Name))
			sb.WriteString(strings.TrimSpace(defaultToolArguments(call.Arguments)))
			sb.WriteString("\n</tool_call>\n")
		}
	case RoleTool:
		sb.WriteString(fmt.Sprintf("<tool_result id=%q name=%q>\n", turn.ToolCallID, turn.ToolName))
		text := strings.TrimSpace(turn.Text)
		if text == "" {
			// 空结果要显式说明。留空的话模型会以为工具还没跑完，
			// 转头把同一个调用再发一遍。
			text = "(the tool returned no output)"
		}
		sb.WriteString(text)
		sb.WriteString("\n</tool_result>\n")
	}
	return sb.String()
}

func defaultToolArguments(arguments string) string {
	if strings.TrimSpace(arguments) == "" {
		return "{}"
	}
	return arguments
}
