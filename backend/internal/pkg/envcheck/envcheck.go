// Package envcheck 用请求头估算客户端是否呈现中国大陆环境特征，供终端和脚本
// 通过 curl 调用。信号集与权重同前端 utils/claudeEnvRisk.ts 保持一致，两边分数
// 可直接对比；权重表参考 MIT 开源项目 LinXiaoTao/FuckClaude。
//
// 服务端只能看到请求头，字体、Intl locale 和 Emoji 渲染这三类必须在浏览器里
// 采集，所以可测权重只有 64/100。结果按 64 归一化到 0–100，避免因为测不全就
// 系统性偏低，同时在 Result.MeasuredWeight 里如实标出可测范围。
package envcheck

import (
	"regexp"
	"strconv"
	"strings"
)

// 与前端 ENV_SIGNAL_WEIGHTS 同源，这里只列服务端可测的那几项。
const (
	weightTimezone  = 26
	weightLanguage  = 20
	weightCnBrowser = 8
	weightCnDevice  = 6
	weightUtcOffset = 4

	// MeasurableWeight 是服务端可测信号的权重合计（满分 100 中的 64）
	MeasurableWeight = weightTimezone + weightLanguage + weightCnBrowser + weightCnDevice + weightUtcOffset

	// hitThreshold 与前端一致：命中比例低于该值不计入「命中信号」
	hitThreshold = 0.25
)

// Signal 是单条信号的评分结果，字段与前端 EnvSignal 对齐。
type Signal struct {
	ID     string  `json:"id"`
	Weight int     `json:"weight"`
	Ratio  float64 `json:"ratio"`
	Score  int     `json:"score"`
	Hit    bool    `json:"hit"`
	Detail string  `json:"detail"`
}

// Result 是一次估算的完整结果。
type Result struct {
	// Score 已按 MeasuredWeight 归一化到 0–100
	Score          int      `json:"score"`
	Level          string   `json:"level"`
	Signals        []Signal `json:"signals"`
	MeasuredWeight int      `json:"measured_weight"`
	// ExitCountry 是 CDN 报告的出口国家，仅作参考，不计入评分：
	// 它反映的是网络出口而非设备特征，与浏览器扫描不可比。
	ExitCountry string `json:"exit_country,omitempty"`
}

// Input 是评分所需的请求侧信息，由 handler 从请求头提取。
type Input struct {
	AcceptLanguage string
	UserAgent      string
	// TimeZone 来自 CDN 注入的地理头，没有则为空
	TimeZone string
	Country  string
}

var mainlandTimezones = map[string]bool{
	"Asia/Shanghai":  true,
	"Asia/Urumqi":    true,
	"Asia/Chongqing": true,
	"Asia/Harbin":    true,
	"Asia/Kashgar":   true,
	"Asia/Macau":     true,
	"PRC":            true,
}

var greaterChinaTimezones = map[string]bool{
	"Asia/Hong_Kong": true,
	"Asia/Taipei":    true,
}

// utcPlus8Timezones 用于在没有偏移信息时推断 UTC+8。服务端拿不到
// getTimezoneOffset()，只能从时区名反推。
var utcPlus8Timezones = map[string]bool{
	"Asia/Shanghai":     true,
	"Asia/Chongqing":    true,
	"Asia/Harbin":       true,
	"Asia/Macau":        true,
	"Asia/Hong_Kong":    true,
	"Asia/Taipei":       true,
	"Asia/Singapore":    true,
	"Asia/Kuala_Lumpur": true,
	"PRC":               true,
}

var cnBrowserPatterns = []struct {
	re    *regexp.Regexp
	label string
}{
	{regexp.MustCompile(`(?i)MicroMessenger`), "WeChat"},
	{regexp.MustCompile(`(?i)QQBrowser|MQQBrowser`), "QQ Browser"},
	{regexp.MustCompile(`(?i)Quark`), "Quark"},
	{regexp.MustCompile(`(?i)UCBrowser|UCWEB`), "UC Browser"},
	{regexp.MustCompile(`(?i)baidubrowser|BIDUBrowser|baiduboxapp`), "Baidu"},
	{regexp.MustCompile(`(?i)QihooBrowser|360SE|360EE`), "360"},
	{regexp.MustCompile(`(?i)SogouMobileBrowser|MetaSr`), "Sogou"},
	{regexp.MustCompile(`(?i)aweme|BytedanceWebview`), "Douyin"},
	{regexp.MustCompile(`(?i)MiuiBrowser`), "MIUI Browser"},
	{regexp.MustCompile(`(?i)HuaweiBrowser|HarmonyBrowser`), "Huawei Browser"},
	{regexp.MustCompile(`(?i)VivoBrowser`), "vivo Browser"},
	{regexp.MustCompile(`(?i)HeyTapBrowser|OppoBrowser`), "OPPO Browser"},
	{regexp.MustCompile(`(?i)LBBROWSER`), "Liebao"},
	{regexp.MustCompile(`(?i)Maxthon`), "Maxthon"},
	{regexp.MustCompile(`(?i)2345Explorer`), "2345"},
	{regexp.MustCompile(`(?i)AlipayClient`), "Alipay"},
	{regexp.MustCompile(`(?i)DingTalk`), "DingTalk"},
}

var cnDevicePatterns = []struct {
	re    *regexp.Regexp
	label string
}{
	{regexp.MustCompile(`(?i)HarmonyOS|OpenHarmony`), "HarmonyOS"},
	{regexp.MustCompile(`(?i)HUAWEI|\bHONOR\b`), "Huawei / HONOR"},
	{regexp.MustCompile(`(?i)Xiaomi|Redmi|POCO`), "Xiaomi"},
	{regexp.MustCompile(`(?i)\bOPPO\b|CPH\d{4}`), "OPPO"},
	{regexp.MustCompile(`(?i)\bvivo\b|\bV\d{4}A\b`), "vivo"},
	{regexp.MustCompile(`(?i)OnePlus`), "OnePlus"},
	{regexp.MustCompile(`(?i)Meizu`), "Meizu"},
	{regexp.MustCompile(`(?i)realme|RMX\d{4}`), "realme"},
	{regexp.MustCompile(`(?i)\bZTE\b|Nubia`), "ZTE"},
}

func matchFirst(patterns []struct {
	re    *regexp.Regexp
	label string
}, text string) string {
	if text == "" {
		return ""
	}
	for _, p := range patterns {
		if p.re.MatchString(text) {
			return p.label
		}
	}
	return ""
}

func scoreTimezone(tz string) float64 {
	switch {
	case mainlandTimezones[tz]:
		return 1
	case greaterChinaTimezones[tz]:
		return 0.5
	default:
		return 0
	}
}

// ParseAcceptLanguage 按 q 值降序取出语言标签。没有 q 值的按 RFC 7231 视为 1。
func ParseAcceptLanguage(header string) []string {
	if strings.TrimSpace(header) == "" {
		return nil
	}
	type entry struct {
		tag string
		q   float64
	}
	var entries []entry
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		tag := strings.TrimSpace(fields[0])
		if tag == "" || tag == "*" {
			continue
		}
		q := 1.0
		for _, f := range fields[1:] {
			f = strings.TrimSpace(f)
			if !strings.HasPrefix(f, "q=") {
				continue
			}
			if parsed, err := strconv.ParseFloat(f[2:], 64); err == nil {
				q = parsed
			}
		}
		entries = append(entries, entry{tag: tag, q: q})
	}
	if len(entries) == 0 {
		return nil
	}
	// 稳定插入排序：条目很少，且要保留同 q 值时的原始顺序
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && entries[j].q > entries[j-1].q; j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.tag)
	}
	return out
}

func scoreLanguages(langs []string) float64 {
	if len(langs) == 0 {
		return 0
	}
	normalized := make([]string, 0, len(langs))
	for _, l := range langs {
		normalized = append(normalized, strings.ToLower(l))
	}
	primary := normalized[0]
	switch {
	case primary == "zh-cn" || primary == "zh-hans" || primary == "zh-hans-cn":
		return 1
	case strings.HasPrefix(primary, "zh"):
		return 0.75
	}
	for _, l := range normalized {
		if strings.HasPrefix(l, "zh") {
			return 0.5
		}
	}
	return 0
}

// Level 按前端同样的分档：0–30 低，31–60 中，61–100 高。
func Level(score int) string {
	switch {
	case score <= 30:
		return "low"
	case score <= 60:
		return "medium"
	default:
		return "high"
	}
}

// Evaluate 根据请求头估算中文环境风险。
func Evaluate(in Input) Result {
	langs := ParseAcceptLanguage(in.AcceptLanguage)
	cnBrowser := matchFirst(cnBrowserPatterns, in.UserAgent)
	cnDevice := matchFirst(cnDevicePatterns, in.UserAgent)

	utcOffsetRatio := 0.0
	utcDetail := ""
	if utcPlus8Timezones[in.TimeZone] {
		// 与前端一致：UTC+8 覆盖新马港台，是弱信号，只给权重的四分之三
		utcOffsetRatio = 0.75
		utcDetail = "UTC+8"
	}

	raw := []struct {
		id     string
		weight int
		ratio  float64
		detail string
	}{
		{"timezone", weightTimezone, scoreTimezone(in.TimeZone), in.TimeZone},
		{"language", weightLanguage, scoreLanguages(langs), strings.Join(langs, ", ")},
		{"cnBrowser", weightCnBrowser, boolRatio(cnBrowser != ""), cnBrowser},
		{"cnDevice", weightCnDevice, boolRatio(cnDevice != ""), cnDevice},
		{"utcOffset", weightUtcOffset, utcOffsetRatio, utcDetail},
	}

	signals := make([]Signal, 0, len(raw))
	total := 0
	for _, item := range raw {
		score := int(float64(item.weight)*item.ratio + 0.5)
		total += score
		signals = append(signals, Signal{
			ID:     item.id,
			Weight: item.weight,
			Ratio:  item.ratio,
			Score:  score,
			Hit:    item.ratio >= hitThreshold,
			Detail: item.detail,
		})
	}

	// 归一化到 100 分制，否则服务端满分只有 64，跟浏览器扫描没法比
	normalized := int(float64(total)/float64(MeasurableWeight)*100 + 0.5)
	if normalized > 100 {
		normalized = 100
	}

	return Result{
		Score:          normalized,
		Level:          Level(normalized),
		Signals:        signals,
		MeasuredWeight: MeasurableWeight,
		ExitCountry:    strings.ToUpper(in.Country),
	}
}

func boolRatio(hit bool) float64 {
	if hit {
		return 1
	}
	return 0
}
