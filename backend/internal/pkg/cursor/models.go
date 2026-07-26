// Package cursor implements the native Cursor Agent upstream bridge.
//
// 协议对照来源：cursor 服务器反代（login-token.js / cursor-session-token.js /
// token-store.js / agent-client.js / cursor-agent-env.js）。上游是
// agent.api5.cursor.sh 的 AgentService/Run，HTTP/2 双向流 + Connect 信封。
package cursor

import "strings"

// PublicModelPrefix 是 cursor 模型对外暴露时的命名空间前缀。
//
// Cursor 的模型表里既有 Claude 也有 GPT 与 Grok 原名，不加前缀会被
// service.DetectModelPlatform 判成 anthropic/openai/grok 而调度错账号池。
const PublicModelPrefix = "cursor/"

// AutoModelID 是 Cursor 的自动选模，对应上游 modelId "default"。
// 反代注释指出具名模型可能受套餐限制，Auto 对绝大多数账号都可用。
const AutoModelID = "default"

// Model describes a Cursor model in OpenAI-compatible /models shape.
type Model struct {
	ID          string `json:"id"`
	Object      string `json:"object"`
	Created     int64  `json:"created,omitempty"`
	OwnedBy     string `json:"owned_by"`
	DisplayName string `json:"display_name,omitempty"`
}

// defaultModels 对应反代 cursor-agent-env.js 的 OFFICIAL_SELECTED_SUBAGENT_MODELS。
// 上游没有公开的模型列表接口可稳定调用，因此这里是内置表。
var defaultModels = []Model{
	{ID: PublicModelPrefix + AutoModelID, Object: "model", OwnedBy: "cursor-agent", DisplayName: "Cursor Auto"},
	{ID: PublicModelPrefix + "claude-fable-5", Object: "model", OwnedBy: "cursor-agent", DisplayName: "Cursor Claude Fable 5"},
	{ID: PublicModelPrefix + "claude-opus-4-8", Object: "model", OwnedBy: "cursor-agent", DisplayName: "Cursor Claude Opus 4.8"},
	{ID: PublicModelPrefix + "claude-sonnet-5", Object: "model", OwnedBy: "cursor-agent", DisplayName: "Cursor Claude Sonnet 5"},
	{ID: PublicModelPrefix + "gpt-5.6-sol", Object: "model", OwnedBy: "cursor-agent", DisplayName: "Cursor GPT 5.6 Sol"},
	{ID: PublicModelPrefix + "gpt-5.6-terra", Object: "model", OwnedBy: "cursor-agent", DisplayName: "Cursor GPT 5.6 Terra"},
	{ID: PublicModelPrefix + "grok-4.5", Object: "model", OwnedBy: "cursor-agent", DisplayName: "Cursor Grok 4.5"},
	{ID: PublicModelPrefix + "composer-2.5", Object: "model", OwnedBy: "cursor-agent", DisplayName: "Cursor Composer 2.5"},
}

// DefaultModels 返回内置模型目录的副本。
func DefaultModels() []Model {
	out := make([]Model, len(defaultModels))
	copy(out, defaultModels)
	return out
}

// DefaultModelIDs 返回内置模型的对外 ID 列表（含 cursor/ 前缀）。
func DefaultModelIDs() []string {
	models := DefaultModels()
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	return ids
}

// knownUpstreamModelIDs 是 defaultModels 去掉前缀后的裸 modelId 集合。
var knownUpstreamModelIDs = func() map[string]struct{} {
	m := make(map[string]struct{}, len(defaultModels))
	for _, model := range defaultModels {
		m[strings.TrimPrefix(model.ID, PublicModelPrefix)] = struct{}{}
	}
	return m
}()

// UpstreamModelID 把对外模型名转换为 Agent wire 上的 modelId。
// 空值与未知名一律回退到 Auto，避免把客户端随手写的模型名直接打给上游。
//
// 回退而不是透传，是因为上游对陌生 modelId 的反应是整轮失败（而且错误信息很含糊）。
// Auto 对绝大多数账号都可用，让请求以"模型不完全如愿"收场，好过直接报错。
func UpstreamModelID(publicModel string) string {
	trimmed := strings.TrimSpace(publicModel)
	if rest := strings.TrimPrefix(trimmed, PublicModelPrefix); rest != trimmed {
		trimmed = strings.TrimSpace(rest)
	}
	if trimmed == "" {
		return AutoModelID
	}
	if _, ok := knownUpstreamModelIDs[trimmed]; !ok {
		return AutoModelID
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
