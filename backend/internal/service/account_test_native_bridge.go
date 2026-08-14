package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// cursor / kiro 的「测试连接」。
//
// 这两个平台此前直接早退报 not implemented，后台的测试入口因此被隐藏——
// 运营方没有任何办法回答「这个号还能用吗」。
//
// 两者都刻意复用生产路径（NewTestClient / NewTestAgentOptions），
// 因此测试覆盖的是真实的 token 刷新链、账号代理强制、以及 cursor 的
// HTTP/2 协商。另起一套测试逻辑的话，测过了也说明不了生产能走通。

// setupSSEHeaders 与其他平台的测试路径保持一致的响应头。
func (s *AccountTestService) setupSSEHeaders(c *gin.Context) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
}

// testKiroAccountConnection 用选中的模型真发一轮最小对话。
//
// 不用 ListAvailableModels 探活：那虽然不烧额度，但这个弹窗的既定语义是
// 「选择测试模型」+「发送测试消息」，做成 GET 会和其他平台不一致，
// 也回答不了「这个模型在这个号上到底能不能跑」——运营方真正关心的是后者。
func (s *AccountTestService) testKiroAccountConnection(c *gin.Context, account *Account, modelID string) error {
	ctx := c.Request.Context()

	if s.kiroGatewayService == nil {
		return s.sendErrorAndEnd(c, "Kiro gateway service is not configured")
	}

	publicModel := strings.TrimSpace(modelID)
	if publicModel == "" {
		publicModel = kiro.DefaultModelIDs()[0]
	}
	upstreamModel := kiro.UpstreamModelID(publicModel)

	s.setupSSEHeaders(c)
	s.sendEvent(c, TestEvent{Type: "test_start", Model: publicModel})

	started := time.Now()
	client, err := s.kiroGatewayService.NewTestClient(ctx, account)
	if err != nil {
		return s.sendErrorAndEnd(c, fmt.Sprintf("Failed to prepare Kiro client: %s", err.Error()))
	}

	state, err := kiro.BuildConversationState(&kiro.AnthropicRequest{
		Model:     publicModel,
		MaxTokens: 64,
		Messages: []kiro.AnthropicMessage{
			{Role: "user", Content: json.RawMessage(`"Reply with the single word: ok"`)},
		},
	}, uuid.NewString(), upstreamModel)
	if err != nil {
		return s.sendErrorAndEnd(c, fmt.Sprintf("Failed to build Kiro request: %s", err.Error()))
	}

	var reply strings.Builder
	_, err = client.GenerateAssistantResponse(ctx, &kiro.GenerateAssistantResponseRequest{
		ConversationState: *state,
	}, func(event kiro.StreamEvent) error {
		if event.AssistantResponse == nil || event.AssistantResponse.Content == "" {
			return nil
		}
		reply.WriteString(event.AssistantResponse.Content)
		s.sendEvent(c, TestEvent{Type: "content", Text: event.AssistantResponse.Content})
		return nil
	})
	if err != nil {
		return s.sendErrorAndEnd(c, fmt.Sprintf("Kiro upstream rejected the request: %s", err.Error()))
	}

	elapsed := time.Since(started)
	if strings.TrimSpace(reply.String()) == "" {
		return s.sendErrorAndEnd(c,
			fmt.Sprintf("Kiro connected but returned no content (%dms)", elapsed.Milliseconds()))
	}

	s.sendEvent(c, TestEvent{
		Type:    "test_complete",
		Success: true,
		Status:  "ok",
		Model:   publicModel,
		Data:    map[string]any{"duration_ms": elapsed.Milliseconds()},
	})
	return nil
}

// testCursorAccountConnection 跑一轮最小 Agent 对话。
//
// Cursor 没有可用于探活的只读接口，而「凭证没过期」并不等于「Agent 真能跑」——
// 它的上游是 HTTP/2 双向流 + 手写 protobuf + 逆向出来的 checksum，
// 任何一环错了都只在真实发起一轮时才暴露。所以这里发一个极短的 prompt，
// 用 Auto 模型（具名模型可能受套餐限制），把真实回复透出来。
func (s *AccountTestService) testCursorAccountConnection(
	c *gin.Context,
	account *Account,
	modelID string,
	modelOptions *cursor.ModelOptions,
) error {
	ctx := c.Request.Context()

	if s.cursorGatewayService == nil {
		return s.sendErrorAndEnd(c, "Cursor gateway service is not configured")
	}

	// 尊重下拉框选中的模型。此前这里硬编码 AutoModelID，界面显示
	// 「Cursor Claude Sonnet 5」却实际用 cursor/default 去跑，对不上。
	publicModel := strings.TrimSpace(modelID)
	if publicModel == "" {
		publicModel = cursor.PublicModelPrefix + cursor.AutoModelID
	}

	s.setupSSEHeaders(c)
	selection, err := cursor.ResolveModelWithOptionsStrict(publicModel, nil, modelOptions)
	if err != nil {
		return s.sendErrorAndEnd(c, fmt.Sprintf("Invalid Cursor model options: %s", err.Error()))
	}
	s.sendEvent(c, TestEvent{Type: "test_start", Model: publicModel})

	started := time.Now()
	options, err := s.cursorGatewayService.NewTestAgentOptions(ctx, account)
	if err != nil {
		return s.sendErrorAndEnd(c, fmt.Sprintf("Failed to prepare Cursor agent: %s", err.Error()))
	}

	var reply strings.Builder
	result, err := cursor.RunAgentTurn(ctx, options, cursor.AgentTurnInput{
		Text:           "Reply with the single word: ok",
		ConversationID: uuid.NewString(),
		// 与生产路径同一份解析：探活要验的就是那条真实链路，
		// 包括 MAX 变体能不能被上游接受。
		ModelID:     selection.ModelID,
		ModelParams: selection.Params,
		MaxMode:     selection.MaxMode,
	}, func(delta cursor.AgentDelta) error {
		if delta.Text == "" {
			return nil
		}
		reply.WriteString(delta.Text)
		s.sendEvent(c, TestEvent{Type: "content", Text: delta.Text})
		return nil
	})
	if err != nil {
		return s.sendErrorAndEnd(c, fmt.Sprintf("Cursor agent turn failed: %s", err.Error()))
	}

	elapsed := time.Since(started)
	text := strings.TrimSpace(reply.String())
	if text == "" && result != nil {
		text = strings.TrimSpace(result.Text)
	}
	if text == "" {
		// 连上了但一个字都没回：算失败更诚实——这种账号在生产里同样产不出内容。
		return s.sendErrorAndEnd(c,
			fmt.Sprintf("Cursor agent connected but returned no content (%dms)", elapsed.Milliseconds()))
	}

	s.sendEvent(c, TestEvent{
		Type:    "test_complete",
		Success: true,
		Status:  "ok",
		Model:   publicModel,
		Data:    map[string]any{"duration_ms": elapsed.Milliseconds()},
	})
	return nil
}
