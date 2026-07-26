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

// agentTestServer 是一个最小的 Agent 桩：按脚本回帧，并记录客户端写入的帧。
type agentTestServer struct {
	t *testing.T
	// script 是收到第一帧请求后依次回出的消息。
	script [][]byte
	// endStreamPayload 非空时作为结束帧的内容。
	endStreamPayload []byte

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
		time.Sleep(5 * time.Millisecond)
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
