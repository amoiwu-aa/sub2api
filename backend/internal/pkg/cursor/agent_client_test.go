package cursor

import (
	"context"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 这些用例跑在真实的 HTTP/2 测试服务器上，覆盖的是整条链路：
// 请求编码 → Connect 分帧 → 双向流 → 服务端消息解析 → stub exec 回执。

// interactionUpdate 把一个 interaction_update 包成 AgentServerMessage。
func interactionUpdate(fieldNum int, payload []byte) []byte {
	return EncodeBytesField(1, EncodeBytesField(fieldNum, payload))
}

func textDeltaMessage(text string) []byte {
	return interactionUpdate(1, EncodeStringField(1, text))
}

func thinkingDeltaMessage(text string) []byte {
	return interactionUpdate(4, EncodeStringField(1, text))
}

func turnEndedMessage() []byte {
	return interactionUpdate(14, nil)
}

func checkpointMessage(state []byte) []byte {
	return EncodeBytesField(3, state)
}

// execServerMessage 构造一条要求客户端跑工具的服务端消息。
func execServerMessage(id uint64, execID string, argFieldNum int) []byte {
	return EncodeBytesField(2, concat(
		EncodeVarintField(1, id),
		EncodeStringField(15, execID),
		EncodeBytesField(argFieldNum, EncodeStringField(1, "echo hi")),
	))
}

// execServerMessageWithoutArgs 构造一条我们无从回执的 exec：连参数字段都没有，
// 挑不出结果该放在哪个字段号上。
func execServerMessageWithoutArgs(id uint64, execID string) []byte {
	return EncodeBytesField(2, concat(
		EncodeVarintField(1, id),
		EncodeStringField(15, execID),
	))
}

// queryServerMessage 构造一条 interaction_query（AgentServerMessage field 7）。
func queryServerMessage() []byte {
	return EncodeBytesField(7, EncodeStringField(1, "pick-one"))
}

// shrinkStallTimeouts 把看门狗压到毫秒级，让超时用例能在测试里跑完。
func shrinkStallTimeouts(t *testing.T, stream, exec time.Duration) {
	t.Helper()
	originalStream, originalExec := streamStallTimeout, execStallTimeout
	streamStallTimeout, execStallTimeout = stream, exec
	t.Cleanup(func() {
		streamStallTimeout, execStallTimeout = originalStream, originalExec
	})
}

// agentTestServer 是一个最小的 Agent 桩：按脚本回帧，并记录客户端写入的帧。
type agentTestServer struct {
	t *testing.T
	// script 是收到第一帧请求后依次回出的消息。
	script [][]byte
	// endStreamPayload 非空时作为结束帧的内容。
	endStreamPayload []byte
	// hangAfterScript 复现上游「等一个不会到来的回执」的样子：脚本发完既不再发帧，
	// 也不结束流，把连接一直吊着。
	hangAfterScript bool
	// frameDelay 覆盖脚本帧之间的间隔。上游是逐个串行生成工具调用的，帧间隔就是
	// 一次生成的耗时；调大它才能复现「后续调用还没生成完就被关流丢掉」。
	frameDelay time.Duration

	mu       sync.Mutex
	received [][]byte
	headers  http.Header
}

func (s *agentTestServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.headers = r.Header.Clone()
	s.mu.Unlock()

	flusher, ok := w.(http.Flusher)
	require.True(s.t, ok)
	w.Header().Set("Content-Type", "application/connect+proto")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// 持续读客户端帧（首帧是 RunRequest，之后是心跳与 exec 回执）。
	done := make(chan struct{})
	go func() {
		defer close(done)
		reader := NewEnvelopeReader(r.Body)
		for {
			envelope, err := reader.Next()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.received = append(s.received, envelope.Payload)
			s.mu.Unlock()
		}
	}()

	for _, message := range s.script {
		if _, err := w.Write(EncodeEnvelope(message, 0)); err != nil {
			return
		}
		flusher.Flush()
		// 给客户端一点时间处理（例如回 exec 回执），模拟真实的交错。
		delay := s.frameDelay
		if delay <= 0 {
			delay = 5 * time.Millisecond
		}
		time.Sleep(delay)
	}
	if s.hangAfterScript {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
		return
	}

	_, _ = w.Write(EncodeEnvelope(s.endStreamPayload, connectFlagEndStream))
	flusher.Flush()

	select {
	case <-done:
	case <-time.After(time.Second):
	}
}

func (s *agentTestServer) capturedHeaders() http.Header {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.headers
}

func (s *agentTestServer) receivedFrames() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]byte, len(s.received))
	copy(out, s.received)
	return out
}

func startAgentTestServer(t *testing.T, server *agentTestServer) (*http.Client, string) {
	t.Helper()
	httpServer := httptest.NewUnstartedServer(server)
	httpServer.EnableHTTP2 = true
	httpServer.StartTLS()
	t.Cleanup(httpServer.Close)

	client := httpServer.Client()
	client.Timeout = 20 * time.Second
	return client, strings.TrimPrefix(httpServer.URL, "https://")
}

func testAgentOptions(t *testing.T, client *http.Client, host string) *AgentOptions {
	t.Helper()
	return &AgentOptions{
		HTTPClient:  client,
		AccessToken: sessionJWT(t),
		Telemetry:   DeriveTelemetryIDs("account-42"),
		SessionID:   "session-fixed",
		Host:        host,
	}
}

func TestRunAgentTurnStreamsDeltasAndCapturesCheckpoint(t *testing.T) {
	server := &agentTestServer{t: t, script: [][]byte{
		thinkingDeltaMessage("let me think"),
		textDeltaMessage("Hello"),
		textDeltaMessage(" world"),
		checkpointMessage([]byte{0xca, 0xfe}),
		turnEndedMessage(),
	}}
	client, host := startAgentTestServer(t, server)

	var deltas []AgentDelta
	result, err := RunAgentTurn(context.Background(), testAgentOptions(t, client, host),
		AgentTurnInput{Text: "hi", ConversationID: "conv-1", ModelID: "default"},
		func(delta AgentDelta) error {
			deltas = append(deltas, delta)
			return nil
		})
	require.NoError(t, err)

	require.Equal(t, "Hello world", result.Text)
	require.Equal(t, "let me think", result.Thinking)
	require.True(t, result.TurnEnded)
	require.Equal(t, []byte{0xca, 0xfe}, result.ConversationState)
	require.Equal(t, []AgentDelta{
		{Thinking: "let me think"}, {Text: "Hello"}, {Text: " world"},
	}, deltas)

	// 首帧必须是 RunRequest（AgentClientMessage 的 field 1）。
	frames := server.receivedFrames()
	require.NotEmpty(t, frames)
	fields, err := ReadFields(frames[0])
	require.NoError(t, err)
	require.Equal(t, 1, fields[0].Number)
}

func TestRunAgentTurnSendsOfficialHeaders(t *testing.T) {
	server := &agentTestServer{t: t, script: [][]byte{turnEndedMessage()}}
	client, host := startAgentTestServer(t, server)
	opts := testAgentOptions(t, client, host)

	_, err := RunAgentTurn(context.Background(), opts,
		AgentTurnInput{Text: "hi", ConversationID: "conv-1"}, nil)
	require.NoError(t, err)

	headers := server.capturedHeaders()
	require.Equal(t, "Bearer "+opts.AccessToken, headers.Get("Authorization"))
	require.Equal(t, "application/connect+proto", headers.Get("Content-Type"))
	require.Equal(t, "1", headers.Get("Connect-Protocol-Version"))
	require.Equal(t, "session-fixed", headers.Get("X-Session-Id"))
	require.Equal(t, "ide", headers.Get("X-Cursor-Client-Type"))

	// checksum 的后缀是设备指纹，前缀随时间桶变化。
	checksum := headers.Get("X-Cursor-Checksum")
	telemetry := DeriveTelemetryIDs("account-42")
	require.True(t, strings.HasSuffix(checksum, telemetry.MachineID+"/"+telemetry.MacMachineID))
}

func TestRunAgentTurnRepliesToExecWithStub(t *testing.T) {
	server := &agentTestServer{t: t, script: [][]byte{
		execServerMessage(7, "exec-1", 2), // shell
		textDeltaMessage("done"),
		turnEndedMessage(),
	}}
	client, host := startAgentTestServer(t, server)

	result, err := RunAgentTurn(context.Background(), testAgentOptions(t, client, host),
		AgentTurnInput{Text: "run something", ConversationID: "conv-1"}, nil)
	require.NoError(t, err)
	require.Equal(t, 1, result.ExecHandled)
	require.Equal(t, "done", result.Text)

	// 回执必须是 AgentClientMessage.field 2 且带上 exec id，否则上游关联不上。
	frames := server.receivedFrames()
	var execReply []byte
	for _, frame := range frames[1:] {
		fields, err := ReadFields(frame)
		require.NoError(t, err)
		if len(fields) > 0 && fields[0].Number == 2 {
			execReply = fields[0].Bytes
			break
		}
	}
	require.NotNil(t, execReply, "no exec reply was sent")

	replyFields, err := ReadFields(execReply)
	require.NoError(t, err)
	require.Equal(t, uint64(7), replyFields[0].Varint)
	require.Equal(t, "exec-1", FieldString(replyFields, 15))
}

func TestRunAgentTurnShellStreamSendsThreeFrames(t *testing.T) {
	// shell_stream 不能用一元的 shell_result 回，上游会认为格式不对。
	server := &agentTestServer{t: t, script: [][]byte{
		execServerMessage(3, "exec-2", 14),
		turnEndedMessage(),
	}}
	client, host := startAgentTestServer(t, server)

	result, err := RunAgentTurn(context.Background(), testAgentOptions(t, client, host),
		AgentTurnInput{Text: "run", ConversationID: "conv-1"}, nil)
	require.NoError(t, err)
	require.Equal(t, 3, result.ExecHandled)
}

func TestRunAgentTurnRepliesToExecWithUnrecognizedArgField(t *testing.T) {
	// execArgFields 只覆盖已知工具。上游加一个新工具时，认不出也必须回执——
	// 否则上游一直等，整轮对话挂死。
	server := &agentTestServer{t: t, script: [][]byte{
		execServerMessage(5, "exec-new", 61), // 61 不在 execArgFields 里
		textDeltaMessage("done"),
		turnEndedMessage(),
	}}
	client, host := startAgentTestServer(t, server)

	result, err := RunAgentTurn(context.Background(), testAgentOptions(t, client, host),
		AgentTurnInput{Text: "hi", ConversationID: "conv-1"}, nil)
	require.NoError(t, err)
	require.Equal(t, 1, result.ExecHandled)
	require.Zero(t, result.ExecUnanswered)
	require.True(t, result.TurnEnded)
	require.Equal(t, "done", result.Text)

	// 结果字段号回落到请求里出现的那个未知字段上。
	message, err := ParseServerMessage(execServerMessage(5, "exec-new", 61))
	require.NoError(t, err)
	require.Equal(t, 61, message.Exec.ArgFieldNum)
	require.Empty(t, message.Exec.Kind)
}

func TestRunAgentTurnWatchdogEndsTurnWhenExecCannotBeAnswered(t *testing.T) {
	// 无参数的 exec 挑不出结果字段号，回执发不出去；上游随后彻底沉默。
	// 看门狗必须把这一轮收掉，而不是让调用方一直阻塞。
	shrinkStallTimeouts(t, 5*time.Second, 150*time.Millisecond)

	server := &agentTestServer{t: t, hangAfterScript: true, script: [][]byte{
		textDeltaMessage("查一下北京的天气"),
		execServerMessageWithoutArgs(9, "exec-stuck"),
	}}
	client, host := startAgentTestServer(t, server)

	done := make(chan struct{})
	var (
		result *AgentTurnResult
		err    error
	)
	go func() {
		defer close(done)
		result, err = RunAgentTurn(context.Background(), testAgentOptions(t, client, host),
			AgentTurnInput{Text: "hi", ConversationID: "conv-1"}, nil)
	}()

	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("readTurn hung: watchdog did not fire")
	}

	require.NoError(t, err)
	require.True(t, result.Stalled)
	require.False(t, result.TurnEnded)
	require.Equal(t, 1, result.ExecUnanswered)
	// 已经产出的文本要留住：调用方靠它给客户端补一个正常的收尾。
	require.Equal(t, "查一下北京的天气", result.Text)
}

func TestRunAgentTurnCountsIgnoredInteractionQuery(t *testing.T) {
	// 收到 query 却没有可验证回执协议时，应立即计入 QueryIgnored 并收尾，
	// 由上层返回明确 unsupported/incomplete，不再等待看门狗。
	shrinkStallTimeouts(t, 5*time.Second, 5*time.Second)

	server := &agentTestServer{t: t, hangAfterScript: true, script: [][]byte{
		textDeltaMessage("需要你确认一下"),
		queryServerMessage(),
	}}
	client, host := startAgentTestServer(t, server)

	done := make(chan struct{})
	var (
		result *AgentTurnResult
		err    error
	)
	go func() {
		defer close(done)
		result, err = RunAgentTurn(context.Background(), testAgentOptions(t, client, host),
			AgentTurnInput{Text: "hi", ConversationID: "conv-1"}, nil)
	}()

	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("readTurn hung: query watchdog did not fire")
	}

	require.NoError(t, err)
	require.True(t, result.Incomplete())
	require.False(t, result.Stalled)
	require.Equal(t, 1, result.QueryIgnored)
	require.Contains(t, result.IncompleteSummary(), "query_ignored=1")
	require.Equal(t, "需要你确认一下", result.Text)
}

func TestAgentTurnResultIncompleteSummary(t *testing.T) {
	require.Empty(t, (&AgentTurnResult{TurnEnded: true}).IncompleteSummary())
	require.Equal(t, "stalled;no_turn_ended;exec_unanswered=2;query_ignored=1",
		(&AgentTurnResult{Stalled: true, ExecUnanswered: 2, QueryIgnored: 1}).IncompleteSummary())
}

func TestRunAgentTurnWatchdogDoesNotCutHealthyStream(t *testing.T) {
	// 每帧之间的间隔小于 stall 超时，看门狗不该误伤正常生成。
	shrinkStallTimeouts(t, 300*time.Millisecond, 300*time.Millisecond)

	server := &agentTestServer{t: t, script: [][]byte{
		textDeltaMessage("a"),
		textDeltaMessage("b"),
		textDeltaMessage("c"),
		turnEndedMessage(),
	}}
	client, host := startAgentTestServer(t, server)

	result, err := RunAgentTurn(context.Background(), testAgentOptions(t, client, host),
		AgentTurnInput{Text: "hi", ConversationID: "conv-1"}, nil)
	require.NoError(t, err)
	require.False(t, result.Stalled)
	require.True(t, result.TurnEnded)
	require.Equal(t, "abc", result.Text)
}

func TestRunAgentTurnSurfacesEndStreamError(t *testing.T) {
	server := &agentTestServer{
		t:                t,
		script:           [][]byte{textDeltaMessage("partial")},
		endStreamPayload: []byte(`{"error":{"code":"resource_exhausted","message":"quota"}}`),
	}
	client, host := startAgentTestServer(t, server)

	_, err := RunAgentTurn(context.Background(), testAgentOptions(t, client, host),
		AgentTurnInput{Text: "hi", ConversationID: "conv-1"}, nil)

	var connectErr *ConnectError
	require.ErrorAs(t, err, &connectErr)
	require.Equal(t, "resource_exhausted", connectErr.Code)
}

func TestRunAgentTurnPropagatesDeltaHandlerError(t *testing.T) {
	server := &agentTestServer{t: t, script: [][]byte{
		textDeltaMessage("a"),
		textDeltaMessage("b"),
		turnEndedMessage(),
	}}
	client, host := startAgentTestServer(t, server)

	seen := 0
	_, err := RunAgentTurn(context.Background(), testAgentOptions(t, client, host),
		AgentTurnInput{Text: "hi", ConversationID: "conv-1"},
		func(AgentDelta) error {
			seen++
			return io.ErrClosedPipe
		})
	require.ErrorIs(t, err, io.ErrClosedPipe)
	// 客户端断开后必须立刻停读，不能把整条流跑完。
	require.Equal(t, 1, seen)
}

func TestRunAgentTurnRejectsNonSessionToken(t *testing.T) {
	client, host := startAgentTestServer(t, &agentTestServer{t: t})
	opts := testAgentOptions(t, client, host)
	opts.AccessToken = webJWT(t)

	// 拿 web token 打 Agent 只会得到 ERROR_NOT_LOGGED_IN，提前失败更好排查。
	_, err := RunAgentTurn(context.Background(), opts, AgentTurnInput{Text: "hi"}, nil)
	require.ErrorIs(t, err, errCursorAgentNeedsSessionToken)
}

func TestRunAgentTurnSurfacesHTTPError(t *testing.T) {
	httpServer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	httpServer.EnableHTTP2 = true
	httpServer.StartTLS()
	defer httpServer.Close()

	opts := testAgentOptions(t, httpServer.Client(), strings.TrimPrefix(httpServer.URL, "https://"))
	_, err := RunAgentTurn(context.Background(), opts, AgentTurnInput{Text: "hi"}, nil)

	var httpErr *HTTPError
	require.ErrorAs(t, err, &httpErr)
	require.Equal(t, http.StatusUnauthorized, httpErr.Status)
	require.True(t, httpErr.Unauthorized())
}

func TestRunAgentTurnRequiresInjectedClient(t *testing.T) {
	_, err := RunAgentTurn(context.Background(), &AgentOptions{}, AgentTurnInput{}, nil)
	require.ErrorContains(t, err, "http client is nil")
}

func TestStubExecRepliesCoverKnownToolKinds(t *testing.T) {
	cases := map[int]int{
		2:  1, // shell → single unary result
		14: 3, // shell_stream → start + stdout + exit
		7:  1, // read
		8:  1, // ls
		5:  1, // grep
		3:  1, // write → honest non-success stub
		4:  1, // delete
	}
	for argField, expected := range cases {
		exec := &ExecRequest{ID: 1, ExecID: "e", ArgFieldNum: argField, Kind: execArgFields[argField]}
		require.Len(t, StubExecReplies(exec), expected, "argField=%d", argField)
	}

	// 认不出来的工具不回执会让上游一直等，所以不能返回空。
	unknown := &ExecRequest{ID: 1, ArgFieldNum: 99}
	require.Len(t, StubExecReplies(unknown), 1)
	require.Nil(t, StubExecReplies(nil))
	require.Nil(t, StubExecReplies(&ExecRequest{ID: 1}))
}

func TestStubExecRepliesWriteIsNotEmptySuccess(t *testing.T) {
	write := &ExecRequest{ID: 1, ExecID: "e", ArgFieldNum: 3, Kind: "write"}
	replies := StubExecReplies(write)
	require.Len(t, replies, 1)
	emptySuccess := EncodeExecClientMessage(1, "e", 3, EncodeBytesField(1, nil))
	require.NotEqual(t, emptySuccess, replies[0])
	require.Contains(t, string(replies[0]), "not forwarded")
}

func TestStubExecRepliesUnknownKindIsNotEmptySuccess(t *testing.T) {
	unknown := &ExecRequest{ID: 1, ExecID: "e", ArgFieldNum: 99, Kind: ""}
	replies := StubExecReplies(unknown)
	require.Len(t, replies, 1)
	emptySuccess := EncodeExecClientMessage(1, "e", 99, EncodeBytesField(1, nil))
	require.NotEqual(t, emptySuccess, replies[0])
	require.Contains(t, string(replies[0]), "not forwarded")
}

func TestParseServerMessageIgnoresUnknownFields(t *testing.T) {
	// 上游随时可能加新事件，一条不认识的消息不该让整轮对话失败。
	message, err := ParseServerMessage(EncodeBytesField(99, []byte("future")))
	require.NoError(t, err)
	require.Equal(t, KindOther, message.Kind)

	message, err = ParseServerMessage(interactionUpdate(77, nil))
	require.NoError(t, err)
	require.Equal(t, KindOther, message.Kind)
}

func TestParseServerMessageDecodesExec(t *testing.T) {
	raw, err := hex.DecodeString(hex.EncodeToString(execServerMessage(12, "exec-9", 8)))
	require.NoError(t, err)

	message, err := ParseServerMessage(raw)
	require.NoError(t, err)
	require.Equal(t, KindExec, message.Kind)
	require.Equal(t, uint64(12), message.Exec.ID)
	require.Equal(t, "exec-9", message.Exec.ExecID)
	require.Equal(t, "ls", message.Exec.Kind)
}
