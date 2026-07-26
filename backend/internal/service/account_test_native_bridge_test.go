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
	// 空值与未知名回退到 Auto，不把客户端随手写的名字打给上游。
	require.Equal(t, cursor.AutoModelID, cursor.UpstreamModelID(""))
	require.Equal(t, cursor.AutoModelID, cursor.UpstreamModelID("cursor/not-a-real-model"))

	require.Equal(t, "claude-sonnet-4.6", kiro.UpstreamModelID("kiro/claude-sonnet-4.6"))
}

// Auto 不能带 effort/fast 这类具名模型参数——与生产路径同一规则。
func TestCursorTestModelParams(t *testing.T) {
	require.Empty(t, cursorTestModelParams(cursor.AutoModelID))
	require.NotEmpty(t, cursorTestModelParams("claude-sonnet-5"))
}
