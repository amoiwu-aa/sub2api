//go:build livespike

package cursor

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"
)

// 打真实上游的 spike，为 exec ↔ tool_calls 桥摸清协议地基。
//
// 要回答的问题：
//
//  1. 不带工具的普通问答能不能正常走到 turn_ended？（基线，排除账号/传输问题）
//  2. exec 请求里的参数长什么样？翻译成 tool_calls 需要命令行本身。
//  3. 什么形状的 exec 回执能让 Agent 接着往下走？现有的 stub 三帧会让它卡死，
//     但卡死原因是「结果内容是废话」还是「回执结构不对」，得分开验证。
//  4. 我们不回执时，上游能等多久？决定「挂起流」方案是否成立。
//
// 需要真实凭证，所以不进常规测试集：
//
//	go test -tags=livespike -run TestLiveSpike -v -timeout 30m ./internal/pkg/cursor
//
// 环境变量：
//
//	CURSOR_ACCESS_TOKEN  必填，账号 credentials 里的 access_token（session 类型）
//	SPIKE_NOREPLY_CAP    选填，不回执实验的最长观察秒数，默认 300

func spikeToken(t *testing.T) string {
	t.Helper()
	token := strings.TrimSpace(os.Getenv("CURSOR_ACCESS_TOKEN"))
	if token == "" {
		t.Skip("CURSOR_ACCESS_TOKEN is not set")
	}
	if !IsSessionToken(token) {
		t.Fatalf("token is not a session token (Agent 只认 type=session)")
	}
	return token
}

func spikeOptions(t *testing.T) *AgentOptions {
	t.Helper()
	token := spikeToken(t)
	return &AgentOptions{
		HTTPClient: &http.Client{
			Timeout:   30 * time.Minute,
			Transport: &http.Transport{ForceAttemptHTTP2: true},
		},
		AccessToken: token,
		Telemetry:   DeriveTelemetryIDs(token),
		SessionID:   "5b1f0c9a-2e44-4a17-9d8e-7c3b6a0f1d22",
	}
}

// ---------------------------------------------------------------------------
// 观测工具
// ---------------------------------------------------------------------------

type spikeFrame struct {
	At     time.Duration
	Kind   ServerMessageKind
	Detail string
	Raw    []byte
	// AfterReply 标记这一帧是否发生在我们回执之后。
	AfterReply bool
}

// dumpProto 把一段 protobuf 递归展开成可读文本。
// 上游没有 .proto，逆向字段语义全靠它。
func dumpProto(data []byte, indent string, depth int) string {
	if depth > 8 || len(data) == 0 {
		return ""
	}
	fields, err := ReadFields(data)
	if err != nil || len(fields) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, field := range fields {
		switch field.WireType {
		case wireVarint:
			sb.WriteString(fmt.Sprintf("%s#%d varint=%d\n", indent, field.Number, field.Varint))
		case wireBytes:
			if len(field.Bytes) == 0 {
				sb.WriteString(fmt.Sprintf("%s#%d empty\n", indent, field.Number))
				continue
			}
			if text, ok := printableString(field.Bytes); ok {
				sb.WriteString(fmt.Sprintf("%s#%d str(%d)=%s\n", indent, field.Number, len(field.Bytes), clip(text, 400)))
				continue
			}
			if nested := dumpProto(field.Bytes, indent+"  ", depth+1); nested != "" {
				sb.WriteString(fmt.Sprintf("%s#%d msg(%d):\n", indent, field.Number, len(field.Bytes)))
				sb.WriteString(nested)
				continue
			}
			sb.WriteString(fmt.Sprintf("%s#%d bytes(%d)=%s\n", indent, field.Number, len(field.Bytes), shortHex(field.Bytes)))
		default:
			sb.WriteString(fmt.Sprintf("%s#%d fixed=%d\n", indent, field.Number, field.Varint))
		}
	}
	return sb.String()
}

func printableString(data []byte) (string, bool) {
	if !utf8.Valid(data) {
		return "", false
	}
	text := string(data)
	for _, r := range text {
		if r == '\n' || r == '\t' || r == '\r' {
			continue
		}
		if !unicode.IsPrint(r) {
			return "", false
		}
	}
	return text, true
}

// spikeClipLimit 控制字符串字段的打印长度。取证时用 SPIKE_CLIP 放大到不截断。
var spikeClipLimit = func() int {
	if raw := strings.TrimSpace(os.Getenv("SPIKE_CLIP")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return n
		}
	}
	return 400
}()

func clip(s string, limit int) string {
	if limit < spikeClipLimit {
		limit = spikeClipLimit
	}
	if len(s) <= limit {
		return strconv.Quote(s)
	}
	return strconv.Quote(s[:limit]) + fmt.Sprintf("…(+%dB)", len(s)-limit)
}

func shortHex(data []byte) string {
	if len(data) > 160 {
		return hex.EncodeToString(data[:160]) + fmt.Sprintf("…(+%dB)", len(data)-160)
	}
	return hex.EncodeToString(data)
}

// ---------------------------------------------------------------------------
// 会话驱动
// ---------------------------------------------------------------------------

type spikeSession struct {
	t       *testing.T
	stream  *agentStream
	start   time.Time
	frames  []spikeFrame
	replied bool
	// verboseAfterReply 让回执之后的帧无条件全量打印，用于取证。
	verboseAfterReply bool

	mu       sync.Mutex
	textBuf  strings.Builder
	thinkBuf strings.Builder
}

func openSpikeSession(t *testing.T, ctx context.Context, prompt string) *spikeSession {
	t.Helper()
	return openSpikeSessionWithID(t, ctx, prompt, "spike-"+strconv.FormatInt(time.Now().UnixNano(), 36))
}

func openSpikeSessionWithID(t *testing.T, ctx context.Context, prompt, conversationID string) *spikeSession {
	t.Helper()

	opts := spikeOptions(t)
	body, err := EncodeRunRequest(RunRequestInput{
		Text:           prompt,
		ConversationID: conversationID,
		RequestContext: EncodeRequestContext(DefaultRequestContextEnv("spike")),
		ModelID:        AutoModelID,
		ModelParams:    []ModelParam{},
	})
	if err != nil {
		t.Fatalf("encode run request: %v", err)
	}

	stream, err := openAgentStream(ctx, opts, conversationID)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	session := &spikeSession{t: t, stream: stream, start: time.Now()}
	if err := stream.send(body); err != nil {
		t.Fatalf("send run request: %v", err)
	}
	stream.startHeartbeat(ctx)
	return session
}

// pump 读帧直到 stop 返回 true、流结束或 deadline 到点。
// verbose 控制是否逐帧打印——变体扫描时只打印关键帧，免得日志淹没。
func (s *spikeSession) pump(deadline time.Duration, verbose bool, onFrame func(ServerMessage, []byte) bool) {
	timer := time.AfterFunc(deadline, func() {
		s.log("=== 观察窗口 %.0fs 到点，关流 ===", deadline.Seconds())
		s.stream.close()
	})
	defer timer.Stop()

	for {
		envelope, err := s.stream.reader.Next()
		if err != nil {
			s.log("流结束: %v", err)
			return
		}
		at := time.Since(s.start)

		if envelope.EndStream() {
			detail := "end_stream"
			if connectErr := ParseEndStreamError(envelope.Payload); connectErr != nil {
				detail = "end_stream ERROR: " + connectErr.Error()
			}
			s.log("%s", detail)
			// 原始载荷要留着：ConnectError 只认 code/message，而 Connect 的错误帧
			// 还可能带 details。上游把 message 写成 "Error" 时，能不能定位问题全看它。
			s.frames = append(s.frames, spikeFrame{
				At: at, Kind: "end_stream", Detail: detail, Raw: envelope.Payload, AfterReply: s.replied,
			})
			return
		}

		message, parseErr := ParseServerMessage(envelope.Payload)
		if parseErr != nil {
			s.log("解析失败: %v raw=%s", parseErr, shortHex(envelope.Payload))
			continue
		}

		verbose := verbose || (s.replied && s.verboseAfterReply)
		frame := spikeFrame{At: at, Kind: message.Kind, Raw: envelope.Payload, AfterReply: s.replied}
		switch message.Kind {
		case KindTextDelta:
			frame.Detail = message.TextDelta
			s.mu.Lock()
			s.textBuf.WriteString(message.TextDelta)
			s.mu.Unlock()
			if verbose {
				s.log("text: %q", message.TextDelta)
			}
		case KindThinkingDelta:
			frame.Detail = message.ThinkingDelta
			s.mu.Lock()
			s.thinkBuf.WriteString(message.ThinkingDelta)
			s.mu.Unlock()
			if verbose {
				s.log("thinking: %q", message.ThinkingDelta)
			}
		case KindTurnEnded:
			s.log("*** TURN_ENDED ***")
		case KindHeartbeat:
			if verbose {
				s.log("heartbeat")
			}
		case KindKV:
			if verbose {
				s.log("kv:\n%s", dumpProto(envelope.Payload, "        ", 0))
			}
		case KindExec:
			s.log("EXEC execId=%s argField=%d kind=%q id=%d",
				message.Exec.ExecID, message.Exec.ArgFieldNum, message.Exec.Kind, message.Exec.ID)
			s.log("exec 结构:\n%s", dumpProto(envelope.Payload, "        ", 0))
		default:
			if verbose {
				s.log("%s:\n%s", message.Kind, dumpProto(envelope.Payload, "        ", 0))
			}
		}
		s.frames = append(s.frames, frame)

		if onFrame != nil && onFrame(message, envelope.Payload) {
			return
		}
	}
}

func (s *spikeSession) log(format string, args ...any) {
	s.t.Logf("[%6.1fs] "+format, append([]any{time.Since(s.start).Seconds()}, args...)...)
}

func (s *spikeSession) text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.textBuf.String()
}

func (s *spikeSession) turnEnded() bool {
	for _, frame := range s.frames {
		if frame.Kind == KindTurnEnded {
			return true
		}
	}
	return false
}

// framesAfterReply 统计回执之后收到的有效帧（心跳不算）。
func (s *spikeSession) framesAfterReply() (total int, text int, ended bool) {
	for _, frame := range s.frames {
		if !frame.AfterReply || frame.Kind == KindHeartbeat {
			continue
		}
		total++
		if frame.Kind == KindTextDelta {
			text++
		}
		if frame.Kind == KindTurnEnded {
			ended = true
		}
	}
	return
}

const spikeProvokeShell = "请在终端里执行 `echo ringstar-spike-marker` 这一条命令，" +
	"然后把命令的真实输出原样告诉我。必须真的执行，不要凭空编造输出。"

const spikeMarkerOutput = "ringstar-spike-marker\n"

// ---------------------------------------------------------------------------
// 实验一：基线。不涉及工具的普通问答能不能正常收尾？
// ---------------------------------------------------------------------------

func TestLiveSpikeBaselineNoTool(t *testing.T) {
	t.Logf("=== 基线：纯文本问答，验证账号/传输/协议本身没问题 ===")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	session := openSpikeSession(t, ctx, "用一句话回答：1+1 等于几？不要使用任何工具。")
	session.pump(120*time.Second, true, func(message ServerMessage, _ []byte) bool {
		return message.Kind == KindTurnEnded
	})

	t.Logf("---- 结论 ----")
	t.Logf("turn_ended=%v 正文=%q", session.turnEnded(), session.text())
	if !session.turnEnded() {
		t.Errorf("基线都没走到 turn_ended，说明问题不在 exec 桥接上")
	}
}

// ---------------------------------------------------------------------------
// 实验二：exec 回执形状扫描。
//
// 现有 stub 三帧回执会让 Agent 卡死。卡死可能是两个原因之一：
// 结果内容是「工具被禁用」这种废话让模型放弃，或者回执的 protobuf 结构压根不对。
// 换不同形状逐个试，能同时区分这两种可能。
// ---------------------------------------------------------------------------

type replyVariant struct {
	Name  string
	Note  string
	Build func(exec *ExecRequest) [][]byte
}

func spikeReplyVariants() []replyVariant {
	const streamField = 14
	stdout := spikeMarkerOutput

	return []replyVariant{
		{
			Name: "stub3-original",
			Note: "现有实现：start + stdout + exit，内容换成真输出",
			Build: func(exec *ExecRequest) [][]byte {
				return [][]byte{
					EncodeExecClientMessage(exec.ID, exec.ExecID, streamField, EncodeBytesField(4, nil)),
					EncodeExecClientMessage(exec.ID, exec.ExecID, streamField,
						EncodeBytesField(1, EncodeStringField(1, stdout))),
					EncodeExecClientMessage(exec.ID, exec.ExecID, streamField,
						EncodeBytesField(3, EncodeVarintField(1, 0))),
				}
			},
		},
		{
			Name: "stream-no-id-field",
			Note: "不写 field 1（上游的 exec 请求里根本没有这个字段）",
			Build: func(exec *ExecRequest) [][]byte {
				build := func(inner []byte) []byte {
					return EncodeBytesField(2, concat(
						EncodeStringField(15, exec.ExecID),
						EncodeBytesField(streamField, inner),
					))
				}
				return [][]byte{
					build(EncodeBytesField(4, nil)),
					build(EncodeBytesField(1, EncodeStringField(1, stdout))),
					build(EncodeBytesField(3, EncodeVarintField(1, 0))),
				}
			},
		},
		{
			Name: "stream-exit-with-stdout",
			Note: "退出帧里同时带 stdout 和 exit_code",
			Build: func(exec *ExecRequest) [][]byte {
				return [][]byte{
					EncodeExecClientMessage(exec.ID, exec.ExecID, streamField, EncodeBytesField(4, nil)),
					EncodeExecClientMessage(exec.ID, exec.ExecID, streamField,
						EncodeBytesField(1, EncodeStringField(1, stdout))),
					EncodeExecClientMessage(exec.ID, exec.ExecID, streamField,
						EncodeBytesField(3, concat(
							EncodeVarintField(1, 0),
							EncodeStringField(2, stdout),
						))),
				}
			},
		},
		{
			Name: "unary-shell-result-field2",
			Note: "按一元 shell 回：{2:{1:{stdout,stderr,exit}}}",
			Build: func(exec *ExecRequest) [][]byte {
				return [][]byte{
					EncodeExecClientMessage(exec.ID, exec.ExecID, 2, encodeShellSuccess(stdout)),
				}
			},
		},
		{
			Name: "unary-on-request-field",
			Note: "一元结果，但放在请求用的字段号 14 上",
			Build: func(exec *ExecRequest) [][]byte {
				return [][]byte{
					EncodeExecClientMessage(exec.ID, exec.ExecID, streamField, encodeShellSuccess(stdout)),
				}
			},
		},
		{
			Name: "stream-stdout-then-exit",
			Note: "省掉 start 帧，只发 stdout + exit",
			Build: func(exec *ExecRequest) [][]byte {
				return [][]byte{
					EncodeExecClientMessage(exec.ID, exec.ExecID, streamField,
						EncodeBytesField(1, EncodeStringField(1, stdout))),
					EncodeExecClientMessage(exec.ID, exec.ExecID, streamField,
						EncodeBytesField(3, EncodeVarintField(1, 0))),
				}
			},
		},
		{
			Name: "stream-exit-only",
			Note: "只发退出帧，看是否 stdout 帧才是多余的那个",
			Build: func(exec *ExecRequest) [][]byte {
				return [][]byte{
					EncodeExecClientMessage(exec.ID, exec.ExecID, streamField,
						EncodeBytesField(3, EncodeVarintField(1, 0))),
				}
			},
		},
		{
			Name: "stream-stdout-field2-of-chunk",
			Note: "stdout 块换成子字段 2（现在用的是 1）",
			Build: func(exec *ExecRequest) [][]byte {
				return [][]byte{
					EncodeExecClientMessage(exec.ID, exec.ExecID, streamField, EncodeBytesField(4, nil)),
					EncodeExecClientMessage(exec.ID, exec.ExecID, streamField,
						EncodeBytesField(1, EncodeStringField(2, stdout))),
					EncodeExecClientMessage(exec.ID, exec.ExecID, streamField,
						EncodeBytesField(3, EncodeVarintField(1, 0))),
				}
			},
		},
	}
}

func TestLiveSpikeReplyVariants(t *testing.T) {
	observe := 30 * time.Second
	if raw := strings.TrimSpace(os.Getenv("SPIKE_VARIANT_OBSERVE")); raw != "" {
		seconds, err := strconv.Atoi(raw)
		if err == nil {
			observe = time.Duration(seconds) * time.Second
		}
	}

	variants := spikeReplyVariants()
	if only := strings.TrimSpace(os.Getenv("SPIKE_VARIANT")); only != "" {
		filtered := variants[:0:0]
		for _, v := range variants {
			if v.Name == only {
				filtered = append(filtered, v)
			}
		}
		variants = filtered
	}

	type outcome struct {
		name    string
		note    string
		gotExec bool
		total   int
		text    int
		ended   bool
		out     string
	}
	results := make([]outcome, 0, len(variants))

	for _, variant := range variants {
		t.Logf("")
		t.Logf("################ 变体 %s ################", variant.Name)
		t.Logf("# %s", variant.Note)

		ctx, cancel := context.WithTimeout(context.Background(), observe+3*time.Minute)
		session := openSpikeSession(t, ctx, spikeProvokeShell)

		gotExec := false
		session.pump(observe+90*time.Second, false, func(message ServerMessage, _ []byte) bool {
			switch message.Kind {
			case KindExec:
				if gotExec {
					// 后续 exec 一律用原 stub 顶住，本轮只考察第一次。
					for _, reply := range StubExecReplies(message.Exec) {
						_ = session.stream.send(reply)
					}
					return false
				}
				gotExec = true
				replies := variant.Build(message.Exec)
				for i, reply := range replies {
					if err := session.stream.send(reply); err != nil {
						session.log("回执 %d 写失败: %v", i, err)
					}
				}
				session.replied = true
				session.log(">>> 变体 %s 已回执 %d 帧，开始观察 %.0fs <<<",
					variant.Name, len(replies), observe.Seconds())
				// 回执后另起一个观察窗口。
				time.AfterFunc(observe, session.stream.close)
				return false
			case KindTurnEnded:
				return true
			}
			return false
		})

		total, text, ended := session.framesAfterReply()
		results = append(results, outcome{
			name: variant.Name, note: variant.Note, gotExec: gotExec,
			total: total, text: text, ended: ended, out: session.text(),
		})
		session.stream.close()
		cancel()
	}

	t.Logf("")
	t.Logf("================ 变体扫描汇总 ================")
	t.Logf("%-30s %-8s %-8s %-8s %-8s", "变体", "触发exec", "回执后帧", "文本帧", "turn_ended")
	for _, r := range results {
		t.Logf("%-30s %-8v %-8d %-8d %-8v", r.name, r.gotExec, r.total, r.text, r.ended)
	}
	t.Logf("")
	for _, r := range results {
		if r.out != "" {
			t.Logf("[%s] 正文: %s", r.name, clip(r.out, 600))
		}
	}
}

// ---------------------------------------------------------------------------
// 取证：回执之后上游到底记了什么。
//
// 八种回执形状全都同样卡死，而 kv 帧显示服务端确实把结果写进了会话——
// 说明问题多半不在 protobuf 结构，而在结果的语义。把回执后的每一帧完整
// 打出来（尤其是那条 role=user 的 kv），才能看清上游认为发生了什么。
// ---------------------------------------------------------------------------

func TestLiveSpikeExecReplyForensics(t *testing.T) {
	observe := 60 * time.Second
	if raw := strings.TrimSpace(os.Getenv("SPIKE_VARIANT_OBSERVE")); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil {
			observe = time.Duration(seconds) * time.Second
		}
	}
	t.Logf("=== 取证：标准三帧回执后，完整 dump 上游的每一帧 ===")

	ctx, cancel := context.WithTimeout(context.Background(), observe+5*time.Minute)
	defer cancel()

	session := openSpikeSession(t, ctx, spikeProvokeShell)
	session.pump(observe+90*time.Second, false, func(message ServerMessage, _ []byte) bool {
		switch message.Kind {
		case KindExec:
			if session.replied {
				for _, reply := range StubExecReplies(message.Exec) {
					_ = session.stream.send(reply)
				}
				return false
			}
			const streamField = 14
			replies := [][]byte{
				EncodeExecClientMessage(message.Exec.ID, message.Exec.ExecID, streamField, EncodeBytesField(4, nil)),
				EncodeExecClientMessage(message.Exec.ID, message.Exec.ExecID, streamField,
					EncodeBytesField(1, EncodeStringField(1, spikeMarkerOutput))),
				EncodeExecClientMessage(message.Exec.ID, message.Exec.ExecID, streamField,
					EncodeBytesField(3, EncodeVarintField(1, 0))),
			}
			for _, reply := range replies {
				_ = session.stream.send(reply)
				t.Logf("        回执帧: %s", hex.EncodeToString(reply))
			}
			session.replied = true
			session.verboseAfterReply = true
			session.log(">>> 已回执，以下为上游后续所有帧的完整内容 <<<")
			time.AfterFunc(observe, session.stream.close)
			return false
		case KindTurnEnded:
			return true
		}
		return false
	})

	t.Logf("---- 结论 ----")
	total, text, ended := session.framesAfterReply()
	t.Logf("回执后帧数=%d 文本帧=%d turn_ended=%v 正文=%q", total, text, ended, session.text())
}

// ---------------------------------------------------------------------------
// 续跑探测：喂完 exec 结果后，要发什么才能让 Agent 接着跑？
//
// 取证已经证明回执被上游收下并回显了 stdout，所以结构没问题——它只是不肯自己
// 发起下一次模型调用。合理的解释是这个 agent 循环由客户端驱动：真实 IDE 在
// 工具执行完之后还要再推一帧。RunRequest 的 field 2 是个 action 包装器
// （现在只用了 {1: 用户消息}），别的 variant 很可能就是「带着工具结果继续」。
// ---------------------------------------------------------------------------

type continuationProbe struct {
	Name  string
	Note  string
	Build func(conversationID string) []byte
}

// bareRunRequest 拼一个不带任何 action 的 RunRequest 骨架，
// action 部分由各个探测变体自己补。
func bareRunRequest(conversationID string, action []byte) []byte {
	parts := [][]byte{}
	if action != nil {
		parts = append(parts, EncodeBytesField(2, action))
	}
	parts = append(parts,
		EncodeBytesField(4, nil),
		EncodeStringField(5, conversationID),
		EncodeBytesField(9, EncodeRequestedModel(RequestedModel{
			ModelID: AutoModelID,
			Params:  []ModelParam{},
		})),
		EncodeBoolField(10, false),
		encodeSelectedSubagentModels(officialSelectedSubagentModels()),
		EncodeBoolField(19, true),
		EncodeBoolField(21, false),
		EncodeBoolField(22, false),
		EncodeBoolField(23, true),
	)
	return EncodeBytesField(1, concat(parts...))
}

func continuationProbes() []continuationProbe {
	probes := []continuationProbe{
		{
			Name:  "run-no-action",
			Note:  "RunRequest 完全不带 action，只带 conversation id 与模型",
			Build: func(id string) []byte { return bareRunRequest(id, nil) },
		},
		{
			Name:  "run-empty-action",
			Note:  "RunRequest 带一个空 action 包装",
			Build: func(id string) []byte { return bareRunRequest(id, nil) },
		},
	}
	// action 包装器里逐个试别的 variant 号：{1:...} 是用户消息，
	// 「继续」多半藏在紧邻的几个号里。
	for _, variant := range []int{2, 3, 4, 5, 6, 7} {
		v := variant
		probes = append(probes, continuationProbe{
			Name:  fmt.Sprintf("run-action-variant-%d", v),
			Note:  fmt.Sprintf("RunRequest.field2 = {%d: 空消息}", v),
			Build: func(id string) []byte { return bareRunRequest(id, EncodeBytesField(v, nil)) },
		})
	}
	// AgentClientMessage 顶层：1=RunRequest 2=exec 回执 7=心跳，
	// 其余号里可能有一个「turn 继续」的信号。
	for _, field := range []int{3, 4, 5, 6, 8, 9} {
		f := field
		probes = append(probes, continuationProbe{
			Name:  fmt.Sprintf("clientmsg-field-%d", f),
			Note:  fmt.Sprintf("AgentClientMessage.field%d = 空", f),
			Build: func(string) []byte { return EncodeBytesField(f, nil) },
		})
	}
	return probes
}

func TestLiveSpikeContinuationProbe(t *testing.T) {
	observe := 20 * time.Second
	if raw := strings.TrimSpace(os.Getenv("SPIKE_VARIANT_OBSERVE")); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil {
			observe = time.Duration(seconds) * time.Second
		}
	}

	probes := continuationProbes()
	if only := strings.TrimSpace(os.Getenv("SPIKE_PROBE")); only != "" {
		filtered := probes[:0:0]
		for _, p := range probes {
			if p.Name == only {
				filtered = append(filtered, p)
			}
		}
		probes = filtered
	}

	type outcome struct {
		name  string
		total int
		text  int
		ended bool
		out   string
		err   string
	}
	results := make([]outcome, 0, len(probes))

	for _, probe := range probes {
		t.Logf("")
		t.Logf("############ 探测 %s ############", probe.Name)
		t.Logf("# %s", probe.Note)

		ctx, cancel := context.WithTimeout(context.Background(), observe+3*time.Minute)
		conversationID := "spike-" + strconv.FormatInt(time.Now().UnixNano(), 36)
		session := openSpikeSessionWithID(t, ctx, spikeProvokeShell, conversationID)

		sendErr := ""
		session.pump(observe+90*time.Second, false, func(message ServerMessage, _ []byte) bool {
			switch message.Kind {
			case KindExec:
				if session.replied {
					for _, reply := range StubExecReplies(message.Exec) {
						_ = session.stream.send(reply)
					}
					return false
				}
				for _, reply := range StubExecRepliesWithOutput(message.Exec, spikeMarkerOutput) {
					_ = session.stream.send(reply)
				}
				session.replied = true
				session.verboseAfterReply = true
				// 结果落地后再推续跑帧，避免和回执抢顺序。
				time.Sleep(300 * time.Millisecond)
				if err := session.stream.send(probe.Build(conversationID)); err != nil {
					sendErr = err.Error()
				}
				session.log(">>> 已回执 + 推送续跑帧 %s，观察 %.0fs <<<", probe.Name, observe.Seconds())
				time.AfterFunc(observe, session.stream.close)
				return false
			case KindTurnEnded:
				return true
			}
			return false
		})

		total, text, ended := session.framesAfterReply()
		results = append(results, outcome{
			name: probe.Name, total: total, text: text,
			ended: ended, out: session.text(), err: sendErr,
		})
		session.stream.close()
		cancel()
	}

	t.Logf("")
	t.Logf("================ 续跑探测汇总 ================")
	t.Logf("%-28s %-10s %-8s %-11s %s", "探测", "回执后帧", "文本帧", "turn_ended", "发送错误")
	for _, r := range results {
		t.Logf("%-28s %-10d %-8d %-11v %s", r.name, r.total, r.text, r.ended, r.err)
	}
	for _, r := range results {
		if r.out != "" {
			t.Logf("[%s] 正文: %s", r.name, clip(r.out, 800))
		}
	}
}

// StubExecRepliesWithOutput 与 StubExecReplies 同构，但把真实输出塞进去。
func StubExecRepliesWithOutput(exec *ExecRequest, stdout string) [][]byte {
	const streamField = 14
	switch exec.Kind {
	case "shell_stream":
		return [][]byte{
			EncodeExecClientMessage(exec.ID, exec.ExecID, streamField, EncodeBytesField(4, nil)),
			EncodeExecClientMessage(exec.ID, exec.ExecID, streamField,
				EncodeBytesField(1, EncodeStringField(1, stdout))),
			EncodeExecClientMessage(exec.ID, exec.ExecID, streamField,
				EncodeBytesField(3, EncodeVarintField(1, 0))),
		}
	case "shell":
		return [][]byte{EncodeExecClientMessage(exec.ID, exec.ExecID, 2, encodeShellSuccess(stdout))}
	default:
		return StubExecReplies(exec)
	}
}

// ---------------------------------------------------------------------------
// MCP 工具验证：声明客户端工具后，模型会不会通过 mcp_args 回调它？
//
// 这是整个 tool-calling 桥的地基。通过的话，opencode / Claude Code / Codex
// 的工具就能原样声明给 Cursor，工具调用交回客户端执行，网关只做协议翻译。
// ---------------------------------------------------------------------------

func spikeBashTool() McpTool {
	return McpTool{
		Name:        "Bash",
		Description: "Execute a shell command and return its stdout, stderr and exit code.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"command": {"type": "string", "description": "The shell command to execute"},
				"timeout": {"type": "number", "description": "Timeout in seconds"}
			},
			"required": ["command"]
		}`),
	}
}

func spikeReadTool() McpTool {
	return McpTool{
		Name:        "Read",
		Description: "Read the contents of a file from the local filesystem.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"file_path": {"type": "string", "description": "Absolute path to the file"},
				"limit": {"type": "number", "description": "Maximum number of lines"}
			},
			"required": ["file_path"]
		}`),
	}
}

func openSpikeSessionWithTools(
	t *testing.T, ctx context.Context, prompt, conversationID string, tools []McpTool,
) *spikeSession {
	t.Helper()

	opts := spikeOptions(t)
	body, err := EncodeRunRequest(RunRequestInput{
		Text:           prompt,
		ConversationID: conversationID,
		RequestContext: EncodeRequestContext(DefaultRequestContextEnv("spike")),
		ModelID:        AutoModelID,
		ModelParams:    []ModelParam{},
		Tools:          tools,
	})
	if err != nil {
		t.Fatalf("encode run request: %v", err)
	}
	stream, err := openAgentStream(ctx, opts, conversationID)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	session := &spikeSession{t: t, stream: stream, start: time.Now()}
	if err := stream.send(body); err != nil {
		t.Fatalf("send run request: %v", err)
	}
	stream.startHeartbeat(ctx)
	return session
}

// TestLiveSpikeMcpToolVisibility 用一个不可能重名的工具名，直接问模型看不看得见它。
//
// 上一轮模型没走 MCP 而是用了自带的 shell，有两种可能：声明压根没送达，
// 或者送达了但模型更偏好内置工具。问一句就能分开这两种情况。
func TestLiveSpikeMcpToolVisibility(t *testing.T) {
	probe := McpTool{
		Name:        "ringstar_probe_zx9",
		Description: "A uniquely named probe tool that echoes back a marker string.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"marker":{"type":"string"}},"required":["marker"]}`),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	session := openSpikeSessionWithTools(t, ctx,
		"逐条列出你当前可以调用的全部工具的准确名称，一个都不要漏。不要调用任何工具，只列名字。",
		"spike-"+strconv.FormatInt(time.Now().UnixNano(), 36),
		[]McpTool{probe})
	session.pump(120*time.Second, false, func(message ServerMessage, _ []byte) bool {
		return message.Kind == KindTurnEnded || message.Kind == KindToolCall || message.Kind == KindExec
	})

	answer := session.text()
	t.Logf("---- 结论 ----")
	t.Logf("模型回答:\n%s", answer)
	if strings.Contains(answer, "ringstar_probe_zx9") {
		t.Logf("*** 声明已送达：模型看得见我们注册的 MCP 工具 ***")
	} else {
		t.Logf("*** 声明未生效：模型的工具清单里没有 ringstar_probe_zx9 ***")
	}
}

func TestLiveSpikeMcpToolCall(t *testing.T) {
	t.Logf("=== 声明 MCP 工具 Bash + Read，配合工具策略前言，看是否走 mcp_args ===")

	tools := []McpTool{spikeBashTool(), spikeReadTool()}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	conversationID := "spike-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	prompt := ToolPolicyPreamble(tools) + "\n\n" + spikeProvokeShell
	t.Logf("工具策略前言:\n%s", ToolPolicyPreamble(tools))
	session := openSpikeSessionWithTools(t, ctx, prompt, conversationID, tools)

	var toolCalls []*McpToolCall
	session.pump(120*time.Second, true, func(message ServerMessage, raw []byte) bool {
		switch message.Kind {
		case KindToolCall:
			toolCalls = append(toolCalls, message.ToolCall)
			t.Logf("        *** MCP 工具调用 *** name=%q call_id=%q args=%s",
				message.ToolCall.Name, message.ToolCall.CallID, message.ToolCall.Arguments)
			t.Logf("        完整帧结构:\n%s", dumpProto(raw, "        ", 0))
			// 无状态桥不在流上回执，收到调用即收尾。
			return true
		case KindExec:
			t.Logf("        （模型用了 Cursor 自带的 %q，不是声明的 MCP 工具）", message.Exec.Kind)
			return true
		case KindTurnEnded:
			return true
		}
		return false
	})

	t.Logf("---- 结论 ----")
	if len(toolCalls) == 0 {
		t.Errorf("没有收到 mcp_args 工具调用；正文=%q turn_ended=%v", session.text(), session.turnEnded())
		return
	}
	for _, call := range toolCalls {
		t.Logf("工具=%s 入参=%s", call.Name, call.Arguments)
	}
	t.Logf("*** MCP 工具声明通路验证成功 ***")
}

// TestLiveSpikeMcpToolRoundTrip 验证完整回合：声明工具 → 拿到调用 → 用重放把
// 工具结果带回去 → 模型基于真实结果继续作答。
func TestLiveSpikeMcpToolRoundTrip(t *testing.T) {
	t.Logf("=== 完整回合：工具调用 → 无状态重放带回结果 → 模型据此作答 ===")

	tools := []McpTool{spikeBashTool()}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	// 第一轮：拿到工具调用。与线上一样走 Conversation.Render()，
	// 工具策略前言由渲染器自己加上。
	firstPrompt := (&Conversation{
		Tools: tools,
		Turns: []Turn{{Role: RoleUser, Text: spikeProvokeShell}},
	}).Render()
	first := openSpikeSessionWithTools(t, ctx, firstPrompt,
		"spike-"+strconv.FormatInt(time.Now().UnixNano(), 36), tools)
	var call *McpToolCall
	first.pump(120*time.Second, false, func(message ServerMessage, _ []byte) bool {
		if message.Kind == KindToolCall {
			call = message.ToolCall
			return true
		}
		return message.Kind == KindTurnEnded
	})
	first.stream.close()

	if call == nil {
		t.Fatalf("第一轮没拿到工具调用，正文=%q", first.text())
	}
	t.Logf("第一轮拿到调用: %s(%s)", call.Name, call.Arguments)

	// 第二轮：完全按客户端会发回来的样子构造对话，用生产的渲染器重放。
	// 这里验证的就是 opencode / Claude Code 下一次请求走的那条路径。
	callID := NewOpenAIToolCallID(call.CallID)
	conversation := &Conversation{
		Tools: tools,
		Turns: []Turn{
			{Role: RoleUser, Text: spikeProvokeShell},
			{Role: RoleAssistant, ToolCalls: []ToolCall{{
				ID: callID, Name: call.Name, Arguments: string(call.Arguments),
			}}},
			{Role: RoleTool, ToolCallID: callID, ToolName: call.Name, Text: spikeMarkerOutput},
		},
	}
	replay := conversation.Render()
	t.Logf("重放渲染结果:\n%s", replay)

	second := openSpikeSessionWithTools(t, ctx, replay,
		"spike-"+strconv.FormatInt(time.Now().UnixNano(), 36), tools)
	second.pump(120*time.Second, false, func(message ServerMessage, _ []byte) bool {
		return message.Kind == KindTurnEnded
	})
	second.stream.close()

	t.Logf("---- 结论 ----")
	t.Logf("第二轮 turn_ended=%v 正文=%q", second.turnEnded(), second.text())
	if !second.turnEnded() {
		t.Errorf("重放这一轮没能正常收尾")
	}
	if !strings.Contains(second.text(), "ringstar-spike-marker") {
		t.Errorf("模型的回答里没有出现工具结果，重放没被采纳")
	} else {
		t.Logf("*** 完整回合走通：模型采纳了重放回去的工具结果 ***")
	}
}

// ---------------------------------------------------------------------------
// 实验三：不回执时上游能等多久。决定「挂起流」方案是否成立。
// ---------------------------------------------------------------------------

func TestLiveSpikeNoExecReply(t *testing.T) {
	observeCap := 300 * time.Second
	if raw := strings.TrimSpace(os.Getenv("SPIKE_NOREPLY_CAP")); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil {
			observeCap = time.Duration(seconds) * time.Second
		}
	}
	t.Logf("=== 收到 exec 后故意不回执，观察上游最长容忍 %.0fs ===", observeCap.Seconds())

	ctx, cancel := context.WithTimeout(context.Background(), observeCap+5*time.Minute)
	defer cancel()

	session := openSpikeSession(t, ctx, spikeProvokeShell)
	var firstExecAt time.Duration
	session.pump(observeCap+90*time.Second, false, func(message ServerMessage, _ []byte) bool {
		if message.Kind == KindExec && firstExecAt == 0 {
			firstExecAt = time.Since(session.start)
			session.log(">>> 故意不回执，开始计时 <<<")
			time.AfterFunc(observeCap, session.stream.close)
		}
		return false
	})

	if firstExecAt == 0 {
		t.Logf("结论：这一轮没触发 exec，测不出容忍时长")
		return
	}
	last := session.frames[len(session.frames)-1]
	t.Logf("---- 结论 ----")
	t.Logf("首个 exec 在 %.1fs，最后一帧在 %.1fs，静默 %.1fs 内上游未主动断流",
		firstExecAt.Seconds(), last.At.Seconds(), (last.At - firstExecAt).Seconds())
}
