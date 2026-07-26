package cursor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// AgentService/Run 的 HTTP/2 双向流客户端。
//
// 可行性已由 transport_spike_test.go 验证：请求体接 io.Pipe、不设
// Content-Length，标准 net/http 就能做到真正的全双工，叠加 CONNECT 代理也成立。

const (
	// AgentHost 是 Agent 的默认上游。
	AgentHost = "agent.api5.cursor.sh"
	agentPath = "/agent.v1.AgentService/Run"

	// heartbeatInterval 对齐反代：长时间不发心跳上游会掐掉这条流。
	heartbeatInterval = 10 * time.Second
	// turnEndGrace 是收到 turn_ended 后再等一小会儿，让尾随的 checkpoint 帧到齐。
	turnEndGrace = 400 * time.Millisecond
)

// AgentOptions 是一次 Agent 调用的传输与身份配置。
type AgentOptions struct {
	// HTTPClient 必须支持 HTTP/2 且按需带账号代理，由调用方注入。
	HTTPClient  *http.Client
	AccessToken string
	Telemetry   TelemetryIDs
	// SessionID 应在同一账号的多次请求间保持稳定，模拟一个长期存在的 IDE 会话。
	SessionID string
	Host      string
}

// AgentTurnInput 是一轮对话的输入。
type AgentTurnInput struct {
	Text              string
	ConversationID    string
	ConversationState []byte
	ModelID           string
	ModelParams       []ModelParam
	MaxMode           *bool
	// ProjectName 影响 RequestContext 里构造出来的项目路径。
	ProjectName string
}

// AgentDelta 是一次增量输出。Text 与 Thinking 同一时刻只会有一个非空。
type AgentDelta struct {
	Text     string
	Thinking string
}

// AgentTurnResult 是一轮对话的结果。
type AgentTurnResult struct {
	Text     string
	Thinking string
	// TurnEnded 为 false 说明流是被超时或上游断开中止的，输出可能不完整。
	TurnEnded bool
	// ConversationState 是最后一个 checkpoint，下一轮要原样带上。
	ConversationState []byte
	ExecHandled       int
}

// RunAgentTurn 发起一轮对话，把增量交给 onDelta。
//
// onDelta 返回错误会中止整轮并向上传播——调用方据此实现客户端断开的提前退出。
func RunAgentTurn(
	ctx context.Context,
	opts *AgentOptions,
	input AgentTurnInput,
	onDelta func(AgentDelta) error,
) (*AgentTurnResult, error) {
	if opts == nil || opts.HTTPClient == nil {
		return nil, errors.New("cursor agent http client is nil")
	}
	if strings.TrimSpace(opts.AccessToken) == "" {
		return nil, errors.New("cursor agent access token is missing")
	}
	if !IsSessionToken(opts.AccessToken) {
		// 提前失败比让上游回一个语焉不详的 ERROR_NOT_LOGGED_IN 好排查。
		return nil, errCursorAgentNeedsSessionToken
	}

	conversationID := strings.TrimSpace(input.ConversationID)
	if conversationID == "" {
		conversationID = uuid.NewString()
	}
	requestBody, err := EncodeRunRequest(RunRequestInput{
		Text:              input.Text,
		ConversationID:    conversationID,
		ConversationState: input.ConversationState,
		RequestContext:    EncodeRequestContext(DefaultRequestContextEnv(input.ProjectName)),
		ModelID:           input.ModelID,
		ModelParams:       input.ModelParams,
		MaxMode:           input.MaxMode,
	})
	if err != nil {
		return nil, err
	}

	stream, err := openAgentStream(ctx, opts, conversationID)
	if err != nil {
		return nil, err
	}
	defer stream.close()

	if err := stream.send(requestBody); err != nil {
		return nil, err
	}
	stream.startHeartbeat(ctx)

	return stream.readTurn(onDelta)
}

var errCursorAgentNeedsSessionToken = errors.New("cursor agent requires a type=session token")

// agentStream 持有一条打开的双向流。
type agentStream struct {
	response *http.Response
	writer   *io.PipeWriter
	reader   *EnvelopeReader

	// writeMu 串行化写入：心跳协程与 exec 回执会并发写同一条流。
	writeMu   sync.Mutex
	writeErr  error
	closeOnce sync.Once
	stopBeat  chan struct{}
}

func openAgentStream(ctx context.Context, opts *AgentOptions, conversationID string) (*agentStream, error) {
	host := strings.TrimSpace(opts.Host)
	if host == "" {
		host = AgentHost
	}

	pipeReader, pipeWriter := io.Pipe()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+host+agentPath, pipeReader)
	if err != nil {
		_ = pipeWriter.Close()
		return nil, fmt.Errorf("build cursor agent request: %w", err)
	}
	// 长度未知是双向流的前提；设了 Content-Length 标准库会缓冲请求体。
	req.ContentLength = -1
	applyAgentHeaders(req, opts, conversationID)

	response, err := opts.HTTPClient.Do(req)
	if err != nil {
		_ = pipeWriter.Close()
		return nil, fmt.Errorf("cursor agent request: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, maxResponseBody))
		_ = response.Body.Close()
		_ = pipeWriter.Close()
		return nil, &HTTPError{Status: response.StatusCode, Operation: "AgentService/Run", Body: string(body)}
	}
	// Connect 的双向流只存在于 HTTP/2。降级到 h1 时请求体会被缓冲，心跳和 exec 回执
	// 永远发不出去，表现为一直挂到 header 超时——那种症状极难从日志上溯到成因，
	// 所以这里直接判掉，把「传输层配错了」变成一条明确的错误。
	if !response.ProtoAtLeast(2, 0) {
		_ = response.Body.Close()
		_ = pipeWriter.Close()
		return nil, fmt.Errorf(
			"cursor agent stream requires HTTP/2, upstream negotiated %s "+
				"(check httpclient Options.ForceAttemptHTTP2 and whether the account proxy downgrades h2)",
			response.Proto)
	}

	return &agentStream{
		response: response,
		writer:   pipeWriter,
		reader:   NewEnvelopeReader(response.Body),
		stopBeat: make(chan struct{}),
	}, nil
}

func applyAgentHeaders(req *http.Request, opts *AgentOptions, conversationID string) {
	sessionID := strings.TrimSpace(opts.SessionID)
	if sessionID == "" {
		sessionID = uuid.NewString()
	}
	requestID := uuid.NewString()

	header := req.Header
	header.Set("authorization", "Bearer "+opts.AccessToken)
	header.Set("content-type", "application/connect+proto")
	header.Set("connect-protocol-version", "1")
	header.Set("x-cursor-checksum", Checksum(opts.Telemetry, timeNow()))
	header.Set("x-cursor-client-version", ClientVersion)
	header.Set("x-cursor-client-type", "ide")
	header.Set("x-cursor-client-device-type", "desktop")
	header.Set("x-cursor-client-os", "linux")
	header.Set("x-cursor-client-os-version", "6.8.0")
	header.Set("x-cursor-client-arch", "x64")
	header.Set("x-cursor-client-layout", "editor")
	header.Set("x-cursor-timezone", "UTC")
	header.Set("x-ghost-mode", "false")
	header.Set("x-new-onboarding-completed", "false")
	header.Set("x-session-id", sessionID)
	header.Set("x-request-id", requestID)
	header.Set("x-amzn-trace-id", "Root="+requestID)
	header.Set("user-agent", "Cursor/"+ClientVersion)
	// 会话 id 与对话 id 分开：前者标识"这台 IDE"，后者标识这段对话。
	header.Set("x-cursor-conversation-id", conversationID)
}

// send 写出一帧。并发安全。
func (s *agentStream) send(payload []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.writeErr != nil {
		return s.writeErr
	}
	if _, err := s.writer.Write(EncodeEnvelope(payload, 0)); err != nil {
		s.writeErr = err
		return err
	}
	return nil
}

// startHeartbeat 开始定期心跳，直到流关闭。
func (s *agentStream) startHeartbeat(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.stopBeat:
				return
			case <-ticker.C:
				// 写失败说明流已经断了，交给读侧去报错。
				if err := s.send(EncodeHeartbeat()); err != nil {
					return
				}
			}
		}
	}()
}

func (s *agentStream) close() {
	s.closeOnce.Do(func() {
		close(s.stopBeat)
		_ = s.writer.Close()
		_ = s.response.Body.Close()
	})
}

// readTurn 读到流结束或 turn_ended 为止。
func (s *agentStream) readTurn(onDelta func(AgentDelta) error) (*AgentTurnResult, error) {
	result := &AgentTurnResult{}
	var text, thinking strings.Builder
	var graceTimer *time.Timer
	defer func() {
		if graceTimer != nil {
			graceTimer.Stop()
		}
	}()

	emit := func(delta AgentDelta) error {
		if onDelta == nil {
			return nil
		}
		return onDelta(delta)
	}

	for {
		envelope, err := s.reader.Next()
		if err != nil {
			// turn_ended 后宽限期到点会关掉 body，读出的错误就是预期的收尾信号。
			if errors.Is(err, io.EOF) || result.TurnEnded {
				break
			}
			return nil, err
		}

		if envelope.EndStream() {
			if connectErr := ParseEndStreamError(envelope.Payload); connectErr != nil {
				return nil, connectErr
			}
			break
		}

		message, err := ParseServerMessage(envelope.Payload)
		if err != nil {
			// 单帧解析失败不该毁掉整轮：跳过继续读。
			continue
		}

		switch message.Kind {
		case KindTextDelta:
			if message.TextDelta != "" {
				text.WriteString(message.TextDelta)
				if err := emit(AgentDelta{Text: message.TextDelta}); err != nil {
					return nil, err
				}
			}
		case KindThinkingDelta:
			if message.ThinkingDelta != "" {
				thinking.WriteString(message.ThinkingDelta)
				if err := emit(AgentDelta{Thinking: message.ThinkingDelta}); err != nil {
					return nil, err
				}
			}
		case KindExec:
			// 不回执上游会一直等，整轮对话就挂在那里。
			for _, reply := range StubExecReplies(message.Exec) {
				if err := s.send(reply); err != nil {
					break
				}
				result.ExecHandled++
			}
		case KindCheckpoint:
			result.ConversationState = message.ConversationState
		case KindTurnEnded:
			if result.TurnEnded {
				continue
			}
			result.TurnEnded = true
			// 半关闭写侧告诉上游我们说完了。
			s.writeMu.Lock()
			_ = s.writer.Close()
			s.writeMu.Unlock()
			// 上游未必会主动结束流。宽限期一到就关掉 body，让读循环解除阻塞，
			// 否则这里会一直挂到 ctx 超时。
			graceTimer = time.AfterFunc(turnEndGrace, s.close)
		}
	}

	result.Text = text.String()
	result.Thinking = thinking.String()
	return result, nil
}
