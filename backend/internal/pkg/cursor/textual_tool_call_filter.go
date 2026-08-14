package cursor

import (
	"encoding/json"
	"regexp"
	"strings"
)

// 文本工具调用拦截器。
//
// 网关重放历史时用 <tool_call id=".." name=".."> 标记渲染过去的调用
// （见 conversation.go 的 renderBody），长对话的上下文里全是这种示范，
// 模型会模仿着把新调用直接写进正文——没人执行，客户端只会看到一坨原始
// 标记（还常常写岔，比如把工具名塞进 id 属性、写到一半断掉）。
//
// 这里在文本出口把它们拦下来：完整且能解析出已声明工具的块转成真正的
// 客户端调用（与协议通道的调用走完全相同的收尾路径）；解析不出的与流
// 结束时仍未闭合的残块直接吞掉并计数——宁可这轮少说一句话，也不能把
// 乱码下发给用户。
//
// 只在本轮声明了客户端工具时启用：纯对话请求的正文不做任何拦截。
// 带工具的请求里，正文出现这些标记只有一种解释——泄漏。

const (
	textualToolCallOpen    = "<tool_call"
	textualInvokeOpen      = "<invoke"
	textualToolResultOpen  = "<tool_result"
	textualToolCallClose   = "</tool_call>"
	textualInvokeClose     = "</invoke>"
	textualToolResultClose = "</tool_result>"

	// maxTextualBlockBytes 是标记块的尺寸上限。合法的文本调用（含 write
	// 整章内容）也就几十 KB；超过上限说明这多半不是调用而是模型跑飞了，
	// 此时把缓冲当普通文本放行（fail-open）——泄漏一次标记原文，
	// 好过把整轮剩余正文全部吞掉。
	maxTextualBlockBytes = 128 * 1024

	// textualMarkupLoopLimit 是单轮吞块数的熔断阈值。正常的格式漂移一轮
	// 也就一两个块；线上实测模型陷入「自导自演整个转写」的死循环时一轮能
	// 吞四十多个块、烧满看门狗的 120 秒。到达阈值说明这轮已经没有可救的
	// 产出了，主动收尾把已过滤的正文交还客户端，别陪模型烧下去。
	textualMarkupLoopLimit = 6
)

// textualStripTags 是直接从正文剥掉的转写结构标签。
//
// 它们全部来自重放格式（conversation.go 的 renderBody）：模型模仿重放时
// 会把这些标签原样写进回复——孤儿闭合标签（前面没有配对的开标签，线上
// 实测出现过 </tool_call> 单独漏出）、以及 <assistant>/<continue> 这类
// 轮次结构标签。剥标签保留正文：标签之间的文字可能是正常内容。
var textualStripTags = []string{
	textualToolCallClose,
	textualInvokeClose,
	textualToolResultClose,
	"<assistant>",
	"</assistant>",
	"<user>",
	"</user>",
	"<continue>",
	"</continue>",
	"<system_instructions>",
	"</system_instructions>",
}

// textualToolCallFilter 是跨增量的状态机：标记可能被切在任意两个增量之间。
type textualToolCallFilter struct {
	// declared 把正文里可能出现的名字归一到客户端声明的工具名：
	// MCP 工具的裸名、原生桥的客户端名与内置键都收录（统一小写索引）。
	declared map[string]string
	// pending 是「还可能长成起始标记」的尾巴，参照 specialTokenFilter 的扣发思路。
	pending string
	// block 在进入标记块后累积整个块的原文。
	block   strings.Builder
	inBlock bool
	// blockSuppressOnly 标记当前块是 <tool_result> 这类只吞不转的伪造块：
	// 模型复读历史工具结果没有任何可执行语义，闭合后直接吞掉。
	blockSuppressOnly bool
	// Suppressed 是被吞掉的不可解析/残缺/伪造块数。
	Suppressed int
	// recoverReadOnly 只允许显式开启时把已声明的只读工具标记恢复成真调用。
	// 默认只吞不执行，避免模型引用示例文本触发客户端真实操作。
	recoverReadOnly bool
}

var enableTextualReadOnlyToolRecovery = envBool("CURSOR_TEXTUAL_TOOL_CALL_RECOVERY", false)

// newTextualToolCallFilter 构造拦截器；declared 为空时返回 nil（禁用）。
func newTextualToolCallFilter(mcpTools []McpTool, nativeBridge NativeToolBridge) *textualToolCallFilter {
	return newTextualToolCallFilterWithRecovery(
		mcpTools,
		nativeBridge,
		enableTextualReadOnlyToolRecovery,
	)
}

func newTextualToolCallFilterWithRecovery(
	mcpTools []McpTool,
	nativeBridge NativeToolBridge,
	recoverReadOnly bool,
) *textualToolCallFilter {
	declared := make(map[string]string, len(mcpTools)+2*len(nativeBridge))
	for _, tool := range mcpTools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			continue
		}
		declared[strings.ToLower(name)] = name
	}
	for builtinKey, target := range nativeBridge {
		clientName := strings.TrimSpace(target.Name)
		if clientName == "" {
			continue
		}
		declared[strings.ToLower(clientName)] = clientName
		declared[strings.ToLower(builtinKey)] = clientName
	}
	if len(declared) == 0 {
		return nil
	}
	return &textualToolCallFilter{declared: declared, recoverReadOnly: recoverReadOnly}
}

// Feed 吃进一块已经过控制 token 过滤的文本，返回可以下发的干净文本与
// 从标记块里解析出来的调用。
func (f *textualToolCallFilter) Feed(chunk string) (string, []*McpToolCall) {
	if f == nil {
		return chunk, nil
	}
	data := f.pending + chunk
	f.pending = ""

	var clean strings.Builder
	var calls []*McpToolCall
	for data != "" {
		if f.inBlock {
			// 闭合标签可能被切在块缓冲与新增量之间，必须拼起来找。
			combined := f.block.String() + data
			f.block.Reset()
			closeIdx, closeLen := findTextualClose(combined)
			if closeIdx < 0 {
				if len(combined) > maxTextualBlockBytes {
					// 块大到不可能是调用：放行原文，别吞掉整轮正文。
					clean.WriteString(combined)
					f.inBlock = false
					data = ""
					break
				}
				f.block.WriteString(combined)
				data = ""
				break
			}
			blockText := combined[:closeIdx+closeLen]
			data = combined[closeIdx+closeLen:]
			f.inBlock = false
			if f.blockSuppressOnly {
				// 伪造的工具结果块：没有可执行语义，整段吞掉。
				f.Suppressed++
			} else if call := parseTextualToolCall(blockText, f.declared); call != nil &&
				f.recoverReadOnly && isTextualRecoveryReadOnlyTool(call.Name) {
				calls = append(calls, call)
			} else {
				f.Suppressed++
			}
			f.blockSuppressOnly = false
			continue
		}

		openIdx, openLen, suppressOnly := findTextualOpen(data)
		if openIdx >= 0 {
			// 起始标记必须后跟空白或 '>'，避免把正文里的 <tool_call_xxx
			// 这类名字误判成标记。判不了（正好切在边界）就先扣住等下一块。
			boundary := openIdx + openLen
			if boundary >= len(data) {
				clean.WriteString(stripTextualTags(data[:openIdx]))
				f.pending = data[openIdx:]
				data = ""
				break
			}
			next := data[boundary]
			if next != ' ' && next != '\t' && next != '\n' && next != '\r' && next != '>' {
				clean.WriteString(stripTextualTags(data[:boundary]))
				data = data[boundary:]
				continue
			}
			clean.WriteString(stripTextualTags(data[:openIdx]))
			f.inBlock = true
			f.blockSuppressOnly = suppressOnly
			f.block.WriteString(data[openIdx:boundary])
			data = data[boundary:]
			continue
		}

		hold := textualOpenHoldbackAt(data)
		clean.WriteString(stripTextualTags(data[:hold]))
		f.pending = data[hold:]
		data = ""
	}
	return clean.String(), calls
}

func isTextualRecoveryReadOnlyTool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "read", "grep", "glob", "listdir", "ls", "webfetch", "lspdiagnostics", "diagnostics":
		return true
	default:
		return false
	}
}

// stripTextualTags 从一段确定不含块开标签的文本里剥掉转写结构标签
// （孤儿闭合标签、<assistant>/<continue> 等轮次标签），保留其间的正文。
func stripTextualTags(s string) string {
	if !strings.Contains(s, "<") {
		return s
	}
	for _, tag := range textualStripTags {
		s = strings.ReplaceAll(s, tag, "")
	}
	return s
}

// Flush 在流结束时调用：未闭合的残块被吞掉计数，扣住的普通尾巴放行。
func (f *textualToolCallFilter) Flush() string {
	if f == nil {
		return ""
	}
	if f.inBlock {
		// 残缺的标记块（模型写到一半断了）：吞掉，绝不下发。
		f.inBlock = false
		f.blockSuppressOnly = false
		f.block.Reset()
		f.Suppressed++
		f.pending = ""
		return ""
	}
	tail := f.pending
	f.pending = ""
	return tail
}

// findTextualOpen 找最早出现的块开标签；第三个返回值标记是否为只吞不转的
// 伪造结果块（<tool_result>）。
func findTextualOpen(s string) (int, int, bool) {
	bestIdx, bestLen := -1, 0
	bestSuppress := false
	for _, candidate := range []struct {
		open     string
		suppress bool
	}{
		{textualToolCallOpen, false},
		{textualInvokeOpen, false},
		{textualToolResultOpen, true},
	} {
		idx := strings.Index(s, candidate.open)
		if idx < 0 {
			continue
		}
		// 同一位置更长的标记优先（<tool_call 与 <tool_result 前缀相同的
		// 情况不存在，但保持「最早优先、等位取更长」的稳定语义）。
		if bestIdx < 0 || idx < bestIdx || (idx == bestIdx && len(candidate.open) > bestLen) {
			bestIdx, bestLen, bestSuppress = idx, len(candidate.open), candidate.suppress
		}
	}
	return bestIdx, bestLen, bestSuppress
}

func findTextualClose(s string) (int, int) {
	bestIdx, bestLen := -1, 0
	for _, closeTag := range []string{textualToolCallClose, textualInvokeClose, textualToolResultClose} {
		idx := strings.Index(s, closeTag)
		if idx < 0 {
			continue
		}
		if bestIdx < 0 || idx < bestIdx {
			bestIdx, bestLen = idx, len(closeTag)
		}
	}
	return bestIdx, bestLen
}

// textualOpenHoldbackAt 返回该从哪个位置开始扣住不发：
// 尾部若是块开标签或剥除标签的真前缀（比完整标签短），要等下一块拼上再判断。
func textualOpenHoldbackAt(s string) int {
	start := strings.LastIndexByte(s, '<')
	if start < 0 {
		return len(s)
	}
	tail := s[start:]
	for _, token := range []string{textualToolCallOpen, textualInvokeOpen, textualToolResultOpen} {
		if len(tail) < len(token) && strings.HasPrefix(token, tail) {
			return start
		}
	}
	for _, token := range textualStripTags {
		if len(tail) < len(token) && strings.HasPrefix(token, tail) {
			return start
		}
	}
	return len(s)
}

var textualAttrPattern = regexp.MustCompile(`(\w+)\s*=\s*"([^"]*)"`)
var textualParameterPattern = regexp.MustCompile(`(?s)<parameter\s+name\s*=\s*"([^"]*)"\s*>(.*?)</parameter>`)

// parseTextualToolCall 从一个完整的标记块里解析出客户端调用。
// 解析不出（名字不在声明集合里、格式面目全非）返回 nil，调用方吞块计数。
func parseTextualToolCall(block string, declared map[string]string) *McpToolCall {
	headEnd := strings.IndexByte(block, '>')
	if headEnd < 0 {
		return nil
	}
	head := block[:headEnd]
	body := block[headEnd+1:]
	if closeIdx, _ := findTextualClose(body); closeIdx >= 0 {
		body = body[:closeIdx]
	}

	attrs := map[string]string{}
	for _, match := range textualAttrPattern.FindAllStringSubmatch(head, -1) {
		attrs[strings.ToLower(match[1])] = match[2]
	}

	// name 属性优先；模型常写岔把工具名塞进 id（如 <tool_call id="Grep"…>），
	// id 命中声明集合时也认。
	clientName := ""
	if candidate := resolveTextualToolName(attrs["name"], declared); candidate != "" {
		clientName = candidate
	} else if candidate := resolveTextualToolName(attrs["id"], declared); candidate != "" {
		clientName = candidate
	}
	if clientName == "" {
		return nil
	}

	arguments := parseTextualToolArguments(body)
	// 另一种走样：参数直接写成起始标签的属性
	// （<tool_call id="Grep" pattern="…" path="…">，标签体为空）。
	// 体内解析不出参数时，把 id/name 之外的属性当参数用，聊胜于吞块。
	if arguments == nil || string(arguments) == "{}" {
		if headArgs := headAttributeArguments(attrs); headArgs != nil {
			arguments = headArgs
		}
	}
	if arguments == nil {
		return nil
	}
	// CallID 刻意留空：模型写的 id 多半是从重放历史里抄来的（如 call_1），
	// 沿用会与历史调用或同轮其他文本调用撞车——Anthropic 客户端会拒绝
	// 重复的 tool_use.id。留空让 NewOpenAIToolCallID 铸造全新唯一 id。
	return &McpToolCall{
		Name:      clientName,
		Arguments: arguments,
	}
}

func headAttributeArguments(attrs map[string]string) json.RawMessage {
	args := make(map[string]any, len(attrs))
	for key, value := range attrs {
		if key == "id" || key == "name" {
			continue
		}
		var typed any
		if err := json.Unmarshal([]byte(value), &typed); err == nil {
			switch typed.(type) {
			case float64, bool, nil:
				args[key] = typed
				continue
			}
		}
		args[key] = value
	}
	if len(args) == 0 {
		return nil
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		return nil
	}
	return encoded
}

func resolveTextualToolName(raw string, declared map[string]string) string {
	name := NormalizeToolName(raw)
	if name == "" {
		return ""
	}
	return declared[strings.ToLower(name)]
}

func parseTextualToolArguments(body string) json.RawMessage {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return json.RawMessage(`{}`)
	}
	// 重放示范的格式：块体就是参数 JSON。
	if strings.HasPrefix(trimmed, "{") && json.Valid([]byte(trimmed)) {
		return json.RawMessage(trimmed)
	}
	// IDE 风格：<parameter name="k">v</parameter> 列表。
	matches := textualParameterPattern.FindAllStringSubmatch(trimmed, -1)
	if len(matches) == 0 {
		return nil
	}
	args := make(map[string]any, len(matches))
	for _, match := range matches {
		key := strings.TrimSpace(match[1])
		if key == "" {
			continue
		}
		value := strings.TrimSpace(match[2])
		// 数字/布尔/null 尽量还原类型，其余按字符串。
		var typed any
		if err := json.Unmarshal([]byte(value), &typed); err == nil {
			switch typed.(type) {
			case float64, bool, nil:
				args[key] = typed
				continue
			}
		}
		args[key] = value
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		return nil
	}
	return encoded
}
