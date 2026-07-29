package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/stretchr/testify/require"
)

// preflightSvc 造一个目录已就绪的网关服务，跳过异步拉取。
func preflightSvc(t *testing.T, accountID int64, models []kiro.AvailableModel) *KiroGatewayService {
	t.Helper()
	s := &KiroGatewayService{}
	s.catalog.entries.Store(accountID, &kiroCatalogEntry{
		catalog: kiro.NewCatalog(models, time.Now()),
	})
	return s
}

func preflightModels() []kiro.AvailableModel {
	return []kiro.AvailableModel{
		{ModelID: "claude-sonnet-4.5", RateMultiplier: 1.3,
			SupportedInputTypes: []string{"TEXT", "IMAGE"},
			TokenLimits:         &kiro.ModelTokenLimits{MaxInputTokens: 200000}},
		{ModelID: "glm-5", RateMultiplier: 0.5,
			SupportedInputTypes: []string{"TEXT"},
			TokenLimits:         &kiro.ModelTokenLimits{MaxInputTokens: 200000}},
		{ModelID: "auto", RateMultiplier: 1.0,
			SupportedInputTypes: []string{"TEXT", "IMAGE"},
			TokenLimits:         &kiro.ModelTokenLimits{MaxInputTokens: 1000000}},
	}
}

func stateWithText(text string) *kiro.ConversationState {
	return &kiro.ConversationState{
		CurrentMessage: kiro.ChatMessage{
			UserInputMessage: &kiro.UserInputMessage{Content: text},
		},
	}
}

func stateWithImage() *kiro.ConversationState {
	return &kiro.ConversationState{
		CurrentMessage: kiro.ChatMessage{
			UserInputMessage: &kiro.UserInputMessage{
				Content: "看这张图",
				Images:  []kiro.ImageBlock{{}},
			},
		},
	}
}

func TestPreflightRejectsImageOnTextOnlyModel(t *testing.T) {
	s := preflightSvc(t, 1, preflightModels())
	acc := kiroCatalogAccount(1)

	msg := s.preflightReject(context.Background(), acc, nil, stateWithImage(), "glm-5")
	require.NotEmpty(t, msg)
	require.Contains(t, msg, "image")
	// 应当给出可用的替代模型，否则用户不知道换哪个
	require.Contains(t, msg, "try")
}

func TestPreflightAllowsImageOnCapableModel(t *testing.T) {
	s := preflightSvc(t, 1, preflightModels())
	acc := kiroCatalogAccount(1)

	require.Empty(t, s.preflightReject(context.Background(), acc, nil, stateWithImage(), "claude-sonnet-4.5"))
}

func TestPreflightRejectsContextOverflow(t *testing.T) {
	s := preflightSvc(t, 1, preflightModels())
	acc := kiroCatalogAccount(1)

	// 估算口径是「字符数 / 4」，要明显超过 20 万 token 的上限（含 15% 余量）
	huge := strings.Repeat("a", 200000*4*2)
	msg := s.preflightReject(context.Background(), acc, nil, stateWithText(huge), "claude-sonnet-4.5")

	require.NotEmpty(t, msg)
	require.Contains(t, msg, "too large")
	// auto 有 100 万上下文，应当被推荐
	require.Contains(t, msg, "auto")
}

// TestPreflightToleratesNearLimit 临界请求不该被误杀：
// 本地估算很粗糙，宁可放过让上游去判。
func TestPreflightToleratesNearLimit(t *testing.T) {
	s := preflightSvc(t, 1, preflightModels())
	acc := kiroCatalogAccount(1)

	// 约 21 万 token，超了上限但在 15% 余量内
	nearLimit := strings.Repeat("a", 210000*4)
	require.Empty(t, s.preflightReject(context.Background(), acc, nil, stateWithText(nearLimit), "claude-sonnet-4.5"))
}

// TestPreflightFailsOpen 目录缺失或模型未知时必须放行。
// 误拦一个本来能用的请求，比多发一个失败请求糟糕得多。
func TestPreflightFailsOpen(t *testing.T) {
	acc := kiroCatalogAccount(99)

	// client 为 nil：不能因此 panic，也不能拦请求。
	// 这不是假想场景——buildClient 失败或调用方没传时就会走到这里，
	// 而 fetch 闭包是在后台 goroutine 里执行的，那里 panic 会直接打挂进程。
	empty := &KiroGatewayService{}
	require.NotPanics(t, func() {
		require.Empty(t, empty.preflightReject(context.Background(), acc, nil, stateWithImage(), "glm-5"))
	})

	// 目录有，但模型不在目录里
	s := preflightSvc(t, 99, preflightModels())
	require.Empty(t, s.preflightReject(context.Background(), acc, nil, stateWithImage(), "unknown-model"))

	// 模型没有 tokenLimits 时不做超限判断
	noLimits := preflightSvc(t, 99, []kiro.AvailableModel{
		{ModelID: "mystery", RateMultiplier: 1.0, SupportedInputTypes: []string{"TEXT", "IMAGE"}},
	})
	huge := strings.Repeat("a", 5000000)
	require.Empty(t, noLimits.preflightReject(context.Background(), acc, nil, stateWithText(huge), "mystery"))
}

func TestPreflightIgnoresNormalRequests(t *testing.T) {
	s := preflightSvc(t, 1, preflightModels())
	acc := kiroCatalogAccount(1)

	require.Empty(t, s.preflightReject(context.Background(), acc, nil, stateWithText("你好"), "claude-sonnet-4.5"))
	require.Empty(t, s.preflightReject(context.Background(), acc, nil, stateWithText("你好"), "glm-5"))
}

func TestConversationStateHasImages(t *testing.T) {
	require.False(t, stateWithText("hi").HasImages())
	require.True(t, stateWithImage().HasImages())
	require.False(t, (*kiro.ConversationState)(nil).HasImages())

	// 历史里的图片也要算上：上下文里带过图，纯文本模型同样处理不了
	withHistory := &kiro.ConversationState{
		History: []kiro.ChatMessage{{
			UserInputMessage: &kiro.UserInputMessage{Images: []kiro.ImageBlock{{}}},
		}},
		CurrentMessage: kiro.ChatMessage{
			UserInputMessage: &kiro.UserInputMessage{Content: "继续"},
		},
	}
	require.True(t, withHistory.HasImages())
}
