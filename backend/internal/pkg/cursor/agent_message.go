package cursor

import "fmt"

// AgentServerMessage 的解析，以及 stub exec 的回执编码。

// ServerMessageKind 标记一条服务端消息的类型。
type ServerMessageKind string

const (
	KindUnknown       ServerMessageKind = ""
	KindTextDelta     ServerMessageKind = "text_delta"
	KindThinkingDelta ServerMessageKind = "thinking_delta"
	KindTurnEnded     ServerMessageKind = "turn_ended"
	KindExec          ServerMessageKind = "exec"
	// KindToolCall 是模型对客户端声明的 MCP 工具发起的调用。
	// 与 KindExec 的区别是它不需要在流上回执：调用方把它翻译成客户端协议的
	// tool_calls 后就结束这一轮，工具结果由客户端在下一次请求里带回来。
	KindToolCall   ServerMessageKind = "tool_call"
	KindCheckpoint ServerMessageKind = "conversation_checkpoint"
	KindKV         ServerMessageKind = "kv"
	KindQuery      ServerMessageKind = "interaction_query"
	KindHeartbeat  ServerMessageKind = "heartbeat"
	KindOther      ServerMessageKind = "other"
)

// ServerMessage 是解析后的一条服务端消息。
type ServerMessage struct {
	Kind ServerMessageKind

	TextDelta     string
	ThinkingDelta string

	// Exec 是服务端要求客户端执行的工具调用。
	Exec *ExecRequest
	// ToolCall 是对客户端声明的 MCP 工具的调用。
	ToolCall *McpToolCall
	// ConversationState 是 checkpoint 帧带回的会话状态，下一轮要原样回传。
	ConversationState []byte
}

// ExecRequest 是一次工具调用请求。
//
// 我们只需要足够回一个 stub 成功回执的信息：id 与 execId 用于关联，
// ArgFieldNum 决定回执要放在哪个结果字段上。
type ExecRequest struct {
	ID          uint64
	ExecID      string
	ArgFieldNum int
	Kind        string
}

// execArgFields 把 exec 参数的字段号映射成工具名。取自反代的 EXEC_ARG_FIELDS。
var execArgFields = map[int]string{
	2:  "shell",
	3:  "write",
	4:  "delete",
	5:  "grep",
	7:  "read",
	8:  "ls",
	14: "shell_stream",
	29: "redacted_read",
	37: "subagent_await",
	41: "shell_allowlist_precheck",
	42: "mcp_allowlist_precheck",
	43: "web_fetch_allowlist_precheck",
}

// ParseServerMessage 解析一帧 AgentServerMessage。
//
// 未知字段一律归到 KindOther 而不是报错：上游随时可能加新事件，
// 一条不认识的消息不该让整轮对话失败。
func ParseServerMessage(payload []byte) (ServerMessage, error) {
	message := ServerMessage{Kind: KindUnknown}
	root, err := ReadFields(payload)
	if err != nil {
		return message, err
	}

	for _, field := range root {
		switch field.Number {
		case 1: // interaction_update
			if field.WireType != wireBytes {
				continue
			}
			updates, err := ReadFields(field.Bytes)
			if err != nil {
				return message, err
			}
			applyInteractionUpdate(&message, updates)
		case 2: // exec_server_message
			if field.WireType != wireBytes {
				continue
			}
			exec, toolCall, err := parseExecServerMessage(field.Bytes)
			if err != nil {
				return message, err
			}
			// MCP 工具调用优先：那是客户端自己声明的工具，要交回客户端执行，
			// 绝不能落进 exec 的 stub 回执分支——回了 stub 等于告诉模型
			// 「你的工具坏了」，这一轮就废了。
			if toolCall != nil {
				message.Kind = KindToolCall
				message.ToolCall = toolCall
				message.Exec = exec
				continue
			}
			message.Kind = KindExec
			message.Exec = exec
		case 3: // conversation_checkpoint
			if field.WireType == wireBytes {
				message.Kind = KindCheckpoint
				message.ConversationState = field.Bytes
			}
		case 4:
			message.Kind = KindKV
		case 7:
			message.Kind = KindQuery
		default:
			if message.Kind == KindUnknown {
				message.Kind = KindOther
			}
		}
	}
	return message, nil
}

func applyInteractionUpdate(message *ServerMessage, updates []Field) {
	for _, update := range updates {
		switch update.Number {
		case 1: // text_delta
			if update.WireType == wireBytes {
				inner, err := ReadFields(update.Bytes)
				if err == nil {
					message.Kind = KindTextDelta
					message.TextDelta = FieldString(inner, 1)
				}
			}
		case 4: // thinking_delta
			if update.WireType == wireBytes {
				inner, err := ReadFields(update.Bytes)
				if err == nil {
					message.Kind = KindThinkingDelta
					message.ThinkingDelta = FieldString(inner, 1)
				}
			}
		case 13:
			message.Kind = KindHeartbeat
		case 14:
			message.Kind = KindTurnEnded
		default:
			if message.Kind == KindUnknown {
				message.Kind = KindOther
			}
		}
	}
}

func parseExecServerMessage(payload []byte) (*ExecRequest, *McpToolCall, error) {
	fields, err := ReadFields(payload)
	if err != nil {
		return nil, nil, err
	}
	exec := &ExecRequest{}
	var toolCall *McpToolCall
	unknownArgField := 0
	for _, field := range fields {
		switch {
		case field.Number == 1 && field.WireType == wireVarint:
			exec.ID = field.Varint
		case field.Number == 15 && field.WireType == wireBytes:
			exec.ExecID = string(field.Bytes)
		case field.Number == mcpArgsField && field.WireType == wireBytes:
			// 解析失败时退回普通 exec：宁可回一个 stub 把这一轮收掉，
			// 也好过因为一条解不开的帧让整条流报错。
			if parsed, parseErr := parseMcpArgs(field.Bytes); parseErr == nil && parsed != nil {
				toolCall = parsed
			}
		default:
			if kind, ok := execArgFields[field.Number]; ok && exec.ArgFieldNum == 0 {
				exec.ArgFieldNum = field.Number
				exec.Kind = kind
			} else if field.WireType == wireBytes && unknownArgField == 0 {
				unknownArgField = field.Number
			}
		}
	}
	// execArgFields 只覆盖了已知工具，上游随时可能加新的。认不出就不回执的话
	// 上游会一直等，整轮对话挂死——退而求其次，把结果放回请求里出现的第一个
	// bytes 字段上，语义未必对，但至少这一轮能收尾。
	if exec.ArgFieldNum == 0 {
		exec.ArgFieldNum = unknownArgField
	}
	if toolCall != nil && toolCall.CallID == "" {
		// 上游没有单独给 MCP 调用发 id，用 exec id 顶上：
		// 客户端需要一个稳定的 tool_call_id 才能把结果对回来。
		toolCall.CallID = exec.ExecID
	}
	return exec, toolCall, nil
}

// EncodeExecClientMessage 编码一条 exec 回执（AgentClientMessage.field 2）。
func EncodeExecClientMessage(id uint64, execID string, resultFieldNum int, resultBytes []byte) []byte {
	// id 必须无条件写出（包括 0）：上游用它关联请求与回执。
	parts := [][]byte{EncodeVarintField(1, id)}
	if execID != "" {
		parts = append(parts, EncodeStringField(15, execID))
	}
	parts = append(parts, EncodeBytesField(resultFieldNum, resultBytes))
	return EncodeBytesField(2, concat(parts...))
}

// StubExecReplies 为一次工具调用生成「假装成功」的回执。
//
// 首期刻意不实现真实执行（对齐反代的 BYTES_ONLY）：这是一台多租户网关，
// 让上游模型驱动本机 shell / 文件系统是不可接受的。但也不能不回——
// 不回执上游会一直等，整轮对话就挂在那里。
func StubExecReplies(exec *ExecRequest) [][]byte {
	if exec == nil || exec.ArgFieldNum == 0 {
		return nil
	}

	switch exec.Kind {
	case "shell_stream":
		return stubShellStreamReplies(exec)
	case "shell":
		return [][]byte{EncodeExecClientMessage(exec.ID, exec.ExecID, 2, encodeShellSuccess(
			"stub: command execution is disabled on this gateway\n"))}
	case "read", "redacted_read":
		return [][]byte{EncodeExecClientMessage(exec.ID, exec.ExecID, exec.ArgFieldNum,
			EncodeBytesField(1, EncodeStringField(1, "stub: file access is disabled on this gateway\n")))}
	default:
		// 其余工具统一回一个空的成功结果：结构上合法，语义上什么都没做。
		return [][]byte{EncodeExecClientMessage(exec.ID, exec.ExecID, exec.ArgFieldNum,
			EncodeBytesField(1, nil))}
	}
}

func encodeShellSuccess(stdout string) []byte {
	return EncodeBytesField(1, concat(
		EncodeStringField(1, stdout),
		EncodeStringField(2, ""),
		EncodeVarintField(3, 0),
	))
}

// stubShellStreamReplies 复刻反代的流式 shell 回执序列：start → stdout → exit。
// shell_stream 的请求不能用一元的 shell_result 回，上游会认为格式不对。
func stubShellStreamReplies(exec *ExecRequest) [][]byte {
	const streamResultField = 14
	stdout := "stub: command execution is disabled on this gateway\n"
	return [][]byte{
		EncodeExecClientMessage(exec.ID, exec.ExecID, streamResultField, EncodeBytesField(4, nil)),
		EncodeExecClientMessage(exec.ID, exec.ExecID, streamResultField,
			EncodeBytesField(1, EncodeStringField(1, stdout))),
		EncodeExecClientMessage(exec.ID, exec.ExecID, streamResultField,
			EncodeBytesField(3, EncodeVarintField(1, 0))),
	}
}

// String 让 exec 请求在日志里可读。
func (e *ExecRequest) String() string {
	if e == nil {
		return "<nil exec>"
	}
	return fmt.Sprintf("exec{id=%d kind=%s field=%d}", e.ID, e.Kind, e.ArgFieldNum)
}
