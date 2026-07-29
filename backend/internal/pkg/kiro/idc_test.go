//go:build unit

package kiro

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestIdCAuthorizeURL 钉住 IdC 的 authorize 参数。
// scopes 是逗号分隔——这点与 OAuth 常见的空格分隔不同，写错会被上游拒。
func TestIdCAuthorizeURL(t *testing.T) {
	raw := IdCAuthorizeURL("https://oidc.us-east-1.amazonaws.com", "cid", "http://127.0.0.1:3128/oauth/callback", "st", "ch")

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
		// scope 必须带 codewhisperer: 前缀。少了它 AWS 会直接回
		// "Invalid scope provided"——实测踩出来的。
		"scopes": "codewhisperer:completions,codewhisperer:analysis,codewhisperer:conversations," +
			"codewhisperer:transformations,codewhisperer:taskassist",
	} {
		if got := q.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestIdCOIDCBase(t *testing.T) {
	base, err := IdCOIDCBase("us-east-1")
	if err != nil {
		t.Fatalf("IdCOIDCBase: %v", err)
	}
	if base != "https://oidc.us-east-1.amazonaws.com" {
		t.Fatalf("base = %q", base)
	}
	// region 来自外部输入，直接拼进 URL 有 SSRF 风险，必须挡住。
	for _, bad := range []string{"", "evil.com", "us_east_1", "../x"} {
		if _, err := IdCOIDCBase(bad); err == nil {
			t.Errorf("region %q 应被拒绝", bad)
		}
	}
}

// TestIdCCallbackRedirectURI 钉住 IdC 用 127.0.0.1 而不是 portal 的 localhost。
// 两条链的回调主机名不同是上游的真实行为，混用会被判 redirect_uri 不匹配。
func TestIdCCallbackRedirectURI(t *testing.T) {
	if got := IdCCallbackRedirectURI(3128); got != "http://127.0.0.1:3128/oauth/callback" {
		t.Fatalf("IdCCallbackRedirectURI = %q", got)
	}
	if strings.Contains(IdCCallbackRedirectURI(3128), "localhost") {
		t.Fatal("IdC 回调不该用 localhost")
	}
}

func TestIsIdCLoginOption(t *testing.T) {
	for _, opt := range []string{"builderid", "awsidc", "internal"} {
		if !IsIdCLoginOption(opt) {
			t.Errorf("%q 应判为 IdC", opt)
		}
	}
	for _, opt := range []string{"google", "github", "external_idp", ""} {
		if IsIdCLoginOption(opt) {
			t.Errorf("%q 不该判为 IdC", opt)
		}
	}
}

// TestIdCProviderComesFromPortalLoginOption 守住一个实测踩到的坑：
// provider 必须取自 portal 那次回调的 login_option。IdC 自己那次回调是
// AWS OIDC 发的，不带 login_option——在那儿取，awsidc 账号会被错记成
// BuilderId（默认值），进而影响 provider 相关的请求头判断。
func TestIdCProviderComesFromPortalLoginOption(t *testing.T) {
	// IdC 回调（第二次）不带 login_option，模拟从那里取的情形。
	if got := IdCProviderFromLoginOption(""); got != "BuilderId" {
		t.Fatalf("空 login_option 落到默认值 %q", got)
	}
	// 从 portal 回调透传的才是对的。
	if got := IdCProviderFromLoginOption("awsidc"); got != "Enterprise" {
		t.Fatalf("awsidc -> %q, want Enterprise", got)
	}
}

func TestIdCProviderFromLoginOption(t *testing.T) {
	for opt, want := range map[string]string{
		"builderid": "BuilderId",
		"internal":  "Internal",
		"awsidc":    "Enterprise",
	} {
		if got := IdCProviderFromLoginOption(opt); got != want {
			t.Errorf("%q -> %q, want %q", opt, got, want)
		}
	}
}

// TestRegisterIdCClientRequestShape 钉住注册请求的形状。
// grantTypes 少了 refresh_token 的话，拿到的凭证一小时后就废了。
func TestRegisterIdCClientRequestShape(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/client/register" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"clientId":"cid","clientSecret":"secret"}`))
	}))
	defer srv.Close()

	reg, err := RegisterIdCClient(context.Background(), srv.Client(), srv.URL, "https://example.awsapps.com/start")
	if err != nil {
		t.Fatalf("RegisterIdCClient: %v", err)
	}
	if reg.ClientID != "cid" || reg.ClientSecret != "secret" {
		t.Fatalf("registration = %+v", reg)
	}
	if body["issuerUrl"] != "https://example.awsapps.com/start" {
		t.Errorf("issuerUrl = %v", body["issuerUrl"])
	}
	if body["clientType"] != IdCClientType {
		t.Errorf("clientType = %v", body["clientType"])
	}
	grants, _ := body["grantTypes"].([]any)
	if len(grants) != 2 || grants[0] != "authorization_code" || grants[1] != "refresh_token" {
		t.Errorf("grantTypes = %v, 缺 refresh_token 的凭证一小时后就废了", body["grantTypes"])
	}
	scopes, _ := body["scopes"].([]any)
	if len(scopes) != len(idcGrantScopes) {
		t.Fatalf("scopes 数量 = %d, want %d", len(scopes), len(idcGrantScopes))
	}
	for _, s := range scopes {
		if !strings.HasPrefix(s.(string), idcScopePrefix+":") {
			t.Errorf("scope %q 缺 %s: 前缀，AWS 会回 Invalid scope provided", s, idcScopePrefix)
		}
	}
}

// TestRegisterIdCClientRejectsIncompleteResponse 确认响应缺字段时报可识别的错误，
// 而不是返回一个空的注册让后续步骤在更远的地方失败。
func TestRegisterIdCClientRejectsIncompleteResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"clientId":"cid"}`))
	}))
	defer srv.Close()

	_, err := RegisterIdCClient(context.Background(), srv.Client(), srv.URL, "https://example.awsapps.com/start")
	if err == nil || !strings.Contains(err.Error(), "client registration") {
		t.Fatalf("应报 client registration 缺失，实际: %v", err)
	}
}

// TestCreateIdCTokenRequestShape 钉住换 token 的请求体。
// 字段名是 camelCase（clientId / codeVerifier / redirectUri），
// 与 portal 那条链的 snake_case 不同。
func TestCreateIdCTokenRequestShape(t *testing.T) {
	var body map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"accessToken":"at","refreshToken":"rt","expiresIn":3600}`))
	}))
	defer srv.Close()

	reg := &IdCClientRegistration{ClientID: "cid", ClientSecret: "secret"}
	tok, err := CreateIdCToken(context.Background(), srv.Client(), srv.URL, reg,
		"the-code", "the-verifier", "http://127.0.0.1:3128/oauth/callback")
	if err != nil {
		t.Fatalf("CreateIdCToken: %v", err)
	}
	if tok.AccessToken != "at" || tok.RefreshToken != "rt" {
		t.Fatalf("token = %+v", tok)
	}
	for key, want := range map[string]string{
		"clientId":     "cid",
		"clientSecret": "secret",
		"grantType":    "authorization_code",
		"code":         "the-code",
		"codeVerifier": "the-verifier",
		"redirectUri":  "http://127.0.0.1:3128/oauth/callback",
	} {
		if body[key] != want {
			t.Errorf("%s = %q, want %q", key, body[key], want)
		}
	}
}

// TestCreateIdCTokenRejectsMissingRefreshToken 确认没有 refresh token 时直接失败。
// 这种凭证一小时后就废了，建号也活不过第一次续期。
func TestCreateIdCTokenRejectsMissingRefreshToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"accessToken":"at","expiresIn":3600}`))
	}))
	defer srv.Close()

	_, err := CreateIdCToken(context.Background(), srv.Client(), srv.URL,
		&IdCClientRegistration{ClientID: "cid", ClientSecret: "secret"},
		"code", "verifier", "http://127.0.0.1:3128/oauth/callback")
	if err == nil {
		t.Fatal("缺 refreshToken 应该失败")
	}
}

// TestCreateIdCTokenSurfacesUpstreamStatus 确认上游状态码没被吞掉，
// 调用方要靠它区分「凭证被拒」与「上游抖动」。
func TestCreateIdCTokenSurfacesUpstreamStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()

	_, err := CreateIdCToken(context.Background(), srv.Client(), srv.URL,
		&IdCClientRegistration{ClientID: "cid", ClientSecret: "secret"},
		"code", "verifier", "http://127.0.0.1:3128/oauth/callback")
	if err == nil {
		t.Fatal("HTTP 400 应该失败")
	}
	var refreshErr *RefreshError
	if !errors.As(err, &refreshErr) {
		t.Fatalf("应能取出 RefreshError，实际: %v", err)
	}
	if !refreshErr.Unauthorized() {
		t.Errorf("HTTP 400 应判为凭证问题，status = %d", refreshErr.Status)
	}
}

// profileProbeClient 按主机名回放 ListAvailableProfiles 的响应。
// key 是 Q 端点的主机名，例如 q.us-east-1.amazonaws.com。
type profileProbeClient struct {
	byHost map[string]string
	hits   []string
}

func (c *profileProbeClient) Do(req *http.Request) (*http.Response, error) {
	c.hits = append(c.hits, req.URL.Host)
	body, ok := c.byHost[req.URL.Host]
	status := http.StatusOK
	if !ok {
		status = http.StatusNotFound
		body = `{"message":"no such endpoint"}`
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{},
		Request:    req,
	}, nil
}

// TestResolveIdCProfileARNProbesCandidateRegions 覆盖「IdC 的 region 与 Q 的
// region 是两回事」这个坑：Identity Center 可以开在任意 region，但 Q 数据面
// 只在少数几个 region 有端点，所以只能逐个候选去试。
//
// 这里让第一个候选查不到、第二个才有，确认探测会继续往下走而不是就此放弃。
func TestResolveIdCProfileARNProbesCandidateRegions(t *testing.T) {
	client := &profileProbeClient{byHost: map[string]string{
		"q.us-east-1.amazonaws.com":    `{"profiles":[]}`,
		"q.eu-central-1.amazonaws.com": `{"profiles":[{"arn":"arn:aws:codewhisperer:eu-central-1:1:profile/P1","profileName":"team"}]}`,
	}}

	arn, profiles, err := ResolveIdCProfileARN(context.Background(), client, &Credentials{
		AccessToken: "at",
		// region 是 IdC 的，故意选一个 Q 没有端点的，确认它没被拿去拼 Q 端点。
		Region: "us-east-2",
	})
	if err != nil {
		t.Fatalf("ResolveIdCProfileARN: %v", err)
	}
	if arn != "arn:aws:codewhisperer:eu-central-1:1:profile/P1" {
		t.Fatalf("arn = %q", arn)
	}
	if len(profiles) != 1 || profiles[0].ProfileName != "team" {
		t.Fatalf("profiles = %+v", profiles)
	}
	for _, host := range client.hits {
		if strings.Contains(host, "us-east-2") {
			t.Fatalf("不该拿 IdC 的 region 去拼 Q 端点，实际打了 %s", host)
		}
	}
}

// TestResolveIdCProfileARNTakesFirstProfile 对齐 Kiro 的 ProfileArnGuard：
// 多个 profile 时取第一个，完整列表交给调用方展示。
func TestResolveIdCProfileARNTakesFirstProfile(t *testing.T) {
	client := &profileProbeClient{byHost: map[string]string{
		"q.us-east-1.amazonaws.com": `{"profiles":[` +
			`{"arn":"arn:aws:codewhisperer:us-east-1:1:profile/A"},` +
			`{"arn":"arn:aws:codewhisperer:us-east-1:1:profile/B"}]}`,
	}}

	arn, profiles, err := ResolveIdCProfileARN(context.Background(), client, &Credentials{AccessToken: "at"})
	if err != nil {
		t.Fatalf("ResolveIdCProfileARN: %v", err)
	}
	if arn != "arn:aws:codewhisperer:us-east-1:1:profile/A" {
		t.Fatalf("arn = %q", arn)
	}
	if len(profiles) != 2 {
		t.Fatalf("应把完整列表带回去，实际 %d 个", len(profiles))
	}
}

// TestResolveIdCProfileARNReportsNoProfile 确认「账号没开通 profile」是一个
// 可识别的错误：调用方要靠它把「联系管理员开通」和「上游抖动」分开。
func TestResolveIdCProfileARNReportsNoProfile(t *testing.T) {
	client := &profileProbeClient{byHost: map[string]string{
		"q.us-east-1.amazonaws.com":    `{"profiles":[]}`,
		"q.eu-central-1.amazonaws.com": `{"profiles":[]}`,
	}}

	_, _, err := ResolveIdCProfileARN(context.Background(), client, &Credentials{AccessToken: "at"})
	if !errors.Is(err, ErrNoIdCProfile) {
		t.Fatalf("应报 ErrNoIdCProfile，实际: %v", err)
	}
}

// TestParseCallbackIdCHandoffHasNoCode 覆盖 IdC 的交接回调：
// 它不带 code，只带 issuer_url + idc_region，不能当成「回调缺 code」拒掉。
func TestParseCallbackIdCHandoffHasNoCode(t *testing.T) {
	raw := "http://localhost:3128/oauth/callback?login_option=awsidc" +
		"&state=st&issuer_url=https%3A%2F%2Fexample.awsapps.com%2Fstart&idc_region=us-east-2"

	params, err := ParseCallback(raw)
	if err != nil {
		t.Fatalf("IdC 交接回调不该被拒: %v", err)
	}
	if !params.IsIdCHandoff() {
		t.Fatal("awsidc 应判为 IdC 交接")
	}
	if params.IssuerURL != "https://example.awsapps.com/start" {
		t.Errorf("issuer_url = %q", params.IssuerURL)
	}
	if params.IdCRegion != "us-east-2" {
		t.Errorf("idc_region = %q", params.IdCRegion)
	}
	if params.State != "st" {
		t.Errorf("state = %q", params.State)
	}
}

// TestParseCallbackSocialStillRequiresCode 确认放宽 code 校验没有波及社交链。
func TestParseCallbackSocialStillRequiresCode(t *testing.T) {
	_, err := ParseCallback("http://localhost:3128/oauth/callback?login_option=google&state=st")
	if err == nil || !strings.Contains(err.Error(), "code") {
		t.Fatalf("社交回调缺 code 仍应报错，实际: %v", err)
	}
}
