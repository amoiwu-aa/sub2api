// Package kiro implements the native Kiro (Amazon Q Developer) upstream bridge.
//
// 协议对照来源：kiro-proxy（token-reader.js / q-client.js / server.js）。
// 上游是 Amazon Q 的 GenerateAssistantResponse，鉴权用 bearer token 而非 SigV4。
package kiro

import "strings"

// PublicModelPrefix 是 kiro 模型对外暴露时的命名空间前缀。
//
// Kiro 的上游模型就是 Claude 原名（claude-sonnet-4.6 等）。若不加前缀，
// composite 分组的 service.DetectModelPlatform 会按 "claude-" 前缀把它判成
// anthropic 并调度到 Claude 账号池。前缀是这两个平台能共存于 composite 的前提。
const PublicModelPrefix = "kiro/"

// Model describes a Kiro model in OpenAI-compatible /models shape.
type Model struct {
	ID          string `json:"id"`
	Object      string `json:"object"`
	Created     int64  `json:"created,omitempty"`
	OwnedBy     string `json:"owned_by"`
	DisplayName string `json:"display_name,omitempty"`
}

// DefaultModelID 是没有指定模型时用的档位。
//
// 取 auto 而不是某个具体模型：可用模型随订阅档位变化，只有 auto 在免费号和
// 企业号上都存在，也是上游 ListAvailableModels 自己返回的 defaultModel。
// 早先这里默认 claude-sonnet-4.6，那是企业号才有的模型，免费号上第一个请求
// 就会被上游回 INVALID_MODEL_ID。
const DefaultModelID = "auto"

// defaultModels 是 ListAvailableModels 不可用时的静态兜底目录。
// 权威列表由上游 ListAvailableModels 返回（见 client.go），此处仅用于
// 后台建组时的候选模型下拉与首次拉取失败的降级。
//
// 这里是**免费档与企业档的并集**，任何单个账号都只能用到其中一部分：
// 实测免费号 9 个、企业号 19 个。所以它只能当候选池，不能当「保证可用」的
// 清单——真要知道某个账号有什么，得看该账号的 Catalog。
var defaultModels = []Model{
	// 两档都有
	{ID: PublicModelPrefix + "auto", Object: "model", OwnedBy: "amazon", DisplayName: "Kiro Auto"},
	{ID: PublicModelPrefix + "claude-sonnet-4.5", Object: "model", OwnedBy: "amazon", DisplayName: "Kiro Claude Sonnet 4.5"},
	{ID: PublicModelPrefix + "claude-sonnet-4", Object: "model", OwnedBy: "amazon", DisplayName: "Kiro Claude Sonnet 4"},
	{ID: PublicModelPrefix + "claude-haiku-4.5", Object: "model", OwnedBy: "amazon", DisplayName: "Kiro Claude Haiku 4.5"},
	{ID: PublicModelPrefix + "deepseek-3.2", Object: "model", OwnedBy: "amazon", DisplayName: "Kiro Deepseek v3.2"},
	{ID: PublicModelPrefix + "minimax-m2.5", Object: "model", OwnedBy: "amazon", DisplayName: "Kiro MiniMax M2.5"},
	{ID: PublicModelPrefix + "minimax-m2.1", Object: "model", OwnedBy: "amazon", DisplayName: "Kiro MiniMax M2.1"},
	{ID: PublicModelPrefix + "glm-5", Object: "model", OwnedBy: "amazon", DisplayName: "Kiro GLM 5"},
	{ID: PublicModelPrefix + "qwen3-coder-next", Object: "model", OwnedBy: "amazon", DisplayName: "Kiro Qwen3 Coder Next"},
	// 仅企业档，免费号点名会被上游回 INVALID_MODEL_ID
	{ID: PublicModelPrefix + "claude-sonnet-4.6", Object: "model", OwnedBy: "amazon", DisplayName: "Kiro Claude Sonnet 4.6"},
	{ID: PublicModelPrefix + "claude-opus-4.6", Object: "model", OwnedBy: "amazon", DisplayName: "Kiro Claude Opus 4.6"},
	{ID: PublicModelPrefix + "claude-opus-5", Object: "model", OwnedBy: "amazon", DisplayName: "Kiro Claude Opus 5"},
	{ID: PublicModelPrefix + "gpt-5.6-sol", Object: "model", OwnedBy: "amazon", DisplayName: "Kiro GPT 5.6 Sol"},
}

// ModelsFromCatalog 把某个账号的实时目录转成对外的模型列表。
//
// 与 defaultModels 的区别是这份一定是「这个账号真能用的」，因为它来自
// 该账号自己的 ListAvailableModels。顺序沿用 Catalog 的计费倍率升序。
func ModelsFromCatalog(c *Catalog) []Model {
	if c == nil || c.Len() == 0 {
		return nil
	}
	ids := c.ModelIDs()
	out := make([]Model, 0, len(ids))
	for _, id := range ids {
		display := id
		if m, ok := c.Lookup(id); ok && strings.TrimSpace(m.ModelName) != "" {
			display = m.ModelName
		}
		out = append(out, Model{
			ID:          PublicModelPrefix + id,
			Object:      "model",
			OwnedBy:     "amazon",
			DisplayName: "Kiro " + display,
		})
	}
	return out
}

// DefaultModels 返回静态兜底模型目录的副本。
func DefaultModels() []Model {
	out := make([]Model, len(defaultModels))
	copy(out, defaultModels)
	return out
}

// DefaultModelIDs 返回静态兜底模型的对外 ID 列表（含 kiro/ 前缀）。
func DefaultModelIDs() []string {
	models := DefaultModels()
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	return ids
}

// UpstreamModelID 把对外模型名转换为 Q API 认识的 modelId。
// 前缀可选：专用 kiro 分组里客户端直接写 claude-sonnet-4.6 也应该能用。
func UpstreamModelID(publicModel string) string {
	trimmed := strings.TrimSpace(publicModel)
	if trimmed == "" {
		return ""
	}
	if rest := strings.TrimPrefix(trimmed, PublicModelPrefix); rest != trimmed {
		return strings.TrimSpace(rest)
	}
	return trimmed
}

// PublicModelID 把上游 modelId 转换为对外模型名（幂等）。
func PublicModelID(upstreamModel string) string {
	trimmed := strings.TrimSpace(upstreamModel)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, PublicModelPrefix) {
		return trimmed
	}
	return PublicModelPrefix + trimmed
}
