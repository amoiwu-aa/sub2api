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

// defaultModels 是 ListAvailableModels 不可用时的静态兜底目录。
// 权威列表由上游 ListAvailableModels 返回（见 client.go），此处仅用于
// 后台建组时的候选模型下拉与首次拉取失败的降级。
var defaultModels = []Model{
	{ID: PublicModelPrefix + "auto", Object: "model", OwnedBy: "amazon", DisplayName: "Kiro Auto"},
	{ID: PublicModelPrefix + "claude-sonnet-4.6", Object: "model", OwnedBy: "amazon", DisplayName: "Kiro Claude Sonnet 4.6"},
	{ID: PublicModelPrefix + "claude-sonnet-4.5", Object: "model", OwnedBy: "amazon", DisplayName: "Kiro Claude Sonnet 4.5"},
	{ID: PublicModelPrefix + "claude-opus-4.6", Object: "model", OwnedBy: "amazon", DisplayName: "Kiro Claude Opus 4.6"},
	{ID: PublicModelPrefix + "claude-haiku-4.5", Object: "model", OwnedBy: "amazon", DisplayName: "Kiro Claude Haiku 4.5"},
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
