package routes

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCursorIDERoutesAreRegistered(t *testing.T) {
	router := newGatewayRoutesTestRouter(service.PlatformCursor)

	registered := make(map[string]string, len(router.Routes()))
	for _, route := range router.Routes() {
		registered[route.Method+" "+route.Path] = route.Handler
	}

	require.Contains(t, registered, "POST /cursor-ide/v1/chat/completions")
	require.Contains(t, registered, "GET /cursor-ide/v1/models")
}

// runStripClientTools 把中间件单独挂起来跑一次，回放它交给下游的请求体。
// 直接打完整网关会连上真实 handler，这里只关心中间件改了什么。
func runStripClientTools(t *testing.T, body string) string {
	t.Helper()
	gin.SetMode(gin.TestMode)

	router := gin.New()
	var forwarded string
	router.POST("/probe", stripClientTools(), func(c *gin.Context) {
		raw, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		forwarded = string(raw)
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/probe", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	return forwarded
}

func TestStripClientToolsRemovesToolDeclarations(t *testing.T) {
	forwarded := runStripClientTools(t, `{
		"model": "cursor/default",
		"messages": [{"role": "user", "content": "hi"}],
		"tools": [{"type": "function", "function": {"name": "Bash"}}],
		"tool_choice": "auto",
		"parallel_tool_calls": true,
		"functions": [{"name": "legacy"}],
		"function_call": "auto"
	}`)

	for _, field := range []string{"tools", "tool_choice", "parallel_tool_calls", "functions", "function_call"} {
		require.False(t, gjson.Get(forwarded, field).Exists(), "field %q 应该被摘掉", field)
	}
}

func TestStripClientToolsKeepsEverythingElse(t *testing.T) {
	// 摘工具不能顺手改坏别的字段：模型名、消息、流式开关、采样参数都要原样透传，
	// 否则 IDE 侧会拿到一个和它发出去的请求语义不同的结果。
	forwarded := runStripClientTools(t, `{
		"model": "cursor/grok-4.5",
		"messages": [{"role": "user", "content": "hi"}],
		"stream": true,
		"temperature": 0.2,
		"tools": [{"type": "function", "function": {"name": "Bash"}}]
	}`)

	require.Equal(t, "cursor/grok-4.5", gjson.Get(forwarded, "model").String())
	require.True(t, gjson.Get(forwarded, "stream").Bool())
	require.InDelta(t, 0.2, gjson.Get(forwarded, "temperature").Float(), 1e-9)
	require.Equal(t, "hi", gjson.Get(forwarded, "messages.0.content").String())
	require.False(t, gjson.Get(forwarded, "tools").Exists())
}

func TestStripClientToolsLeavesToolFreeBodyByteIdentical(t *testing.T) {
	// 没有工具声明时不该重写请求体：多一次序列化就多一次字段顺序或数字精度
	// 被改写的机会，而下游对原始字节是敏感的。
	const body = `{"model":"cursor/default","messages":[{"role":"user","content":"hi"}]}`
	require.Equal(t, body, runStripClientTools(t, body))
}

func TestStripClientToolsPassesThroughInvalidJSON(t *testing.T) {
	// 非法 JSON 交给下游去报错：中间件在这里拦一道只会把错误信息换成
	// 一条与协议无关的话，客户端更难定位。
	const body = `not json at all`
	require.Equal(t, body, runStripClientTools(t, body))
}
