package cursor

import "strings"

// 上游偶发把模型的原始控制 token 混进文本流。线上在 opencode 的会话里见过正文
// 结尾挂着一个字面量 <|eos|>，说明这一段没经过上游自己的后处理就发下来了。
//
// 这类 token 是模型内部的分段标记，任何客户端拿到都只会当成乱码显示，直接滤掉。
// 代价是用户如果真想让模型原样输出这些字面量，会被一并抹掉——这种诉求罕见，
// 而漏出控制 token 是每个会话都可能碰上的，两害相权取轻。
var specialTokens = []string{
	// 实际观测到的
	"<|eos|>",
	// 各家常见的结束/分段标记，一并挡掉，省得换个上游模型又漏一种
	"<|endoftext|>",
	"<|eot_id|>",
	"<|end_of_turn|>",
	"<|im_start|>",
	"<|im_end|>",
	"<|start|>",
	"<|end|>",
	"<|message|>",
	"<|channel|>",
	"<|constrain|>",
	"<|return|>",
	"<|call|>",
}

// specialTokenFilter 从文本增量里剔除控制 token。
//
// 流式下一个 token 可能被切在两个增量之间（先来 "<|e"，再来 "os|>"），逐块独立
// 判断会漏。所以这里留一手：把「还可能长成 token」的尾巴扣住不发，等下一块拼上
// 再判断；流结束时若始终没长成 token，再把它当普通文本补发出去。
type specialTokenFilter struct {
	pending string
}

// Feed 吃进一块增量，返回这一刻可以安全下发的文本。
func (f *specialTokenFilter) Feed(chunk string) string {
	if chunk == "" && f.pending == "" {
		return ""
	}
	merged := stripSpecialTokens(f.pending + chunk)
	hold := holdbackAt(merged)
	f.pending = merged[hold:]
	return merged[:hold]
}

// Flush 交出扣住的尾巴，流结束时调用。到这一步还没闭合的就不是控制 token。
func (f *specialTokenFilter) Flush() string {
	tail := f.pending
	f.pending = ""
	return tail
}

func stripSpecialTokens(s string) string {
	// 绝大多数增量里没有 "<|"，先挡掉省去逐个 ReplaceAll。
	if !strings.Contains(s, "<|") {
		return s
	}
	for _, token := range specialTokens {
		s = strings.ReplaceAll(s, token, "")
	}
	return s
}

// holdbackAt 返回该从哪个位置开始扣住不发。
// 没有可疑尾巴时返回 len(s)，也就是整块都能发。
func holdbackAt(s string) int {
	start := strings.LastIndexByte(s, '<')
	if start < 0 {
		return len(s)
	}
	tail := s[start:]
	for _, token := range specialTokens {
		// 只扣「比完整 token 短、且正好是它的前缀」的尾巴。已经等长却没匹配上的，
		// 说明不是控制 token，照常发出去。
		if len(tail) < len(token) && strings.HasPrefix(token, tail) {
			return start
		}
	}
	return len(s)
}
