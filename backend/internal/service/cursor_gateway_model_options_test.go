//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/stretchr/testify/require"
)

func TestResolveCursorModelSelectionMergesStandardAndCustomOptions(t *testing.T) {
	body := []byte(`{
		"model":"cursor/grok-4.6-max",
		"reasoning_effort":"low",
		"cursor_options":{"effort":"xhigh","fast":false,"max_mode":false}
	}`)

	standardEffort := cursor.ModelEffortLow
	selection, err := resolveCursorModelSelection(
		body,
		"cursor/grok-4.6-max",
		&standardEffort,
	)

	require.NoError(t, err)
	require.Equal(t, "grok-4.6", selection.ModelID)
	require.Equal(t, cursor.ModelEffortXHigh, cursorModelParamValue(selection.Params, "effort"))
	require.Equal(t, "false", cursorModelParamValue(selection.Params, "fast"))
	require.NotNil(t, selection.MaxMode)
	require.False(t, *selection.MaxMode)
}

func TestResolveCursorModelSelectionUsesProtocolStandardEffort(t *testing.T) {
	standardEffort := cursor.ModelEffortMedium
	selection, err := resolveCursorModelSelection(
		[]byte(`{"model":"cursor/grok-4.6"}`),
		"cursor/grok-4.6",
		&standardEffort,
	)

	require.NoError(t, err)
	require.Equal(t, cursor.ModelEffortMedium, cursorModelParamValue(selection.Params, "effort"))
	require.Equal(t, "true", cursorModelParamValue(selection.Params, "fast"))
	require.Nil(t, selection.MaxMode)
}

func TestResolveCursorModelSelectionRejectsMalformedOptions(t *testing.T) {
	_, err := resolveCursorModelSelection(
		[]byte(`{"model":"cursor/grok-4.6","cursor_options":{"fast":"yes"}}`),
		"cursor/grok-4.6",
		nil,
	)

	require.ErrorContains(t, err, "invalid cursor_options")
}

func TestResolveCursorModelSelectionRejectsExplicitBlankEffort(t *testing.T) {
	blank := "  "
	_, err := resolveCursorModelSelection(
		[]byte(`{"model":"cursor/grok-4.6","reasoning_effort":"  "}`),
		"cursor/grok-4.6",
		&blank,
	)
	require.ErrorContains(t, err, "effort must not be empty")
}

func TestNormalizeAnthropicCursorEffortMapsOnlyMaxAlias(t *testing.T) {
	require.Nil(t, normalizeAnthropicCursorEffort(nil))

	max := " MAX "
	mapped := normalizeAnthropicCursorEffort(&max)
	require.NotNil(t, mapped)
	require.Equal(t, cursor.ModelEffortXHigh, *mapped)

	high := cursor.ModelEffortHigh
	unchanged := normalizeAnthropicCursorEffort(&high)
	require.Same(t, &high, unchanged)
}

func TestAnnotateCursorModelSelectionPersistsEffortAndSpeedTier(t *testing.T) {
	result := &ForwardResult{Model: "cursor/grok-4.6"}
	annotateCursorModelSelection(result, cursor.ModelSelection{
		Params: []cursor.ModelParam{
			{ID: "effort", Value: cursor.ModelEffortXHigh},
			{ID: "fast", Value: "false"},
		},
	})

	require.NotNil(t, result.ReasoningEffort)
	require.Equal(t, cursor.ModelEffortXHigh, *result.ReasoningEffort)
	require.NotNil(t, result.ServiceTier)
	require.Equal(t, ServiceTierStandard, *result.ServiceTier)
	require.Equal(t, "cursor/grok-4.6", result.Model, "MaxMode 未指定时不得改名")
}

func TestAnnotateCursorModelSelectionRecordsFastTier(t *testing.T) {
	result := &ForwardResult{Model: "cursor/grok-4.6"}
	annotateCursorModelSelection(result, cursor.ModelSelection{
		Params: []cursor.ModelParam{{ID: "fast", Value: "true"}},
	})

	require.NotNil(t, result.ServiceTier)
	require.Equal(t, ServiceTierFast, *result.ServiceTier)
}

func TestAnnotateCursorModelSelectionLeavesAutoUntouched(t *testing.T) {
	result := &ForwardResult{Model: "cursor/default"}
	annotateCursorModelSelection(result, cursor.ModelSelection{
		ModelID: cursor.AutoModelID,
		Params:  []cursor.ModelParam{},
	})

	require.Nil(t, result.ReasoningEffort)
	require.Nil(t, result.ServiceTier)
	require.Equal(t, "cursor/default", result.Model)
}

// MAX 用量必须落到 -max 模型名上：MAX 与非 MAX 是两个价，混在一个名字里
// 账单与统计都无法区分（这正是老的 -max 后缀方案的设计初衷）。
func TestAnnotateCursorModelSelectionNormalizesMaxSuffix(t *testing.T) {
	maxOn := true
	maxOff := false

	tests := []struct {
		name    string
		model   string
		maxMode *bool
		want    string
	}{
		{"cursor_options 开 MAX 时补后缀", "cursor/grok-4.6", &maxOn, "cursor/grok-4.6-max"},
		{"模型名已带后缀时不重复", "cursor/grok-4.6-max", &maxOn, "cursor/grok-4.6-max"},
		{"cursor_options 关 MAX 时剥后缀", "cursor/grok-4.6-max", &maxOff, "cursor/grok-4.6"},
		{"显式 false 且无后缀时不变", "cursor/grok-4.6", &maxOff, "cursor/grok-4.6"},
		{"未指定 MaxMode 时不变", "cursor/grok-4.6", nil, "cursor/grok-4.6"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &ForwardResult{Model: tt.model}
			annotateCursorModelSelection(result, cursor.ModelSelection{MaxMode: tt.maxMode})
			require.Equal(t, tt.want, result.Model)
		})
	}
}

// 通用记账路径必须把 ServiceTier 落进用量日志，否则 fast 档只影响本次计费，
// 事后对账/重算无从知道当时的档位。
func TestBuildRecordUsageLogPersistsServiceTier(t *testing.T) {
	tier := ServiceTierFast
	result := &ForwardResult{
		RequestID:   "req-cursor-fast",
		Model:       "cursor/grok-4.6",
		ServiceTier: &tier,
	}

	log := (&GatewayService{}).buildRecordUsageLog(
		context.Background(),
		&recordUsageCoreInput{},
		result,
		&APIKey{ID: 2},
		&User{ID: 1},
		&Account{ID: 3},
		nil,
		result.Model,
		1,
		1,
		1,
		BillingTypeBalance,
		false,
		nil,
		&recordUsageOpts{},
	)

	require.NotNil(t, log.ServiceTier)
	require.Equal(t, ServiceTierFast, *log.ServiceTier)
}

func cursorModelParamValue(params []cursor.ModelParam, id string) string {
	for _, param := range params {
		if param.ID == id {
			return param.Value
		}
	}
	return ""
}
