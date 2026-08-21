package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
)

type cursorModelOptionsEnvelope struct {
	CursorOptions *cursor.ModelOptions `json:"cursor_options,omitempty"`
}

// resolveCursorModelSelection 把兼容协议的标准 effort 与 RingStar 扩展项合并成
// Cursor Agent wire 上的一份 RequestedModel。三个 HTTP 入口共用它，避免同一组
// 参数在 Chat Completions / Responses / Messages 下产生不同结果。
func resolveCursorModelSelection(
	body []byte,
	publicModel string,
	standardEffort *string,
) (cursor.ModelSelection, error) {
	var standardOptions *cursor.ModelOptions
	if standardEffort != nil {
		standardOptions = &cursor.ModelOptions{Effort: standardEffort}
	}
	return resolveCursorModelSelectionWithStandardOptions(body, publicModel, standardOptions)
}

func resolveCursorModelSelectionWithStandardOptions(
	body []byte,
	publicModel string,
	standardOptions *cursor.ModelOptions,
) (cursor.ModelSelection, error) {
	var envelope cursorModelOptionsEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return cursor.ModelSelection{}, fmt.Errorf("invalid cursor_options: %w", err)
	}
	return cursor.ResolveModelWithStandardOptionsStrict(publicModel, standardOptions, envelope.CursorOptions)
}

// resolveCursorAccountModelSelection applies the account-level model mapping
// and then enforces the Sand catalog at the final forwarding boundary. The
// scheduler already filters unsupported accounts, but this guard also covers
// direct account tests and future call paths that may bypass normal selection.
func resolveCursorAccountModelSelection(
	account *Account,
	body []byte,
	publicModel string,
	standardEffort *string,
) (cursor.ModelSelection, error) {
	var standardOptions *cursor.ModelOptions
	if standardEffort != nil {
		standardOptions = &cursor.ModelOptions{Effort: standardEffort}
	}
	return resolveCursorAccountModelSelectionWithStandardOptions(
		account,
		body,
		publicModel,
		standardOptions,
	)
}

func resolveCursorAccountModelSelectionWithStandardOptions(
	account *Account,
	body []byte,
	publicModel string,
	standardOptions *cursor.ModelOptions,
) (cursor.ModelSelection, error) {
	effectiveModel := strings.TrimSpace(publicModel)
	if account != nil && account.CursorAgentProfile() == cursor.AgentProfileSand {
		if len(account.GetModelMapping()) > 0 {
			mappedModel, matched := account.ResolveMappedModel(effectiveModel)
			if !matched {
				return cursor.ModelSelection{}, fmt.Errorf(
					"model %q is not enabled for this Grok Bot account",
					publicModel,
				)
			}
			effectiveModel = mappedModel
		}
		if !cursor.IsSandModelSupported(effectiveModel) {
			return cursor.ModelSelection{}, fmt.Errorf(
				"model %q is not available through Grok Bot weekly usage",
				publicModel,
			)
		}
	}
	return resolveCursorModelSelectionWithStandardOptions(body, effectiveModel, standardOptions)
}

func cursorFastFromServiceTier(raw string) (*bool, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	normalized := normalizeOpenAIServiceTier(trimmed)
	if normalized == nil {
		return nil, fmt.Errorf("unsupported service_tier %q", raw)
	}
	fast := *normalized == "priority"
	return &fast, nil
}

// annotateCursorModelSelection 把本次生效的选型回写到 ForwardResult，供用量
// 日志与计费使用：
//   - effort → ReasoningEffort；
//   - fast → ServiceTier（"fast"/"standard"），计费层按 fast 档价格计价；
//   - MAX → 归一化 Model 的 -max 后缀。cursor_options.max_mode 与旧的 -max
//     模型名后缀因此在账单和统计里落到同一个模型名，MAX 用量不会隐身。
func annotateCursorModelSelection(result *ForwardResult, selection cursor.ModelSelection) {
	if result == nil {
		return
	}
	for _, param := range selection.Params {
		switch param.ID {
		case "effort":
			effort := param.Value
			result.ReasoningEffort = &effort
		case "fast":
			tier := ServiceTierStandard
			if param.Value == "true" {
				tier = ServiceTierFast
			}
			result.ServiceTier = &tier
		}
	}
	if selection.MaxMode != nil && result.Model != "" {
		base := strings.TrimSuffix(result.Model, cursor.MaxModeSuffix)
		if *selection.MaxMode {
			result.Model = base + cursor.MaxModeSuffix
		} else {
			result.Model = base
		}
	}
}
