//go:build unit

package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/stretchr/testify/require"
)

// cursor / kiro 的对外模型名带 "cursor/" "kiro/" 命名空间前缀（composite 分组靠模型名
// 前缀反推平台，不加前缀会把 kiro 的 claude-* 调度到 Claude 账号池）。计价目录只认裸名，
// 前缀不剥掉就一路查不到价 —— 而 GetModelPricing 查不到是 fail-closed 的，落到
// usage_logs 里成本恒为 0，user_platform_quotas 上的 USD 限额就成了一个静默失效的开关。
//
// 这组测试锁住「每一个对外暴露的 cursor/kiro 模型都能算出非零单价」这条不变式，
// 后续往 models.go 里加模型时会自动被覆盖到。
func TestGetModelPricing_NamespacedModelsAlwaysPriced(t *testing.T) {
	svc := newTestBillingService()

	ids := append(cursor.DefaultModelIDs(), kiro.DefaultModelIDs()...)
	require.NotEmpty(t, ids, "model catalogs must not be empty")

	for _, id := range ids {
		t.Run(id, func(t *testing.T) {
			pricing, err := svc.GetModelPricing(id)
			require.NoErrorf(t, err, "%s must resolve to a price", id)
			require.NotNil(t, pricing)
			require.Greaterf(t, pricing.InputPricePerToken, 0.0,
				"%s resolved to a zero input price, quota limits would silently stop working", id)
			require.Greaterf(t, pricing.OutputPricePerToken, 0.0,
				"%s resolved to a zero output price", id)
		})
	}
}

// Auto 选模在任何价目表里都没有条目，必须显式锚定，否则整条请求不计费。
func TestGetModelPricing_AutoAliasesAnchorToSonnet(t *testing.T) {
	svc := newTestBillingService()

	anchor, err := svc.GetModelPricing(autoModelPricingAlias)
	require.NoError(t, err)

	for _, id := range []string{"cursor/default", "kiro/auto", "cursor/", "kiro/"} {
		pricing, err := svc.GetModelPricing(id)
		require.NoErrorf(t, err, "%s must resolve", id)
		require.Equalf(t, anchor.InputPricePerToken, pricing.InputPricePerToken, "%s input price", id)
		require.Equalf(t, anchor.OutputPricePerToken, pricing.OutputPricePerToken, "%s output price", id)
	}
}

// 上游随时会冒出新模型名（Cursor 的 composer-*、Kiro 的 ListAvailableModels）。
// 对其他平台 fail-closed 是对的，但这两个平台记 0 等于不计费，必须兜底。
func TestGetModelPricing_UnknownNamespacedModelFallsBackInsteadOfFailing(t *testing.T) {
	svc := newTestBillingService()

	for _, id := range []string{"cursor/some-unreleased-model", "kiro/whatever-next"} {
		pricing, err := svc.GetModelPricing(id)
		require.NoErrorf(t, err, "%s must not fail closed", id)
		require.Greater(t, pricing.InputPricePerToken, 0.0)
	}

	// 反证：非命名空间的未知模型仍然 fail-closed，兜底没有波及其他平台。
	_, err := svc.GetModelPricing("totally-unknown-vendor-model")
	require.ErrorIs(t, err, ErrModelPricingUnavailable)
}

// MAX 变体（如 cursor/grok-4.6-max）在任何价目表里都没有条目。它必须按基础模型
// 计价，而不是掉进 cursor/kiro 的 Sonnet 兜底锚点——同一个模型开不开 MAX 落在
// 两个差很远的单价上，账单没法解释。
func TestGetModelPricing_MaxVariantPricesLikeItsBaseModel(t *testing.T) {
	svc := newTestBillingService()

	for _, base := range []string{"cursor/grok-4.6", "cursor/grok-4.5"} {
		t.Run(base, func(t *testing.T) {
			got, err := svc.GetModelPricing(base + "-max")
			require.NoError(t, err)
			want, err := svc.GetModelPricing(base)
			require.NoError(t, err)

			require.Equal(t, want.InputPricePerToken, got.InputPricePerToken)
			require.Equal(t, want.OutputPricePerToken, got.OutputPricePerToken)

			// 反证：确实没有走 Sonnet 锚点，否则这条断言会失效。
			anchor, err := svc.GetModelPricing(autoModelPricingAlias)
			require.NoError(t, err)
			require.NotEqual(t, anchor.InputPricePerToken, got.InputPricePerToken,
				"grok 与 sonnet 单价相同的话本用例就失去意义了，换一个基础模型")
		})
	}
}

// 剥前缀后必须命中裸名对应的真实价格，而不是滑到某个更便宜的同族回退。
// 例：kiro/claude-haiku-4.5 若不剥前缀会落进 fallback 的 "haiku" 分支拿到
// claude-3-haiku 的价，比实际便宜数倍。
func TestGetModelPricing_NamespacedMatchesBareModel(t *testing.T) {
	svc := newTestBillingService()

	for _, pair := range []struct{ namespaced, bare string }{
		{"kiro/claude-sonnet-4.6", "claude-sonnet-4.6"},
		{"kiro/claude-haiku-4.5", "claude-haiku-4.5"},
		{"kiro/claude-opus-4.6", "claude-opus-4.6"},
		{"cursor/claude-opus-4-8", "claude-opus-4-8"},
		{"cursor/gpt-5.6-sol", "gpt-5.6-sol"},
	} {
		t.Run(pair.namespaced, func(t *testing.T) {
			got, err := svc.GetModelPricing(pair.namespaced)
			require.NoError(t, err)
			want, err := svc.GetModelPricing(pair.bare)
			require.NoError(t, err)
			require.Equal(t, want.InputPricePerToken, got.InputPricePerToken)
			require.Equal(t, want.OutputPricePerToken, got.OutputPricePerToken)
		})
	}
}
