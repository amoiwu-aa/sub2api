package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
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
	nativeBridgeMode string
	// quotaReader 用于按模型判定 Auto / API 两档额度是否还有余量。
	quotaReader cursorQuotaSnapshotReader
}

func NewCursorGatewayService(
	tokenProvider *CursorTokenProvider,
	rateLimitService *RateLimitService,
	quotaReader cursorQuotaSnapshotReader,
	cfg *config.Config,
) *CursorGatewayService {
	mode := CursorNativeToolBridgeModeShadow
	if cfg != nil {
		mode = normalizeCursorNativeToolBridgeMode(cfg.Gateway.CursorNativeToolBridgeMode)
	}
	return &CursorGatewayService{
		tokenProvider:    tokenProvider,
		rateLimitService: rateLimitService,
		nativeBridgeMode: mode,
		quotaReader:      quotaReader,
	}
}

// NativeToolBridgeMode exposes the effective mode for capability discovery.
func (s *CursorGatewayService) NativeToolBridgeMode() string {
	if s == nil {
		return CursorNativeToolBridgeModeShadow
	}
	return normalizeCursorNativeToolBridgeMode(s.nativeBridgeMode)
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

	conversation := req.Conversation()
	if err := conversation.ValidationError(); err != nil {
		return nil, s.writeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	nativeBridge, mcpTools, err := resolveCursorNativeToolBridge(body, conversation.Tools, s.nativeBridgeMode)
	if err != nil {
		return nil, s.writeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	conversation.Tools = mcpTools
	conversation.NativeToolBridge = nativeBridge
	prompt := conversation.Render()
	if strings.TrimSpace(prompt) == "" {
		return nil, s.writeError(c, http.StatusBadRequest, "invalid_request_error", "messages contain no text content")
	}
	if cursor.PromptTooLarge(prompt) {
		return nil, s.writeError(c, http.StatusRequestEntityTooLarge, "invalid_request_error",
			cursorPromptTooLargeMessage(prompt))
	}

	publicModel := req.Model
	selection, err := resolveCursorModelSelection(body, publicModel, req.ReasoningEffort)
	if err != nil {
		return nil, s.writeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	upstreamModel := selection.ModelID

	if err := s.ensureModelQuota(account, selection.ModelID, func(status int, errType, message string) error {
		return s.writeError(c, status, errType, message)
	}); err != nil {
		return nil, err
	}

	options, err := s.agentOptions(ctx, account)
	if err != nil {
		return nil, err
	}

	conversationID := resolveCursorConversationID(c, account, conversation, req.ID)

	agentCtx, cancel := context.WithTimeout(ctx, cursorAgentTimeout)
	defer cancel()

	input := cursor.AgentTurnInput{
		Text:                     prompt,
		ConversationID:           conversationID,
		Images:                   conversation.Images(),
		ModelID:                  selection.ModelID,
		ModelParams:              selection.Params,
		MaxMode:                  selection.MaxMode,
		Tools:                    conversation.Tools,
		NativeToolBridge:         conversation.NativeToolBridge,
		DisableParallelToolCalls: conversation.DisableParallelToolCalls,
	}

	var result *ForwardResult
	if req.Stream {
		includeUsage := req.StreamOptions != nil && req.StreamOptions.IncludeUsage
		result, err = s.forwardStreaming(
			agentCtx,
			c,
			account,
			options,
			input,
			publicModel,
			upstreamModel,
			prompt,
			startTime,
			includeUsage,
		)
	} else {
		result, err = s.forwardBuffered(agentCtx, c, account, options, input, publicModel, upstreamModel, prompt, startTime)
	}
	annotateCursorModelSelection(result, selection)
	return result, err
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

// cursorConversationID 为一段对话派生一个稳定的会话 id。
//
// 每个请求换一个新 uuid，上游看到的就是一串「各说一句话就消失」的会话，风控上
// 显眼，同一段对话在上游侧也无从关联。种子取首条用户消息并混入账号与 API Key：
// 同一段对话每轮算出同一个 id，不同租户之间不会碰撞。
//
// 稳定 id 不会造成上下文翻倍：agent_live_spike_checkpoint_test.go 的 S1 实测过，
// 沿用同一个 conversation_id 且不带任何状态时模型完全不记得上一轮，这条链路对
// 上游是无状态的，conversation_id 只是关联标识。
func cursorConversationID(c *gin.Context, account *Account, conversation *cursor.Conversation) string {
	accountID := accountIDOrZero(account)
	apiKeyID := getAPIKeyIDFromContext(c)
	if (conversation == nil || len(conversation.Turns) == 0) && accountID == 0 && apiKeyID == 0 {
		return ""
	}

	seedRole := ""
	seedText := ""
	if conversation != nil && len(conversation.Turns) > 0 {
		seed := conversation.Turns[0]
		for _, turn := range conversation.Turns {
			if turn.Role == cursor.RoleUser {
				seed = turn
				break
			}
		}
		seedRole = string(seed.Role)
		seedText = strings.TrimSpace(seed.Text)
	}
	return generateSessionUUID(strings.Join([]string{
		"cursor-conversation",
		strconv.FormatInt(accountID, 10),
		strconv.FormatInt(apiKeyID, 10),
		seedRole,
		seedText,
	}, "\x00"))
}

func resolveCursorConversationID(
	c *gin.Context,
	account *Account,
	conversation *cursor.Conversation,
	explicitBodyID string,
) string {
	if normalized := NormalizeClientSessionID(explicitBodyID); normalized != "" {
		return normalized
	}
	if clientSessionID := ExtractClientSessionID(c); clientSessionID != "" {
		return clientSessionID
	}
	if derived := cursorConversationID(c, account, conversation); derived != "" {
		return derived
	}
	return uuid.NewString()
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
	includeUsage bool,
) (*ForwardResult, error) {
	responseID := "chatcmpl-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	created := time.Now().Unix()

	var (
		headersWritten bool
		roleWritten    bool
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
		if !roleWritten {
			rolePayload, err := json.Marshal(cursor.NewOpenAIRoleChunk(responseID, publicModel, created))
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", rolePayload); err != nil {
				return err
			}
			c.Writer.Flush()
			roleWritten = true
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

	toolCallIndex := 0
	result, err := cursor.RunAgentTurn(ctx, options, input, func(delta cursor.AgentDelta) error {
		if firstTokenMs == nil && (delta.Text != "" || delta.Thinking != "" || delta.ToolCall != nil) {
			elapsed := int(time.Since(startTime).Milliseconds())
			firstTokenMs = &elapsed
		}
		// Cursor Agent 的 thinking_delta 走 reasoning_content；丢掉的话 OpenCode
		// 会一直停在「思考中」，直到（可能永远等不到的）首个正文 token。
		if delta.Thinking != "" {
			if err := writeChunk(cursor.NewOpenAIReasoningChunk(responseID, publicModel, created, delta.Thinking)); err != nil {
				clientGone = true
				return err
			}
		}
		if delta.ToolCall != nil {
			chunk := cursor.NewOpenAIToolCallChunk(responseID, publicModel, created, toolCallIndex, *delta.ToolCall)
			toolCallIndex++
			if err := writeChunk(chunk); err != nil {
				clientGone = true
				return err
			}
		}
		if delta.Text == "" {
			return nil
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
	s.recordAgentIncomplete(c, account, publicModel, upstreamModel, result)
	if result.Incomplete() {
		return nil, s.upstreamError(ctx, c, account, publicModel,
			cursorIncompleteTurnError(result), headersWritten)
	}

	// finish_reason 必须如实反映这一轮是不是停在工具调用上：报成 stop
	// 会让客户端以为任务已经做完，工具永远不会被执行。
	finishReason := "stop"
	if result.EndedWithToolCalls() {
		finishReason = cursor.FinishReasonToolCalls
	}
	if err := writeChunk(cursor.NewOpenAIFinalChunk(responseID, publicModel, created, finishReason)); err != nil {
		return s.buildResult(prompt, result.Text, publicModel, upstreamModel, true, firstTokenMs, startTime, true), nil
	}
	if includeUsage {
		if err := writeChunk(cursorEstimatedChatUsageChunk(
			responseID,
			publicModel,
			created,
			prompt,
			result.Text,
			cursorAgentUsageDetails(input, result),
		)); err != nil {
			return s.buildResult(prompt, result.Text, publicModel, upstreamModel, true, firstTokenMs, startTime, true), nil
		}
	}
	if _, err := fmt.Fprint(c.Writer, "data: [DONE]\n\n"); err == nil {
		c.Writer.Flush()
	}
	return s.buildResult(
		prompt, result.Text, publicModel, upstreamModel, true, firstTokenMs, startTime, false,
		cursorAgentUsageDetails(input, result),
	), nil
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
	s.recordAgentIncomplete(c, account, publicModel, upstreamModel, result)
	if result.Incomplete() {
		return nil, s.upstreamError(ctx, c, account, publicModel,
			cursorIncompleteTurnError(result), false)
	}

	promptTokens := cursor.EstimateTokens(prompt)
	completionTokens := cursor.EstimateTokens(result.Text)
	finishReason := "stop"
	if result.EndedWithToolCalls() {
		finishReason = cursor.FinishReasonToolCalls
	}
	c.JSON(http.StatusOK, cursor.OpenAIResponse{
		ID:      "chatcmpl-" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   publicModel,
		Choices: []cursor.OpenAIChoice{{
			Index: 0,
			Message: cursor.OpenAIChoiceMessage{
				Role:             "assistant",
				Content:          result.Text,
				ReasoningContent: result.Thinking,
				ToolCalls:        cursor.NewOpenAIToolCalls(result.ToolCalls),
			},
			FinishReason: finishReason,
		}},
		Usage: cursor.OpenAIUsage{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      promptTokens + completionTokens,
		},
	})
	return s.buildResult(
		prompt, result.Text, publicModel, upstreamModel, false, nil, startTime, false,
		cursorAgentUsageDetails(input, result),
	), nil
}

// cursorPromptTooLargeMessage 说明超限原因。带上实际字节数和上限，客户端才知道
// 要裁掉多少历史；不说明的话只会看到一个没头没尾的 413。
func cursorPromptTooLargeMessage(prompt string) string {
	return fmt.Sprintf(
		"Conversation history is too large for the Cursor upstream: rendered prompt is %d bytes, limit is %d bytes. "+
			"Beyond this size the upstream accepts the request and then stops responding, "+
			"so the turn would burn the full stall timeout and still return nothing. "+
			"Drop earlier turns and retry.",
		len(prompt), cursor.MaxPromptBytes)
}

// recordAgentIncomplete 把「HTTP 200 但流实际挂死」的情况打进日志和 ops 上游错误，
// 避免用量里只剩 200 + 极少 output、看板上看不见原因。
func (s *CursorGatewayService) recordAgentIncomplete(
	c *gin.Context,
	account *Account,
	publicModel, upstreamModel string,
	result *cursor.AgentTurnResult,
) {
	if result == nil || !result.Incomplete() {
		return
	}
	summary := result.IncompleteSummary()
	accountID := accountIDOrZero(account)
	accountName := ""
	if account != nil {
		accountName = account.Name
	}
	slog.Warn("cursor.agent_turn_incomplete",
		"account_id", accountID,
		"account_name", accountName,
		"model", publicModel,
		"upstream_model", upstreamModel,
		"summary", summary,
		"text_chars", len(result.Text),
		"thinking_chars", len(result.Thinking),
		"stalled", result.Stalled,
		"turn_ended", result.TurnEnded,
		"exec_handled", result.ExecHandled,
		"exec_unanswered", result.ExecUnanswered,
		"query_ignored", result.QueryIgnored,
		"kv_seen", result.KVSeen,
		"mcp_tool_calls", result.MCPToolCalls,
		"native_tool_calls", result.NativeToolCalls,
		"textual_tool_calls", result.TextualToolCalls,
		"tool_call_duplicates", result.DuplicateToolCalls,
		"tool_call_conflicts", result.ConflictingToolCalls,
		"tool_call_collection_ms", result.ToolCallCollectionMs,
		"tool_call_collection_timeout", result.ToolCallCollectionTimedOut,
	)
	if c == nil {
		return
	}
	c.Header("X-RingStar-Cursor-Agent", summary)
	msg := "Cursor agent turn incomplete: " + summary
	SetOpsUpstreamError(c, http.StatusOK, msg, summary)
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform:           PlatformCursor,
		AccountID:          accountID,
		AccountName:        accountName,
		UpstreamStatusCode: http.StatusOK,
		Kind:               "cursor_agent_stall",
		Stage:              string(GatewayFailureStageInference),
		Scope:              string(GatewayFailureScopeAccount),
		Reason:             summary,
		Message:            msg,
		Detail:             summary,
		UpstreamURL:        "https://" + cursor.AgentHost + cursorAgentPathHint(),
	})
}

func cursorAgentPathHint() string {
	return "/agent.v1.AgentService/Run"
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
	details ...cursor.AgentUsageDetails,
) *ForwardResult {
	usageDetails := cursor.AgentUsageDetails{}
	if len(details) > 0 {
		usageDetails = details[0]
	}
	result := &ForwardResult{
		Usage: ClaudeUsage{
			InputTokens:      int(cursor.EstimateAgentInputTokens(prompt, usageDetails)),
			OutputTokens:     int(cursor.EstimateAgentOutputTokens(completion, usageDetails)),
			CacheUsageSource: CacheUsageSourceEstimated,
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
	failure := s.prepareCursorUpstreamFailure(ctx, c, account, model, err)
	body := cursorGatewayErrorBody(failure.status, failure.message)
	if headersWritten {
		// Chat Completions 流已经开出去了，只能在流里补 OpenAI 错误事件收尾。
		_, _ = fmt.Fprintf(c.Writer, "data: %s\n\ndata: [DONE]\n\n", body)
		c.Writer.Flush()
		return errors.New(failure.message)
	}
	return &UpstreamFailoverError{StatusCode: failure.status, ResponseBody: body}
}

type cursorUpstreamFailure struct {
	status     int
	message    string
	connectErr *cursor.ConnectError
}

func (s *CursorGatewayService) prepareCursorUpstreamFailure(
	ctx context.Context, c *gin.Context, account *Account, model string, err error,
) cursorUpstreamFailure {
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
		// 上游把真正的原因埋在 details 里，顶层 message 恒为 "Error"。不透出来的话，
		// 欠费、24 小时内设备过多、额度真的耗尽这三种情况在面板上长得一模一样，
		// 运维只能看到一个 429 然后按「额度耗尽」去误判。
		if description := connectErr.Description(); description != "" {
			message = "Cursor upstream rejected the request: " + description
			if upstream := connectErr.UpstreamCode(); upstream != "" {
				message = "Cursor upstream rejected the request (" + upstream + "): " + description
			}
		}
		if connectErr.Code == "resource_exhausted" {
			status = http.StatusTooManyRequests
		}
	}

	body := cursorGatewayErrorBody(status, message)
	s.recordUpstreamOpsError(c, account, status, message, connectErr)
	// 能否 failover 与账号健康度是两件事：即便流已经开出去无法重试，
	// 429/5xx/401 仍然要影响这个账号下一次能不能被选中。
	s.reportUpstreamError(ctx, account, status, body, model)
	return cursorUpstreamFailure{status: status, message: message, connectErr: connectErr}
}

// recordUpstreamOpsError 把上游拒绝的真实原因写进 ops 上下文。
//
// 必须单独记一份，不能只靠返回给客户端的错误体：failover 在所有账号都失败之后
// 会用自己的 "All available accounts exhausted" 覆盖响应，ops_error_logs 的
// error_message 也跟着变成这句话。不写 ops 的上游字段，面板上就永远看不到
// 「账单未支付」「24 小时内设备过多」这种只有上游知道、而且必须由人处理的原因。
func (s *CursorGatewayService) recordUpstreamOpsError(
	c *gin.Context,
	account *Account,
	status int,
	message string,
	connectErr *cursor.ConnectError,
) {
	if c == nil {
		return
	}
	detail, reason := "", ""
	if connectErr != nil {
		detail = connectErr.Description()
		reason = connectErr.UpstreamCode()
	}
	accountName := ""
	if account != nil {
		accountName = account.Name
	}
	SetOpsUpstreamError(c, status, message, detail)
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform:           PlatformCursor,
		AccountID:          accountIDOrZero(account),
		AccountName:        accountName,
		UpstreamStatusCode: status,
		Kind:               "upstream_rejected",
		Stage:              string(GatewayFailureStageInference),
		Scope:              string(GatewayFailureScopeAccount),
		Reason:             reason,
		Message:            message,
		Detail:             detail,
		UpstreamURL:        "https://" + cursor.AgentHost + cursorAgentPathHint(),
	})
}

func (s *CursorGatewayService) writeError(c *gin.Context, status int, errType, message string) error {
	MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
	c.JSON(status, gin.H{"error": gin.H{"type": errType, "message": message}})
	return errors.New(message)
}

func cursorGatewayErrorBody(status int, message string) []byte {
	errType := cursorGatewayErrorType(status)
	body, err := json.Marshal(map[string]any{
		"error": map[string]any{"type": errType, "message": message},
	})
	if err != nil {
		return []byte(`{"error":{"type":"api_error","message":"Cursor agent request failed"}}`)
	}
	return body
}

func cursorGatewayErrorType(status int) string {
	errType := "api_error"
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		errType = "authentication_error"
	case status == http.StatusTooManyRequests:
		errType = "rate_limit_error"
	case status >= 400 && status < 500:
		errType = "invalid_request_error"
	}
	return errType
}

func cursorIncompleteTurnError(result *cursor.AgentTurnResult) error {
	summary := "unknown"
	if result != nil && result.IncompleteSummary() != "" {
		summary = result.IncompleteSummary()
	}
	return fmt.Errorf("cursor agent turn incomplete: %s", summary)
}

// NewTestAgentOptions 为后台「测试连接」构造 Agent 参数。
//
// 与 NewTestClient 同理，复用 agentOptions：包含 ForceAttemptHTTP2
// （Agent 是 HTTP/2 双向流，退回 h1 根本建不起来）、账号代理、
// 以及以 access token 为种子的设备指纹。
func (s *CursorGatewayService) NewTestAgentOptions(ctx context.Context, account *Account) (*cursor.AgentOptions, error) {
	return s.agentOptions(ctx, account)
}
