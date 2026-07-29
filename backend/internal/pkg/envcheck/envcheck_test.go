package envcheck

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	wechatOnXiaomiUA = "Mozilla/5.0 (Linux; Android 13; Redmi K60 Build/TKQ1) AppleWebKit/537.36 " +
		"MicroMessenger/8.0.42.2460(0x28002A35)"
	macSafariUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 " +
		"(KHTML, like Gecko) Version/17.4 Safari/605.1.15"
)

func signalScore(t *testing.T, r Result, id string) int {
	t.Helper()
	for _, s := range r.Signals {
		if s.ID == id {
			return s.Score
		}
	}
	t.Fatalf("signal %s not found", id)
	return 0
}

func TestEvaluate_MainlandEnvironment(t *testing.T) {
	got := Evaluate(Input{
		AcceptLanguage: "zh-CN,zh;q=0.9,en;q=0.8",
		UserAgent:      wechatOnXiaomiUA,
		TimeZone:       "Asia/Shanghai",
		Country:        "cn",
	})

	require.Equal(t, 26, signalScore(t, got, "timezone"))
	require.Equal(t, 20, signalScore(t, got, "language"))
	require.Equal(t, 8, signalScore(t, got, "cnBrowser"))
	require.Equal(t, 6, signalScore(t, got, "cnDevice"))
	// UTC+8 也覆盖新马港台，与前端一致只给权重的四分之三
	require.Equal(t, 3, signalScore(t, got, "utcOffset"))

	// 63/64 归一化到百分制
	require.Equal(t, 98, got.Score)
	require.Equal(t, "high", got.Level)
	require.Equal(t, "CN", got.ExitCountry)
	require.Equal(t, 64, got.MeasuredWeight)
}

func TestEvaluate_CleanEnvironment(t *testing.T) {
	got := Evaluate(Input{
		AcceptLanguage: "en-US,en;q=0.9",
		UserAgent:      macSafariUA,
		TimeZone:       "America/Los_Angeles",
		Country:        "US",
	})

	require.Equal(t, 0, got.Score)
	require.Equal(t, "low", got.Level)
	for _, s := range got.Signals {
		require.False(t, s.Hit, "signal %s must not hit on a clean environment", s.ID)
	}
}

// 请求头缺失是常态：多数 CDN 不注入时区头，此时时区与偏移都应判 0 而不是崩。
func TestEvaluate_NoGeoHeaders(t *testing.T) {
	got := Evaluate(Input{AcceptLanguage: "zh-CN", UserAgent: macSafariUA})
	require.Equal(t, 0, signalScore(t, got, "timezone"))
	require.Equal(t, 0, signalScore(t, got, "utcOffset"))
	require.Equal(t, 20, signalScore(t, got, "language"))
	require.Empty(t, got.ExitCountry)
}

func TestEvaluate_GreaterChinaTimezoneHalfScore(t *testing.T) {
	hk := Evaluate(Input{TimeZone: "Asia/Hong_Kong"})
	require.Equal(t, 13, signalScore(t, hk, "timezone"))
	// 港台同属 UTC+8，弱信号照常命中
	require.Equal(t, 3, signalScore(t, hk, "utcOffset"))

	// 新加坡是 UTC+8 但不在大中华区，只应命中偏移
	sg := Evaluate(Input{TimeZone: "Asia/Singapore"})
	require.Equal(t, 0, signalScore(t, sg, "timezone"))
	require.Equal(t, 3, signalScore(t, sg, "utcOffset"))
}

func TestParseAcceptLanguage_SortsByQValue(t *testing.T) {
	require.Equal(t,
		[]string{"zh-CN", "en-US", "ja"},
		ParseAcceptLanguage("en-US;q=0.8,zh-CN,ja;q=0.5"),
	)
	// 同 q 值保持原始顺序
	require.Equal(t, []string{"en", "fr"}, ParseAcceptLanguage("en,fr"))
	require.Nil(t, ParseAcceptLanguage(""))
	require.Nil(t, ParseAcceptLanguage("*"))
}

func TestScoreLanguages_Tiers(t *testing.T) {
	full := Evaluate(Input{AcceptLanguage: "zh-CN"})
	other := Evaluate(Input{AcceptLanguage: "zh-TW"})
	secondary := Evaluate(Input{AcceptLanguage: "en-US,zh-CN;q=0.8"})
	none := Evaluate(Input{AcceptLanguage: "en-US,ja;q=0.8"})

	require.Equal(t, 20, signalScore(t, full, "language"))
	require.Equal(t, 15, signalScore(t, other, "language"))
	require.Equal(t, 10, signalScore(t, secondary, "language"))
	require.Equal(t, 0, signalScore(t, none, "language"))
}

func TestLevel_Bands(t *testing.T) {
	require.Equal(t, "low", Level(0))
	require.Equal(t, "low", Level(30))
	require.Equal(t, "medium", Level(31))
	require.Equal(t, "medium", Level(60))
	require.Equal(t, "high", Level(61))
	require.Equal(t, "high", Level(100))
}
