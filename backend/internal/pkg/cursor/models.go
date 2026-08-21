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

const BridgeProtocolVersion = "1.1"

// ModelCapabilities describes verified request-level controls for one model.
type ModelCapabilities struct {
	BridgeVersion string   `json:"bridge_version"`
	Efforts       []string `json:"efforts,omitempty"`
	DefaultEffort string   `json:"default_effort,omitempty"`
	Fast          bool     `json:"fast"`
	DefaultFast   bool     `json:"default_fast"`
	Thinking      bool     `json:"thinking"`
	MaxMode       bool     `json:"max_mode"`
}

type modelControlSpec struct {
	Efforts       []string
	DefaultEffort string
	Fast          bool
	DefaultFast   bool
	Thinking      bool
	MaxMode       bool
	DefaultParams []ModelParam
}

var modelControlSpecs = map[string]modelControlSpec{
	"claude-fable-5": {
		Efforts:       []string{ModelEffortLow, ModelEffortMedium, ModelEffortHigh, ModelEffortXHigh, ModelEffortMax},
		DefaultEffort: ModelEffortHigh,
		Thinking:      true,
		DefaultParams: []ModelParam{
			{ID: "thinking", Value: "true"},
			{ID: "effort", Value: ModelEffortHigh},
		},
	},
	"claude-opus-4-8": {
		Efforts:       []string{ModelEffortLow, ModelEffortMedium, ModelEffortHigh, ModelEffortXHigh, ModelEffortMax},
		DefaultEffort: ModelEffortHigh,
		Fast:          true,
		Thinking:      true,
		DefaultParams: []ModelParam{
			{ID: "thinking", Value: "true"},
			{ID: "context", Value: "300k"},
			{ID: "effort", Value: ModelEffortHigh},
			{ID: "fast", Value: "false"},
		},
	},
	"claude-sonnet-5": {
		Efforts:       []string{ModelEffortLow, ModelEffortMedium, ModelEffortHigh, ModelEffortXHigh, ModelEffortMax},
		DefaultEffort: ModelEffortHigh,
		Thinking:      true,
		DefaultParams: []ModelParam{
			{ID: "thinking", Value: "true"},
			{ID: "effort", Value: ModelEffortHigh},
		},
	},
	"gpt-5.6-sol": {
		Efforts:       []string{ModelEffortNone, ModelEffortLow, ModelEffortMedium, ModelEffortHigh, ModelEffortXHigh, ModelEffortMax},
		DefaultEffort: ModelEffortMedium,
		Fast:          true,
		DefaultParams: []ModelParam{
			{ID: "effort", Value: ModelEffortMedium},
			{ID: "fast", Value: "false"},
		},
	},
	"gpt-5.6-terra": {
		Efforts:       []string{ModelEffortNone, ModelEffortLow, ModelEffortMedium, ModelEffortHigh, ModelEffortXHigh, ModelEffortMax},
		DefaultEffort: ModelEffortMedium,
		Fast:          true,
		DefaultParams: []ModelParam{
			{ID: "effort", Value: ModelEffortMedium},
			{ID: "fast", Value: "false"},
		},
	},
	"grok-4.6": {
		Efforts:       []string{ModelEffortLow, ModelEffortMedium, ModelEffortHigh, ModelEffortXHigh},
		DefaultEffort: ModelEffortHigh,
		Fast:          true,
		DefaultFast:   true,
		MaxMode:       true,
		DefaultParams: []ModelParam{
			{ID: "effort", Value: ModelEffortHigh},
			{ID: "fast", Value: "true"},
		},
	},
	"grok-4.5": {
		Efforts:       []string{ModelEffortLow, ModelEffortMedium, ModelEffortHigh},
		DefaultEffort: ModelEffortHigh,
		Fast:          true,
		DefaultFast:   true,
		MaxMode:       true,
		DefaultParams: []ModelParam{
			{ID: "effort", Value: ModelEffortHigh},
			{ID: "fast", Value: "true"},
		},
	},
	"composer-2.5": {
		Fast:        true,
		DefaultFast: true,
		DefaultParams: []ModelParam{
			{ID: "fast", Value: "true"},
		},
	},
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
	if spec, ok := modelControlSpecs[selection.ModelID]; ok {
		capabilities.Efforts = append([]string(nil), spec.Efforts...)
		capabilities.DefaultEffort = spec.DefaultEffort
		capabilities.Fast = spec.Fast
		capabilities.DefaultFast = spec.DefaultFast
		capabilities.Thinking = spec.Thinking
		capabilities.MaxMode = spec.MaxMode
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

// sandSupportedUpstreamModelIDs is the compact RingStar catalog verified
// against Grok Bot's AgentService/GetUsableModels response. Sand returns many
// effort/fast variants; the public bridge keeps the existing compact IDs and
// expresses those controls through RequestedModel parameters.
var sandSupportedUpstreamModelIDs = map[string]struct{}{
	AutoModelID:       {},
	"claude-fable-5":  {},
	"claude-opus-4-8": {},
	"claude-sonnet-5": {},
	"gpt-5.6-sol":     {},
	"gpt-5.6-terra":   {},
	"grok-4.6":        {},
	"grok-4.5":        {},
	"composer-2.5":    {},
}

// IsSandModelSupported reports whether a public or bare Cursor model belongs
// to the verified Grok Bot/Sand catalog. Unknown models are never allowed to
// fall back to cursor/default for this check.
func IsSandModelSupported(publicModel string) bool {
	selection, err := ResolveModelStrict(publicModel)
	if err != nil {
		return false
	}
	_, ok := sandSupportedUpstreamModelIDs[selection.ModelID]
	return ok
}

// SandDefaultModels returns the compact model catalog exposed for Grok Bot
// accounts. IDs remain cursor/... so existing clients keep routing them to the
// Cursor platform; the account profile selects the Sand transport.
func SandDefaultModels() []Model {
	defaults := DefaultModels()
	models := make([]Model, 0, len(defaults))
	for _, model := range defaults {
		if !IsSandModelSupported(model.ID) {
			continue
		}
		model.OwnedBy = "grok-bot"
		model.DisplayName = strings.Replace(model.DisplayName, "Cursor ", "Grok Bot ", 1)
		models = append(models, model)
	}
	return models
}

func SandDefaultModelIDs() []string {
	models := SandDefaultModels()
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
	ModelEffortNone   = "none"
	ModelEffortLow    = "low"
	ModelEffortMedium = "medium"
	ModelEffortHigh   = "high"
	ModelEffortXHigh  = "xhigh"
	ModelEffortMax    = "max"
)

// ModelOptions 是公共 API 对 Cursor RequestedModel 的可调覆盖项。
//
// Fast / MaxMode 用指针区分「没有指定」与显式 false。没有指定时保留模型目录的
// 默认值，旧客户端只传 model 的行为因此完全不变。
type ModelOptions struct {
	Effort   *string `json:"effort,omitempty"`
	Fast     *bool   `json:"fast,omitempty"`
	Thinking *bool   `json:"thinking,omitempty"`
	MaxMode  *bool   `json:"max_mode,omitempty"`
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
		if spec, ok := modelControlSpecs[rest]; ok && spec.MaxMode {
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

	selection := ModelSelection{ModelID: bare, Params: DefaultModelParamsFor(bare)}
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
		spec, ok := modelControlSpecs[rest]
		if !ok || !spec.MaxMode {
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
	return applyModelOptions(selection, modelOptionsWithEffort(standardEffort), options)
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
	return applyModelOptions(selection, modelOptionsWithEffort(standardEffort), options)
}

// ResolveModelWithStandardOptions applies protocol-standard effort, speed and
// thinking controls before the RingStar cursor_options override.
func ResolveModelWithStandardOptions(
	publicModel string,
	standardOptions *ModelOptions,
	options *ModelOptions,
) (ModelSelection, error) {
	selection := ResolveModel(publicModel)
	return applyModelOptions(selection, standardOptions, options)
}

// ResolveModelWithStandardOptionsStrict is the strict catalog variant used by
// public gateways.
func ResolveModelWithStandardOptionsStrict(
	publicModel string,
	standardOptions *ModelOptions,
	options *ModelOptions,
) (ModelSelection, error) {
	selection, err := ResolveModelStrict(publicModel)
	if err != nil {
		return ModelSelection{}, err
	}
	return applyModelOptions(selection, standardOptions, options)
}

func applyModelOptions(
	selection ModelSelection,
	standardOptions *ModelOptions,
	options *ModelOptions,
) (ModelSelection, error) {
	if !hasModelOptions(standardOptions) && !hasModelOptions(options) {
		return selection, nil
	}

	if selection.ModelID == AutoModelID {
		return ModelSelection{}, fmt.Errorf("cursor model options require a named model, not cursor/default")
	}
	if _, ok := modelControlSpecs[selection.ModelID]; !ok {
		return ModelSelection{}, fmt.Errorf("cursor model options are not supported by %s", PublicModelID(selection.ModelID))
	}

	normalizedStandard, err := normalizeModelOptions(selection.ModelID, standardOptions)
	if err != nil {
		return ModelSelection{}, err
	}
	normalizedCustom, err := normalizeModelOptions(selection.ModelID, options)
	if err != nil {
		return ModelSelection{}, err
	}
	effective := mergeModelOptions(normalizedStandard, normalizedCustom)

	selection.Params = append([]ModelParam(nil), selection.Params...)
	if effective.Effort != nil {
		selection.Params = setModelParam(selection.Params, "effort", *effective.Effort)
	}
	if effective.Fast != nil {
		selection.Params = setModelParam(selection.Params, "fast", fmt.Sprintf("%t", *effective.Fast))
	}
	if effective.Thinking != nil {
		selection.Params = setModelParam(selection.Params, "thinking", fmt.Sprintf("%t", *effective.Thinking))
	}
	if effective.MaxMode != nil {
		maxMode := *effective.MaxMode
		selection.MaxMode = &maxMode
	}
	return selection, nil
}

func modelOptionsWithEffort(effort *string) *ModelOptions {
	if effort == nil {
		return nil
	}
	return &ModelOptions{Effort: effort}
}

func hasModelOptions(options *ModelOptions) bool {
	return options != nil &&
		(options.Effort != nil || options.Fast != nil || options.Thinking != nil || options.MaxMode != nil)
}

func normalizeModelOptions(modelID string, options *ModelOptions) (*ModelOptions, error) {
	if options == nil {
		return nil, nil
	}
	spec, ok := modelControlSpecs[modelID]
	if !ok {
		return nil, fmt.Errorf("cursor model options are not supported by %s", PublicModelID(modelID))
	}
	normalized := &ModelOptions{}
	if options.Effort != nil {
		effort, err := normalizeModelEffortForModel(modelID, *options.Effort)
		if err != nil {
			return nil, err
		}
		normalized.Effort = &effort
	}
	if options.Fast != nil {
		if *options.Fast && !spec.Fast {
			return nil, fmt.Errorf("fast mode is not supported by %s", PublicModelID(modelID))
		}
		if spec.Fast {
			fast := *options.Fast
			normalized.Fast = &fast
		}
	}
	if options.Thinking != nil {
		if *options.Thinking && !spec.Thinking {
			return nil, fmt.Errorf("thinking mode is not supported by %s", PublicModelID(modelID))
		}
		if spec.Thinking {
			thinking := *options.Thinking
			normalized.Thinking = &thinking
		}
	}
	if options.MaxMode != nil {
		if *options.MaxMode && !spec.MaxMode {
			return nil, fmt.Errorf("MAX mode is not supported by %s", PublicModelID(modelID))
		}
		if spec.MaxMode {
			maxMode := *options.MaxMode
			normalized.MaxMode = &maxMode
		}
	}
	return normalized, nil
}

func mergeModelOptions(standardOptions, options *ModelOptions) ModelOptions {
	var merged ModelOptions
	for _, source := range []*ModelOptions{standardOptions, options} {
		if source == nil {
			continue
		}
		if source.Effort != nil {
			value := *source.Effort
			merged.Effort = &value
		}
		if source.Fast != nil {
			value := *source.Fast
			merged.Fast = &value
		}
		if source.Thinking != nil {
			value := *source.Thinking
			merged.Thinking = &value
		}
		if source.MaxMode != nil {
			value := *source.MaxMode
			merged.MaxMode = &value
		}
	}
	return merged
}

func normalizeModelEffortForModel(modelID, raw string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "" {
		return "", fmt.Errorf("effort must not be empty")
	}
	switch normalized {
	case "minimal":
		normalized = ModelEffortNone
	case "x-high", "extra-high", "extra_high":
		normalized = ModelEffortXHigh
	case "ultra":
		normalized = ModelEffortMax
	}

	spec, ok := modelControlSpecs[modelID]
	if !ok || len(spec.Efforts) == 0 {
		return "", fmt.Errorf("effort %q is not supported by %s", strings.TrimSpace(raw), PublicModelID(modelID))
	}
	if stringSliceContains(spec.Efforts, normalized) {
		return normalized, nil
	}
	if normalized == ModelEffortMax {
		if stringSliceContains(spec.Efforts, ModelEffortXHigh) {
			return ModelEffortXHigh, nil
		}
		if stringSliceContains(spec.Efforts, ModelEffortHigh) {
			return ModelEffortHigh, nil
		}
	}
	if normalized == ModelEffortXHigh && modelID == "grok-4.5" {
		return ModelEffortHigh, nil
	}
	return "", fmt.Errorf("effort %q is not supported by %s", strings.TrimSpace(raw), PublicModelID(modelID))
}

func stringSliceContains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func cloneModelParams(params []ModelParam) []ModelParam {
	return append([]ModelParam(nil), params...)
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
