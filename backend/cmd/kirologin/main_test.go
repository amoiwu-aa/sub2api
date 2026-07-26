package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestCodeChallengeMatchesRFC7636 用 RFC 7636 附录 B 的官方向量钉住 PKCE 推导。
// 扩展里是 createHash("sha256").update(codeVerifier)，对的是 verifier 字符串本身，
// 不是生成它的那串随机字节——这里一旦搞错，portal 会在回调阶段就拒掉。
func TestCodeChallengeMatchesRFC7636(t *testing.T) {
	const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	const want = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"

	sum := sha256.Sum256([]byte(verifier))
	got := base64.RawURLEncoding.EncodeToString(sum[:])

	if got != want {
		t.Fatalf("code_challenge = %q, want %q", got, want)
	}
}

// TestRandomBase64URLIsUnpadded 确认 verifier 不带 padding，与 Node 的 base64url 一致。
func TestRandomBase64URLIsUnpadded(t *testing.T) {
	v, err := randomBase64URL(32)
	if err != nil {
		t.Fatalf("randomBase64URL: %v", err)
	}
	if strings.ContainsAny(v, "=+/") {
		t.Fatalf("verifier 含 padding 或非 URL 安全字符: %q", v)
	}
	if len(v) != 43 {
		t.Fatalf("32 字节 base64url 应为 43 字符，实际 %d: %q", len(v), v)
	}
}

// TestIdCAuthorizeURL 钉住 IdC 的 authorize 参数。
// scopes 是逗号分隔——这点与 OAuth 常见的空格分隔不同，写错会被上游拒。
func TestIdCAuthorizeURL(t *testing.T) {
	raw := idcAuthorizeURL("https://oidc.us-east-1.amazonaws.com", "cid", "http://127.0.0.1:3128/oauth/callback", "st", "ch")

	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("解析 authorize URL: %v", err)
	}
	if u.Host != "oidc.us-east-1.amazonaws.com" || u.Path != "/authorize" {
		t.Fatalf("端点不对: %s", raw)
	}

	q := u.Query()
	for key, want := range map[string]string{
		"response_type":         "code",
		"client_id":             "cid",
		"redirect_uri":          "http://127.0.0.1:3128/oauth/callback",
		"state":                 "st",
		"code_challenge":        "ch",
		"code_challenge_method": "S256",
		"scopes":                "completions,analysis,conversations,transformations,taskassist",
	} {
		if got := q.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestIdCOIDCBase(t *testing.T) {
	base, err := idcOIDCBase("us-east-1")
	if err != nil {
		t.Fatalf("idcOIDCBase: %v", err)
	}
	if base != "https://oidc.us-east-1.amazonaws.com" {
		t.Fatalf("base = %q", base)
	}
	// region 来自外部输入，直接拼进 URL 有 SSRF 风险，必须挡住。
	for _, bad := range []string{"", "evil.com", "us_east_1", "../x"} {
		if _, err := idcOIDCBase(bad); err == nil {
			t.Errorf("region %q 应被拒绝", bad)
		}
	}
}

func TestIsIdCLoginOption(t *testing.T) {
	for _, opt := range []string{"builderid", "awsidc", "internal"} {
		if !isIdCLoginOption(opt) {
			t.Errorf("%q 应判为 IdC", opt)
		}
	}
	for _, opt := range []string{"google", "github", "external_idp", ""} {
		if isIdCLoginOption(opt) {
			t.Errorf("%q 不该判为 IdC", opt)
		}
	}
}

func TestIdCProviderFromLoginOption(t *testing.T) {
	for opt, want := range map[string]string{
		"builderid": "BuilderId",
		"internal":  "Internal",
		"awsidc":    "Enterprise",
	} {
		if got := idcProviderFromLoginOption(opt); got != want {
			t.Errorf("%q -> %q, want %q", opt, got, want)
		}
	}
}

func TestBuildPortalURL(t *testing.T) {
	raw := buildPortalURL("https://app.kiro.dev", "state-1", "challenge-1", "http://localhost:3128")

	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("解析 portal URL: %v", err)
	}
	if u.Path != "/signin" {
		t.Fatalf("path = %q, want /signin", u.Path)
	}

	q := u.Query()
	for key, want := range map[string]string{
		"state":                 "state-1",
		"code_challenge":        "challenge-1",
		"code_challenge_method": "S256",
		// 进 portal 时只带 origin，不带回调路径。
		"redirect_uri":  "http://localhost:3128",
		"redirect_from": "KiroIDE",
	} {
		if got := q.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

// TestBuildPortalURLTrimsTrailingSlash 对齐扩展里的 replace(/\/$/, "")。
func TestBuildPortalURLTrimsTrailingSlash(t *testing.T) {
	raw := buildPortalURL("https://app.kiro.dev/", "s", "c", "http://localhost:3128")
	if !strings.HasPrefix(raw, "https://app.kiro.dev/signin?") {
		t.Fatalf("base 末尾斜杠未归一: %q", raw)
	}
}

func TestSocialProvider(t *testing.T) {
	tests := []struct {
		loginOption string
		want        string
		wantErr     bool
	}{
		{loginOption: "google", want: "Google"},
		{loginOption: "github", want: "Github"},
		{loginOption: "builderid", wantErr: true},
		{loginOption: "awsidc", wantErr: true},
		{loginOption: "internal", wantErr: true},
		{loginOption: "external_idp", wantErr: true},
		{loginOption: "", wantErr: true},
	}
	for _, tc := range tests {
		got, err := socialProvider(callbackData{LoginOption: tc.loginOption})
		if tc.wantErr {
			if err == nil {
				t.Errorf("login_option=%q 应报错，实际返回 %q", tc.loginOption, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("login_option=%q: %v", tc.loginOption, err)
			continue
		}
		if got != tc.want {
			t.Errorf("login_option=%q -> %q, want %q", tc.loginOption, got, tc.want)
		}
	}
}

// TestExchangeTokenRequestShape 钉住换 token 的请求体与请求头。
// 请求体是 snake_case、响应体是 camelCase，两边不对称是上游的真实形状。
func TestExchangeTokenRequestShape(t *testing.T) {
	var gotBody map[string]string
	var gotUA, gotCT, gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotUA = r.Header.Get("User-Agent")
		gotCT = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"accessToken":"at","refreshToken":"rt","profileArn":"arn:aws:codewhisperer:us-east-1:1:profile/X","expiresIn":3600}`))
	}))
	defer srv.Close()

	tok, err := exchangeToken(srv.Client(), srv.URL, "KiroIDE-1.0.212-mid", exchangeParams{
		code:         "the-code",
		codeVerifier: "the-verifier",
		redirectURI:  "http://localhost:3128/oauth/callback?login_option=google",
	})
	if err != nil {
		t.Fatalf("exchangeToken: %v", err)
	}

	if gotPath != "/oauth/token" {
		t.Errorf("path = %q, want /oauth/token", gotPath)
	}
	if gotUA != "KiroIDE-1.0.212-mid" {
		t.Errorf("User-Agent = %q", gotUA)
	}
	if gotCT != "application/json" {
		t.Errorf("Content-Type = %q", gotCT)
	}
	if gotBody["code"] != "the-code" || gotBody["code_verifier"] != "the-verifier" {
		t.Errorf("请求体字段不对: %#v", gotBody)
	}
	// 换 token 时的 redirect_uri 必须带回调路径与 login_option。
	if gotBody["redirect_uri"] != "http://localhost:3128/oauth/callback?login_option=google" {
		t.Errorf("redirect_uri = %q", gotBody["redirect_uri"])
	}
	// 没传邀请码时不应该出现这个键。
	if _, ok := gotBody["invitation_code"]; ok {
		t.Errorf("未指定邀请码却发了 invitation_code")
	}
	if tok.ProfileARN == "" || tok.AccessToken != "at" {
		t.Errorf("响应解析不对: %#v", tok)
	}
}

// TestExchangeTokenRejectsMissingProfileARN 覆盖「响应缺 profileArn」。
// profileArn 是 Q API 每个请求都要带的字段，缺了这张凭证等于废的。
func TestExchangeTokenRejectsMissingProfileARN(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"accessToken":"at","refreshToken":"rt","expiresIn":3600}`))
	}))
	defer srv.Close()

	_, err := exchangeToken(srv.Client(), srv.URL, "ua", exchangeParams{code: "c", codeVerifier: "v"})
	if err == nil || !strings.Contains(err.Error(), "profileArn") {
		t.Fatalf("应因缺 profileArn 报错，实际: %v", err)
	}
}

func TestExchangeTokenRejectsMissingRefreshToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"accessToken":"at","profileArn":"arn:x","expiresIn":3600}`))
	}))
	defer srv.Close()

	_, err := exchangeToken(srv.Client(), srv.URL, "ua", exchangeParams{code: "c", codeVerifier: "v"})
	if err == nil || !strings.Contains(err.Error(), "refreshToken") {
		t.Fatalf("应因缺 refreshToken 报错，实际: %v", err)
	}
}

// flakyTransport 前 failures 次返回传输层错误，之后交给真实 transport。
type flakyTransport struct {
	failures int
	attempts int
	inner    http.RoundTripper
}

func (f *flakyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.attempts++
	if f.attempts <= f.failures {
		return nil, errors.New("simulated network failure")
	}
	return f.inner.RoundTrip(r)
}

// TestExchangeTokenRetriesOnNetworkError 确认传输层失败会重试。
func TestExchangeTokenRetriesOnNetworkError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"accessToken":"at","refreshToken":"rt","profileArn":"arn:x","expiresIn":3600}`))
	}))
	defer srv.Close()

	ft := &flakyTransport{failures: 1, inner: srv.Client().Transport}
	client := &http.Client{Transport: ft}

	if _, err := exchangeToken(client, srv.URL, "ua", exchangeParams{code: "c", codeVerifier: "v"}); err != nil {
		t.Fatalf("重试后应成功，实际: %v", err)
	}
	if ft.attempts != 2 {
		t.Fatalf("尝试次数 = %d, want 2", ft.attempts)
	}
}

// TestExchangeTokenDoesNotRetryOn4xx 确认 code 被拒时不会重复提交。
// 授权码是一次性的，重发只会把它彻底作废，还会掩盖真正的错误。
func TestExchangeTokenDoesNotRetryOn4xx(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"invalid_grant"}`))
	}))
	defer srv.Close()

	_, err := exchangeToken(srv.Client(), srv.URL, "ua", exchangeParams{code: "c", codeVerifier: "v"})
	if err == nil {
		t.Fatal("400 应该报错")
	}
	if hits != 1 {
		t.Fatalf("请求次数 = %d, want 1（不得重发一次性授权码）", hits)
	}
}

// TestCallbackServerValidatesState 确认 state 不匹配会被拒，且不会误判为成功。
func TestCallbackServerValidatesState(t *testing.T) {
	s := &callbackServer{
		expectedState: "expected",
		result:        make(chan callbackData, 1),
		failure:       make(chan error, 1),
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/oauth/callback?state=forged&code=abc&login_option=google", nil)
	s.handleCallback(rec, req)

	select {
	case cb := <-s.result:
		t.Fatalf("伪造 state 不该通过，却收到 %#v", cb)
	case err := <-s.failure:
		if !strings.Contains(err.Error(), "state") {
			t.Fatalf("错误信息应提到 state，实际: %v", err)
		}
	default:
		t.Fatal("既没成功也没失败")
	}
}

// TestCallbackServerAcceptsValidCallback 覆盖正常回调，并确认路径被记下来——
// 它要拼进换 token 的 redirect_uri，写错就会被判不匹配。
func TestCallbackServerAcceptsValidCallback(t *testing.T) {
	s := &callbackServer{
		expectedState: "expected",
		result:        make(chan callbackData, 1),
		failure:       make(chan error, 1),
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/signin/callback?state=expected&code=abc&login_option=github", nil)
	s.handleCallback(rec, req)

	select {
	case cb := <-s.result:
		if cb.Code != "abc" || cb.LoginOption != "github" {
			t.Fatalf("回调解析不对: %#v", cb)
		}
		if cb.Path != "/signin/callback" {
			t.Fatalf("Path = %q, want /signin/callback", cb.Path)
		}
	case err := <-s.failure:
		t.Fatalf("合法回调被拒: %v", err)
	default:
		t.Fatal("没有收到回调结果")
	}

	if rec.Code != http.StatusFound {
		t.Errorf("状态码 = %d, want 302", rec.Code)
	}
}

// TestCallbackServerPropagatesProviderError 覆盖 IdP 侧报错的分支。
func TestCallbackServerPropagatesProviderError(t *testing.T) {
	s := &callbackServer{
		expectedState: "expected",
		result:        make(chan callbackData, 1),
		failure:       make(chan error, 1),
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/oauth/callback?error=access_denied&error_description=user+refused", nil)
	s.handleCallback(rec, req)

	select {
	case <-s.result:
		t.Fatal("IdP 报错不该算成功")
	case err := <-s.failure:
		if !strings.Contains(err.Error(), "user refused") {
			t.Fatalf("应透出 error_description，实际: %v", err)
		}
	default:
		t.Fatal("既没成功也没失败")
	}
}
