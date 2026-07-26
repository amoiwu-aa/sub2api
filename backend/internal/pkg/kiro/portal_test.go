//go:build unit

package kiro

import (
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

// code_challenge 必须是对 verifier **字符串**做 sha256，而不是对生成它的随机字节做。
// 扩展里是 createHash("sha256").update(codeVerifier)，此时 codeVerifier 已经是
// base64url 文本。算错的话 portal 会直接判 invalid_grant，且错误信息毫无指向性。
func TestNewPKCEChallengeIsHashOfVerifierString(t *testing.T) {
	pkce, err := NewPKCE()
	require.NoError(t, err)
	require.NotEmpty(t, pkce.State)
	require.NotEmpty(t, pkce.Verifier)

	sum := sha256.Sum256([]byte(pkce.Verifier))
	require.Equal(t, base64.RawURLEncoding.EncodeToString(sum[:]), pkce.Challenge)

	// base64url 不能含 + / =，否则放进 query 会被转义、与 portal 侧对不上。
	require.NotContains(t, pkce.Challenge, "+")
	require.NotContains(t, pkce.Challenge, "/")
	require.NotContains(t, pkce.Challenge, "=")

	// 两次生成必须不同，否则并发登录会互相顶掉。
	other, err := NewPKCE()
	require.NoError(t, err)
	require.NotEqual(t, pkce.Verifier, other.Verifier)
	require.NotEqual(t, pkce.State, other.State)
}

// 进 portal 时 redirect_uri 只有 origin，没有路径；换 token 时必须带路径和
// login_option。两处形状不同是上游的真实行为，写反了会被判 redirect_uri 不匹配。
func TestRedirectURIShapesDifferBetweenPortalAndExchange(t *testing.T) {
	pkce := &PKCE{State: "st", Verifier: "vf", Challenge: "ch"}

	portal, err := url.Parse(BuildPortalURL(pkce, DefaultCallbackPort))
	require.NoError(t, err)
	require.Equal(t, "app.kiro.dev", portal.Host)
	require.Equal(t, "/signin", portal.Path)

	q := portal.Query()
	require.Equal(t, "st", q.Get("state"))
	require.Equal(t, "ch", q.Get("code_challenge"))
	require.Equal(t, "S256", q.Get("code_challenge_method"))
	require.Equal(t, "KiroIDE", q.Get("redirect_from"))
	// 裸 origin：无路径、无尾斜杠。
	require.Equal(t, "http://localhost:3128", q.Get("redirect_uri"))

	// 换 token：带路径与 login_option。
	require.Equal(t,
		"http://localhost:3128/oauth/callback?login_option=google",
		ExchangeRedirectURI(DefaultCallbackPort, "google"))
	// 没有 login_option 时不能留一个空的 query，否则字符串对不上。
	require.Equal(t,
		"http://localhost:3128/oauth/callback",
		ExchangeRedirectURI(DefaultCallbackPort, ""))
}

func TestParseCallback(t *testing.T) {
	t.Run("完整回调 URL", func(t *testing.T) {
		got, err := ParseCallback(
			"http://localhost:3128/oauth/callback?login_option=google&code=abc123&state=xyz")
		require.NoError(t, err)
		require.Equal(t, "abc123", got.Code)
		require.Equal(t, "xyz", got.State)
		require.Equal(t, "google", got.LoginOption)
		require.Equal(t, 3128, got.Port)
	})

	// 管理员本机若有 Kiro IDE 占着 3128，回调会落到列表里靠后的端口；
	// 换 token 的 redirect_uri 必须跟着走，否则不匹配。
	t.Run("非默认端口要被识别出来", func(t *testing.T) {
		got, err := ParseCallback(
			"http://localhost:49153/oauth/callback?login_option=github&code=c1&state=s1")
		require.NoError(t, err)
		require.Equal(t, 49153, got.Port)
		require.Equal(t, "github", got.LoginOption)
	})

	t.Run("裸 code 也接受", func(t *testing.T) {
		got, err := ParseCallback("  just-a-code  ")
		require.NoError(t, err)
		require.Equal(t, "just-a-code", got.Code)
		require.Equal(t, DefaultCallbackPort, got.Port)
	})

	t.Run("只有 query 片段", func(t *testing.T) {
		got, err := ParseCallback("?code=q1&state=q2&login_option=google")
		require.NoError(t, err)
		require.Equal(t, "q1", got.Code)
		require.Equal(t, "q2", got.State)
	})

	// portal 出错时把它的原话透出去，比笼统的「导入失败」有用得多。
	t.Run("portal 报错要透传", func(t *testing.T) {
		_, err := ParseCallback(
			"http://localhost:3128/signin?auth_status=error&error_message=Invalid+scope+provided")
		require.Error(t, err)
		require.Contains(t, err.Error(), "Invalid scope provided")
	})

	t.Run("缺 code 要报错", func(t *testing.T) {
		_, err := ParseCallback("http://localhost:3128/oauth/callback?state=only")
		require.Error(t, err)
		require.Contains(t, err.Error(), "authorization code")
	})

	t.Run("空输入要报错", func(t *testing.T) {
		_, err := ParseCallback("   ")
		require.Error(t, err)
	})
}

func TestPortalUserAgentFallsBackToDefaults(t *testing.T) {
	require.Equal(t, "KiroIDE-1.0.212-"+DefaultMachineID, PortalUserAgent("", ""))
	require.Equal(t, "KiroIDE-9.9.9-machine-1", PortalUserAgent("9.9.9", "machine-1"))
}
