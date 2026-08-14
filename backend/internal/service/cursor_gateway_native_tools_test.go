//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func nativeBridgeClientTools() []cursor.McpTool {
	return []cursor.McpTool{
		{Name: "Read", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "Grep", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "WebSearch", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}
}

func TestResolveCursorNativeToolBridgeSplitsMcpRegistration(t *testing.T) {
	body := []byte(`{"cursor_options":{"native_tools":{"read":"Read","grep":"Grep"}}}`)

	bridge, mcpTools, err := resolveCursorNativeToolBridge(body, nativeBridgeClientTools(), CursorNativeToolBridgeModeShadow)
	require.NoError(t, err)
	require.Equal(t, cursor.NativeToolBridge{
		"read": {Name: "Read"},
		"grep": {Name: "Grep"},
	}, bridge)
	// 被桥接的工具必须从 MCP 注册中移除，未桥接的保留。
	require.Len(t, mcpTools, 1)
	require.Equal(t, "WebSearch", mcpTools[0].Name)
}

// cursor_options 是同一个扩展点:模型参数与工具桥配置会同时出现,
// 两个解析器互不吞字段。
func TestCursorOptionsCombinesModelParamsAndNativeTools(t *testing.T) {
	body := []byte(`{
		"model": "cursor/grok-4.6",
		"cursor_options": {
			"effort": "xhigh",
			"fast": false,
			"max_mode": true,
			"native_tools": {"read": "Read"}
		}
	}`)

	selection, err := resolveCursorModelSelection(body, "cursor/grok-4.6", nil)
	require.NoError(t, err)
	require.Equal(t, "grok-4.6", selection.ModelID)
	require.Equal(t, cursor.ModelEffortXHigh, cursorModelParamValue(selection.Params, "effort"))
	require.Equal(t, "false", cursorModelParamValue(selection.Params, "fast"))
	require.NotNil(t, selection.MaxMode)
	require.True(t, *selection.MaxMode)

	bridge, mcpTools, err := resolveCursorNativeToolBridge(body, nativeBridgeClientTools(), CursorNativeToolBridgeModeShadow)
	require.NoError(t, err)
	require.Equal(t, cursor.NativeToolBridge{"read": {Name: "Read"}}, bridge)
	require.Len(t, mcpTools, 2)
}

// 没有可校验的 schema 时不推断：夹具里的工具只声明了 {"type":"object"}，
// 属性表为空，绑定无从谈起，只能整份回落 MCP。
func TestResolveCursorNativeToolBridgeAbsentKeepsToolsIntact(t *testing.T) {
	tools := nativeBridgeClientTools()
	bridge, mcpTools, err := resolveCursorNativeToolBridge(
		[]byte(`{"model":"cursor/grok-4.6"}`), tools, CursorNativeToolBridgeModeShadow)
	require.NoError(t, err)
	require.Nil(t, bridge)
	require.Equal(t, tools, mcpTools)
}

func TestResolveCursorNativeToolBridgeNormalizesKeyCase(t *testing.T) {
	body := []byte(`{"cursor_options":{"native_tools":{" Read ":"Read"}}}`)
	bridge, _, err := resolveCursorNativeToolBridge(body, nativeBridgeClientTools(), CursorNativeToolBridgeModeShadow)
	require.NoError(t, err)
	require.Equal(t, cursor.NativeToolBridge{"read": {Name: "Read"}}, bridge)
}

func TestResolveCursorNativeToolBridgeRejectsInvalidMappings(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "computer_use 不在白名单",
			body: `{"cursor_options":{"native_tools":{"computer_use":"Computer"}}}`,
			want: `unsupported built-in tool "computer_use"`,
		},
		{
			name: "shell 合法但映射目标未声明",
			body: `{"cursor_options":{"native_tools":{"shell":"Bash"}}}`,
			want: `not declared in tools`,
		},
		{
			name: "空客户端工具名",
			body: `{"cursor_options":{"native_tools":{"read":"  "}}}`,
			want: `must name a client tool`,
		},
		{
			name: "客户端未声明该工具",
			body: `{"cursor_options":{"native_tools":{"ls":"LS"}}}`,
			want: `not declared in tools`,
		},
		{
			name: "大小写归一后键重复",
			body: `{"cursor_options":{"native_tools":{"read":"Read","READ":"Grep"}}}`,
			want: `duplicate mapping for "read"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := resolveCursorNativeToolBridge(
				[]byte(tt.body), nativeBridgeClientTools(), CursorNativeToolBridgeModeShadow)
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestCursorGatewayRejectsInvalidNativeToolsAcrossProtocols(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &CursorGatewayService{}
	account := &Account{ID: 46, Platform: PlatformCursor}

	toolsJSON := `[{"type":"function","function":{"name":"Read","parameters":{"type":"object"}}}]`
	anthropicToolsJSON := `[{"name":"Read","input_schema":{"type":"object"}}]`

	tests := []struct {
		name string
		body string
		call func(context.Context, *gin.Context, *Account, []byte) (*ForwardResult, error)
	}{
		{
			name: "chat_completions",
			body: `{"model":"cursor/grok-4.6","messages":[{"role":"user","content":"hi"}],` +
				`"tools":` + toolsJSON + `,` +
				`"cursor_options":{"native_tools":{"computer_use":"Read"}}}`,
			call: svc.forwardChatCompletionsOnce,
		},
		{
			name: "responses",
			body: `{"model":"cursor/grok-4.6","input":"hi",` +
				`"tools":[{"type":"function","name":"Read","parameters":{"type":"object"}}],` +
				`"cursor_options":{"native_tools":{"read":"Undeclared"}}}`,
			call: svc.forwardResponsesOnce,
		},
		{
			name: "anthropic_messages",
			body: `{"model":"cursor/grok-4.6","max_tokens":16,"messages":[{"role":"user","content":"hi"}],` +
				`"tools":` + anthropicToolsJSON + `,` +
				`"cursor_options":{"native_tools":{"read":""}}}`,
			call: svc.forwardMessagesOnce,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

			result, err := tt.call(context.Background(), c, account, []byte(tt.body))

			require.Nil(t, result)
			require.Error(t, err)
			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Contains(t, rec.Body.String(), "native_tools")
		})
	}
}
