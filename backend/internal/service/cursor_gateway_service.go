package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	// cursorAgentTimeout 覆盖整轮 Agent 会话；双向流是长连接，要给足。
	cursorAgentTimeout = 15 * time.Minute
	// cursorAgentHeaderTimeout 只限制首字节前的等待。
	cursorAgentHeaderTimeout = 90 * time.Second
)

// CursorGatewayService 把 OpenAI Chat Completions 桥接到 Cursor Agent。
//
// 与 Kiro 一样，它只负责「单次账号尝试」：选号、并发、计费、failover、
// 用量落库都由 gateway_handler 的公共链路完成。
type CursorGatewayService struct {
	tokenProvider    *CursorTokenProvider
	rateLimitService *RateLimitService
}

func NewCursorGatewayService(tokenProvider *CursorTokenProvider, rateLimitService *RateLimitService) *CursorGatewayService {
	return &CursorGatewayService{tokenProvider: tokenProvider, rateLimitService: rateLimitService}
}

// reportUpstreamError 把上游故障喂给账号健康度体系。
//
// 自有上游桥不经过 gateway_upstream_response.go 的 handleFailoverSideEffects，
// 不自己上报的话，cursor 账号永远不会被标记限流/不健康/自动禁用。
func (s *CursorGatewayService) reportUpstreamError(ctx context.Context, account *Account, status int, body []byte, model string) {
	if s.rateLimitService == nil || account == nil {
		return
	}
	s.rateLimitService.HandleUpstreamError(ctx, account, status, nil, body, model)
}

// ForwardAsChatCompletions 执行一次 Agent 调用并把响应写给客户端。
//
// 鉴权失败时会作废缓存 token 后重试一次：cursor 的 session token 可能在过期前
// 就被上游作废（用户在别处重新登录），只看 exp 是发现不了的。
func (s *CursorGatewayService) ForwardAsChatCompletions(ctx context.Context, c *gin.Context, account *Account, body []byte) (*ForwardResult, error) {
	writerSizeBefore := 0
	if c != nil {
		writerSizeBefore = c.Writer.Size()
	}
	result, err := s.forwardChatCompletionsOnce(ctx, c, account, body)
	if shouldRetryNativeBridgeAuth(ctx, c, writerSizeBefore, err, func(retryCtx context.Context) error {
		return s.tokenProvider.InvalidateToken(retryCtx, account)
	}, "cursor_gateway", accountIDOrZero(account)) {
		return s.forwardChatCompletionsOnce(markNativeBridgeAuthRetried(ctx), c, account, body)
	}
	return result, err
}

func (s *CursorGatewayService) forwardChatCompletionsOnce(ctx context.Context, c *gin.Context, account *Account, body []byte) (*ForwardResult, error) {
	startTime := time.Now()

	var req cursor.OpenAIRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, s.writeError(c, http.StatusBadRequest, "invalid_request_error", "Invalid request body")
	}
	if strings.TrimSpace(req.Model) == "" {
		return nil, s.writeError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
	}
	if len(req.Messages) == 0 {
		return nil, s.writeError(c, http.StatusBadRequest, "invalid_request_error", "messages is required")
	}

	prompt := cursor.MessagesToAgentText(req.Messages)
	if strings.TrimSpace(prompt) == "" {
		return nil, s.writeError(c, http.StatusBadRequest, "invalid_request_error", "messages contain no text content")
	}

	publicModel := req.Model
	selection := cursor.ResolveModel(publicModel)
	upstreamModel := selection.ModelID

	options, err := s.agentOptions(ctx, account)
	if err != nil {
		return nil, err
	}

	conversationID := req.ResolveConversationID()
	if conversationID == "" {
		conversationID = uuid.NewString()
	}

	agentCtx, cancel := context.WithTimeout(ctx, cursorAgentTimeout)
	defer cancel()

	input := cursor.AgentTurnInput{
		Text:           prompt,
		ConversationID: conversationID,
		ModelID:        selection.ModelID,
		ModelParams:    selection.Params,
		MaxMode:        selection.MaxMode,
	}

	if req.Stream {
		return s.forwardStreaming(agentCtx, c, account, options, input, publicModel, upstreamModel, prompt, startTime)
	}
	return s.forwardBuffered(agentCtx, c, account, options, input, publicModel, upstreamModel, prompt, startTime)
}

func (s *CursorGatewayService) agentOptions(ctx context.Context, account *Account) (*cursor.AgentOptions, error) {
	if s.tokenProvider == nil {
		return nil, &UpstreamFailoverError{
			StatusCode:   http.StatusBadGateway,
			ResponseBody: []byte(`{"error":{"type":"api_error","message":"Cursor token provider is not configured"}}`),
		}
	}
	accessToken, err := s.tokenProvider.GetAccessToken(ctx, account)
	if err != nil {
		return nil, &UpstreamFailoverError{
			StatusCode:   http.StatusBadGateway,
			ResponseBody: []byte(`{"error":{"type":"authentication_error","message":"Failed to get upstream access token"}}`),
			Stage:        GatewayFailureStageAccountAuth,
		}
	}

	proxyURL := ""
	if account.ProxyID != nil {
		if account.Proxy == nil {
			return nil, &UpstreamFailoverError{
				StatusCode:   http.StatusBadGateway,
				ResponseBody: []byte(`{"error":{"type":"api_error","message":"Configured proxy is unavailable"}}`),
				Stage:        GatewayFailureStageAccountAuth,
			}
		}
		proxyURL = account.Proxy.URL()
	}

	// ForceAttemptHTTP2 是硬要求：AgentService/Run 是 HTTP/2 双向流，
	// 退回 HTTP/1.1 会让请求体被缓冲，整条流退化成一问一答。
	//
	// 注意这个字段不能省：httpclient 恒定设置 DialContext（为了 dial 超时），
	// 而 net/http 只在没有自定义 dialer 时才自动开 h2——不显式要求就是纯 h1。
	client, err := httpclient.GetClient(httpclient.Options{
		ProxyURL:              proxyURL,
		Timeout:               cursorAgentTimeout,
		ResponseHeaderTimeout: cursorAgentHeaderTimeout,
		ForceAttemptHTTP2:     true,
	})
	if err != nil {
		return nil, &UpstreamFailoverError{
			StatusCode:   http.StatusBadGateway,
			ResponseBody: []byte(`{"error":{"type":"api_error","message":"Invalid proxy configuration"}}`),
			Stage:        GatewayFailureStageAccountAuth,
		}
	}

	return &cursor.AgentOptions{
		HTTPClient:  client,
		AccessToken: accessToken,
		// 设备指纹以 access token 为种子：同一账号在 token 轮换前保持同一台"设备"。
		Telemetry: cursor.DeriveTelemetryIDs(accessToken),
		SessionID: cursorSessionID(account),
	}, nil
}

// cursorSessionID 为账号派生一个稳定的会话标识。
// 每次请求换一个新 uuid 会让上游看到大量一次性 IDE 会话，风控上很显眼。
func cursorSessionID(account *Account) string {
	if account == nil {
		return uuid.NewString()
	}
	ids := cursor.DeriveTelemetryIDs(fmt.Sprintf("session:%d", account.ID))
	// MachineID 是 64 位十六进制，截成 uuid 形状。
	hex := ids.MachineID
	return hex[0:8] + "-" + hex[8:12] + "-" + hex[12:16] + "-" + hex[16:20] + "-" + hex[20:32]
}

func (s *CursorGatewayService) forwardStreaming(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	options *cursor.AgentOptions,
	input cursor.AgentTurnInput,
	publicModel, upstreamModel, prompt string,
	startTime time.Time,
) (*ForwardResult, error) {
	responseID := "chatcmpl-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	created := time.Now().Unix()

	var (
		headersWritten bool
		firstTokenMs   *int
		clientGone     bool
	)
	writeChunk := func(chunk cursor.OpenAIChunk) error {
		if !headersWritten {
			c.Header("Content-Type", "text/event-stream")
			c.Header("Cache-Control", "no-cache")
			c.Header("Connection", "keep-alive")
			c.Header("X-Accel-Buffering", "no")
			c.Status(http.StatusOK)
			headersWritten = true
		}
		payload, err := json.Marshal(chunk)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", payload); err != nil {
			return err
		}
		c.Writer.Flush()
		return nil
	}

	result, err := cursor.RunAgentTurn(ctx, options, input, func(delta cursor.AgentDelta) error {
		// thinking 按设计不外露：OpenAI 的 chunk 结构里没有它的位置。
		if delta.Text == "" {
			return nil
		}
		if firstTokenMs == nil {
			elapsed := int(time.Since(startTime).Milliseconds())
			firstTokenMs = &elapsed
		}
		if err := writeChunk(cursor.NewOpenAIChunk(responseID, publicModel, created, delta.Text)); err != nil {
			clientGone = true
			return err
		}
		return nil
	})
	if err != nil {
		if clientGone {
			return s.buildResult(prompt, "", publicModel, upstreamModel, true, firstTokenMs, startTime, true), nil
		}
		return nil, s.upstreamError(ctx, c, account, publicModel, err, headersWritten)
	}

	if err := writeChunk(cursor.NewOpenAIFinalChunk(responseID, publicModel, created, "stop")); err != nil {
		return s.buildResult(prompt, result.Text, publicModel, upstreamModel, true, firstTokenMs, startTime, true), nil
	}
	if _, err := fmt.Fprint(c.Writer, "data: [DONE]\n\n"); err == nil {
		c.Writer.Flush()
	}
	return s.buildResult(prompt, result.Text, publicModel, upstreamModel, true, firstTokenMs, startTime, false), nil
}

func (s *CursorGatewayService) forwardBuffered(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	options *cursor.AgentOptions,
	input cursor.AgentTurnInput,
	publicModel, upstreamModel, prompt string,
	startTime time.Time,
) (*ForwardResult, error) {
	result, err := cursor.RunAgentTurn(ctx, options, input, nil)
	if err != nil {
		return nil, s.upstreamError(ctx, c, account, publicModel, err, false)
	}

	promptTokens := cursor.EstimateTokens(prompt)
	completionTokens := cursor.EstimateTokens(result.Text)
	c.JSON(http.StatusOK, cursor.OpenAIResponse{
		ID:      "chatcmpl-" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   publicModel,
		Choices: []cursor.OpenAIChoice{{
			Index:        0,
			Message:      cursor.OpenAIChoiceMessage{Role: "assistant", Content: result.Text},
			FinishReason: "stop",
		}},
		Usage: cursor.OpenAIUsage{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      promptTokens + completionTokens,
		},
	})
	return s.buildResult(prompt, result.Text, publicModel, upstreamModel, false, nil, startTime, false), nil
}

// buildResult 组装计费用的结果。
//
// Cursor 上游不返回 token 用量，这里的数字是本地估算的（见 cursor.EstimateTokens）。
// 不估算的话 usage_logs 的成本会是 0，平台配额就成了一个静默失效的开关。
func (s *CursorGatewayService) buildResult(
	prompt, completion string,
	publicModel, upstreamModel string,
	stream bool,
	firstTokenMs *int,
	startTime time.Time,
	clientDisconnect bool,
) *ForwardResult {
	result := &ForwardResult{
		Usage: ClaudeUsage{
			InputTokens:  int(cursor.EstimateTokens(prompt)),
			OutputTokens: int(cursor.EstimateTokens(completion)),
		},
		Model:            publicModel,
		Stream:           stream,
		Duration:         time.Since(startTime),
		FirstTokenMs:     firstTokenMs,
		ClientDisconnect: clientDisconnect,
	}
	if upstreamModel != publicModel {
		result.UpstreamModel = upstreamModel
	}
	return result
}

func (s *CursorGatewayService) upstreamError(
	ctx context.Context, c *gin.Context, account *Account, model string, err error, headersWritten bool,
) error {
	status := http.StatusBadGateway
	message := "Cursor agent request failed"

	var httpErr *cursor.HTTPError
	if errors.As(err, &httpErr) {
		status = httpErr.Status
		if httpErr.Unauthorized() {
			message = "Cursor credentials were rejected by the upstream"
		}
	}
	var connectErr *cursor.ConnectError
	if errors.As(err, &connectErr) {
		message = "Cursor agent stream ended with an error"
		if connectErr.Code == "resource_exhausted" {
			status = http.StatusTooManyRequests
		}
	}

	body := cursorGatewayErrorBody(status, message)
	// 能否 failover 与账号健康度是两件事：即便流已经开出去无法重试，
	// 429/5xx/401 仍然要影响这个账号下一次能不能被选中。
	s.reportUpstreamError(ctx, account, status, body, model)
	if headersWritten {
		// 流已经开出去了，只能在流里补一个错误事件收尾。
		_, _ = fmt.Fprintf(c.Writer, "data: %s\n\ndata: [DONE]\n\n", body)
		c.Writer.Flush()
		return errors.New(message)
	}
	return &UpstreamFailoverError{StatusCode: status, ResponseBody: body}
}

func (s *CursorGatewayService) writeError(c *gin.Context, status int, errType, message string) error {
	MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
	c.JSON(status, gin.H{"error": gin.H{"type": errType, "message": message}})
	return errors.New(message)
}

func cursorGatewayErrorBody(status int, message string) []byte {
	errType := "api_error"
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		errType = "authentication_error"
	case status == http.StatusTooManyRequests:
		errType = "rate_limit_error"
	case status >= 400 && status < 500:
		errType = "invalid_request_error"
	}
	body, err := json.Marshal(map[string]any{
		"error": map[string]any{"type": errType, "message": message},
	})
	if err != nil {
		return []byte(`{"error":{"type":"api_error","message":"Cursor agent request failed"}}`)
	}
	return body
}

// NewTestAgentOptions 为后台「测试连接」构造 Agent 参数。
//
// 与 NewTestClient 同理，复用 agentOptions：包含 ForceAttemptHTTP2
// （Agent 是 HTTP/2 双向流，退回 h1 根本建不起来）、账号代理、
// 以及以 access token 为种子的设备指纹。
func (s *CursorGatewayService) NewTestAgentOptions(ctx context.Context, account *Account) (*cursor.AgentOptions, error) {
	return s.agentOptions(ctx, account)
}
