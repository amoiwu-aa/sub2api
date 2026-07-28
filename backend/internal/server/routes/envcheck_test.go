package routes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/envcheck"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newEnvCheckRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	registerEnvCheckRoute(r)
	return r
}

func doEnvCheck(t *testing.T, target string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	newEnvCheckRouter().ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	return w
}

func TestEnvCheckRoute_TextReport(t *testing.T) {
	w := doEnvCheck(t, "/api/v1/env-check", map[string]string{
		"Accept-Language":      "en-US,en;q=0.9",
		"User-Agent":           "Mozilla/5.0 (Linux; Android 13; Redmi K60) MicroMessenger/8.0.42",
		"CF-Timezone":          "Asia/Shanghai",
		"CF-IPCountry":         "CN",
		"X-Vercel-IP-Timezone": "",
	})

	body := w.Body.String()
	require.Contains(t, body, "server-side estimate")
	require.Contains(t, body, "Asia/Shanghai")
	require.Contains(t, body, "WeChat")
	require.Contains(t, body, "Xiaomi")
	require.Contains(t, body, "CN")
	// MIT 许可要求保留署名
	require.Contains(t, body, "FuckClaude")
}

// 响应语言跟随 Accept-Language，与 FuckClaude 的 /api/check 行为一致。
func TestEnvCheckRoute_FollowsAcceptLanguage(t *testing.T) {
	zh := doEnvCheck(t, "/api/v1/env-check", map[string]string{"Accept-Language": "zh-CN,zh;q=0.9"})
	require.Contains(t, zh.Body.String(), "服务端估算")

	en := doEnvCheck(t, "/api/v1/env-check", map[string]string{"Accept-Language": "en-US"})
	require.Contains(t, en.Body.String(), "server-side estimate")
	require.NotContains(t, en.Body.String(), "服务端估算")
}

func TestEnvCheckRoute_JSONFormat(t *testing.T) {
	w := doEnvCheck(t, "/api/v1/env-check?format=json", map[string]string{
		"Accept-Language": "zh-CN",
		"CF-Timezone":     "Asia/Shanghai",
	})

	var got envcheck.Result
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Equal(t, 64, got.MeasuredWeight)
	require.Equal(t, "high", got.Level)
	require.NotEmpty(t, got.Signals)
}

// 不带任何头也必须正常返回，脚本里裸 curl 是最常见的用法。
func TestEnvCheckRoute_BareRequest(t *testing.T) {
	w := doEnvCheck(t, "/api/v1/env-check", nil)
	require.Contains(t, w.Body.String(), "no signal matched")
}

func TestFirstHeader_PrefersEarlierNames(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.Header.Set("CF-Timezone", "Asia/Tokyo")
	c.Request.Header.Set("X-Vercel-IP-Timezone", "Asia/Shanghai")

	// X-Vercel-IP-Timezone 排在列表前面，应优先
	require.Equal(t, "Asia/Shanghai", firstHeader(c, timezoneHeaders))

	// 空白值视为未设置，继续往后找
	c.Request.Header.Set("X-Vercel-IP-Timezone", "   ")
	require.Equal(t, "Asia/Tokyo", firstHeader(c, timezoneHeaders))

	require.Equal(t, "", firstHeader(c, []string{"X-Missing"}))
}

func TestRenderEnvCheckReport_ListsOnlyMatchedSignals(t *testing.T) {
	result := envcheck.Evaluate(envcheck.Input{
		AcceptLanguage: "zh-CN",
		TimeZone:       "Asia/Shanghai",
	})
	report := renderEnvCheckReport(result, false)

	require.Contains(t, report, "timezone")
	require.Contains(t, report, "language")
	// 未命中的信号不该出现在明细里，否则终端输出全是 0 分噪音
	require.NotContains(t, report, "cnBrowser")
	require.Equal(t, 1, strings.Count(report, "Score:"))
}
