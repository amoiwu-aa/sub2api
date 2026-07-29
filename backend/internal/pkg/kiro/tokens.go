package kiro

import "unicode/utf8"

// 本文件对照 kiro-proxy 的 token-counter.js。
//
// 为什么需要本地估算：Amazon Q 的流里 tokenUsage 挂在 metadataEvent 上，而这个事件
// **不保证出现**——工具调用轮、被中断的轮、以及部分账号类型都可能整轮没有 metadata。
// 缺了它如果直接记 0，usage_logs 的成本就是 0，user_platform_quotas 上的 USD 限额
// 会变成一个静默失效的开关（限额只在成本非 0 时才可能被触发）。
// 因此：上游给了就用上游的（权威），没给才落到这里的近似值。

// perMessageOverhead 对齐 token-counter.js 里每条消息固定 +4 的做法，
// 用来粗略计入 role / 分隔符的开销。
const perMessageOverhead = 4

// EstimateTokens 用「字符数 / 4」近似 token 数，与反代同口径。
//
// 按 rune 而非 byte 计数：UTF-8 下一个汉字占 3 字节，按字节算会把中文对话
// 的估算抬高约三倍。这个近似会低估 CJK、高估代码，但作为分摊用量的尺子够用。
func EstimateTokens(text string) int {
	if text == "" {
		return 0
	}
	estimated := utf8.RuneCountInString(text) / 4
	if estimated < 1 {
		return 1
	}
	return estimated
}

// EstimateConversationTokens 估算一次请求发给上游的输入 token 数。
//
// 直接吃已经构造好的 ConversationState，而不是原始的 AnthropicRequest：
// 那才是真正发到线上的内容（system 已被折叠进 history、tool_result 已展开、
// web_search 已过滤），估出来的数与实际计费口径一致。
func EstimateConversationTokens(state *ConversationState) int {
	if state == nil {
		return 0
	}
	total := 0
	for i := range state.History {
		total += estimateChatMessageTokens(&state.History[i])
	}
	total += estimateChatMessageTokens(&state.CurrentMessage)
	return total
}

func estimateChatMessageTokens(msg *ChatMessage) int {
	if msg == nil {
		return 0
	}
	total := perMessageOverhead
	if user := msg.UserInputMessage; user != nil {
		total += EstimateTokens(user.Content)
		if ctx := user.UserInputMessageContext; ctx != nil {
			for _, result := range ctx.ToolResults {
				for _, block := range result.Content {
					total += EstimateTokens(block.Text)
					total += EstimateTokens(string(block.JSON))
				}
			}
			// 工具定义每轮都随 currentMessage 发一遍，是真实占用上下文的输入。
			for _, tool := range ctx.Tools {
				spec := tool.ToolSpecification
				total += EstimateTokens(spec.Name)
				total += EstimateTokens(spec.Description)
				total += EstimateTokens(string(spec.InputSchema.JSON))
			}
		}
	}
	if assistant := msg.AssistantResponseMessage; assistant != nil {
		total += EstimateTokens(assistant.Content)
		for _, use := range assistant.ToolUses {
			total += EstimateTokens(use.Name)
			total += EstimateTokens(string(use.Input))
		}
	}
	return total
}

// EstimatedOutputTokens 估算本轮产出的 token 数。
// thinking 与 tool_use 的入参都要计入——它们同样是模型生成的 token，
// 只是没有出现在对外可见的 text 里。
func (t *ResponseTranslator) EstimatedOutputTokens() int {
	if t == nil {
		return 0
	}
	total := 0
	for _, item := range t.contentItems {
		switch item.Type {
		case "text":
			total += EstimateTokens(item.Text)
		case "thinking":
			total += EstimateTokens(item.Thinking)
		case "tool_use":
			total += EstimateTokens(item.Name)
			total += EstimateTokens(string(item.Input))
		}
	}
	return total
}
