package cursor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
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

// 看门狗的两档超时。做成变量只为让测试能压到毫秒级。
var (
	// streamStallTimeout 是「久久没有新帧」的兜底上限。生成过程中文本增量会
	// 不断刷新它，留得宽是为了不误伤长时间的思考阶段。上游发来的心跳不刷新它：
	// 上游一边心跳一边等我们回执，正是要兜住的那种挂死。
	streamStallTimeout = 120 * time.Second
	// execStallTimeout 用在一次 exec 没能回执之后。此时上游在等一个永远不会到来
	// 的回复，没必要再等满 streamStallTimeout。
	execStallTimeout = 15 * time.Second
	// toolCallGrace 是收到一个工具调用后再等多久，好把同一轮里其余的调用收齐。
	// 每来一个调用就重置，等满了才主动关流：上游此刻在等一个 exec 回执，而无状态桥
	// 不打算给——工具由客户端执行，结果走下一次请求重放回来。
	//
	// 上游是逐个串行生成工具调用的，不是一批发过来。实测一个调用从上一帧算起要
	// 半秒左右才到（参数越长越久），所以窗口取 500ms 时，模型声明四个并行调用
	// 只有第一个能收到，其余在生成途中被关流丢掉——客户端那边就表现为工具调用
	// 的参数漏成正文。留到 3 秒是按「参数较长的调用也能生成完」取的，代价是带
	// 工具的那一轮多等最多 3 秒。
	toolCallGrace = envDuration("CURSOR_TOOL_CALL_GRACE_MS", 3*time.Second, time.Millisecond)
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
	// Tools 是客户端声明的工具，注册为 MCP 工具供模型调用。
	Tools []McpTool
	// ProjectName 影响 RequestContext 里构造出来的项目路径。
	ProjectName string
}

// AgentDelta 是一次增量输出。Text / Thinking / ToolCall 同一时刻只有一个有值。
type AgentDelta struct {
	Text     string
	Thinking string
	// ToolCall 非空表示模型要调用一个客户端声明的工具。
	ToolCall *ToolCall
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
	// ExecUnanswered 是没能回执的 exec 数。非 0 说明 execArgFields 漏了上游的新
	// 工具，这一轮多半是被看门狗收尾的。
	ExecUnanswered int
	// QueryIgnored 是收到但未回执的 interaction_query 数。
	// 当前网关不实现 query 协议；上游若在等回答，流会静默到看门狗超时。
	QueryIgnored int
	// KVSeen 是收到的 kv 帧数（仅观测，不参与回执）。
	KVSeen int
	// Stalled 为 true 说明这一轮是看门狗超时中止的，不是上游正常结束。
	Stalled bool
	// ToolCalls 是模型这一轮要客户端执行的工具调用。非空时这一轮以
	// finish_reason=tool_calls 收尾，结果由客户端在下一次请求里带回来。
	ToolCalls []ToolCall
}

// EndedWithToolCalls 报告这一轮是否以工具调用收尾。
func (r *AgentTurnResult) EndedWithToolCalls() bool {
	return r != nil && len(r.ToolCalls) > 0
}

// Incomplete 报告这一轮是否因挂死或关键未处理事件而不完整。
// 仅看门狗/未回执类问题；干净的 turn_ended 收尾不算。
//
// 以工具调用收尾也算完整：那是双方约好的暂停点，不是故障。上游确实还开着流
// 在等一个我们不打算给的 exec 回执，但这一轮对客户端而言已经交付完毕。
func (r *AgentTurnResult) Incomplete() bool {
	if r == nil || r.EndedWithToolCalls() {
		return false
	}
	return r.Stalled || r.ExecUnanswered > 0 || r.QueryIgnored > 0
}

// IncompleteSummary 把挂死原因收成一句可落日志/运维错误的短文。
// 正常收尾返回空串。
func (r *AgentTurnResult) IncompleteSummary() string {
	if r == nil || !r.Incomplete() {
		return ""
	}
	parts := make([]string, 0, 5)
	if r.Stalled {
		parts = append(parts, "stalled")
	}
	if !r.TurnEnded {
		parts = append(parts, "no_turn_ended")
	}
	if r.ExecUnanswered > 0 {
		parts = append(parts, fmt.Sprintf("exec_unanswered=%d", r.ExecUnanswered))
	}
	if r.ExecHandled > 0 {
		parts = append(parts, fmt.Sprintf("exec_handled=%d", r.ExecHandled))
	}
	if r.QueryIgnored > 0 {
		parts = append(parts, fmt.Sprintf("query_ignored=%d", r.QueryIgnored))
	}
	if r.KVSeen > 0 {
		parts = append(parts, fmt.Sprintf("kv=%d", r.KVSeen))
	}
	return strings.Join(parts, ";")
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
		Tools:             input.Tools,
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
	// 控制 token 可能被切在两个增量之间，过滤要跨增量保持状态。
	var textFilter, thinkingFilter specialTokenFilter
	var graceTimer *time.Timer

	// 上游在等一个我们给不出的回执时，这条流会永远不再有新帧，读循环就一直阻塞
	// 在 Next() 上。看门狗到点关掉 body 解除阻塞，把「客户端挂死」降级成
	// 「这一轮输出不完整」——后者调用方已经能正常收尾。
	var stalled atomic.Bool
	// endingOnToolCall 让读循环把「因工具调用而主动关流」与「被看门狗掐断」
	// 区分开：前者是正常收尾，后者要标成不完整。
	var endingOnToolCall atomic.Bool
	var toolCallTimer *time.Timer
	stallTimer := time.AfterFunc(streamStallTimeout, func() {
		stalled.Store(true)
		s.close()
	})
	defer func() {
		stallTimer.Stop()
		if graceTimer != nil {
			graceTimer.Stop()
		}
		if toolCallTimer != nil {
			toolCallTimer.Stop()
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
			// turn_ended 后宽限期到点会关掉 body，读出的错误就是预期的收尾信号；
			// 工具调用后的主动关流同理。看门狗关 body 也走这里，只是要标成不完整。
			if errors.Is(err, io.EOF) || result.TurnEnded || endingOnToolCall.Load() {
				break
			}
			if stalled.Load() {
				result.Stalled = true
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
			stallTimer.Reset(streamStallTimeout)
			if clean := textFilter.Feed(message.TextDelta); clean != "" {
				text.WriteString(clean)
				if err := emit(AgentDelta{Text: clean}); err != nil {
					return nil, err
				}
			}
		case KindThinkingDelta:
			stallTimer.Reset(streamStallTimeout)
			if clean := thinkingFilter.Feed(message.ThinkingDelta); clean != "" {
				thinking.WriteString(clean)
				if err := emit(AgentDelta{Thinking: clean}); err != nil {
					return nil, err
				}
			}
		case KindToolCall:
			// 客户端声明的工具：不在流上回执，交回客户端执行。
			// 收齐这一轮的并行调用后主动关流，把控制权还给调用方。
			if message.ToolCall == nil {
				continue
			}
			stallTimer.Stop()
			call := ToolCall{
				ID:        NewOpenAIToolCallID(message.ToolCall.CallID),
				Name:      message.ToolCall.Name,
				Arguments: string(message.ToolCall.Arguments),
			}
			result.ToolCalls = append(result.ToolCalls, call)
			if err := emit(AgentDelta{ToolCall: &call}); err != nil {
				return nil, err
			}
			if toolCallTimer == nil {
				endingOnToolCall.Store(true)
				toolCallTimer = time.AfterFunc(toolCallGrace, s.close)
			} else {
				toolCallTimer.Reset(toolCallGrace)
			}
		case KindExec:
			// 不回执上游会一直等，整轮对话就挂在那里。
			replies := StubExecReplies(message.Exec)
			answered := len(replies) > 0
			for _, reply := range replies {
				if err := s.send(reply); err != nil {
					answered = false
					break
				}
				result.ExecHandled++
			}
			if answered {
				stallTimer.Reset(streamStallTimeout)
				break
			}
			// 认不出的工具，或者回执没写出去：上游在等一个不会到来的回复，
			// 让看门狗早点收尾，别让客户端干等两分钟。
			result.ExecUnanswered++
			stallTimer.Reset(execStallTimeout)
		case KindQuery:
			// 网关目前不实现 interaction_query 回执。上游若在等用户选择/确认，
			// 会像未回执的 exec 一样静默挂起——记数并缩短看门狗。
			result.QueryIgnored++
			stallTimer.Reset(execStallTimeout)
		case KindKV:
			result.KVSeen++
			stallTimer.Reset(streamStallTimeout)
		case KindCheckpoint:
			stallTimer.Reset(streamStallTimeout)
			result.ConversationState = message.ConversationState
		case KindTurnEnded:
			if result.TurnEnded {
				continue
			}
			result.TurnEnded = true
			// 收尾交给 graceTimer，看门狗不必再盯着。
			stallTimer.Stop()
			// 半关闭写侧告诉上游我们说完了。
			s.writeMu.Lock()
			_ = s.writer.Close()
			s.writeMu.Unlock()
			// 上游未必会主动结束流。宽限期一到就关掉 body，让读循环解除阻塞，
			// 否则这里会一直挂到 ctx 超时。
			graceTimer = time.AfterFunc(turnEndGrace, s.close)
		}
	}

	// 扣住的尾巴到这里还没长成控制 token，说明是普通文本，补发出去。
	// 此刻整轮已经读完，emit 再报错只可能是客户端先走了，没有可挽回的动作。
	if tail := textFilter.Flush(); tail != "" {
		text.WriteString(tail)
		_ = emit(AgentDelta{Text: tail})
	}
	if tail := thinkingFilter.Flush(); tail != "" {
		thinking.WriteString(tail)
		_ = emit(AgentDelta{Thinking: tail})
	}

	result.Text = text.String()
	result.Thinking = thinking.String()
	return result, nil
}
