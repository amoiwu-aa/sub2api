package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Anthropic Messages 入口。Claude Code 只说这套协议。
//
// 与 ForwardAsChatCompletions 共用同一条内核：请求归一化成 cursor.Conversation，
// 客户端工具注册为 MCP 工具，模型的工具调用翻译成 tool_use 块交回客户端执行。
// 差别只在出站的编码形态。

// Forward 执行一次 Agent 调用并按 Anthropic Messages 协议回写。
func (s *CursorGatewayService) Forward(ctx context.Context, c *gin.Context, account *Account, body []byte) (*ForwardResult, error) {
	writerSizeBefore := 0
	if c != nil {
		writerSizeBefore = c.Writer.Size()
	}
	result, err := s.forwardMessagesOnce(ctx, c, account, body)
	if shouldRetryNativeBridgeAuth(ctx, c, writerSizeBefore, err, func(retryCtx context.Context) error {
		return s.tokenProvider.InvalidateToken(retryCtx, account)
	}, "cursor_gateway_anthropic", accountIDOrZero(account)) {
		return s.forwardMessagesOnce(markNativeBridgeAuthRetried(ctx), c, account, body)
	}
	return result, err
}

func (s *CursorGatewayService) forwardMessagesOnce(
	ctx context.Context, c *gin.Context, account *Account, body []byte,
) (*ForwardResult, error) {
	startTime := time.Now()

	var req cursor.AnthropicRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, s.writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", "Invalid request body")
	}
	if strings.TrimSpace(req.Model) == "" {
		return nil, s.writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
	}
	if len(req.Messages) == 0 {
		return nil, s.writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", "messages is required")
	}

	conversation := req.Conversation()
	if err := conversation.ValidationError(); err != nil {
		return nil, s.writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	if depth, limit, exceeded := s.toolLoopExceeded(conversation); exceeded {
		slog.Warn("cursor.tool_loop_blocked",
			"protocol", "anthropic_messages",
			"continuations", depth,
			"limit", limit,
		)
		return nil, s.writeAnthropicError(c, http.StatusConflict, "invalid_request_error",
			cursorToolLoopMessage(depth, limit))
	}
	nativeBridge, mcpTools, err := s.resolveNativeToolBridgeWithRecovery(
		body,
		conversation,
		"anthropic_messages",
	)
	if err != nil {
		return nil, s.writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	conversation.Tools = mcpTools
	conversation.NativeToolBridge = nativeBridge
	prompt := conversation.Render()
	if strings.TrimSpace(prompt) == "" {
		return nil, s.writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error",
			"messages contain no text content")
	}
	if cursor.PromptTooLarge(prompt) {
		return nil, s.writeAnthropicError(c, http.StatusRequestEntityTooLarge, "invalid_request_error",
			cursorPromptTooLargeMessage(prompt))
	}

	publicModel := req.Model
	var standardEffort *string
	if req.OutputConfig != nil {
		standardEffort = req.OutputConfig.Effort
	}
	standardThinking, err := cursorThinkingFromAnthropic(req.Thinking)
	if err != nil {
		return nil, s.writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	standardFast, err := cursorFastFromAnthropic(req.Speed, c.GetHeader("anthropic-beta"))
	if err != nil {
		return nil, s.writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	selection, err := resolveCursorAccountModelSelectionWithStandardOptions(
		account,
		body,
		publicModel,
		&cursor.ModelOptions{
			Effort:   standardEffort,
			Fast:     standardFast,
			Thinking: standardThinking,
		},
	)
	if err != nil {
		return nil, s.writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	upstreamModel := selection.ModelID

	// 配额 429 也要用 Anthropic 的错误体包装，与该入口其它错误一致。
	if err := s.ensureModelQuota(account, selection.ModelID, func(status int, errType, message string) error {
		return s.writeAnthropicError(c, status, errType, message)
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
		Images:                   conversation.CurrentInputImages(),
		ModelID:                  selection.ModelID,
		ModelParams:              selection.Params,
		MaxMode:                  selection.MaxMode,
		Tools:                    conversation.Tools,
		NativeToolBridge:         conversation.NativeToolBridge,
		DisableParallelToolCalls: conversation.DisableParallelToolCalls,
	}

	var result *ForwardResult
	if req.Stream {
		result, err = s.forwardMessagesStreaming(agentCtx, c, account, options, input, publicModel, upstreamModel, prompt, startTime)
	} else {
		result, err = s.forwardMessagesBuffered(agentCtx, c, account, options, input, publicModel, upstreamModel, prompt, startTime)
	}
	annotateCursorModelSelection(result, selection)
	return result, err
}

func cursorThinkingFromAnthropic(raw json.RawMessage) (*bool, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	if trimmed == "true" || trimmed == "false" {
		enabled := trimmed == "true"
		return &enabled, nil
	}
	var config struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, fmt.Errorf("invalid thinking configuration")
	}
	switch strings.ToLower(strings.TrimSpace(config.Type)) {
	case "adaptive", "enabled":
		enabled := true
		return &enabled, nil
	case "disabled":
		enabled := false
		return &enabled, nil
	default:
		return nil, fmt.Errorf("unsupported thinking type %q", config.Type)
	}
}

func cursorFastFromAnthropic(speed, betaHeader string) (*bool, error) {
	switch normalized := strings.ToLower(strings.TrimSpace(speed)); normalized {
	case "":
		if containsBetaToken(betaHeader, claude.BetaFastMode) {
			fast := true
			return &fast, nil
		}
		return nil, nil
	case "fast":
		fast := true
		return &fast, nil
	case "standard", "default":
		fast := false
		return &fast, nil
	default:
		return nil, fmt.Errorf("unsupported speed %q", speed)
	}
}

// anthropicStreamWriter 维护 Messages SSE 的块索引与开合状态。
//
// Anthropic 的流是有状态的：每个内容块必须成对出现 content_block_start /
// content_block_stop，索引连续。文本与 tool_use 交错时最容易出错，
// 所以把这套记账收在一个地方。
type anthropicStreamWriter struct {
	sink       *cursorStreamSink
	textOpen   bool
	blockIndex int
}

func (w *anthropicStreamWriter) write(event cursor.AnthropicEvent) error {
	payload, err := json.Marshal(event.Data)
	if err != nil {
		return err
	}
	return w.sink.writeFrame(fmt.Sprintf("event: %s\ndata: %s\n\n", event.Event, payload))
}

func (w *anthropicStreamWriter) openText() error {
	if w.textOpen {
		return nil
	}
	if err := w.write(cursor.NewAnthropicTextBlockStart(w.blockIndex)); err != nil {
		return err
	}
	w.textOpen = true
	return nil
}

func (w *anthropicStreamWriter) closeText() error {
	if !w.textOpen {
		return nil
	}
	if err := w.write(cursor.NewAnthropicBlockStop(w.blockIndex)); err != nil {
		return err
	}
	w.textOpen = false
	w.blockIndex++
	return nil
}

func (w *anthropicStreamWriter) writeText(text string) error {
	if err := w.openText(); err != nil {
		return err
	}
	return w.write(cursor.NewAnthropicTextDelta(w.blockIndex, text))
}

func (w *anthropicStreamWriter) writeToolUse(call cursor.ToolCall) error {
	if err := w.closeText(); err != nil {
		return err
	}
	if err := w.write(cursor.NewAnthropicToolUseBlockStart(w.blockIndex, call)); err != nil {
		return err
	}
	if err := w.write(cursor.NewAnthropicToolUseDelta(w.blockIndex, call.Arguments)); err != nil {
		return err
	}
	if err := w.write(cursor.NewAnthropicBlockStop(w.blockIndex)); err != nil {
		return err
	}
	w.blockIndex++
	return nil
}

func (s *CursorGatewayService) forwardMessagesStreaming(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	options *cursor.AgentOptions,
	input cursor.AgentTurnInput,
	publicModel, upstreamModel, prompt string,
	startTime time.Time,
) (*ForwardResult, error) {
	messageID := "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	sink := newCursorStreamSink(c)
	stopKeepalive := sink.startKeepalive(s.cursorStreamKeepaliveInterval(), cursorSSEAnthropicPing)
	defer stopKeepalive()
	writer := &anthropicStreamWriter{sink: sink}
	promptTokens := cursor.EstimateAgentInputTokens(prompt, cursorAgentUsageDetails(input, nil))
	messageStarted := false
	ensureMessageStarted := func() error {
		if messageStarted {
			return nil
		}
		if err := writer.write(cursor.NewAnthropicMessageStart(messageID, publicModel, promptTokens)); err != nil {
			return err
		}
		messageStarted = true
		return nil
	}

	var firstTokenMs *int
	result, err := cursor.RunAgentTurn(ctx, options, input, func(delta cursor.AgentDelta) error {
		if firstTokenMs == nil && (delta.Text != "" || delta.Thinking != "" || delta.ToolCall != nil) {
			elapsed := int(time.Since(startTime).Milliseconds())
			firstTokenMs = &elapsed
		}
		if err := ensureMessageStarted(); err != nil {
			return err
		}
		// thinking 不外发（见 cursor.NewAnthropicContent 的说明），但思考期可能
		// 长达数十秒，中间代理会把静默的连接掐掉——用 ping 顶住。
		if delta.Thinking != "" {
			return writer.write(cursor.NewAnthropicPing())
		}
		if delta.ToolCall != nil {
			return writer.writeToolUse(*delta.ToolCall)
		}
		if delta.Text == "" {
			return nil
		}
		return writer.writeText(delta.Text)
	})
	stopKeepalive()
	headersWritten := sink.headersWrittenLocked()
	if err != nil {
		if sink.clientGoneLocked() {
			return s.buildResult(prompt, "", publicModel, upstreamModel, true, firstTokenMs, startTime, true), nil
		}
		return nil, s.upstreamAnthropicError(ctx, c, account, publicModel, err, headersWritten)
	}
	s.recordAgentIncomplete(c, account, publicModel, upstreamModel, result)
	if result.Incomplete() {
		return nil, s.upstreamAnthropicError(ctx, c, account, publicModel,
			cursorIncompleteTurnError(result), headersWritten)
	}

	if err := ensureMessageStarted(); err != nil {
		return nil, s.upstreamAnthropicError(ctx, c, account, publicModel, err, headersWritten)
	}
	stopReason := "end_turn"
	if result.EndedWithToolCalls() {
		stopReason = cursor.StopReasonToolUse
	}
	// 一个字都没产出时也要给一个空文本块：客户端普遍假设 content 非空。
	if !writer.textOpen && writer.blockIndex == 0 {
		if err := writer.openText(); err != nil {
			return s.buildResult(prompt, result.Text, publicModel, upstreamModel, true, firstTokenMs, startTime, true), nil
		}
	}
	if err := writer.closeText(); err != nil {
		return s.buildResult(prompt, result.Text, publicModel, upstreamModel, true, firstTokenMs, startTime, true), nil
	}
	outputTokens := cursor.EstimateAgentOutputTokens(
		result.Text,
		cursorAgentUsageDetails(input, result),
	)
	if err := writer.write(cursor.NewAnthropicMessageDelta(stopReason, outputTokens)); err != nil {
		return s.buildResult(prompt, result.Text, publicModel, upstreamModel, true, firstTokenMs, startTime, true), nil
	}
	if err := writer.write(cursor.NewAnthropicMessageStop()); err != nil {
		return s.buildResult(prompt, result.Text, publicModel, upstreamModel, true, firstTokenMs, startTime, true), nil
	}
	return s.buildResult(
		prompt, result.Text, publicModel, upstreamModel, true, firstTokenMs, startTime, false,
		cursorAgentUsageDetails(input, result),
	), nil
}

func (s *CursorGatewayService) forwardMessagesBuffered(
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
		return nil, s.upstreamAnthropicError(ctx, c, account, publicModel, err, false)
	}
	s.recordAgentIncomplete(c, account, publicModel, upstreamModel, result)
	if result.Incomplete() {
		return nil, s.upstreamAnthropicError(ctx, c, account, publicModel,
			cursorIncompleteTurnError(result), false)
	}

	stopReason := "end_turn"
	if result.EndedWithToolCalls() {
		stopReason = cursor.StopReasonToolUse
	}
	c.JSON(http.StatusOK, cursor.AnthropicResponse{
		ID:         "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		Type:       "message",
		Role:       "assistant",
		Model:      publicModel,
		Content:    cursor.NewAnthropicContent(result.Text, result.ToolCalls),
		StopReason: stopReason,
		Usage: cursor.AnthropicUsage{
			InputTokens: cursor.EstimateAgentInputTokens(
				prompt,
				cursorAgentUsageDetails(input, nil),
			),
			OutputTokens: cursor.EstimateAgentOutputTokens(
				result.Text,
				cursorAgentUsageDetails(input, result),
			),
		},
	})
	return s.buildResult(
		prompt, result.Text, publicModel, upstreamModel, false, nil, startTime, false,
		cursorAgentUsageDetails(input, result),
	), nil
}

func (s *CursorGatewayService) writeAnthropicError(c *gin.Context, status int, errType, message string) error {
	MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
	c.JSON(status, gin.H{
		"type":  "error",
		"error": gin.H{"type": errType, "message": message},
	})
	return errors.New(message)
}

func (s *CursorGatewayService) upstreamAnthropicError(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	model string,
	err error,
	headersWritten bool,
) error {
	if isCursorTurnIncomplete(err) {
		return s.writeCursorIncompleteAnthropic(c, err, headersWritten)
	}
	failure := s.prepareCursorUpstreamFailure(ctx, c, account, model, err)
	body, marshalErr := json.Marshal(gin.H{
		"type": "error",
		"error": gin.H{
			"type":    cursorGatewayErrorType(failure.status),
			"message": failure.message,
		},
	})
	if marshalErr != nil {
		body = []byte(`{"type":"error","error":{"type":"api_error","message":"Cursor agent request failed"}}`)
	}
	if headersWritten {
		_, _ = fmt.Fprintf(c.Writer, "event: error\ndata: %s\n\n", body)
		c.Writer.Flush()
		return errors.New(failure.message)
	}
	return &UpstreamFailoverError{StatusCode: failure.status, ResponseBody: body}
}

func (s *CursorGatewayService) writeCursorIncompleteAnthropic(c *gin.Context, err error, headersWritten bool) error {
	message := cursorIncompleteClientMessage(err)
	if c == nil || c.Writer == nil {
		return err
	}
	body, marshalErr := json.Marshal(gin.H{
		"type": "error",
		"error": gin.H{
			"type":    "api_error",
			"message": message,
		},
	})
	if marshalErr != nil {
		body = []byte(`{"type":"error","error":{"type":"api_error","message":"Cursor agent turn incomplete"}}`)
	}
	if headersWritten {
		_, _ = fmt.Fprintf(c.Writer, "event: error\ndata: %s\n\n", body)
		c.Writer.Flush()
		MarkResponseCommitted(c)
		return err
	}
	c.JSON(http.StatusBadGateway, gin.H{
		"type":  "error",
		"error": gin.H{"type": "api_error", "message": message},
	})
	MarkResponseCommitted(c)
	return err
}
