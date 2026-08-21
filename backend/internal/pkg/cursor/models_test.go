//go:build unit

package cursor

import (
	"fmt"
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
	selection := ResolveModel("cursor/grok-4.6")

	require.Equal(t, "grok-4.6", selection.ModelID)
	require.Equal(t, DefaultModelParams(), selection.Params)
	// 没要 MAX 就不写 field 2，上游区分「没说」与「显式 false」。
	require.Nil(t, selection.MaxMode)
}

// TestResolveModelMaxSuffix 覆盖 MAX 变体：后缀被拆掉，modelId 回到裸名，
// MaxMode 显式为 true。
func TestResolveModelMaxSuffix(t *testing.T) {
	selection := ResolveModel("cursor/grok-4.6" + MaxModeSuffix)

	require.Equal(t, "grok-4.6", selection.ModelID, "MAX 变体不是上游模型名，必须拆回裸名")
	require.Equal(t, DefaultModelParams(), selection.Params)
	require.NotNil(t, selection.MaxMode)
	require.True(t, *selection.MaxMode)
}

func TestResolveModelWithOptionsGrok46AllCombinations(t *testing.T) {
	for _, effort := range []string{
		ModelEffortLow,
		ModelEffortMedium,
		ModelEffortHigh,
		ModelEffortXHigh,
	} {
		for _, fast := range []bool{false, true} {
			for _, maxMode := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/fast=%t/max=%t", effort, fast, maxMode), func(t *testing.T) {
					selection, err := ResolveModelWithOptions(
						"cursor/grok-4.6",
						nil,
						&ModelOptions{Effort: &effort, Fast: &fast, MaxMode: &maxMode},
					)

					require.NoError(t, err)
					require.Equal(t, "grok-4.6", selection.ModelID)
					require.Equal(t, effort, modelParamValue(selection.Params, "effort"))
					require.Equal(t, fmt.Sprintf("%t", fast), modelParamValue(selection.Params, "fast"))
					require.NotNil(t, selection.MaxMode)
					require.Equal(t, maxMode, *selection.MaxMode)
				})
			}
		}
	}
}

func TestResolveModelWithOptionsGrok45EffortAllowlist(t *testing.T) {
	for _, effort := range []string{ModelEffortLow, ModelEffortMedium, ModelEffortHigh} {
		effort := effort
		selection, err := ResolveModelWithOptions("cursor/grok-4.5", &effort, nil)
		require.NoError(t, err)
		require.Equal(t, effort, modelParamValue(selection.Params, "effort"))
	}
}

func TestResolveModelWithOptionsPrecedence(t *testing.T) {
	fast := false
	maxMode := false
	standardEffort := ModelEffortLow
	customEffort := ModelEffortXHigh
	selection, err := ResolveModelWithOptions(
		"cursor/grok-4.6-max",
		&standardEffort,
		&ModelOptions{Effort: &customEffort, Fast: &fast, MaxMode: &maxMode},
	)

	require.NoError(t, err)
	require.Equal(t, ModelEffortXHigh, modelParamValue(selection.Params, "effort"),
		"cursor_options.effort must override the protocol-standard effort")
	require.Equal(t, "false", modelParamValue(selection.Params, "fast"))
	require.NotNil(t, selection.MaxMode)
	require.False(t, *selection.MaxMode, "explicit max_mode=false must override the legacy -max suffix")
}

func TestResolveModelWithOptionsRejectsUnsupportedCombinations(t *testing.T) {
	fast := true
	xhigh := ModelEffortXHigh

	selection, err := ResolveModelWithOptions("cursor/grok-4.5", &xhigh, nil)
	require.NoError(t, err)
	require.Equal(t, ModelEffortHigh, modelParamValue(selection.Params, "effort"))

	low := ModelEffortLow
	_, err = ResolveModelWithOptions("cursor/default", &low, nil)
	require.ErrorContains(t, err, "require a named model")

	_, err = ResolveModelWithOptions("cursor/claude-sonnet-5", nil, &ModelOptions{Fast: &fast})
	require.ErrorContains(t, err, "fast mode is not supported")

	selection, err = ResolveModelWithOptions("cursor/claude-sonnet-5", nil, nil)
	require.NoError(t, err, "old model-only requests must remain compatible")
	require.Equal(t, "claude-sonnet-5", selection.ModelID)
}

func TestResolveModelWithOptionsComposerFastToggle(t *testing.T) {
	// composer 默认 fast=true 且 Fast 官方价是标准价的数倍。fast:false 是
	// 标准档价格唯一的到达路径，必须放行；effort 对 composer 仍要拒绝。
	fast := false
	selection, err := ResolveModelWithOptions("cursor/composer-2.5", nil, &ModelOptions{Fast: &fast})
	require.NoError(t, err)
	require.Equal(t, "composer-2.5", selection.ModelID)
	require.Equal(t, "false", modelParamValue(selection.Params, "fast"))

	high := ModelEffortHigh
	_, err = ResolveModelWithOptions("cursor/composer-2.5", nil, &ModelOptions{Effort: &high})
	require.ErrorContains(t, err, `effort "high" is not supported`)
}

func TestResolveModelWithOptionsRejectsBlankAndUndocumentedEfforts(t *testing.T) {
	for _, effort := range []string{"", "   ", "turbo", "thinking"} {
		effort := effort
		t.Run(fmt.Sprintf("%q", effort), func(t *testing.T) {
			_, err := ResolveModelWithOptions(
				"cursor/grok-4.6",
				nil,
				&ModelOptions{Effort: &effort},
			)
			require.Error(t, err)
		})
	}
}

func TestResolveModelWithOptionsNormalizesClientEffortAliases(t *testing.T) {
	tests := []struct {
		model  string
		effort string
		want   string
	}{
		{model: "cursor/gpt-5.6-sol", effort: "minimal", want: ModelEffortNone},
		{model: "cursor/gpt-5.6-sol", effort: "ultra", want: ModelEffortMax},
		{model: "cursor/claude-opus-4-8", effort: "max", want: ModelEffortMax},
		{model: "cursor/grok-4.6", effort: "max", want: ModelEffortXHigh},
		{model: "cursor/grok-4.6", effort: "extra-high", want: ModelEffortXHigh},
		{model: "cursor/grok-4.5", effort: "xhigh", want: ModelEffortHigh},
	}
	for _, tt := range tests {
		t.Run(tt.model+"/"+tt.effort, func(t *testing.T) {
			selection, err := ResolveModelWithOptions(tt.model, &tt.effort, nil)
			require.NoError(t, err)
			require.Equal(t, tt.want, modelParamValue(selection.Params, "effort"))
		})
	}
}

// TestResolveModelMaxSuffixOnUnknownBaseFallsBack 确认 "-max" 不是一个可以
// 凭空造模型的口子：剥掉后缀对不上真实模型时，整体回退到 Auto。
func TestResolveModelMaxSuffixOnUnknownBaseFallsBack(t *testing.T) {
	selection := ResolveModel("cursor/not-a-real-model" + MaxModeSuffix)

	require.Equal(t, AutoModelID, selection.ModelID)
	require.Nil(t, selection.MaxMode, "回退到 Auto 时不该还带着 MAX")
	require.Empty(t, selection.Params)
}

func TestResolveModelStrictRejectsUnknownInsteadOfFallingBack(t *testing.T) {
	_, err := ResolveModelStrict("cursor/not-a-real-model")
	require.ErrorContains(t, err, "unknown cursor model")

	_, err = ResolveModelWithOptionsStrict("cursor/not-a-real-model", nil, nil)
	require.ErrorContains(t, err, "unknown cursor model")

	selection, err := ResolveModelStrict("cursor/grok-4.6-max")
	require.NoError(t, err)
	require.Equal(t, "grok-4.6", selection.ModelID)
	require.NotNil(t, selection.MaxMode)
	require.True(t, *selection.MaxMode)

	_, err = ResolveModelStrict("cursor/gpt-5.6-sol-max")
	require.ErrorContains(t, err, "unknown cursor model",
		"GPT effort=max must not be parsed as Cursor MAX mode")
}

func TestDefaultBridgeCapabilitiesAreVersionedAndDeterministic(t *testing.T) {
	capabilities := DefaultBridgeCapabilities("shadow")
	require.Equal(t, BridgeProtocolVersion, capabilities.Version)
	require.Equal(t, "shadow", capabilities.DefaultMode)
	require.Equal(t, []string{"chat_completions", "anthropic_messages", "responses"}, capabilities.Protocols)
	require.True(t, capabilities.ParallelToolCalls)
	require.True(t, capabilities.ProtocolTerminalErrors)
	require.False(t, capabilities.InteractionQueries)
	require.False(t, capabilities.StatefulContinuation)
	require.Len(t, capabilities.NativeTools, len(NativeToolBridgeKeys()))
	for i, key := range NativeToolBridgeKeys() {
		require.Equal(t, key, capabilities.NativeTools[i].Key)
		require.NotEmpty(t, capabilities.NativeTools[i].Arguments)
	}
}

func TestDefaultModelsIncludeVerifiedCursorCapabilities(t *testing.T) {
	models := DefaultModels()
	require.NotEmpty(t, models)
	byID := make(map[string]Model, len(models))
	for _, model := range models {
		require.NotNil(t, model.CursorCapabilities)
		require.Equal(t, BridgeProtocolVersion, model.CursorCapabilities.BridgeVersion)
		byID[model.ID] = model
	}

	grok := byID["cursor/grok-4.6"].CursorCapabilities
	require.Equal(t,
		[]string{ModelEffortLow, ModelEffortMedium, ModelEffortHigh, ModelEffortXHigh},
		grok.Efforts)
	require.Equal(t, ModelEffortHigh, grok.DefaultEffort)
	require.True(t, grok.Fast)
	require.True(t, grok.DefaultFast)
	require.True(t, grok.MaxMode)

	opus := byID["cursor/claude-opus-4-8"].CursorCapabilities
	require.Equal(t,
		[]string{ModelEffortLow, ModelEffortMedium, ModelEffortHigh, ModelEffortXHigh, ModelEffortMax},
		opus.Efforts)
	require.True(t, opus.Fast)
	require.True(t, opus.Thinking)
	require.False(t, opus.MaxMode)

	sonnet := byID["cursor/claude-sonnet-5"].CursorCapabilities
	require.True(t, sonnet.Thinking)
	require.False(t, sonnet.Fast)

	gpt := byID["cursor/gpt-5.6-sol"].CursorCapabilities
	require.Equal(t, ModelEffortMedium, gpt.DefaultEffort)
	require.Equal(t, ModelEffortNone, gpt.Efforts[0])
	require.True(t, gpt.Fast)
	require.False(t, gpt.DefaultFast)
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

func TestSandDefaultModelsStayInsideVerifiedCatalog(t *testing.T) {
	models := SandDefaultModels()
	require.NotEmpty(t, models)
	require.Equal(t, len(DefaultModels()), len(models))

	for _, model := range models {
		require.True(t, IsSandModelSupported(model.ID), model.ID)
		require.Equal(t, "grok-bot", model.OwnedBy)
		require.Contains(t, model.DisplayName, "Grok Bot")
	}
	require.False(t, IsSandModelSupported("cursor/not-a-sand-model"))
}

func TestResolveModelWithOptionsValidatesOverriddenStandardEffort(t *testing.T) {
	customHigh := ModelEffortHigh
	for _, tc := range []struct {
		model          string
		standardEffort string
	}{
		{model: "cursor/grok-4.6", standardEffort: "none"},
		{model: "cursor/grok-4.6", standardEffort: "turbo"},
		{model: "cursor/claude-sonnet-5", standardEffort: "minimal"},
	} {
		tc := tc
		t.Run(tc.model+"/"+tc.standardEffort, func(t *testing.T) {
			_, err := ResolveModelWithOptions(
				tc.model,
				&tc.standardEffort,
				&ModelOptions{Effort: &customHigh},
			)
			require.ErrorContains(t, err, fmt.Sprintf(`effort %q is not supported`, tc.standardEffort))
		})
	}
}

func TestResolveModelWithStandardOptionsMapsThinkingAndFast(t *testing.T) {
	effort := ModelEffortMax
	fast := true
	thinking := false
	selection, err := ResolveModelWithStandardOptionsStrict(
		"cursor/claude-opus-4-8",
		&ModelOptions{Effort: &effort, Fast: &fast, Thinking: &thinking},
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, ModelEffortMax, modelParamValue(selection.Params, "effort"))
	require.Equal(t, "true", modelParamValue(selection.Params, "fast"))
	require.Equal(t, "false", modelParamValue(selection.Params, "thinking"))

	customFast := false
	selection, err = ResolveModelWithStandardOptionsStrict(
		"cursor/gpt-5.6-terra",
		&ModelOptions{Fast: &fast},
		&ModelOptions{Fast: &customFast},
	)
	require.NoError(t, err)
	require.Equal(t, "false", modelParamValue(selection.Params, "fast"),
		"cursor_options must override protocol-standard Fast")
}

func TestDefaultModelParamsForUsesPerModelDefaults(t *testing.T) {
	require.Equal(t, ModelEffortMedium, modelParamValue(DefaultModelParamsFor("gpt-5.6-sol"), "effort"))
	require.Equal(t, "false", modelParamValue(DefaultModelParamsFor("gpt-5.6-sol"), "fast"))
	require.Equal(t, "true", modelParamValue(DefaultModelParamsFor("claude-sonnet-5"), "thinking"))
	require.Equal(t, "false", modelParamValue(DefaultModelParamsFor("claude-opus-4-8"), "fast"))
	require.Equal(t, "true", modelParamValue(DefaultModelParamsFor("grok-4.6"), "fast"))
	require.Equal(t, "true", modelParamValue(DefaultModelParamsFor("composer-2.5"), "fast"))
}

func modelParamValue(params []ModelParam, id string) string {
	for _, param := range params {
		if param.ID == id {
			return param.Value
		}
	}
	return ""
}
