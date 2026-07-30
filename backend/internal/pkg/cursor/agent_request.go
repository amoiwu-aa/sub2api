package cursor

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
)

// AgentService/Run 的请求编码。
//
// 字段号全部来自反代 agent-client.js 的 encodeRunRequest 与
// cursor-agent-env.js 的 buildRequestContextEnv / encodeRequestedModel。
// 上游没有公开 schema，改动这里之前先跑 agent_request_test.go 的对拍。

// ModelParam 是 RequestedModel 的一个键值参数（effort / fast / thinking …）。
type ModelParam struct {
	ID    string
	Value string
}

// DefaultModelParams 是反代对具名模型使用的默认参数。
func DefaultModelParams() []ModelParam {
	return []ModelParam{{ID: "effort", Value: "high"}, {ID: "fast", Value: "true"}}
}

// encodeModelParam 编码 RequestedModel.params（field 3）。
func encodeModelParam(param ModelParam) []byte {
	return EncodeBytesField(3, concat(
		EncodeStringField(1, param.ID),
		EncodeStringField(2, param.Value),
	))
}

// RequestedModel 对应 agent.v1.RequestedModel。
type RequestedModel struct {
	ModelID string
	Params  []ModelParam
	// MaxMode 为 nil 时不写 field 2；上游区分「没说」与「显式 false」。
	MaxMode *bool
}

// EncodeRequestedModel 编码 RequestedModel 的消息体（不含外层 tag）。
func EncodeRequestedModel(model RequestedModel) []byte {
	parts := [][]byte{EncodeStringField(1, model.ModelID)}
	if model.MaxMode != nil {
		parts = append(parts, EncodeBoolField(2, *model.MaxMode))
	}
	for _, param := range model.Params {
		parts = append(parts, encodeModelParam(param))
	}
	return concat(parts...)
}

// officialSelectedSubagentModels 对应反代抓到的官方 selected_subagent_models。
// 升级 Cursor 版本时要跟着 IDE 的模型目录一起更新。
func officialSelectedSubagentModels() []RequestedModel {
	return []RequestedModel{
		{ModelID: "default"},
		{ModelID: "grok-4.5", Params: DefaultModelParams()},
		{ModelID: "composer-2.5", Params: []ModelParam{{ID: "fast", Value: "true"}}},
		{ModelID: "claude-opus-4-8", Params: []ModelParam{
			{ID: "thinking", Value: "true"},
			{ID: "context", Value: "300k"},
			{ID: "effort", Value: "high"},
			{ID: "fast", Value: "false"},
		}},
	}
}

// encodeSelectedSubagentModels 编码 RunRequest.selected_subagent_models（field 14，repeated）。
func encodeSelectedSubagentModels(models []RequestedModel) []byte {
	parts := make([][]byte, 0, len(models))
	for _, model := range models {
		parts = append(parts, EncodeBytesField(14, EncodeRequestedModel(model)))
	}
	return concat(parts...)
}

// RequestContextEnv 是 RequestContext.env 里我们会填的字段。
//
// 服务端没有真实的编辑器环境，这些值是构造出来的：目标是让请求看起来像一个
// 打开了空窗口的 Cursor 客户端，而不是让上游拿到本机的真实路径。
type RequestContextEnv struct {
	OSVersion      string
	WorkspacePaths []string
	Shell          string
	TerminalsPath  string
	Timezone       string
	ProjectFolder  string
	TranscriptPath string
}

// DefaultRequestContextEnv 构造一份「空窗口」环境。
//
// conversationID 只用于派生 notes 目录名，这里不发送 notes 路径（最新的官方
// 抓包也省略了它们），保留参数是为了与反代的调用形态一致。
func DefaultRequestContextEnv(projectName string) RequestContextEnv {
	if strings.TrimSpace(projectName) == "" {
		projectName = "empty-window"
	}
	// 用固定的、看起来像 Cursor 默认布局的路径，而不是服务器的真实目录。
	home := "/home/cursor"
	projectFolder := home + "/.cursor/projects/" + projectName
	return RequestContextEnv{
		OSVersion:      "linux 6.8.0",
		WorkspacePaths: []string{home},
		Shell:          "bash",
		TerminalsPath:  projectFolder + "/terminals",
		Timezone:       "UTC",
		ProjectFolder:  projectFolder,
		TranscriptPath: projectFolder + "/agent-transcripts",
	}
}

// EncodeRequestContextEnv 编码 RequestContext.env。
//
// 字段的出现与否本身是信号：官方在 sandbox_enabled 为 false 时直接省略 field 5，
// 补一个 0 反而与真实客户端不一致。
func EncodeRequestContextEnv(env RequestContextEnv) []byte {
	parts := [][]byte{EncodeStringField(1, env.OSVersion)}
	for _, path := range env.WorkspacePaths {
		parts = append(parts, EncodeStringField(2, path))
	}
	parts = append(parts,
		EncodeStringField(3, env.Shell),
		EncodeStringField(7, env.TerminalsPath),
		EncodeStringField(10, env.Timezone),
		EncodeStringField(11, env.ProjectFolder),
		EncodeStringField(12, env.TranscriptPath),
		EncodeBoolField(14, false), // sandbox_supported
		EncodeBoolField(16, true),  // sandbox_network_has_defaults
		EncodeBoolField(19, false), // computer_use_supported
		EncodeBoolField(20, false), // is_working_dir_home_dir
		EncodeBoolField(22, false), // smart_mode_classifier_auto_mode_enabled
	)
	return concat(parts...)
}

// EncodeRequestContext 编码 env-only 模式的 RequestContext。
//
// 反代还有一个 full 模式，会把本机抓到的 rules/tools/mcp blob 拼进来。
// 服务端没有那份抓包，也不该依赖它，所以只实现 env-only。
func EncodeRequestContext(env RequestContextEnv) []byte {
	return concat(
		EncodeBytesField(4, EncodeRequestContextEnv(env)),
		EncodeBoolField(17, true),        // web_search_enabled
		EncodeBoolField(24, true),        // web_fetch_enabled
		EncodeStringField(26, "enabled"), // commit attribution
		EncodeStringField(27, "enabled"), // pr attribution
		EncodeBytesField(28, nil),        // empty blob the official client always sends
		EncodeBoolField(32, true),        // supports_mcp_auth
		EncodeBoolField(33, true),        // git_repo_info_complete
		EncodeBoolField(35, false),       // read_lints_enabled
		EncodeBoolField(36, true),        // mcp_info_complete
		EncodeBoolField(39, true),        // rules_info_complete
		EncodeBoolField(40, true),        // env_info_complete
		EncodeBoolField(41, true),        // repository_info_complete
		EncodeBoolField(42, true),        // custom_subagents_info_complete
		EncodeBoolField(43, false),       // agent_skills_info_complete
		EncodeBoolField(44, true),        // mcp_file_system_info_complete
		EncodeBoolField(45, false),       // git_status_info_complete
		EncodeBoolField(50, false),       // search_conversations_enabled
	)
}

// RunRequestInput 是构造一次 Run 请求需要的全部输入。
type RunRequestInput struct {
	Text           string
	ConversationID string
	// ConversationState 是上一轮 checkpoint 返回的字节，首轮为空。
	ConversationState []byte
	RequestContext    []byte
	ModelID           string
	ModelParams       []ModelParam
	MaxMode           *bool
	// Tools 是要声明给模型的客户端工具，走 MCP 通道。
	// 为空时 field 4 编成与纯文本请求完全一致的占位字节。
	Tools []McpTool
	// MessageID 留空时自动生成；测试里可固定以便对拍。
	MessageID string
}

// TipTap 富文本：UserMessage.field 8。官方客户端把同一段文本同时以纯文本
// （field 1）和 TipTap JSON 两种形式发送。
//
// 用结构体而不是 map，是因为 Go 序列化 map 会按键名排序，而官方客户端是 JS
// 对象字面量、按书写顺序输出。字节不一致就不是同一个客户端指纹了。
type tiptapText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type tiptapParagraph struct {
	Type    string       `json:"type"`
	Content []tiptapText `json:"content"`
}

type tiptapDocument struct {
	Type    string            `json:"type"`
	Content []tiptapParagraph `json:"content"`
}

func tiptapDoc(text string) (string, error) {
	doc := tiptapDocument{
		Type: "doc",
		Content: []tiptapParagraph{{
			Type:    "paragraph",
			Content: []tiptapText{{Type: "text", Text: text}},
		}},
	}

	// 必须关掉 HTML 转义：Go 默认把 < > & 写成 \u003c 之类，JSON.stringify 不会。
	// 用户消息里出现这些字符（写代码时几乎必然）会让两边的字节分叉。
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(doc); err != nil {
		return "", err
	}
	// Encoder.Encode 会附一个换行，官方载荷里没有。
	return strings.TrimRight(buf.String(), "\n"), nil
}

// EncodeRunRequest 构造 AgentClientMessage，其 field 1 是 RunRequest。
func EncodeRunRequest(input RunRequestInput) ([]byte, error) {
	messageID := input.MessageID
	if messageID == "" {
		messageID = uuid.NewString()
	}
	rich, err := tiptapDoc(input.Text)
	if err != nil {
		return nil, err
	}

	userMessage := concat(
		EncodeStringField(1, input.Text),
		EncodeStringField(2, messageID),
		EncodeBytesField(3, nil),
		EncodeVarintField(4, 1), // role = user
		EncodeStringField(8, rich),
	)
	userMessageAction := concat(
		EncodeBytesField(1, userMessage),
		EncodeBytesField(2, input.RequestContext),
	)

	modelID := strings.TrimSpace(input.ModelID)
	if modelID == "" {
		modelID = AutoModelID
	}
	params := input.ModelParams
	if params == nil {
		params = DefaultModelParams()
	}
	requestedModel := EncodeRequestedModel(RequestedModel{
		ModelID: modelID,
		Params:  params,
		MaxMode: input.MaxMode,
	})

	runRequest := concat(
		EncodeBytesField(1, input.ConversationState),
		EncodeBytesField(2, EncodeBytesField(1, userMessageAction)),
		// field 4 是 McpTools。空列表编成零字节，与不带工具时的占位符
		// EncodeBytesField(4, nil) 逐字节相同，所以纯对话请求的指纹不变。
		EncodeBytesField(mcpToolsField, EncodeMcpTools(input.Tools)),
		EncodeStringField(5, input.ConversationID),
		EncodeBytesField(9, requestedModel),
		EncodeBoolField(10, false),
		encodeSelectedSubagentModels(officialSelectedSubagentModels()),
		EncodeBoolField(19, true),
		EncodeBoolField(21, false),
		EncodeBoolField(22, false),
		EncodeBoolField(23, true),
	)
	return EncodeBytesField(1, runRequest), nil
}

// EncodeHeartbeat 是客户端心跳：AgentClientMessage 的空 field 7。
// 反代每 10 秒发一次，长时间没有心跳上游会掐掉这条流。
func EncodeHeartbeat() []byte {
	return EncodeBytesField(7, nil)
}
