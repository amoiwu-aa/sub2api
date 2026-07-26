package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
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

// testKiroAccountConnection 用 ListAvailableModels 探活。
//
// 选它而不是发一轮真实对话：这是个 GET，能一次性验证 access token、
// profileArn、由 ARN 推出的 region 和账号代理，且不消耗任何推理额度。
// 返回的模型列表顺带告诉运营方这个号实际能用哪些模型。
func (s *AccountTestService) testKiroAccountConnection(c *gin.Context, account *Account) error {
	ctx := c.Request.Context()

	if s.kiroGatewayService == nil {
		return s.sendErrorAndEnd(c, "Kiro gateway service is not configured")
	}

	s.setupSSEHeaders(c)
	s.sendEvent(c, TestEvent{Type: "test_start", Model: "ListAvailableModels"})

	started := time.Now()
	client, err := s.kiroGatewayService.NewTestClient(ctx, account)
	if err != nil {
		return s.sendErrorAndEnd(c, fmt.Sprintf("Failed to prepare Kiro client: %s", err.Error()))
	}

	models, defaultModel, err := client.ListAvailableModels(ctx)
	if err != nil {
		return s.sendErrorAndEnd(c, fmt.Sprintf("Kiro upstream rejected the request: %s", err.Error()))
	}

	elapsed := time.Since(started)
	names := make([]string, 0, len(models))
	for _, m := range models {
		if name := strings.TrimSpace(m.ModelID); name != "" {
			names = append(names, name)
		}
	}

	summary := fmt.Sprintf("凭证有效，上游返回 %d 个可用模型（耗时 %dms）", len(names), elapsed.Milliseconds())
	if defaultModel != nil && strings.TrimSpace(defaultModel.ModelID) != "" {
		summary += fmt.Sprintf("，默认模型 %s", defaultModel.ModelID)
	}
	s.sendEvent(c, TestEvent{Type: "content", Text: summary})
	if len(names) > 0 {
		s.sendEvent(c, TestEvent{Type: "content", Text: strings.Join(names, ", ")})
	}

	s.sendEvent(c, TestEvent{
		Type:    "test_complete",
		Success: true,
		Status:  "ok",
		Data:    map[string]any{"models": names, "duration_ms": elapsed.Milliseconds()},
	})
	return nil
}

// testCursorAccountConnection 跑一轮最小 Agent 对话。
//
// Cursor 没有可用于探活的只读接口，而「凭证没过期」并不等于「Agent 真能跑」——
// 它的上游是 HTTP/2 双向流 + 手写 protobuf + 逆向出来的 checksum，
// 任何一环错了都只在真实发起一轮时才暴露。所以这里发一个极短的 prompt，
// 用 Auto 模型（具名模型可能受套餐限制），把真实回复透出来。
func (s *AccountTestService) testCursorAccountConnection(c *gin.Context, account *Account) error {
	ctx := c.Request.Context()

	if s.cursorGatewayService == nil {
		return s.sendErrorAndEnd(c, "Cursor gateway service is not configured")
	}

	s.setupSSEHeaders(c)
	s.sendEvent(c, TestEvent{Type: "test_start", Model: cursor.PublicModelPrefix + cursor.AutoModelID})

	started := time.Now()
	options, err := s.cursorGatewayService.NewTestAgentOptions(ctx, account)
	if err != nil {
		return s.sendErrorAndEnd(c, fmt.Sprintf("Failed to prepare Cursor agent: %s", err.Error()))
	}

	var reply strings.Builder
	result, err := cursor.RunAgentTurn(ctx, options, cursor.AgentTurnInput{
		Text:           "Reply with the single word: ok",
		ConversationID: uuid.NewString(),
		ModelID:        cursor.AutoModelID,
		// Auto 模式不带 effort/fast 这类具名模型参数。
		ModelParams: []cursor.ModelParam{},
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
		Data:    map[string]any{"duration_ms": elapsed.Milliseconds()},
	})
	return nil
}
