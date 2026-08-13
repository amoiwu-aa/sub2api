package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// OpenAI Responses 入口。Codex CLI 说的是这套协议。
//
// 不自己合成 Responses 的事件序列——那套状态机（reasoning 项、message 项、
// function_call 项的开合与 output_index 编号）在 apicompat 里已经有一份经过
// 打磨的实现，OpenAI 通道的 chat 降级路径也在用它。这里只做两件事：
// 把 Responses 请求降成 Chat Completions 形态复用同一条内核，
// 再把 Agent 的增量包装成 ChatCompletionsChunk 喂给那台状态机。

// ForwardAsResponses 执行一次 Agent 调用并按 Responses 协议回写。
func (s *CursorGatewayService) ForwardAsResponses(
	ctx context.Context, c *gin.Context, account *Account, body []byte,
) (*ForwardResult, error) {
	writerSizeBefore := 0
	if c != nil {
		writerSizeBefore = c.Writer.Size()
	}
	result, err := s.forwardResponsesOnce(ctx, c, account, body)
	if shouldRetryNativeBridgeAuth(ctx, c, writerSizeBefore, err, func(retryCtx context.Context) error {
		return s.tokenProvider.InvalidateToken(retryCtx, account)
	}, "cursor_gateway_responses", accountIDOrZero(account)) {
		return s.forwardResponsesOnce(markNativeBridgeAuthRetried(ctx), c, account, body)
	}
	return result, err
}

func (s *CursorGatewayService) forwardResponsesOnce(
	ctx context.Context, c *gin.Context, account *Account, body []byte,
) (*ForwardResult, error) {
	startTime := time.Now()

	var responsesReq apicompat.ResponsesRequest
	if err := json.Unmarshal(body, &responsesReq); err != nil {
		return nil, s.writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", "Invalid request body")
	}
	publicModel := strings.TrimSpace(responsesReq.Model)
	if publicModel == "" {
		return nil, s.writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
	}

	// custom / tool_search / namespace 这几类工具在降级时会被摊平，
	// 回程要按这些映射还原，否则 Codex 会把调用判成 unsupported。
	effectiveTools, err := apicompat.EffectiveResponsesTools(&responsesReq)
	if err != nil {
		return nil, s.writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	chatReq, err := apicompat.ResponsesToChatCompletionsRequest(&responsesReq)
	if err != nil {
		return nil, s.writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
	}

	chatBody, err := json.Marshal(chatReq)
	if err != nil {
		return nil, fmt.Errorf("marshal chat completions form of responses request: %w", err)
	}
	var openAIReq cursor.OpenAIRequest
	if err := json.Unmarshal(chatBody, &openAIReq); err != nil {
		return nil, fmt.Errorf("reparse chat completions form: %w", err)
	}
	if len(openAIReq.Messages) == 0 {
		return nil, s.writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", "input is required")
	}

	conversation := openAIReq.Conversation()
	if err := conversation.ValidationError(); err != nil {
		return nil, s.writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	nativeBridge, mcpTools, err := resolveCursorNativeToolBridge(body, conversation.Tools)
	if err != nil {
		return nil, s.writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	conversation.Tools = mcpTools
	conversation.NativeToolBridge = nativeBridge
	prompt := conversation.Render()
	if strings.TrimSpace(prompt) == "" {
		return nil, s.writeResponsesError(c, http.StatusBadRequest, "invalid_request_error",
			"input contains no text content")
	}
	if cursor.PromptTooLarge(prompt) {
		return nil, s.writeResponsesError(c, http.StatusRequestEntityTooLarge, "invalid_request_error",
			cursorPromptTooLargeMessage(prompt))
	}

	var effortEnvelope struct {
		Reasoning *struct {
			Effort *string `json:"effort,omitempty"`
		} `json:"reasoning,omitempty"`
	}
	if err := json.Unmarshal(body, &effortEnvelope); err != nil {
		return nil, s.writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", "Invalid reasoning.effort")
	}
	var standardEffort *string
	if effortEnvelope.Reasoning != nil {
		standardEffort = effortEnvelope.Reasoning.Effort
	}
	selection, err := resolveCursorModelSelection(body, publicModel, standardEffort)
	if err != nil {
		return nil, s.writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	if reason := s.quotaBlockReason(account, selection.ModelID); reason != "" {
		return nil, s.writeResponsesError(c, http.StatusTooManyRequests, "quota_exceeded", reason)
	}

	options, err := s.agentOptions(ctx, account)
	if err != nil {
		return nil, err
	}

	agentCtx, cancel := context.WithTimeout(ctx, cursorAgentTimeout)
	defer cancel()

	input := cursor.AgentTurnInput{
		Text:             prompt,
		ConversationID:   resolveCursorConversationID(c, account, conversation, responsesReq.ID),
		Images:           conversation.Images(),
		ModelID:          selection.ModelID,
		ModelParams:      selection.Params,
		MaxMode:          selection.MaxMode,
		Tools:            conversation.Tools,
		NativeToolBridge: conversation.NativeToolBridge,
	}

	bridge := &cursorResponsesBridge{
		state:          apicompat.NewChatCompletionsToResponsesStreamState(publicModel),
		customTools:    apicompat.CustomToolNames(effectiveTools),
		toolSearch:     apicompat.HasToolSearchTool(effectiveTools),
		namespaceTools: apicompat.NamespaceToolNames(effectiveTools),
	}
	bridge.state.CustomTools = bridge.customTools
	bridge.state.ToolSearchDeclared = bridge.toolSearch
	bridge.state.NamespaceTools = bridge.namespaceTools

	var result *ForwardResult
	if responsesReq.Stream {
		includeUsage := responsesReq.StreamOptions != nil && responsesReq.StreamOptions.IncludeUsage
		result, err = s.forwardResponsesStreaming(agentCtx, c, account, options, input, bridge,
			publicModel, selection.ModelID, prompt, startTime, includeUsage)
	} else {
		result, err = s.forwardResponsesBuffered(agentCtx, c, account, options, input, bridge,
			publicModel, selection.ModelID, prompt, startTime)
	}
	annotateCursorModelSelection(result, selection)
	return result, err
}

// cursorResponsesBridge 把 Agent 增量转成 Responses 事件。
type cursorResponsesBridge struct {
	state          *apicompat.ChatCompletionsToResponsesStreamState
	customTools    map[string]bool
	toolSearch     bool
	namespaceTools map[string]apicompat.NamespacedToolName
	toolCallIndex  int
}

// chunk 把一条 Agent 增量包装成上游看起来像 chat.completion.chunk 的东西。
func (b *cursorResponsesBridge) chunk(model string, delta cursor.AgentDelta) *apicompat.ChatCompletionsChunk {
	chatDelta := apicompat.ChatDelta{}
	switch {
	case delta.ToolCall != nil:
		index := b.toolCallIndex
		b.toolCallIndex++
		call := apicompat.ChatToolCall{Index: &index, ID: delta.ToolCall.ID, Type: "function"}
		call.Function.Name = delta.ToolCall.Name
		call.Function.Arguments = delta.ToolCall.Arguments
		if strings.TrimSpace(call.Function.Arguments) == "" {
			call.Function.Arguments = "{}"
		}
		chatDelta.ToolCalls = []apicompat.ChatToolCall{call}
	case delta.Thinking != "":
		thinking := delta.Thinking
		chatDelta.ReasoningContent = &thinking
	case delta.Text != "":
		text := delta.Text
		chatDelta.Content = &text
	default:
		return nil
	}
	return &apicompat.ChatCompletionsChunk{
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []apicompat.ChatChunkChoice{{Index: 0, Delta: chatDelta}},
	}
}

func (s *CursorGatewayService) forwardResponsesStreaming(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	options *cursor.AgentOptions,
	input cursor.AgentTurnInput,
	bridge *cursorResponsesBridge,
	publicModel, upstreamModel, prompt string,
	startTime time.Time,
	includeUsage bool,
) (*ForwardResult, error) {
	var (
		headersWritten bool
		clientGone     bool
		firstTokenMs   *int
	)
	writeEvents := func(events []apicompat.ResponsesStreamEvent) error {
		if len(events) == 0 || clientGone {
			return nil
		}
		if !headersWritten {
			c.Header("Content-Type", "text/event-stream")
			c.Header("Cache-Control", "no-cache")
			c.Header("Connection", "keep-alive")
			c.Header("X-Accel-Buffering", "no")
			c.Status(http.StatusOK)
			headersWritten = true
		}
		for _, event := range events {
			sse, err := apicompat.ResponsesEventToSSE(event)
			if err != nil {
				// 单条事件序列化失败不该毁掉整条流：跳过继续。
				continue
			}
			if _, err := fmt.Fprint(c.Writer, sse); err != nil {
				clientGone = true
				return err
			}
		}
		c.Writer.Flush()
		return nil
	}

	result, err := cursor.RunAgentTurn(ctx, options, input, func(delta cursor.AgentDelta) error {
		if firstTokenMs == nil && (delta.Text != "" || delta.Thinking != "" || delta.ToolCall != nil) {
			elapsed := int(time.Since(startTime).Milliseconds())
			firstTokenMs = &elapsed
		}
		chunk := bridge.chunk(publicModel, delta)
		if chunk == nil {
			return nil
		}
		return writeEvents(apicompat.ChatCompletionsChunkToResponsesEvents(chunk, bridge.state))
	})
	if err != nil {
		if clientGone {
			return s.buildResult(prompt, "", publicModel, upstreamModel, true, firstTokenMs, startTime, true), nil
		}
		return nil, s.upstreamError(ctx, c, account, publicModel, err, headersWritten)
	}
	s.recordAgentIncomplete(c, account, publicModel, upstreamModel, result)

	// 收尾事件必须发：Codex 在等 response.completed，缺了它会一直挂着。
	if includeUsage {
		bridge.state.Usage = cursorEstimatedResponsesUsage(prompt, result.Text)
	}
	if err := writeEvents(apicompat.FinalizeChatCompletionsResponsesStream(bridge.state)); err != nil {
		return s.buildResult(prompt, result.Text, publicModel, upstreamModel, true, firstTokenMs, startTime, true), nil
	}
	if !clientGone {
		if _, err := fmt.Fprint(c.Writer, "data: [DONE]\n\n"); err == nil {
			c.Writer.Flush()
		}
	}
	return s.buildResult(prompt, result.Text, publicModel, upstreamModel, true, firstTokenMs, startTime, false), nil
}

func (s *CursorGatewayService) forwardResponsesBuffered(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	options *cursor.AgentOptions,
	input cursor.AgentTurnInput,
	bridge *cursorResponsesBridge,
	publicModel, upstreamModel, prompt string,
	startTime time.Time,
) (*ForwardResult, error) {
	result, err := cursor.RunAgentTurn(ctx, options, input, nil)
	if err != nil {
		return nil, s.upstreamError(ctx, c, account, publicModel, err, false)
	}
	s.recordAgentIncomplete(c, account, publicModel, upstreamModel, result)

	finishReason := "stop"
	if result.EndedWithToolCalls() {
		finishReason = cursor.FinishReasonToolCalls
	}
	promptTokens := int(cursor.EstimateTokens(prompt))
	completionTokens := int(cursor.EstimateTokens(result.Text))

	chatResp := &apicompat.ChatCompletionsResponse{
		ID:      "chatcmpl-" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   publicModel,
		Choices: []apicompat.ChatChoice{{
			Index:        0,
			Message:      cursorChatMessage(result),
			FinishReason: finishReason,
		}},
		Usage: &apicompat.ChatUsage{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      promptTokens + completionTokens,
		},
	}
	responsesResp := apicompat.ChatCompletionsResponseToResponses(
		chatResp, publicModel, bridge.customTools, bridge.toolSearch, bridge.namespaceTools)
	c.JSON(http.StatusOK, responsesResp)

	return s.buildResult(prompt, result.Text, publicModel, upstreamModel, false, nil, startTime, false), nil
}

func cursorChatMessage(result *cursor.AgentTurnResult) apicompat.ChatMessage {
	message := apicompat.ChatMessage{Role: "assistant"}
	if result == nil {
		return message
	}
	content := result.Text
	message.Content = json.RawMessage(mustMarshalJSONString(content))
	for i, call := range result.ToolCalls {
		index := i
		toolCall := apicompat.ChatToolCall{Index: &index, ID: call.ID, Type: "function"}
		toolCall.Function.Name = call.Name
		toolCall.Function.Arguments = call.Arguments
		if strings.TrimSpace(toolCall.Function.Arguments) == "" {
			toolCall.Function.Arguments = "{}"
		}
		message.ToolCalls = append(message.ToolCalls, toolCall)
	}
	return message
}

func mustMarshalJSONString(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(encoded)
}

func (s *CursorGatewayService) writeResponsesError(c *gin.Context, status int, errType, message string) error {
	MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
	c.JSON(status, gin.H{"error": gin.H{"type": errType, "message": message}})
	return errors.New(message)
}
