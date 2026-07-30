package cursor

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// feedAll 把若干增量依次喂给过滤器，返回下发出去的完整文本。
func feedAll(chunks ...string) string {
	var filter specialTokenFilter
	var sb strings.Builder
	for _, chunk := range chunks {
		sb.WriteString(filter.Feed(chunk))
	}
	sb.WriteString(filter.Flush())
	return sb.String()
}

func TestSpecialTokenFilterStripsObservedEosToken(t *testing.T) {
	// 线上在 opencode 会话里见到的就是这个形态：正文末尾挂着一个字面量 <|eos|>。
	require.Equal(t, "所有工具调用已发出。", feedAll("所有工具调用已发出。<|eos|>"))
}

func TestSpecialTokenFilterStripsTokenSplitAcrossChunks(t *testing.T) {
	// 流式下 token 会被切在两个增量之间，逐块独立判断会漏。
	require.Equal(t, "done", feedAll("done", "<|e", "os|>"))
	require.Equal(t, "done", feedAll("done<", "|eos", "|>"))
	require.Equal(t, "done", feedAll("d", "o", "n", "e", "<", "|", "e", "o", "s", "|", ">"))
}

func TestSpecialTokenFilterKeepsOrdinaryAngleBrackets(t *testing.T) {
	// 尖括号本身在代码和 XML 里到处都是，不能误伤。
	require.Equal(t, "a < b && c > d", feedAll("a < b && c > d"))
	require.Equal(t, "<div class=\"x\">hi</div>", feedAll("<div class=\"x\">hi</div>"))
	require.Equal(t, "泛型写作 Vec<T>", feedAll("泛型写作 Vec<T>"))
}

func TestSpecialTokenFilterFlushesUnfinishedFragment(t *testing.T) {
	// 扣住的尾巴最终没长成 token，就得当普通文本补发，不能吞掉。
	require.Equal(t, "结果是 <|", feedAll("结果是 <|"))
	require.Equal(t, "比较 a <", feedAll("比较 a <"))
	require.Equal(t, "写成 <|eo", feedAll("写成 <|eo"))
}

func TestSpecialTokenFilterStripsMidTextTokens(t *testing.T) {
	// 控制 token 出现在中间同样是上游漏出来的，一并滤掉。
	require.Equal(t, "前半后半", feedAll("前半<|im_end|>后半"))
	require.Equal(t, "ab", feedAll("a<|endoftext|>b"))
}

func TestSpecialTokenFilterEmitsIncrementallyWithoutBuffering(t *testing.T) {
	// 不含可疑尾巴时必须立刻下发，否则流式会变成一次性吐出来。
	var filter specialTokenFilter
	require.Equal(t, "hello ", filter.Feed("hello "))
	require.Equal(t, "world", filter.Feed("world"))
	require.Equal(t, "", filter.Flush())
}

func TestPromptTooLarge(t *testing.T) {
	original := MaxPromptBytes
	MaxPromptBytes = 10
	t.Cleanup(func() { MaxPromptBytes = original })

	require.False(t, PromptTooLarge(strings.Repeat("x", 10)))
	require.True(t, PromptTooLarge(strings.Repeat("x", 11)))

	// 上限设成 0 表示不限制，别让配错的值把所有请求都挡了。
	MaxPromptBytes = 0
	require.False(t, PromptTooLarge(strings.Repeat("x", 1<<20)))
}
