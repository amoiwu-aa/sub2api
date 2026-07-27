//go:build unit

package cursor

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestResolveModelAutoSendsNoParams 守住一个很容易踩空的区别：
// EncodeRunRequest 见到 nil params 会补上 DefaultModelParams()，所以
// Auto 必须拿到一个**非 nil 的空切片**才能真的不带 effort/fast。
// 返回 nil 的话 Auto 会被偷偷加上具名模型的参数。
func TestResolveModelAutoSendsNoParams(t *testing.T) {
	selection := ResolveModel(PublicModelPrefix + AutoModelID)

	require.Equal(t, AutoModelID, selection.ModelID)
	require.NotNil(t, selection.Params, "nil 会被编码器替换成 DefaultModelParams()")
	require.Empty(t, selection.Params)
	require.Nil(t, selection.MaxMode)
}

// TestResolveModelNamedCarriesDefaultParams 对齐官方抓包：具名模型带
// effort=high + fast=true，这正是 IDE 里的「fast」。
func TestResolveModelNamedCarriesDefaultParams(t *testing.T) {
	selection := ResolveModel("cursor/grok-4.5")

	require.Equal(t, "grok-4.5", selection.ModelID)
	require.Equal(t, DefaultModelParams(), selection.Params)
	// 没要 MAX 就不写 field 2，上游区分「没说」与「显式 false」。
	require.Nil(t, selection.MaxMode)
}

// TestResolveModelMaxSuffix 覆盖 MAX 变体：后缀被拆掉，modelId 回到裸名，
// MaxMode 显式为 true。
func TestResolveModelMaxSuffix(t *testing.T) {
	selection := ResolveModel("cursor/grok-4.5" + MaxModeSuffix)

	require.Equal(t, "grok-4.5", selection.ModelID, "MAX 变体不是上游模型名，必须拆回裸名")
	require.Equal(t, DefaultModelParams(), selection.Params)
	require.NotNil(t, selection.MaxMode)
	require.True(t, *selection.MaxMode)
}

// TestResolveModelMaxSuffixOnUnknownBaseFallsBack 确认 "-max" 不是一个可以
// 凭空造模型的口子：剥掉后缀对不上真实模型时，整体回退到 Auto。
func TestResolveModelMaxSuffixOnUnknownBaseFallsBack(t *testing.T) {
	selection := ResolveModel("cursor/not-a-real-model" + MaxModeSuffix)

	require.Equal(t, AutoModelID, selection.ModelID)
	require.Nil(t, selection.MaxMode, "回退到 Auto 时不该还带着 MAX")
	require.Empty(t, selection.Params)
}

// TestMaxVariantsAreNotUpstreamModelIDs 是这次改动最容易回归的地方：
// MAX 变体进了对外目录，但绝不能被当成可以直接发给上游的 modelId。
func TestMaxVariantsAreNotUpstreamModelIDs(t *testing.T) {
	var sawMaxVariant bool
	for _, id := range DefaultModelIDs() {
		bare := strings.TrimPrefix(id, PublicModelPrefix)
		if !strings.HasSuffix(bare, MaxModeSuffix) {
			continue
		}
		sawMaxVariant = true
		require.NotContains(t, knownUpstreamModelIDs, bare,
			"%s 是对外拼出来的名字，原样发给上游会被当成陌生模型", id)
		// 但它必须能解析出一个真实模型，否则目录里就是个死条目。
		require.Contains(t, knownUpstreamModelIDs, ResolveModel(id).ModelID)
	}
	require.True(t, sawMaxVariant, "目录里应至少有一个 MAX 变体")
}

// TestDefaultModelsAreResolvable 保证对外目录里没有死条目：
// 每一项都得解析到一个真实的上游 modelId，而不是悄悄退成 Auto。
func TestDefaultModelsAreResolvable(t *testing.T) {
	for _, id := range DefaultModelIDs() {
		t.Run(id, func(t *testing.T) {
			resolved := ResolveModel(id).ModelID
			if strings.TrimPrefix(id, PublicModelPrefix) == AutoModelID {
				require.Equal(t, AutoModelID, resolved)
				return
			}
			require.NotEqual(t, AutoModelID, resolved,
				"%s 出现在模型列表里却退成了 Auto，用户会以为选中了却没生效", id)
		})
	}
}
