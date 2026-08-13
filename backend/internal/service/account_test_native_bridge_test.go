//go:build unit

package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/stretchr/testify/require"
)

// 下拉框传下来的是模型 ID（cursor/claude-sonnet-5），不是展示名。
// 之前测试里硬编码了 AutoModelID，界面显示「Cursor Claude Sonnet 5」
// 却实际跑 cursor/default，两边对不上。
func TestSelectedModelMapsToUpstream(t *testing.T) {
	require.Equal(t, "claude-sonnet-5", cursor.UpstreamModelID("cursor/claude-sonnet-5"))
	require.Equal(t, "gpt-5.6-sol", cursor.UpstreamModelID("cursor/gpt-5.6-sol"))
	require.Equal(t, "grok-4.6", cursor.UpstreamModelID("cursor/grok-4.6"))
	// 空值与未知名回退到 Auto，不把客户端随手写的名字打给上游。
	require.Equal(t, cursor.AutoModelID, cursor.UpstreamModelID(""))
	require.Equal(t, cursor.AutoModelID, cursor.UpstreamModelID("cursor/not-a-real-model"))

	require.Equal(t, "claude-sonnet-4.6", kiro.UpstreamModelID("kiro/claude-sonnet-4.6"))
}

// 探活与生产路径共用 cursor.ResolveModel，这里守住三条规则：
// Auto 不带具名模型参数、具名模型带、MAX 变体的开关能传下去。
func TestCursorTestSelectionMatchesGatewayRules(t *testing.T) {
	require.Empty(t, cursor.ResolveModel(cursor.PublicModelPrefix+cursor.AutoModelID).Params)
	require.NotEmpty(t, cursor.ResolveModel("cursor/claude-sonnet-5").Params)

	maxSelection := cursor.ResolveModel("cursor/grok-4.6" + cursor.MaxModeSuffix)
	require.Equal(t, "grok-4.6", maxSelection.ModelID)
	require.NotNil(t, maxSelection.MaxMode)
	require.True(t, *maxSelection.MaxMode)
}
