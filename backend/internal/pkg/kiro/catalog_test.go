package kiro

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// catalogFixture 取自企业账号 ListAvailableModels 的真实响应，
// 保留了各模型倍率、上下文上限与输入类型的真实差异。
func catalogFixture() []AvailableModel {
	limits := func(in int64) *ModelTokenLimits {
		return &ModelTokenLimits{MaxInputTokens: in, MaxOutputTokens: 64000}
	}
	both := []string{"TEXT", "IMAGE"}
	textOnly := []string{"TEXT"}

	return []AvailableModel{
		{ModelID: "auto", RateMultiplier: 1.0, RateUnit: "Credit", SupportedInputTypes: both, TokenLimits: limits(1000000)},
		{ModelID: "claude-opus-5", RateMultiplier: 2.2, SupportedInputTypes: both, TokenLimits: limits(1000000)},
		{ModelID: "claude-sonnet-4.5", RateMultiplier: 1.3, SupportedInputTypes: both, TokenLimits: limits(200000)},
		{ModelID: "claude-haiku-4.5", RateMultiplier: 0.4, SupportedInputTypes: both, TokenLimits: limits(200000)},
		{ModelID: "gpt-5.6-sol", RateMultiplier: 2.4, SupportedInputTypes: both, TokenLimits: limits(272000)},
		{ModelID: "deepseek-3.2", RateMultiplier: 0.25, SupportedInputTypes: both, TokenLimits: limits(164000)},
		{ModelID: "minimax-m2.5", RateMultiplier: 0.25, SupportedInputTypes: textOnly, TokenLimits: limits(196000)},
		{ModelID: "glm-5", RateMultiplier: 0.5, SupportedInputTypes: textOnly, TokenLimits: limits(200000)},
		{ModelID: "qwen3-coder-next", RateMultiplier: 0.05, SupportedInputTypes: both, TokenLimits: limits(256000)},
	}
}

func TestCatalogLookupAndRates(t *testing.T) {
	c := NewCatalog(catalogFixture(), time.Now())
	require.Equal(t, 9, c.Len())

	require.InDelta(t, 1.3, c.RateMultiplier("claude-sonnet-4.5"), 1e-9)
	require.InDelta(t, 0.05, c.RateMultiplier("qwen3-coder-next"), 1e-9)
	require.Equal(t, int64(1000000), c.MaxInputTokens("auto"))
	require.Equal(t, int64(164000), c.MaxInputTokens("deepseek-3.2"))

	// 查不到返回 0 表示「不知道」，调用方必须退回估算而不是按免费算。
	require.Zero(t, c.RateMultiplier("does-not-exist"))
	require.Zero(t, c.MaxInputTokens("does-not-exist"))

	_, ok := c.Lookup("auto")
	require.True(t, ok)
	_, ok = c.Lookup("nope")
	require.False(t, ok)
}

func TestCatalogModelIDsSortedByPrice(t *testing.T) {
	c := NewCatalog(catalogFixture(), time.Now())
	ids := c.ModelIDs()

	require.Equal(t, "qwen3-coder-next", ids[0], "最便宜的应排在最前")
	require.Equal(t, "gpt-5.6-sol", ids[len(ids)-1], "最贵的应排在最后")

	// 全序必须单调不减，否则 CheapestSupporting 的「第一个即最便宜」不成立。
	for i := 1; i < len(ids); i++ {
		require.LessOrEqual(t, c.RateMultiplier(ids[i-1]), c.RateMultiplier(ids[i]))
	}
}

// TestCatalogSupportsImages 覆盖实测中只支持 TEXT 的那两个模型。
func TestCatalogSupportsImages(t *testing.T) {
	c := NewCatalog(catalogFixture(), time.Now())

	require.True(t, c.SupportsImages("claude-sonnet-4.5"))
	require.False(t, c.SupportsImages("minimax-m2.5"))
	require.False(t, c.SupportsImages("glm-5"))

	// 目录里没有的模型放行：宁可让上游判，也不要因目录过期误拦。
	require.True(t, c.SupportsImages("unknown-model"))
	require.True(t, (*Catalog)(nil).SupportsImages("anything"))
}

func TestCatalogCheapestSupporting(t *testing.T) {
	c := NewCatalog(catalogFixture(), time.Now())

	// 无约束：直接取最便宜的
	require.Equal(t, "qwen3-coder-next", c.CheapestSupporting(false, 0))

	// 要图片：qwen 支持图片，仍是它
	require.Equal(t, "qwen3-coder-next", c.CheapestSupporting(true, 0))

	// 上下文要 30 万：qwen(25.6万) 装不下，deepseek(16.4万) 更不行，
	// 往上找到第一个够大的
	got := c.CheapestSupporting(false, 300000)
	require.Equal(t, "auto", got)
	require.GreaterOrEqual(t, c.MaxInputTokens(got), int64(300000))

	// 上下文 20 万 + 要图片：排除只支持文本的 glm-5(20万)
	got = c.CheapestSupporting(true, 200000)
	require.True(t, c.SupportsImages(got))
	require.GreaterOrEqual(t, c.MaxInputTokens(got), int64(200000))

	// 没有任何模型能满足时返回空串，而不是硬塞一个必然失败的
	require.Empty(t, c.CheapestSupporting(false, 99_000_000))
}

func TestCatalogNilSafe(t *testing.T) {
	var c *Catalog
	require.Zero(t, c.Len())
	require.Zero(t, c.RateMultiplier("x"))
	require.Zero(t, c.MaxInputTokens("x"))
	require.Nil(t, c.ModelIDs())
	require.Empty(t, c.CheapestSupporting(false, 0))
	require.True(t, c.FetchedAt().IsZero())
}

func TestCatalogSkipsModelsWithoutRate(t *testing.T) {
	// 倍率缺失的条目不能被选中：按 0 计费会把成本算没。
	c := NewCatalog([]AvailableModel{
		{ModelID: "broken", RateMultiplier: 0, TokenLimits: &ModelTokenLimits{MaxInputTokens: 999999}},
		{ModelID: "ok", RateMultiplier: 1.0, TokenLimits: &ModelTokenLimits{MaxInputTokens: 100000}},
	}, time.Now())

	require.Equal(t, "ok", c.CheapestSupporting(false, 0))
}
