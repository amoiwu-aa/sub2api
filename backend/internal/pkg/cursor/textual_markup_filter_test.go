package cursor

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func testMarkupFilter() *textualToolCallFilter {
	return newTextualToolCallFilterWithRecovery(
		[]McpTool{{Name: "Grep"}, {Name: "Read"}},
		nativeBridgeOf(map[string]string{"write": "Write"}),
		true,
	)
}

func enableTextualRecoveryForTest(t *testing.T) {
	t.Helper()
	original := enableTextualReadOnlyToolRecovery
	enableTextualReadOnlyToolRecovery = true
	t.Cleanup(func() { enableTextualReadOnlyToolRecovery = original })
}

func feedMarkupChunks(f *textualToolCallFilter, chunks ...string) (string, []*McpToolCall) {
	var clean string
	var calls []*McpToolCall
	for _, chunk := range chunks {
		text, chunkCalls := f.Feed(chunk)
		clean += text
		calls = append(calls, chunkCalls...)
	}
	clean += f.Flush()
	return clean, calls
}

func TestTextualFilterConvertsReplayStyleBlock(t *testing.T) {
	f := testMarkupFilter()
	clean, calls := feedMarkupChunks(f,
		"先扫一遍禁写项。\n",
		`<tool_call id="call_9" name="Grep">`+"\n"+`{"pattern":"禁写|时线","path":"章节"}`+"\n</tool_call>",
		"\n然后继续。")

	require.Equal(t, "先扫一遍禁写项。\n\n然后继续。", clean)
	require.Len(t, calls, 1)
	require.Equal(t, "Grep", calls[0].Name)
	// 模型写的 id 多半抄自重放历史（call_1 之类），沿用会与历史或同轮
	// 其他文本调用撞车——必须丢弃，由上层铸造全新 id。
	require.Empty(t, calls[0].CallID)
	require.JSONEq(t, `{"pattern":"禁写|时线","path":"章节"}`, string(calls[0].Arguments))
	require.Zero(t, f.Suppressed)
}

func TestTextualFilterSuppressesTruncatedBlock(t *testing.T) {
	// 线上截图的形态：写到一半断流，属性还写岔了（工具名塞进 id）。
	f := testMarkupFilter()
	clean, calls := feedMarkupChunks(f, "056 已落盘。", `<tool_call id="Grep" pattern`)

	require.Equal(t, "056 已落盘。", clean)
	require.Empty(t, calls)
	require.Equal(t, 1, f.Suppressed)
}

func TestTextualFilterRecoversToolNameFromIDAttribute(t *testing.T) {
	f := testMarkupFilter()
	clean, calls := feedMarkupChunks(f,
		`<tool_call id="Grep">{"pattern":"交合"}</tool_call>`)

	require.Empty(t, clean)
	require.Len(t, calls, 1)
	require.Equal(t, "Grep", calls[0].Name)
	// id 已被用作工具名，不能再当 call id。
	require.Empty(t, calls[0].CallID)
}

func TestTextualFilterStripsMcpNamespacePrefix(t *testing.T) {
	f := testMarkupFilter()
	_, calls := feedMarkupChunks(f,
		`<tool_call name="`+McpToolNamespacePrefix+`Read">{"path":"a.md"}</tool_call>`)

	require.Len(t, calls, 1)
	require.Equal(t, "Read", calls[0].Name)
}

func TestTextualFilterParsesInvokeParameterStyle(t *testing.T) {
	f := testMarkupFilter()
	_, calls := feedMarkupChunks(f,
		`<invoke name="Read">`+
			`<parameter name="path">章节/013.md</parameter>`+
			`<parameter name="offset">50</parameter>`+
			`<parameter name="case_insensitive">true</parameter>`+
			`</invoke>`)

	require.Len(t, calls, 1)
	require.Equal(t, "Read", calls[0].Name)
	require.JSONEq(t, `{"path":"章节/013.md","offset":50,"case_insensitive":true}`, string(calls[0].Arguments))
}

func TestTextualFilterParsesHeadAttributeArguments(t *testing.T) {
	// 截图泄漏格式的完整版：参数写在起始标签属性里，标签体为空。
	f := testMarkupFilter()
	_, calls := feedMarkupChunks(f,
		`<tool_call id="Grep" pattern="交合|双修" path="章节" head_limit="50"></tool_call>`)

	require.Len(t, calls, 1)
	require.Equal(t, "Grep", calls[0].Name)
	require.JSONEq(t, `{"pattern":"交合|双修","path":"章节","head_limit":50}`, string(calls[0].Arguments))
}

func TestTextualFilterNeverRecoversMutatingTools(t *testing.T) {
	// 即使显式开启只读恢复，write 也只能吞掉，不能由模型正文触发真实写入。
	f := testMarkupFilter()
	_, calls := feedMarkupChunks(f,
		`<tool_call name="write">{"path":"a.md","content":"x"}</tool_call>`)

	require.Empty(t, calls)
	require.Equal(t, 1, f.Suppressed)
}

func TestTextualFilterDefaultIsSuppressOnly(t *testing.T) {
	f := newTextualToolCallFilter([]McpTool{{Name: "Grep"}}, nil)
	clean, calls := feedMarkupChunks(f,
		`before<tool_call name="Grep">{"pattern":"x"}</tool_call>after`)

	require.Equal(t, "beforeafter", clean)
	require.Empty(t, calls)
	require.Equal(t, 1, f.Suppressed)
}

func TestTextualFilterSuppressesUndeclaredTools(t *testing.T) {
	f := testMarkupFilter()
	clean, calls := feedMarkupChunks(f,
		`<tool_call name="NotATool">{"x":1}</tool_call>`)

	require.Empty(t, clean)
	require.Empty(t, calls)
	require.Equal(t, 1, f.Suppressed)
}

func TestTextualFilterHandlesCrossChunkSplits(t *testing.T) {
	f := testMarkupFilter()
	clean, calls := feedMarkupChunks(f,
		"开场白 <tool_", `call name="Gr`, `ep">{"pattern":"x"}</tool_`, `call> 收尾`)

	require.Equal(t, "开场白  收尾", clean)
	require.Len(t, calls, 1)
	require.Equal(t, "Grep", calls[0].Name)
}

func TestTextualFilterLeavesLookalikeNamesAlone(t *testing.T) {
	// <tool_call_log> 这类相似名不是标记，不能扣。
	f := testMarkupFilter()
	clean, calls := feedMarkupChunks(f, "见 <tool_call_log> 与 <invoker> 两节。")

	require.Equal(t, "见 <tool_call_log> 与 <invoker> 两节。", clean)
	require.Empty(t, calls)
	require.Zero(t, f.Suppressed)
}

func TestTextualFilterLeavesLookalikeSplitAcrossChunksAlone(t *testing.T) {
	// lookalike 恰好切在起始标记的边界字符处：第一块以 "<tool_call" 收尾
	//（此刻无法判断是不是标记），第二块揭晓是 "_log>"——必须原样放行。
	f := testMarkupFilter()
	clean, calls := feedMarkupChunks(f, "见 <tool_call", "_log> 一节。")

	require.Equal(t, "见 <tool_call_log> 一节。", clean)
	require.Empty(t, calls)
	require.Zero(t, f.Suppressed)
}

func TestTextualFilterStripsOrphanClosingTags(t *testing.T) {
	// 线上实测形态：模型复读重放格式时漏出没有配对开标签的闭合标签。
	f := testMarkupFilter()
	clean, calls := feedMarkupChunks(f,
		"盘上最新是 057。\n\n</tool_call>\n盘上最新是 057。\n</assistant>继续。")

	require.Equal(t, "盘上最新是 057。\n\n\n盘上最新是 057。\n继续。", clean)
	require.Empty(t, calls)
	require.Zero(t, f.Suppressed, "剥标签保留正文，不算吞块")
}

func TestTextualFilterSwallowsFabricatedToolResultBlocks(t *testing.T) {
	// 模型把历史里的工具结果整段复读出来：没有任何可执行语义，整块吞掉。
	f := testMarkupFilter()
	clean, calls := feedMarkupChunks(f,
		"三路稿在跑。",
		`<tool_result id="call_2" name="Grep">`+"\nFound 0 file(s)\n</tool_result>",
		"我先对一下细节。")

	require.Equal(t, "三路稿在跑。我先对一下细节。", clean)
	require.Empty(t, calls)
	require.Equal(t, 1, f.Suppressed)
}

func TestTextualFilterStripsTranscriptStructureTags(t *testing.T) {
	f := testMarkupFilter()
	clean, calls := feedMarkupChunks(f,
		"<assistant>\n写完了。\n</assistant>\n<continue>\n继续写。\n</continue>")

	require.Equal(t, "\n写完了。\n\n\n继续写。\n", clean)
	require.Empty(t, calls)
	require.Zero(t, f.Suppressed)
}

func TestTextualFilterStripsSplitOrphanCloser(t *testing.T) {
	// 孤儿闭合标签切在两个增量之间也要剥干净。
	f := testMarkupFilter()
	clean, calls := feedMarkupChunks(f, "写完。</tool_", "call>收尾。")

	require.Equal(t, "写完。收尾。", clean)
	require.Empty(t, calls)
}

func TestTextualFilterKeepsLookalikeStructureTags(t *testing.T) {
	// <assistant_note> 之类相似名不是转写标签，原样保留。
	f := testMarkupFilter()
	clean, _ := feedMarkupChunks(f, "见 <assistant_note> 与 </tool_call_log> 两处。")

	require.Equal(t, "见 <assistant_note> 与 </tool_call_log> 两处。", clean)
}

func TestTextualFilterFailsOpenOnOversizedBlock(t *testing.T) {
	// 起始标记后模型跑飞不闭合：超过尺寸上限要把缓冲当正文放行，
	// 宁可泄漏一次标记原文，不能吞掉整轮剩余正文。
	f := testMarkupFilter()
	runaway := `<tool_call name="Grep">` + strings.Repeat("正文", maxTextualBlockBytes/4)

	var clean string
	for i := 0; i < len(runaway); i += 4096 {
		end := i + 4096
		if end > len(runaway) {
			end = len(runaway)
		}
		text, calls := f.Feed(runaway[i:end])
		clean += text
		require.Empty(t, calls)
	}
	clean += f.Flush()

	require.Greater(t, len(clean), maxTextualBlockBytes, "超限缓冲必须放行为正文")
	require.Contains(t, clean, `<tool_call name="Grep">`)
	require.Zero(t, f.Suppressed, "放行不算吞块")
}

func TestRunAgentTurnMintsUniqueIDsForDuplicateTextualCalls(t *testing.T) {
	enableTextualRecoveryForTest(t)
	// 模型把两个文本调用写成同一个 id（抄重放历史的典型行为），
	// 下发给客户端的 tool_call id 必须互不相同。
	server := &agentTestServer{t: t, script: [][]byte{
		textDeltaMessage(`<tool_call id="call_1" name="Grep">{"pattern":"a"}</tool_call>`),
		textDeltaMessage(`<tool_call id="call_1" name="Read">{"path":"b.md"}</tool_call>`),
		turnEndedMessage(),
	}}
	client, host := startAgentTestServer(t, server)

	result, err := RunAgentTurn(context.Background(), testAgentOptions(t, client, host),
		AgentTurnInput{
			Text:           "续写",
			ConversationID: "conv-dup-ids",
			Tools:          []McpTool{{Name: "Grep"}, {Name: "Read"}},
		}, nil)
	require.NoError(t, err)
	require.Len(t, result.ToolCalls, 2)
	require.NotEqual(t, result.ToolCalls[0].ID, result.ToolCalls[1].ID)
	require.NotEqual(t, "call_1", result.ToolCalls[0].ID)
}

func TestTextualFilterDisabledWithoutTools(t *testing.T) {
	require.Nil(t, newTextualToolCallFilter(nil, nil))
	var f *textualToolCallFilter
	clean, calls := f.Feed(`<tool_call name="Grep">{}</tool_call>`)
	require.Equal(t, `<tool_call name="Grep">{}</tool_call>`, clean)
	require.Empty(t, calls)
	require.Empty(t, f.Flush())
}

func TestRunAgentTurnConvertsTextualToolMarkup(t *testing.T) {
	enableTextualRecoveryForTest(t)
	// 文本标记转换出的调用不触发提前关流：上游没在等回执，这轮以
	// turn_ended 自然收尾，标记之后的正文必须保留。
	server := &agentTestServer{t: t, script: [][]byte{
		textDeltaMessage("扫一遍禁写项。"),
		textDeltaMessage(`<tool_call id="Grep" name="Grep">` + "\n"),
		textDeltaMessage(`{"pattern":"禁写","path":"章节"}` + "\n</tool_call>"),
		textDeltaMessage("扫完继续写。"),
		turnEndedMessage(),
	}}
	client, host := startAgentTestServer(t, server)

	result, err := RunAgentTurn(context.Background(), testAgentOptions(t, client, host),
		AgentTurnInput{
			Text:           "续写",
			ConversationID: "conv-markup",
			Tools:          []McpTool{{Name: "Grep"}},
		}, nil)
	require.NoError(t, err)

	require.True(t, result.EndedWithToolCalls())
	require.True(t, result.TurnEnded)
	require.Len(t, result.ToolCalls, 1)
	require.Equal(t, 1, result.TextualToolCalls)
	require.Equal(t, "Grep", result.ToolCalls[0].Name)
	// 正文必须干净且完整：标记一个字符都不能漏，标记后的正文不能被截断。
	require.Equal(t, "扫一遍禁写项。扫完继续写。", result.Text)
	require.Zero(t, result.TextualToolMarkupSuppressed)
	require.False(t, result.Incomplete())
}

func TestRunAgentTurnSuppressesTruncatedTextualMarkup(t *testing.T) {
	server := &agentTestServer{t: t, script: [][]byte{
		textDeltaMessage("056 已落盘。"),
		textDeltaMessage(`<tool_call id="Grep" pattern`),
		turnEndedMessage(),
	}}
	client, host := startAgentTestServer(t, server)

	result, err := RunAgentTurn(context.Background(), testAgentOptions(t, client, host),
		AgentTurnInput{
			Text:           "续写",
			ConversationID: "conv-markup-trunc",
			Tools:          []McpTool{{Name: "Grep"}},
		}, nil)
	require.NoError(t, err)

	require.Equal(t, "056 已落盘。", result.Text)
	require.Empty(t, result.ToolCalls)
	require.Equal(t, 1, result.TextualToolMarkupSuppressed)
}

func TestRunAgentTurnBreaksTextualMarkupLoop(t *testing.T) {
	// 模型陷入伪造转写死循环（一轮几十个伪造块、上游不发 turn_ended）时，
	// 到达熔断阈值要主动收尾：已过滤的正文交还客户端、不标 stalled，
	// 而不是陪它烧满 120 秒看门狗。
	script := [][]byte{textDeltaMessage("先核对 059。")}
	for i := 0; i < textualMarkupLoopLimit+2; i++ {
		script = append(script, textDeltaMessage(
			`<tool_result id="call_x" name="Grep">`+"\n伪造结果\n</tool_result>\n"))
	}
	server := &agentTestServer{t: t, hangAfterScript: true, script: script}
	client, host := startAgentTestServer(t, server)

	result, err := RunAgentTurn(context.Background(), testAgentOptions(t, client, host),
		AgentTurnInput{
			Text:           "续写",
			ConversationID: "conv-markup-loop",
			Tools:          []McpTool{{Name: "Grep"}},
		}, nil)
	require.NoError(t, err)

	require.GreaterOrEqual(t, result.TextualToolMarkupSuppressed, textualMarkupLoopLimit)
	require.Equal(t, "先核对 059。", strings.TrimSpace(result.Text))
	require.False(t, result.Stalled, "熔断是主动收尾，不是看门狗超时")
	require.False(t, result.Incomplete())
}

func TestRunAgentTurnLeavesMarkupAloneWithoutDeclaredTools(t *testing.T) {
	// 纯对话请求（无工具）不做拦截：小说正文里哪怕出现同样字样也原样保留。
	server := &agentTestServer{t: t, script: [][]byte{
		textDeltaMessage(`他敲下 <tool_call name="Grep"> 这行字。`),
		turnEndedMessage(),
	}}
	client, host := startAgentTestServer(t, server)

	result, err := RunAgentTurn(context.Background(), testAgentOptions(t, client, host),
		AgentTurnInput{Text: "续写", ConversationID: "conv-no-tools"}, nil)
	require.NoError(t, err)
	require.Equal(t, `他敲下 <tool_call name="Grep"> 这行字。`, result.Text)
	require.Zero(t, result.TextualToolMarkupSuppressed)
}
