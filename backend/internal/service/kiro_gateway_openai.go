package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ForwardAsChatCompletions 把 kiro 上游桥接到 OpenAI Chat Completions。
//
// 与 Anthropic 路径的差异是刻意的（对齐 kiro-proxy）：这条路只透出文本，
// thinking 与 tool_use 都不外露，tools 也不发给上游。
func (s *KiroGatewayService) ForwardAsChatCompletions(ctx context.Context, c *gin.Context, account *Account, body []byte) (*ForwardResult, error) {
	writerSizeBefore := 0
	if c != nil {
		writerSizeBefore = c.Writer.Size()
	}
	result, err := s.forwardChatCompletionsOnce(ctx, c, account, body)
	if shouldRetryNativeBridgeAuth(ctx, c, writerSizeBefore, err, func(retryCtx context.Context) error {
		return s.tokenProvider.InvalidateToken(retryCtx, account)
	}, "kiro_gateway_openai", accountIDOrZero(account)) {
		return s.forwardChatCompletionsOnce(markNativeBridgeAuthRetried(ctx), c, account, body)
	}
	return result, err
}

func (s *KiroGatewayService) forwardChatCompletionsOnce(ctx context.Context, c *gin.Context, account *Account, body []byte) (*ForwardResult, error) {
	startTime := time.Now()

	var openAIReq kiro.OpenAIRequest
	if err := json.Unmarshal(body, &openAIReq); err != nil {
		return nil, s.writeOpenAIError(c, http.StatusBadRequest, "invalid_request_error", "Invalid request body")
	}
	if strings.TrimSpace(openAIReq.Model) == "" {
		return nil, s.writeOpenAIError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
	}
	if len(openAIReq.Messages) == 0 {
		return nil, s.writeOpenAIError(c, http.StatusBadRequest, "invalid_request_error", "messages is required")
	}

	publicModel := openAIReq.Model
	upstreamModel := kiro.UpstreamModelID(publicModel)

	client, err := s.buildClient(ctx, account)
	if err != nil {
		return nil, err
	}

	anthropicReq := openAIReq.ToAnthropicRequest()
	state, err := kiro.BuildConversationState(anthropicReq, uuid.NewString(), upstreamModel)
	if err != nil {
		return nil, s.writeOpenAIError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
	}

	upstreamCtx, cancel := context.WithTimeout(ctx, kiroUpstreamTimeout)
	defer cancel()

	responseID := "chatcmpl-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	created := time.Now().Unix()

	if openAIReq.Stream {
		return s.streamChatCompletions(upstreamCtx, c, client, state, chatCompletionsMeta{
			state:         state,
			account:       account,
			responseID:    responseID,
			created:       created,
			publicModel:   publicModel,
			upstreamModel: upstreamModel,
			startTime:     startTime,
		})
	}
	return s.bufferChatCompletions(upstreamCtx, c, client, state, chatCompletionsMeta{
		state:         state,
		account:       account,
		responseID:    responseID,
		created:       created,
		publicModel:   publicModel,
		upstreamModel: upstreamModel,
		startTime:     startTime,
	})
}

type chatCompletionsMeta struct {
	// state 是发给上游的会话内容，用于 metadataEvent 缺失时本地估算输入 token。
	state *kiro.ConversationState
	// account 用于把上游故障上报给账号健康度体系。
	account       *Account
	responseID    string
	created       int64
	publicModel   string
	upstreamModel string
	startTime     time.Time
}

func (s *KiroGatewayService) streamChatCompletions(
	ctx context.Context,
	c *gin.Context,
	client *kiro.Client,
	state *kiro.ConversationState,
	meta chatCompletionsMeta,
) (*ForwardResult, error) {
	var (
		headersWritten bool
		firstTokenMs   *int
		clientGone     bool
	)
	writeChunk := func(chunk kiro.OpenAIChunk) error {
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

	translator := kiro.NewResponseTranslator("", meta.publicModel, nil)

	_, err := client.GenerateAssistantResponse(ctx, &kiro.GenerateAssistantResponseRequest{
		ConversationState: *state,
	}, func(event kiro.StreamEvent) error {
		if err := translator.Handle(event); err != nil {
			return err
		}
		// 只有文本增量对外可见；thinking 与 tool_use 按设计吞掉。
		if event.AssistantResponse == nil || event.AssistantResponse.Content == "" {
			return nil
		}
		if firstTokenMs == nil {
			elapsed := int(time.Since(meta.startTime).Milliseconds())
			firstTokenMs = &elapsed
		}
		if err := writeChunk(kiro.NewOpenAIChunk(meta.responseID, meta.publicModel, meta.created, event.AssistantResponse.Content)); err != nil {
			clientGone = true
			return err
		}
		return nil
	})
	if err != nil {
		if clientGone {
			_ = translator.Finish()
			return s.buildChatCompletionsResult(translator, meta, true, firstTokenMs, true), nil
		}
		return nil, s.upstreamError(ctx, c, meta.account, meta.publicModel, err, headersWritten)
	}
	if err := translator.Finish(); err != nil {
		return s.buildChatCompletionsResult(translator, meta, true, firstTokenMs, true), nil
	}

	usage := chatCompletionsUsage(translator, meta.state)
	if err := writeChunk(kiro.NewOpenAIFinalChunk(meta.responseID, meta.publicModel, meta.created, "stop", usage)); err != nil {
		return s.buildChatCompletionsResult(translator, meta, true, firstTokenMs, true), nil
	}
	if _, err := fmt.Fprint(c.Writer, "data: [DONE]\n\n"); err == nil {
		c.Writer.Flush()
	}
	return s.buildChatCompletionsResult(translator, meta, true, firstTokenMs, false), nil
}

func (s *KiroGatewayService) bufferChatCompletions(
	ctx context.Context,
	c *gin.Context,
	client *kiro.Client,
	state *kiro.ConversationState,
	meta chatCompletionsMeta,
) (*ForwardResult, error) {
	translator := kiro.NewResponseTranslator("", meta.publicModel, nil)

	_, err := client.GenerateAssistantResponse(ctx, &kiro.GenerateAssistantResponseRequest{
		ConversationState: *state,
	}, translator.Handle)
	if err != nil {
		return nil, s.upstreamError(ctx, c, meta.account, meta.publicModel, err, false)
	}
	if err := translator.Finish(); err != nil {
		return nil, s.upstreamError(ctx, c, meta.account, meta.publicModel, err, false)
	}

	usage := chatCompletionsUsage(translator, meta.state)
	c.JSON(http.StatusOK, kiro.OpenAIResponse{
		ID:      meta.responseID,
		Object:  "chat.completion",
		Created: meta.created,
		Model:   meta.publicModel,
		Choices: []kiro.OpenAIChoice{{
			Index:        0,
			Message:      kiro.OpenAIChoiceMessage{Role: "assistant", Content: translator.TextContent()},
			FinishReason: "stop",
		}},
		Usage: *usage,
	})
	return s.buildChatCompletionsResult(translator, meta, false, nil, false), nil
}

// chatCompletionsUsage 与 buildResult 用同一套口径，保证响应体里报给客户端的
// usage 和落进 usage_logs 的计费数字一致——两处不一致会让对账无从下手。
func chatCompletionsUsage(translator *kiro.ResponseTranslator, state *kiro.ConversationState) *kiro.OpenAIUsage {
	usage := translator.Usage()
	promptTokens := usage.InputTokens
	completionTokens := usage.OutputTokens
	if !translator.HasUpstreamUsage() {
		promptTokens = int64(kiro.EstimateConversationTokens(state))
		completionTokens = int64(translator.EstimatedOutputTokens())
	}
	return &kiro.OpenAIUsage{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      promptTokens + completionTokens,
	}
}

func (s *KiroGatewayService) buildChatCompletionsResult(
	translator *kiro.ResponseTranslator,
	meta chatCompletionsMeta,
	stream bool,
	firstTokenMs *int,
	clientDisconnect bool,
) *ForwardResult {
	return s.buildResult(translator, meta.state, meta.publicModel, meta.upstreamModel, stream, firstTokenMs, meta.startTime, clientDisconnect)
}

func (s *KiroGatewayService) writeOpenAIError(c *gin.Context, status int, errType, message string) error {
	MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
	c.JSON(status, gin.H{"error": gin.H{"type": errType, "message": message}})
	return fmt.Errorf("%s", message)
}
