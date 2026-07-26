package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
)

// 这个文件搭了一套完整的模拟环境：假 portal + 假 auth 服务 + 假浏览器，
// 把 login() 从起本地回调一路跑到产出凭证，中间不碰真实网络。
//
// 单测只能各管一段（回调 handler 单独调、换 token 单独打 httptest），
// 真正容易出错的是各段之间的衔接——尤其是 redirect_uri 在
// 「进 portal」与「换 token」两处形状不同这件事。
// 这里让假 auth 服务真去校验 PKCE 和授权码，衔接错了就会失败。

// fakeKiro 同时扮演 app.kiro.dev 与 auth 服务，并记录两边收到的东西。
type fakeKiro struct {
	portal *httptest.Server
	auth   *httptest.Server

	loginOption string
	// forgeState 非空时，portal 回调会带一个伪造的 state，用来验 CSRF 校验。
	forgeState string
	// omitCode 为真时回调不带 code。
	omitCode bool

	mu sync.Mutex
	// portal 侧收到的
	gotState        string
	gotChallenge    string
	gotMethod       string
	gotRedirectURI  string
	gotRedirectFrom string
	issuedCode      string
	// auth 侧收到的
	gotExchangeRedirectURI string
	gotExchangeUA          string
	exchangeHits           int
}

func newFakeKiro(t *testing.T, loginOption string) *fakeKiro {
	t.Helper()
	f := &fakeKiro{loginOption: loginOption, issuedCode: "authz-code-abc123"}

	f.portal = httptest.NewServer(http.HandlerFunc(f.handleSignin))
	f.auth = httptest.NewServer(http.HandlerFunc(f.handleAuth))
	t.Cleanup(func() {
		f.portal.Close()
		f.auth.Close()
	})
	return f
}

// handleSignin 扮演 app.kiro.dev/signin：记下 PKCE 参数，然后把浏览器 302 回本地回调。
func (f *fakeKiro) handleSignin(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/signin" {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()

	f.mu.Lock()
	f.gotState = q.Get("state")
	f.gotChallenge = q.Get("code_challenge")
	f.gotMethod = q.Get("code_challenge_method")
	f.gotRedirectURI = q.Get("redirect_uri")
	f.gotRedirectFrom = q.Get("redirect_from")
	code := f.issuedCode
	state := f.gotState
	if f.forgeState != "" {
		state = f.forgeState
	}
	redirectURI := f.gotRedirectURI
	omitCode := f.omitCode
	loginOption := f.loginOption
	f.mu.Unlock()

	// portal 回调打到的是「origin + 回调路径」，origin 就是它收到的 redirect_uri。
	params := url.Values{}
	params.Set("login_option", loginOption)
	params.Set("state", state)
	if !omitCode {
		params.Set("code", code)
	}
	// IdC 那几条路不发 code，改回 issuer_url + idc_region。
	switch loginOption {
	case "builderid", "awsidc", "internal":
		params.Del("code")
		params.Set("issuer_url", "https://identitycenter.amazonaws.com/ssoins-1234567890")
		params.Set("idc_region", "us-east-1")
	}

	http.Redirect(w, r, redirectURI+"/oauth/callback?"+params.Encode(), http.StatusFound)
}

// handleAuth 扮演 prod.us-east-1.auth.desktop.kiro.dev，并真的校验 PKCE。
func (f *fakeKiro) handleAuth(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/oauth/token" {
		http.NotFound(w, r)
		return
	}

	var body map[string]string
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"message":"bad json"}`, http.StatusBadRequest)
		return
	}

	f.mu.Lock()
	f.exchangeHits++
	f.gotExchangeRedirectURI = body["redirect_uri"]
	f.gotExchangeUA = r.Header.Get("User-Agent")
	wantChallenge := f.gotChallenge
	wantCode := f.issuedCode
	f.mu.Unlock()

	// 真 PKCE 校验：sha256(code_verifier) 必须等于 portal 那一步收到的 challenge。
	sum := sha256.Sum256([]byte(body["code_verifier"]))
	if base64.RawURLEncoding.EncodeToString(sum[:]) != wantChallenge {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"invalid_grant: PKCE 校验失败"}`))
		return
	}
	if body["code"] != wantCode {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"invalid_grant: 授权码不匹配"}`))
		return
	}

	_, _ = w.Write([]byte(`{
		"accessToken":  "aoaAAAAAGnI-fake-access-token",
		"refreshToken": "aorAAAAAGnI-fake-refresh-token",
		"profileArn":   "arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3GRVQMUK",
		"expiresIn":    3600
	}`))
}

// browser 扮演用户的浏览器：跟随 portal 的 302 打到本地回调服务。
// 回调服务处理完会再 302 到真实的 app.kiro.dev 成功页，
// 所以离开 localhost 就必须停下来，否则测试会真的出网。
func (f *fakeKiro) browser() func(string) error {
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 10 {
				return errors.New("重定向过多")
			}
			host := req.URL.Hostname()
			if host != "localhost" && host != "127.0.0.1" {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
	return func(target string) error {
		resp, err := client.Get(target)
		if err != nil {
			return err
		}
		defer func() { _ = resp.Body.Close() }()
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
}

func (f *fakeKiro) opts() options {
	return options{
		portalURL:    f.portal.URL,
		authEndpoint: f.auth.URL,
		kiroVersion:  "1.0.212",
	}
}

// TestE2ESocialLoginFlow 跑通整条 Google 登录链。
func TestE2ESocialLoginFlow(t *testing.T) {
	fake := newFakeKiro(t, "google")

	file, err := login(fake.opts(), fake.auth.Client(), fake.browser())
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	// --- 产出的凭证 ---
	if file.AccessToken != "aoaAAAAAGnI-fake-access-token" {
		t.Errorf("accessToken = %q", file.AccessToken)
	}
	if file.RefreshToken != "aorAAAAAGnI-fake-refresh-token" {
		t.Errorf("refreshToken = %q", file.RefreshToken)
	}
	if file.AuthMethod != "social" || file.Provider != "Google" {
		t.Errorf("authMethod/provider = %q/%q, want social/Google", file.AuthMethod, file.Provider)
	}
	if !strings.HasPrefix(file.ProfileARN, "arn:aws:codewhisperer:") {
		t.Errorf("profileArn = %q", file.ProfileARN)
	}
	// expiresAt 要和 JS 的 toISOString() 一样是毫秒精度 + Z。
	if _, err := time.Parse("2006-01-02T15:04:05.000Z", file.ExpiresAt); err != nil {
		t.Errorf("expiresAt 格式不对 %q: %v", file.ExpiresAt, err)
	}
	// 默认不写 machineId / kiroVersion，保持与官方文件一致。
	if file.MachineID != "" || file.KiroVersion != "" {
		t.Errorf("默认不该写出 machineId/kiroVersion: %q %q", file.MachineID, file.KiroVersion)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()

	// --- portal 侧收到的 ---
	if fake.gotMethod != "S256" {
		t.Errorf("code_challenge_method = %q", fake.gotMethod)
	}
	if fake.gotRedirectFrom != "KiroIDE" {
		t.Errorf("redirect_from = %q", fake.gotRedirectFrom)
	}
	if fake.gotState == "" {
		t.Error("portal 没收到 state")
	}
	// 进 portal 的 redirect_uri 只有 origin，不能带路径或末尾斜杠。
	if !strings.HasPrefix(fake.gotRedirectURI, "http://localhost:") {
		t.Errorf("portal redirect_uri = %q", fake.gotRedirectURI)
	}
	if strings.HasSuffix(fake.gotRedirectURI, "/") || strings.Count(fake.gotRedirectURI, "/") != 2 {
		t.Errorf("portal redirect_uri 应是裸 origin，实际 %q", fake.gotRedirectURI)
	}

	// --- auth 侧收到的 ---
	// 换 token 的 redirect_uri 必须是 origin + 回调路径 + login_option。
	wantExchange := fake.gotRedirectURI + "/oauth/callback?login_option=google"
	if fake.gotExchangeRedirectURI != wantExchange {
		t.Errorf("换 token redirect_uri = %q, want %q", fake.gotExchangeRedirectURI, wantExchange)
	}
	// auth 服务用连字符版 UA，别和 Q API 的空格版混了。
	if !strings.HasPrefix(fake.gotExchangeUA, "KiroIDE-1.0.212-") {
		t.Errorf("换 token User-Agent = %q", fake.gotExchangeUA)
	}
	if fake.exchangeHits != 1 {
		t.Errorf("换 token 次数 = %d, want 1", fake.exchangeHits)
	}
}

// TestE2EOutputIsImportableByRingStar 把产出的凭证喂给 RingStar 自己的解析器。
//
// 这一步是 -verify 的离线部分：光看字段名对不对没用，得让真正要消费它的
// 那套代码点头。上游连通性没法在这儿验（q.<region>.amazonaws.com 的
// 地址是从 profileArn 推出来的，改不了），那部分靠 -verify 在真登录时打。
func TestE2EOutputIsImportableByRingStar(t *testing.T) {
	fake := newFakeKiro(t, "google")

	file, err := login(fake.opts(), fake.auth.Client(), fake.browser())
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	raw, err := json.Marshal(file)
	if err != nil {
		t.Fatalf("序列化凭证: %v", err)
	}

	creds, err := kiro.ParseAuthToken(raw)
	if err != nil {
		t.Fatalf("RingStar 解析不了 kirologin 的产出: %v", err)
	}
	if err := creds.Validate(); err != nil {
		t.Fatalf("凭证没通过 RingStar 的完整性校验: %v", err)
	}

	if creds.AuthMethod != kiro.AuthMethodSocial {
		t.Errorf("authMethod = %q, want %q", creds.AuthMethod, kiro.AuthMethodSocial)
	}
	if creds.Provider != "Google" {
		t.Errorf("provider = %q, want Google", creds.Provider)
	}
	// profileArn 决定了网关往哪个 region 打，推错了请求会发到不存在的端点。
	if creds.QRegion() != "us-east-1" {
		t.Errorf("QRegion = %q, want us-east-1", creds.QRegion())
	}
	// 刚拿到的凭证有效期 1 小时，不该立刻就要求续期。
	if creds.NeedsRefresh(time.Now(), kiro.RefreshBuffer) {
		t.Errorf("新凭证不该立刻需要刷新, expiresAt=%s", creds.ExpiresAt)
	}
}

// TestE2EGithubLoginFlow 确认 github 这条分支的 provider 映射与 redirect_uri 拼装。
func TestE2EGithubLoginFlow(t *testing.T) {
	fake := newFakeKiro(t, "github")

	file, err := login(fake.opts(), fake.auth.Client(), fake.browser())
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if file.Provider != "Github" {
		t.Errorf("provider = %q, want Github", file.Provider)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if !strings.HasSuffix(fake.gotExchangeRedirectURI, "?login_option=github") {
		t.Errorf("换 token redirect_uri = %q", fake.gotExchangeRedirectURI)
	}
}

// TestE2ERingstarFieldsWrittenAndPersisted 覆盖 -ringstar 与落盘这一段。
func TestE2ERingstarFieldsWrittenAndPersisted(t *testing.T) {
	fake := newFakeKiro(t, "google")
	opts := fake.opts()
	opts.ringstar = true
	opts.output = filepath.Join(t.TempDir(), "kiro-auth-token.json")

	file, err := login(opts, fake.auth.Client(), fake.browser())
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if file.MachineID == "" || file.KiroVersion != "1.0.212" {
		t.Fatalf("-ringstar 应写出 machineId/kiroVersion，实际 %q %q", file.MachineID, file.KiroVersion)
	}
	if err := emit(file, opts); err != nil {
		t.Fatalf("emit: %v", err)
	}

	raw, err := os.ReadFile(opts.output)
	if err != nil {
		t.Fatalf("读回凭证: %v", err)
	}

	// 落盘的必须是 camelCase，RingStar 的 ParseAuthToken 和官方文件都认这个。
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("落盘的不是合法 JSON: %v", err)
	}
	for _, key := range []string{"accessToken", "refreshToken", "profileArn", "expiresAt", "authMethod", "provider", "machineId", "kiroVersion"} {
		if _, ok := parsed[key]; !ok {
			t.Errorf("凭证缺字段 %q", key)
		}
	}
}

// TestE2EDefaultOutputOmitsMachineFields 确认不带 -ringstar 时落盘就是官方那 6 个字段。
func TestE2EDefaultOutputOmitsMachineFields(t *testing.T) {
	fake := newFakeKiro(t, "google")
	opts := fake.opts()
	opts.output = filepath.Join(t.TempDir(), "kiro-auth-token.json")

	file, err := login(opts, fake.auth.Client(), fake.browser())
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if err := emit(file, opts); err != nil {
		t.Fatalf("emit: %v", err)
	}

	raw, err := os.ReadFile(opts.output)
	if err != nil {
		t.Fatalf("读回凭证: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("落盘的不是合法 JSON: %v", err)
	}
	if len(parsed) != 6 {
		t.Fatalf("默认应只有官方那 6 个字段，实际 %d 个: %v", len(parsed), parsed)
	}
}

// TestE2EForgedStateRejected 确认伪造 state 的回调走不通。
func TestE2EForgedStateRejected(t *testing.T) {
	fake := newFakeKiro(t, "google")
	fake.forgeState = "forged-state"

	_, err := login(fake.opts(), fake.auth.Client(), fake.browser())
	if err == nil {
		t.Fatal("伪造 state 应该失败")
	}
	if !strings.Contains(err.Error(), "state") {
		t.Fatalf("错误应提到 state，实际: %v", err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.exchangeHits != 0 {
		t.Fatalf("state 没过就不该去换 token，实际打了 %d 次", fake.exchangeHits)
	}
}

// TestE2EIdCLoginOptionReportsClearly 确认 IdC 那几条路会明确报错而不是静默失败，
// 且把排查需要的 issuer_url 打出来。
func TestE2EIdCLoginOptionReportsClearly(t *testing.T) {
	for _, loginOption := range []string{"builderid", "awsidc", "internal"} {
		t.Run(loginOption, func(t *testing.T) {
			fake := newFakeKiro(t, loginOption)

			_, err := login(fake.opts(), fake.auth.Client(), fake.browser())
			if err == nil {
				t.Fatalf("login_option=%s 应该报错", loginOption)
			}
			if !strings.Contains(err.Error(), "identitycenter.amazonaws.com") {
				t.Errorf("错误里应带上 issuer_url，实际: %v", err)
			}
			if !strings.Contains(err.Error(), "us-east-1") {
				t.Errorf("错误里应带上 idc_region，实际: %v", err)
			}

			fake.mu.Lock()
			defer fake.mu.Unlock()
			if fake.exchangeHits != 0 {
				t.Errorf("IdC 不该去 portal 换 token，实际打了 %d 次", fake.exchangeHits)
			}
		})
	}
}

// TestE2EMissingCodeRejected 覆盖回调缺 code 的情况。
func TestE2EMissingCodeRejected(t *testing.T) {
	fake := newFakeKiro(t, "google")
	fake.omitCode = true

	_, err := login(fake.opts(), fake.auth.Client(), fake.browser())
	if err == nil {
		t.Fatal("缺 code 应该失败")
	}
	if !strings.Contains(err.Error(), "code") {
		t.Fatalf("错误应提到 code，实际: %v", err)
	}
}

// TestE2EPKCEVerifierIsFreshEachRun 确认两次登录用的是不同的 verifier 与 state，
// 复用会让上一次的授权码有机会被重放。
func TestE2EPKCEVerifierIsFreshEachRun(t *testing.T) {
	seenChallenge := map[string]bool{}
	seenState := map[string]bool{}

	for i := 0; i < 3; i++ {
		fake := newFakeKiro(t, "google")
		if _, err := login(fake.opts(), fake.auth.Client(), fake.browser()); err != nil {
			t.Fatalf("第 %d 次 login: %v", i+1, err)
		}

		fake.mu.Lock()
		challenge, state := fake.gotChallenge, fake.gotState
		fake.mu.Unlock()

		if seenChallenge[challenge] {
			t.Fatalf("code_challenge 重复了: %s", challenge)
		}
		if seenState[state] {
			t.Fatalf("state 重复了: %s", state)
		}
		seenChallenge[challenge] = true
		seenState[state] = true
	}
}

// TestE2ECallbackPortIsOneOfTheAllowedOnes 确认本地回调落在 portal 放行的端口里。
func TestE2ECallbackPortIsOneOfTheAllowedOnes(t *testing.T) {
	fake := newFakeKiro(t, "google")

	if _, err := login(fake.opts(), fake.auth.Client(), fake.browser()); err != nil {
		t.Fatalf("login: %v", err)
	}

	fake.mu.Lock()
	got := fake.gotRedirectURI
	fake.mu.Unlock()

	var ok bool
	for _, p := range callbackPorts {
		if got == fmt.Sprintf("http://localhost:%d", p) {
			ok = true
			break
		}
	}
	if !ok {
		t.Fatalf("回调地址 %q 不在放行端口 %v 里", got, callbackPorts)
	}
}
