// Package cursor implements the native Cursor Agent upstream bridge.
//
// 协议对照来源：cursor 服务器反代（login-token.js / cursor-session-token.js /
// token-store.js / agent-client.js / cursor-agent-env.js）。上游是
// agent.api5.cursor.sh 的 AgentService/Run，HTTP/2 双向流 + Connect 信封。
package cursor

import (
	"fmt"
	"strings"
)

// PublicModelPrefix 是 cursor 模型对外暴露时的命名空间前缀。
//
// Cursor 的模型表里既有 Claude 也有 GPT 与 Grok 原名，不加前缀会被
// service.DetectModelPlatform 判成 anthropic/openai/grok 而调度错账号池。
const PublicModelPrefix = "cursor/"

// AutoModelID 是 Cursor 的自动选模，对应上游 modelId "default"。
// 反代注释指出具名模型可能受套餐限制，Auto 对绝大多数账号都可用。
const AutoModelID = "default"

// MaxModeSuffix 是 MAX 模式在对外模型名上的后缀。
//
// Cursor 的 IDE 把 MAX 做成一个独立于模型名的开关（wire 上是 RequestedModel
// 的 field 2），但对外这一层只有一个 OpenAI 风格的模型名可用，所以把它编进
// 名字里：cursor/grok-4.6-max 等价于「选 grok-4.6 并打开 MAX」。
//
// 这样客户端在模型下拉框里就能选，不必额外传自定义字段；用量日志里也是两个
// 不同的模型名，MAX 的开销天然可以单独计价和统计。
//
// 前提是没有哪个上游模型的真名以 -max 结尾。真出现了就得换一种表达，
// 否则 ResolveModel 会把它误拆成「某模型 + MAX」。
const MaxModeSuffix = "-max"

// Model describes a Cursor model in OpenAI-compatible /models shape.
type Model struct {
	ID          string `json:"id"`
	Object      string `json:"object"`
	Created     int64  `json:"created,omitempty"`
	OwnedBy     string `json:"owned_by"`
	DisplayName string `json:"display_name,omitempty"`
	// CursorCapabilities is a versioned extension. OpenAI-compatible clients
	// ignore unknown fields; RingStar-aware clients use it instead of probing 400s.
	CursorCapabilities *ModelCapabilities `json:"cursor_capabilities,omitempty"`
}

const BridgeProtocolVersion = "1.0"

// ModelCapabilities describes verified request-level controls for one model.
type ModelCapabilities struct {
	BridgeVersion string   `json:"bridge_version"`
	Efforts       []string `json:"efforts,omitempty"`
	Fast          bool     `json:"fast"`
	MaxMode       bool     `json:"max_mode"`
}

// NativeToolCapability describes the canonical arguments emitted for one
// native bridge key.
type NativeToolCapability struct {
	Key       string          `json:"key"`
	Arguments []NativeToolArg `json:"arguments"`
}

// BridgeCapabilities is returned with Cursor /models responses.
type BridgeCapabilities struct {
	Version                string                 `json:"version"`
	DefaultMode            string                 `json:"default_mode"`
	Modes                  []string               `json:"modes"`
	Protocols              []string               `json:"protocols"`
	NativeTools            []NativeToolCapability `json:"native_tools"`
	ParallelToolCalls      bool                   `json:"parallel_tool_calls"`
	Images                 bool                   `json:"images"`
	InteractionQueries     bool                   `json:"interaction_queries"`
	StatefulContinuation   bool                   `json:"stateful_continuation"`
	ProtocolTerminalErrors bool                   `json:"protocol_terminal_errors"`
}

// defaultModels 对应反代 cursor-agent-env.js 的 OFFICIAL_SELECTED_SUBAGENT_MODELS。
// 上游没有公开的模型列表接口可稳定调用，因此这里是内置表。
//
// 带 MaxModeSuffix 的条目不是上游模型名，是我们对外拼出来的 MAX 变体，
// 发给上游前会被 ResolveModel 拆回「裸名 + MaxMode」。只列出确认可用的组合：
// 未列出的组合仍然能手写（ResolveModel 认后缀，不认目录），但不主动推荐。
var defaultModels = []Model{
	{ID: PublicModelPrefix + AutoModelID, Object: "model", OwnedBy: "cursor-agent", DisplayName: "Cursor Auto"},
	{ID: PublicModelPrefix + "claude-fable-5", Object: "model", OwnedBy: "cursor-agent", DisplayName: "Cursor Claude Fable 5"},
	{ID: PublicModelPrefix + "claude-opus-4-8", Object: "model", OwnedBy: "cursor-agent", DisplayName: "Cursor Claude Opus 4.8"},
	{ID: PublicModelPrefix + "claude-sonnet-5", Object: "model", OwnedBy: "cursor-agent", DisplayName: "Cursor Claude Sonnet 5"},
	{ID: PublicModelPrefix + "gpt-5.6-sol", Object: "model", OwnedBy: "cursor-agent", DisplayName: "Cursor GPT 5.6 Sol"},
	{ID: PublicModelPrefix + "gpt-5.6-terra", Object: "model", OwnedBy: "cursor-agent", DisplayName: "Cursor GPT 5.6 Terra"},
	{ID: PublicModelPrefix + "grok-4.6", Object: "model", OwnedBy: "cursor-agent", DisplayName: "Cursor Grok 4.6"},
	{ID: PublicModelPrefix + "grok-4.6" + MaxModeSuffix, Object: "model", OwnedBy: "cursor-agent", DisplayName: "Cursor Grok 4.6 (MAX)"},
	{ID: PublicModelPrefix + "grok-4.5", Object: "model", OwnedBy: "cursor-agent", DisplayName: "Cursor Grok 4.5"},
	{ID: PublicModelPrefix + "grok-4.5" + MaxModeSuffix, Object: "model", OwnedBy: "cursor-agent", DisplayName: "Cursor Grok 4.5 (MAX)"},
	{ID: PublicModelPrefix + "composer-2.5", Object: "model", OwnedBy: "cursor-agent", DisplayName: "Cursor Composer 2.5"},
}

// DefaultModels 返回内置模型目录的副本。
func DefaultModels() []Model {
	out := make([]Model, len(defaultModels))
	copy(out, defaultModels)
	for i := range out {
		capabilities := modelCapabilities(out[i].ID)
		out[i].CursorCapabilities = &capabilities
	}
	return out
}

// DefaultBridgeCapabilities returns a deterministic capability contract.
func DefaultBridgeCapabilities(defaultMode string) BridgeCapabilities {
	tools := make([]NativeToolCapability, 0, len(nativeToolArgSpecs))
	for _, key := range NativeToolBridgeKeys() {
		spec := NativeToolArgSpec(key)
		args := append([]NativeToolArg(nil), spec...)
		tools = append(tools, NativeToolCapability{Key: key, Arguments: args})
	}
	return BridgeCapabilities{
		Version:                BridgeProtocolVersion,
		DefaultMode:            defaultMode,
		Modes:                  []string{"off", "shadow", "explicit", "infer_readonly", "infer_all"},
		Protocols:              []string{"chat_completions", "anthropic_messages", "responses"},
		NativeTools:            tools,
		ParallelToolCalls:      true,
		Images:                 true,
		InteractionQueries:     false,
		StatefulContinuation:   false,
		ProtocolTerminalErrors: true,
	}
}

func modelCapabilities(publicModel string) ModelCapabilities {
	selection := ResolveModel(publicModel)
	capabilities := ModelCapabilities{BridgeVersion: BridgeProtocolVersion}
	switch selection.ModelID {
	case "grok-4.6":
		capabilities.Efforts = []string{ModelEffortLow, ModelEffortMedium, ModelEffortHigh, ModelEffortXHigh}
		capabilities.Fast = true
		capabilities.MaxMode = true
	case "grok-4.5":
		capabilities.Efforts = []string{ModelEffortLow, ModelEffortMedium, ModelEffortHigh}
		capabilities.Fast = true
		capabilities.MaxMode = true
	case "composer-2.5":
		capabilities.Fast = true
	}
	return capabilities
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

// knownUpstreamModelIDs 是真实存在于上游的裸 modelId 集合。
//
// MAX 变体不收进来：那是对外拼出来的名字，原样发给上游只会被当成陌生模型。
var knownUpstreamModelIDs = func() map[string]struct{} {
	m := make(map[string]struct{}, len(defaultModels))
	for _, model := range defaultModels {
		bare := strings.TrimPrefix(model.ID, PublicModelPrefix)
		if strings.HasSuffix(bare, MaxModeSuffix) {
			continue
		}
		m[bare] = struct{}{}
	}
	return m
}()

// ModelSelection 是一次请求解析出来的完整选型。
//
// 上游的 RequestedModel 有三部分，对外却只有一个模型名，ResolveModel 负责
// 把后者拆成前者。
type ModelSelection struct {
	// ModelID 是 Agent wire 上的 modelId。
	ModelID string
	// Params 是 RequestedModel.params。
	//
	// 必须区分 nil 与空切片：EncodeRunRequest 见到 nil 会补上
	// DefaultModelParams()，所以 Auto 要传非 nil 的空切片才能真的不带参数。
	Params []ModelParam
	// MaxMode 为 nil 时不写 field 2；上游区分「没说」与「显式 false」。
	MaxMode *bool
}

const (
	ModelEffortLow    = "low"
	ModelEffortMedium = "medium"
	ModelEffortHigh   = "high"
	ModelEffortXHigh  = "xhigh"
)

// ModelOptions 是公共 API 对 Cursor RequestedModel 的可调覆盖项。
//
// Fast / MaxMode 用指针区分「没有指定」与显式 false。没有指定时保留模型目录的
// 默认值，旧客户端只传 model 的行为因此完全不变。
type ModelOptions struct {
	Effort  *string `json:"effort,omitempty"`
	Fast    *bool   `json:"fast,omitempty"`
	MaxMode *bool   `json:"max_mode,omitempty"`
}

// ResolveModel 把对外模型名解析成上游选型。
//
// 空值与未知名一律回退到 Auto，避免把客户端随手写的模型名直接打给上游。
// 回退而不是透传，是因为上游对陌生 modelId 的反应是整轮失败（而且错误信息很含糊）。
// Auto 对绝大多数账号都可用，让请求以"模型不完全如愿"收场，好过直接报错。
func ResolveModel(publicModel string) ModelSelection {
	bare := strings.TrimSpace(publicModel)
	if rest := strings.TrimPrefix(bare, PublicModelPrefix); rest != bare {
		bare = strings.TrimSpace(rest)
	}

	maxMode := false
	if rest, found := strings.CutSuffix(bare, MaxModeSuffix); found {
		// 只有剥掉后缀能对上一个真实模型时才认它是 MAX 变体，
		// 否则 "foo-max" 这种名字会被拆成一个并不存在的 "foo"。
		if _, ok := knownUpstreamModelIDs[rest]; ok {
			bare, maxMode = rest, true
		}
	}

	if _, ok := knownUpstreamModelIDs[bare]; !ok {
		bare = AutoModelID
		maxMode = false
	}
	if bare == AutoModelID {
		// Auto 不该带 effort/fast 这类具名模型的参数，也没有 MAX 可言。
		return ModelSelection{ModelID: AutoModelID, Params: []ModelParam{}}
	}

	selection := ModelSelection{ModelID: bare, Params: DefaultModelParams()}
	if maxMode {
		selection.MaxMode = &maxMode
	}
	return selection
}

// ResolveModelStrict resolves only models present in the verified catalog.
// Public gateway APIs use it so a typo cannot silently run cursor/default while
// the response still claims the requested model.
func ResolveModelStrict(publicModel string) (ModelSelection, error) {
	bare := strings.TrimSpace(publicModel)
	if rest := strings.TrimPrefix(bare, PublicModelPrefix); rest != bare {
		bare = strings.TrimSpace(rest)
	}
	if bare == "" {
		return ModelSelection{}, fmt.Errorf("cursor model is required")
	}
	if rest, found := strings.CutSuffix(bare, MaxModeSuffix); found {
		if _, ok := knownUpstreamModelIDs[rest]; !ok {
			return ModelSelection{}, fmt.Errorf("unknown cursor model %q", publicModel)
		}
		return ResolveModel(publicModel), nil
	}
	if _, ok := knownUpstreamModelIDs[bare]; !ok {
		return ModelSelection{}, fmt.Errorf("unknown cursor model %q", publicModel)
	}
	return ResolveModel(publicModel), nil
}

// ResolveModelWithOptions 在模型名解析结果上应用请求级参数。
//
// standardEffort 来自各兼容协议的标准字段：
//   - Chat Completions: reasoning_effort
//   - Responses: reasoning.effort
//   - Anthropic Messages: output_config.effort
//
// options 来自三种协议共用的 cursor_options。cursor_options.effort 的优先级高于
// 标准字段，显式 fast/max_mode 则覆盖目录默认值与旧的 -max 模型后缀。
func ResolveModelWithOptions(
	publicModel string,
	standardEffort *string,
	options *ModelOptions,
) (ModelSelection, error) {
	selection := ResolveModel(publicModel)
	return applyModelOptions(selection, standardEffort, options)
}

// ResolveModelWithOptionsStrict combines strict catalog validation with
// request-level effort/fast/MAX overrides.
func ResolveModelWithOptionsStrict(
	publicModel string,
	standardEffort *string,
	options *ModelOptions,
) (ModelSelection, error) {
	selection, err := ResolveModelStrict(publicModel)
	if err != nil {
		return ModelSelection{}, err
	}
	return applyModelOptions(selection, standardEffort, options)
}

func applyModelOptions(
	selection ModelSelection,
	standardEffort *string,
	options *ModelOptions,
) (ModelSelection, error) {
	effort := ""
	standardEffortValue := ""
	customEffortValue := ""
	hasEffort := false
	if standardEffort != nil {
		normalized, err := normalizeModelEffort(*standardEffort)
		if err != nil {
			return ModelSelection{}, err
		}
		standardEffortValue = normalized
		effort = normalized
		hasEffort = true
	}
	if options != nil {
		if options.Effort != nil {
			normalized, err := normalizeModelEffort(*options.Effort)
			if err != nil {
				return ModelSelection{}, err
			}
			customEffortValue = normalized
			effort = normalized
			hasEffort = true
		}
	}
	hasOverrides := hasEffort || (options != nil && (options.Fast != nil || options.MaxMode != nil))
	if !hasOverrides {
		return selection, nil
	}

	if selection.ModelID == AutoModelID {
		return ModelSelection{}, fmt.Errorf("cursor model options require a named model, not cursor/default")
	}
	if !supportsRequestModelOptions(selection.ModelID) {
		return ModelSelection{}, fmt.Errorf(
			"cursor model options are not verified for %q; supported models are cursor/grok-4.6, cursor/grok-4.5 and cursor/composer-2.5",
			PublicModelID(selection.ModelID),
		)
	}
	for _, explicitEffort := range []string{standardEffortValue, customEffortValue} {
		if explicitEffort != "" && !supportsModelEffort(selection.ModelID, explicitEffort) {
			return ModelSelection{}, fmt.Errorf(
				"effort %q is not supported by %s",
				explicitEffort,
				PublicModelID(selection.ModelID),
			)
		}
	}

	selection.Params = append([]ModelParam(nil), selection.Params...)
	if effort != "" {
		selection.Params = setModelParam(selection.Params, "effort", effort)
	}
	if options != nil {
		if options.Fast != nil {
			selection.Params = setModelParam(selection.Params, "fast", fmt.Sprintf("%t", *options.Fast))
		}
		if options.MaxMode != nil {
			maxMode := *options.MaxMode
			selection.MaxMode = &maxMode
		}
	}
	return selection, nil
}

func normalizeModelEffort(raw string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "" {
		return "", fmt.Errorf("effort must not be empty")
	}
	return normalized, nil
}

func supportsRequestModelOptions(modelID string) bool {
	switch modelID {
	case "grok-4.6", "grok-4.5":
		return true
	case "composer-2.5":
		// composer 的默认参数就带 fast=true（见官方目录
		// officialSelectedSubagentModels），fast 是它的合法参数。必须允许
		// cursor_options.fast=false：composer 的 Fast 官方价是标准价的
		// 3~7.5 倍，没有这个退出通道，标准档价格就是不可达的。
		// effort 仍会被 supportsModelEffort 拒掉（composer 无档位）。
		return true
	default:
		return false
	}
}

func supportsModelEffort(modelID, effort string) bool {
	switch modelID {
	case "grok-4.6":
		return effort == ModelEffortLow ||
			effort == ModelEffortMedium ||
			effort == ModelEffortHigh ||
			effort == ModelEffortXHigh
	case "grok-4.5":
		return effort == ModelEffortLow ||
			effort == ModelEffortMedium ||
			effort == ModelEffortHigh
	default:
		return false
	}
}

func setModelParam(params []ModelParam, id, value string) []ModelParam {
	for i := range params {
		if params[i].ID == id {
			params[i].Value = value
			return params
		}
	}
	return append(params, ModelParam{ID: id, Value: value})
}

// UpstreamModelID 把对外模型名转换为 Agent wire 上的 modelId。
//
// 只要 modelId 的调用方（日志、探活）用它；需要完整选型的走 ResolveModel。
func UpstreamModelID(publicModel string) string {
	return ResolveModel(publicModel).ModelID
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
