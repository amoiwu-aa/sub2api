package kiro

// 模型能力目录：把 ListAvailableModels 的结果整理成可直接查询的形式。
//
// 这些数据此前全被丢掉了——目录只用来列个模型名，倍率、上下文上限、
// 输入类型都没解析。结果是两类本可以避免的浪费：
//
//   - 计费按估算而不是上游的真实倍率。实测倍率跨度 0.05 ~ 2.4，相差 48 倍，
//     不查表就不会知道 auto（1.0）其实比 claude-sonnet-4.5（1.3）还便宜，
//     而且上下文是它的 5 倍。
//   - 明知会失败的请求照样发出去：超上下文上限的、给纯文本模型带图片的，
//     都要等上游报错才发现，白白浪费一次请求与一轮 failover。

import (
	"sort"
	"strings"
	"time"
)

// 输入类型，取自 ListAvailableModels 的 supportedInputTypes。
const (
	InputTypeText  = "TEXT"
	InputTypeImage = "IMAGE"
)

// Catalog 是某账号可用模型的快照。
//
// 目录随账号等级变化（实测免费号 9 个、企业号 19 个），所以它是按账号缓存的，
// 不能全局共用一份。
type Catalog struct {
	models    map[string]AvailableModel
	fetchedAt time.Time
}

// NewCatalog 从 ListAvailableModels 的结果构建目录。
func NewCatalog(models []AvailableModel, fetchedAt time.Time) *Catalog {
	c := &Catalog{models: make(map[string]AvailableModel, len(models)), fetchedAt: fetchedAt}
	for _, m := range models {
		if id := strings.TrimSpace(m.ModelID); id != "" {
			c.models[id] = m
		}
	}
	return c
}

// FetchedAt 返回快照时间，供调用方判断是否该刷新。
func (c *Catalog) FetchedAt() time.Time {
	if c == nil {
		return time.Time{}
	}
	return c.fetchedAt
}

// Len 返回目录里的模型数。
func (c *Catalog) Len() int {
	if c == nil {
		return 0
	}
	return len(c.models)
}

// Lookup 按 modelId 查模型，找不到返回 false。
func (c *Catalog) Lookup(modelID string) (AvailableModel, bool) {
	if c == nil {
		return AvailableModel{}, false
	}
	m, ok := c.models[strings.TrimSpace(modelID)]
	return m, ok
}

// ModelIDs 返回按计费倍率升序排列的模型列表（便宜的在前）。
func (c *Catalog) ModelIDs() []string {
	if c == nil {
		return nil
	}
	ids := make([]string, 0, len(c.models))
	for id := range c.models {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		ri, rj := c.models[ids[i]].RateMultiplier, c.models[ids[j]].RateMultiplier
		if ri == rj {
			return ids[i] < ids[j]
		}
		return ri < rj
	})
	return ids
}

// RateMultiplier 返回该模型的计费倍率；模型不在目录里时返回 0。
//
// 返回 0 表示「不知道」，调用方不应把它当成免费——查不到时应退回原有的
// 估算口径，而不是按 0 计费。
func (c *Catalog) RateMultiplier(modelID string) float64 {
	m, ok := c.Lookup(modelID)
	if !ok {
		return 0
	}
	return m.RateMultiplier
}

// MaxInputTokens 返回该模型的上下文上限；未知返回 0。
func (c *Catalog) MaxInputTokens(modelID string) int64 {
	m, ok := c.Lookup(modelID)
	if !ok || m.TokenLimits == nil {
		return 0
	}
	return m.TokenLimits.MaxInputTokens
}

// SupportsImages 报告该模型是否接受图片输入。
//
// 实测 minimax-m2.5 与 glm-5 只支持 TEXT，带图片路由过去必然失败。
// 目录里查不到该模型时返回 true：宁可发出去让上游判，也不要因为目录过期
// 就把本来能用的请求拦下来。
func (c *Catalog) SupportsImages(modelID string) bool {
	m, ok := c.Lookup(modelID)
	if !ok || len(m.SupportedInputTypes) == 0 {
		return true
	}
	for _, t := range m.SupportedInputTypes {
		if strings.EqualFold(t, InputTypeImage) {
			return true
		}
	}
	return false
}

// HasImages 报告这次会话里是否带了图片。
//
// 用来在发请求之前挡掉「纯文本模型 + 图片」的组合：实测 minimax-m2.5 与
// glm-5 只支持 TEXT，这种请求发出去必然失败，白白浪费一次调用与一轮 failover。
func (s *ConversationState) HasImages() bool {
	if s == nil {
		return false
	}
	if msg := s.CurrentMessage.UserInputMessage; msg != nil && len(msg.Images) > 0 {
		return true
	}
	for _, m := range s.History {
		if msg := m.UserInputMessage; msg != nil && len(msg.Images) > 0 {
			return true
		}
	}
	return false
}

// CheapestSupporting 在目录里挑一个最便宜、且满足给定约束的模型。
//
// needImages 为真时跳过纯文本模型；minInputTokens 大于 0 时跳过上下文
// 装不下的模型。都不满足时返回空串。
//
// 这是「按真实成本路由」的最小可用形式：倍率差 48 倍，换个模型比任何
// 微观优化都管用。
func (c *Catalog) CheapestSupporting(needImages bool, minInputTokens int64) string {
	if c == nil {
		return ""
	}
	for _, id := range c.ModelIDs() { // 已按倍率升序
		m := c.models[id]
		if m.RateMultiplier <= 0 {
			continue
		}
		if needImages && !c.SupportsImages(id) {
			continue
		}
		if minInputTokens > 0 {
			if m.TokenLimits == nil || m.TokenLimits.MaxInputTokens < minInputTokens {
				continue
			}
		}
		return id
	}
	return ""
}
