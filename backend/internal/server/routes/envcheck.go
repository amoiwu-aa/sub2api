package routes

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/envcheck"
	"github.com/gin-gonic/gin"
)

// 各家 CDN 注入地理信息的头名各不相同，按常见程度依次尝试。
var (
	timezoneHeaders = []string{"X-Vercel-IP-Timezone", "CF-Timezone", "X-Client-Timezone"}
	countryHeaders  = []string{"CF-IPCountry", "X-Vercel-IP-Country", "X-Client-Country"}
)

func firstHeader(c *gin.Context, names []string) string {
	for _, name := range names {
		if v := strings.TrimSpace(c.GetHeader(name)); v != "" {
			return v
		}
	}
	return ""
}

// registerEnvCheckRoute 挂载 curl 友好的环境检测接口。
//
// 放在 /api/v1 下而不是 /env-check，是因为后者已被前端 SPA 路由占用，而 /api/
// 是嵌入式前端中间件的绕行前缀。接口不鉴权：它只回显请求方自己的请求头信息，
// 不接触任何账号数据，也不查库。
func registerEnvCheckRoute(r *gin.Engine) {
	r.GET("/api/v1/env-check", func(c *gin.Context) {
		result := envcheck.Evaluate(envcheck.Input{
			AcceptLanguage: c.GetHeader("Accept-Language"),
			UserAgent:      c.GetHeader("User-Agent"),
			TimeZone:       firstHeader(c, timezoneHeaders),
			Country:        firstHeader(c, countryHeaders),
		})

		if c.Query("format") == "json" {
			c.JSON(http.StatusOK, result)
			return
		}
		c.String(http.StatusOK, renderEnvCheckReport(result, prefersChinese(c.GetHeader("Accept-Language"))))
	})
}

func prefersChinese(acceptLanguage string) bool {
	langs := envcheck.ParseAcceptLanguage(acceptLanguage)
	return len(langs) > 0 && strings.HasPrefix(strings.ToLower(langs[0]), "zh")
}

// renderEnvCheckReport 输出纯文本报告，终端里直接可读。
func renderEnvCheckReport(result envcheck.Result, zh bool) string {
	label := map[string]string{
		"title":     "Claude env check (server-side estimate)",
		"score":     "Score",
		"level":     "Level",
		"exit":      "Exit country",
		"signals":   "Signals",
		"measured":  "Server-measurable weight",
		"caveat":    "Fonts, Intl locale and emoji rendering can only be probed in a browser; open the web page for the full scan.",
		"credit":    "Signals adapted from LinXiaoTao/FuckClaude (MIT).",
		"noSignals": "no signal matched",
	}
	if zh {
		label = map[string]string{
			"title":     "Claude 环境检测（服务端估算）",
			"score":     "得分",
			"level":     "风险等级",
			"exit":      "出口国家",
			"signals":   "信号明细",
			"measured":  "服务端可测权重",
			"caveat":    "字体、Intl 区域设置与 Emoji 渲染只能在浏览器里探测，完整扫描请打开网页版。",
			"credit":    "检测信号参考 LinXiaoTao/FuckClaude（MIT）。",
			"noSignals": "未命中任何信号",
		}
	}
	levelText := map[string]string{"low": "low", "medium": "medium", "high": "high"}
	if zh {
		levelText = map[string]string{"low": "低风险", "medium": "中风险", "high": "高风险"}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", label["title"])
	fmt.Fprintf(&b, "  %-22s %d / 100\n", label["score"]+":", result.Score)
	fmt.Fprintf(&b, "  %-22s %s\n", label["level"]+":", levelText[result.Level])
	if result.ExitCountry != "" {
		fmt.Fprintf(&b, "  %-22s %s\n", label["exit"]+":", result.ExitCountry)
	}
	fmt.Fprintf(&b, "  %-22s %d / 100\n\n", label["measured"]+":", result.MeasuredWeight)

	fmt.Fprintf(&b, "%s:\n", label["signals"])
	matched := 0
	for _, s := range result.Signals {
		if !s.Hit {
			continue
		}
		matched++
		detail := s.Detail
		if detail == "" {
			detail = "-"
		}
		fmt.Fprintf(&b, "  [+%2d/%2d] %-12s %s\n", s.Score, s.Weight, s.ID, detail)
	}
	if matched == 0 {
		fmt.Fprintf(&b, "  %s\n", label["noSignals"])
	}

	fmt.Fprintf(&b, "\n%s\n%s\n", label["caveat"], label["credit"])
	return b.String()
}
