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
	var envelope cursorModelOptionsEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return cursor.ModelSelection{}, fmt.Errorf("invalid cursor_options: %w", err)
	}
	return cursor.ResolveModelWithOptionsStrict(publicModel, standardEffort, envelope.CursorOptions)
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
