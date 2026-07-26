// Command kirologin 复刻 Kiro IDE 的 portal 登录流程，产出可直接导入 RingStar 的凭证。
//
// 协议来源：Kiro 1.0.212 的 kiro.kiro-agent 扩展
// （packages/kiro-shared/dist/portal-auth-provider-*.js 与 sso-oidc-client-*.js）。
//
// 流程与 IDE 完全一致：
//
//  1. 本地起 HTTP 服务，端口从 CALLBACK_PORTS 依次尝试，只监听 127.0.0.1。
//  2. 生成 state（CSRF）与 PKCE 的 code_verifier / code_challenge(S256)。
//  3. 打开浏览器到 https://app.kiro.dev/signin?...，由 portal 承载 Google/GitHub 登录。
//  4. portal 带着 code + state 回调到本地 /oauth/callback 或 /signin/callback。
//  5. 用 code + code_verifier 向 auth 服务换 token，落盘成 kiro-auth-token.json。
//
// 注意 redirect_uri 在两处的形状不同，这是上游的真实行为，不是笔误：
// 进 portal 时只带 origin，换 token 时要带上回调路径与 login_option 查询串。
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	// defaultPortalURL 对应扩展里的 DEFAULT_AUTH_PORTAL_URL。
	defaultPortalURL = "https://app.kiro.dev"
	// defaultAuthEndpoint 对应 AuthServiceClient 的 DEFAULT_CONFIG.endpoint。
	defaultAuthEndpoint = "https://prod.us-east-1.auth.desktop.kiro.dev"
	// defaultKiroVersion 仅用于 User-Agent，探测不到本机安装时兜底。
	defaultKiroVersion = "1.0.212"

	// authFlowTimeout 对齐 authenticationFlowTimeoutInMs。
	authFlowTimeout = 10 * time.Minute
	// exchangeTimeout 对齐 AuthServiceClient 的 axios timeout，是单次尝试的上限。
	exchangeTimeout = 10 * time.Second
	// exchangeRetries 对齐 axios-retry 的 retries: 3。
	exchangeRetries = 3

	maxResponseBody = 1 << 20
)

// callbackPorts 与扩展的 CALLBACK_PORTS 逐个对齐，顺序不能改：
// portal 侧只放行这些端口的 localhost 回调。
var callbackPorts = []int{3128, 4649, 6588, 8008, 9091, 49153, 50153, 51153, 52153, 53153}

// callbackPaths 与扩展的 PortalAuthServer.callbackPaths 一致。
var callbackPaths = []string{"/oauth/callback", "/signin/callback"}

// callbackData 是 portal 回调带回来的全部参数。
type callbackData struct {
	LoginOption string
	Code        string
	State       string
	Path        string
	IssuerURL   string
	IdcRegion   string
	ClientID    string
	Scopes      string
	LoginHint   string
	Audience    string
}

// tokenResponse 是 POST /oauth/token 的响应体。
type tokenResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ProfileARN   string `json:"profileArn"`
	ExpiresIn    int64  `json:"expiresIn"`
}

// authTokenFile 是 ~/.aws/sso/cache/kiro-auth-token.json 的形状。
// 字段顺序与官方写出的文件一致，RingStar 的 kiro.ParseAuthToken 直接吃这个格式。
type authTokenFile struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ProfileARN   string `json:"profileArn"`
	ExpiresAt    string `json:"expiresAt"`
	AuthMethod   string `json:"authMethod"`
	Provider     string `json:"provider"`

	// 以下两项官方文件里没有，加上是为了让 RingStar 网关复刻本机的
	// `KiroIDE {version} {machineId}` User-Agent。仅 -ringstar 时写出。
	MachineID   string `json:"machineId,omitempty"`
	KiroVersion string `json:"kiroVersion,omitempty"`
}

type options struct {
	portalURL      string
	authEndpoint   string
	kiroVersion    string
	output         string
	install        bool
	ringstar       bool
	noBrowser      bool
	proxy          string
	invitationCode string
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "\n登录失败: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	opts := parseFlags()

	client, err := buildHTTPClient(opts.proxy)
	if err != nil {
		return err
	}

	file, err := login(opts, client, openBrowser)
	if err != nil {
		return err
	}
	return emit(file, opts)
}

// login 跑完整条登录链：起本地回调 -> 把用户送进 portal -> 收 code -> 换 token。
//
// open 是「把用户送进 portal」这一步，生产上就是拉起浏览器；
// 单独抽出来是为了让测试能用一个模拟浏览器驱动完整流程，
// 而不必把端口选择、回调处理、换 token 这些环节各自拆开单测。
func login(opts options, client *http.Client, open func(string) error) (authTokenFile, error) {
	var zero authTokenFile

	// PKCE 与 state：完全复刻扩展里的生成方式。
	state, err := randomUUID()
	if err != nil {
		return zero, fmt.Errorf("生成 state: %w", err)
	}
	verifier, err := randomBase64URL(32)
	if err != nil {
		return zero, fmt.Errorf("生成 code_verifier: %w", err)
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	srv, err := startCallbackServer(state)
	if err != nil {
		return zero, err
	}
	defer srv.close()

	// 进 portal 的 redirect_uri 只有 origin，没有路径。
	redirectURI := fmt.Sprintf("http://localhost:%d", srv.port)
	portalURL := buildPortalURL(opts.portalURL, state, challenge, redirectURI)

	fmt.Printf("本地回调已就绪: %s\n", redirectURI)
	fmt.Printf("登录地址:\n  %s\n\n", portalURL)

	if opts.noBrowser {
		fmt.Println("已按 -no-browser 跳过自动打开，请手动在浏览器里打开上面的地址。")
	} else if err := open(portalURL); err != nil {
		fmt.Printf("自动打开浏览器失败（%v），请手动打开上面的地址。\n", err)
	}
	fmt.Printf("等待回调，超时 %s...\n", authFlowTimeout)

	cb, err := srv.wait(authFlowTimeout)
	if err != nil {
		return zero, err
	}
	fmt.Printf("已收到回调: login_option=%s\n", cb.LoginOption)

	provider, err := socialProvider(cb)
	if err != nil {
		return zero, err
	}
	if cb.Code == "" {
		return zero, errors.New("回调里没有 code")
	}

	machineID := machineID()
	userAgent := fmt.Sprintf("KiroIDE-%s-%s", opts.kiroVersion, machineID)

	// 换 token 用的 redirect_uri 必须带回调路径与 login_option，与 IDE 一致。
	exchangeRedirectURI := fmt.Sprintf("%s%s?login_option=%s", redirectURI, cb.Path, cb.LoginOption)

	tok, err := exchangeToken(client, opts.authEndpoint, userAgent, exchangeParams{
		code:           cb.Code,
		codeVerifier:   verifier,
		redirectURI:    exchangeRedirectURI,
		invitationCode: opts.invitationCode,
	})
	if err != nil {
		return zero, err
	}

	expiresIn := tok.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	file := authTokenFile{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		ProfileARN:   tok.ProfileARN,
		// 与 JS 的 new Date().toISOString() 对齐：毫秒精度 + Z。
		ExpiresAt:  time.Now().UTC().Add(time.Duration(expiresIn) * time.Second).Format("2006-01-02T15:04:05.000Z"),
		AuthMethod: "social",
		Provider:   provider,
	}
	if opts.ringstar {
		file.MachineID = machineID
		file.KiroVersion = opts.kiroVersion
	}
	return file, nil
}

func parseFlags() options {
	var opts options
	flag.StringVar(&opts.portalURL, "portal", envOr("KIRO_AUTH_PORTAL_URL", defaultPortalURL), "登录 portal 地址")
	flag.StringVar(&opts.authEndpoint, "endpoint", defaultAuthEndpoint, "Kiro auth 服务地址")
	flag.StringVar(&opts.kiroVersion, "kiro-version", detectKiroVersion(), "User-Agent 里的 Kiro 版本")
	flag.StringVar(&opts.output, "o", "kiro-auth-token.json", "凭证输出路径，- 表示只打印到标准输出")
	flag.BoolVar(&opts.install, "install", false, "同时写入 ~/.aws/sso/cache/kiro-auth-token.json（会覆盖本机 Kiro 的登录态）")
	flag.BoolVar(&opts.ringstar, "ringstar", false, "额外写出 machineId / kiroVersion，供 RingStar 复刻本机 User-Agent")
	flag.BoolVar(&opts.noBrowser, "no-browser", false, "不自动打开浏览器，只打印登录地址")
	flag.StringVar(&opts.proxy, "proxy", "", "换 token 走的代理，如 http://127.0.0.1:7890 或 socks5://...")
	flag.StringVar(&opts.invitationCode, "invitation-code", "", "邀请码，仅在 portal 要求时填写")
	flag.Parse()
	return opts
}

// startCallbackServer 依次尝试 callbackPorts，返回第一个监听成功的服务。
func startCallbackServer(expectedState string) (*callbackServer, error) {
	for _, port := range callbackPorts {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue
		}
		srv := &callbackServer{
			port:          port,
			expectedState: expectedState,
			listener:      ln,
			result:        make(chan callbackData, 1),
			failure:       make(chan error, 1),
		}
		srv.serve()
		return srv, nil
	}
	return nil, fmt.Errorf("callback 端口全部被占用，尝试过 %v", callbackPorts)
}

type callbackServer struct {
	port          int
	expectedState string
	listener      net.Listener
	httpSrv       *http.Server
	result        chan callbackData
	failure       chan error
}

func (s *callbackServer) serve() {
	mux := http.NewServeMux()
	for _, p := range callbackPaths {
		mux.HandleFunc(p, s.handleCallback)
	}
	s.httpSrv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = s.httpSrv.Serve(s.listener) }()
}

func (s *callbackServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	if e := q.Get("error"); e != "" {
		msg := q.Get("error_description")
		if msg == "" {
			msg = e
		}
		s.reject(w, r, fmt.Errorf("身份提供方返回错误: %s", msg))
		return
	}
	// state 校验：防 CSRF，与扩展的 InvalidStateError 对齐。
	state := q.Get("state")
	if state == "" {
		s.reject(w, r, errors.New("回调缺少 state"))
		return
	}
	if state != s.expectedState {
		s.reject(w, r, errors.New("回调 state 不匹配"))
		return
	}

	cb := callbackData{
		LoginOption: q.Get("login_option"),
		Code:        q.Get("code"),
		State:       state,
		Path:        r.URL.Path,
		IssuerURL:   q.Get("issuer_url"),
		IdcRegion:   q.Get("idc_region"),
		ClientID:    q.Get("client_id"),
		Scopes:      q.Get("scopes"),
		LoginHint:   q.Get("login_hint"),
		Audience:    q.Get("audience"),
	}

	select {
	case s.result <- cb:
	default:
	}
	// 与 IDE 一样把浏览器打到 portal 的成功页，用户不会停在空白页上。
	http.Redirect(w, r, successPageURL(), http.StatusFound)
}

func (s *callbackServer) reject(w http.ResponseWriter, r *http.Request, err error) {
	select {
	case s.failure <- err:
	default:
	}
	http.Redirect(w, r, errorPageURL(err.Error()), http.StatusFound)
}

func (s *callbackServer) wait(timeout time.Duration) (callbackData, error) {
	select {
	case cb := <-s.result:
		return cb, nil
	case err := <-s.failure:
		return callbackData{}, err
	case <-time.After(timeout):
		return callbackData{}, errors.New("等待浏览器回调超时")
	}
}

func (s *callbackServer) close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.httpSrv.Shutdown(ctx)
}

func buildPortalURL(base, state, challenge, redirectURI string) string {
	params := url.Values{}
	params.Set("state", state)
	params.Set("code_challenge", challenge)
	params.Set("code_challenge_method", "S256")
	params.Set("redirect_uri", redirectURI)
	params.Set("redirect_from", "KiroIDE")
	return strings.TrimRight(base, "/") + "/signin?" + params.Encode()
}

func successPageURL() string {
	return defaultPortalURL + "/signin?auth_status=success&redirect_from=KiroIDE"
}

func errorPageURL(msg string) string {
	return defaultPortalURL + "/signin?auth_status=error&redirect_from=KiroIDE&error_message=" + url.QueryEscape(msg)
}

// socialProvider 把 login_option 映射成 token 文件里的 provider。
// portal 也可能回 builderid / awsidc / internal / external_idp，那几条路
// 不在 portal 里发 token，需要接着走 IdC 设备码流程，本工具不覆盖。
func socialProvider(cb callbackData) (string, error) {
	switch cb.LoginOption {
	case "google":
		return "Google", nil
	case "github":
		return "Github", nil
	case "builderid", "awsidc", "internal":
		return "", fmt.Errorf("login_option=%s 走的是 IAM Identity Center 设备码流程（issuer_url=%s, idc_region=%s），"+
			"本工具只实现了 portal 社交登录；请在登录页选择 Google 或 GitHub", cb.LoginOption, cb.IssuerURL, cb.IdcRegion)
	case "external_idp":
		return "", fmt.Errorf("login_option=external_idp 需要与企业 IdP 再做一次 OAuth（issuer_url=%s, client_id=%s），本工具未覆盖",
			cb.IssuerURL, cb.ClientID)
	default:
		return "", fmt.Errorf("未知的 login_option: %q", cb.LoginOption)
	}
}

type exchangeParams struct {
	code           string
	codeVerifier   string
	redirectURI    string
	invitationCode string
}

// exchangeToken 复刻 AuthServiceClient.createToken：
// POST {endpoint}/oauth/token，请求体是 snake_case，响应体是 camelCase。
func exchangeToken(client *http.Client, endpoint, userAgent string, p exchangeParams) (*tokenResponse, error) {
	body := map[string]string{
		"code":          p.code,
		"code_verifier": p.codeVerifier,
		"redirect_uri":  p.redirectURI,
	}
	if p.invitationCode != "" {
		body["invitation_code"] = p.invitationCode
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("编码换 token 请求: %w", err)
	}
	target := strings.TrimRight(endpoint, "/") + "/oauth/token"

	// 只在「压根没拿到响应」时重试，与扩展的 axios-retry 配置一致：
	// 它的 retryCondition 排除了 5xx，而 POST 又不是幂等方法，
	// 实际生效的只剩网络错误这一类。code 是一次性的，多打一次已发出的请求只会白白作废它。
	var raw []byte
	var status int
	var lastErr error
	for attempt := 0; attempt <= exchangeRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt-1)) * 500 * time.Millisecond
			fmt.Printf("换 token 网络失败（%v），%s 后重试 (%d/%d)...\n", lastErr, backoff, attempt, exchangeRetries)
			time.Sleep(backoff)
		}
		raw, status, lastErr = doExchange(client, target, userAgent, payload)
		if lastErr == nil {
			break
		}
	}
	if lastErr != nil {
		return nil, fmt.Errorf("换 token 请求失败: %w", lastErr)
	}

	if status < 200 || status >= 300 {
		// code 只能用一次，且有效期很短；到这一步基本只能重新走一遍登录。
		return nil, fmt.Errorf("换 token 被拒绝 (HTTP %d): %s\n"+
			"授权码是一次性的且很快过期，请重新运行本工具走一遍登录", status, strings.TrimSpace(string(raw)))
	}

	var tok tokenResponse
	if err := json.Unmarshal(raw, &tok); err != nil {
		return nil, fmt.Errorf("解析换 token 响应: %w", err)
	}
	if strings.TrimSpace(tok.RefreshToken) == "" {
		// 没有 refresh_token 的凭证一小时后就废了，导入 RingStar 也活不过第一次续期。
		return nil, errors.New("换 token 响应里没有 refreshToken")
	}
	if strings.TrimSpace(tok.AccessToken) == "" {
		return nil, errors.New("换 token 响应里没有 accessToken")
	}
	if strings.TrimSpace(tok.ProfileARN) == "" {
		// profileArn 是 Q API 每个请求都要带的字段，缺了这个凭证不可用。
		return nil, errors.New("换 token 响应里没有 profileArn")
	}
	return &tok, nil
}

// doExchange 发一次换 token 请求。
// 返回的 error 只代表传输层失败（没拿到响应），HTTP 状态码由调用方判断。
func doExchange(client *http.Client, target, userAgent string, payload []byte) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), exchangeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, fmt.Errorf("构造换 token 请求: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("读取换 token 响应: %w", err)
	}
	return raw, resp.StatusCode, nil
}

func emit(file authTokenFile, opts options) error {
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化凭证: %w", err)
	}
	data = append(data, '\n')

	if opts.output == "-" {
		fmt.Printf("\n%s", data)
	} else {
		if err := os.WriteFile(opts.output, data, 0o600); err != nil {
			return fmt.Errorf("写入 %s: %w", opts.output, err)
		}
		abs, _ := filepath.Abs(opts.output)
		fmt.Printf("\n凭证已写入: %s\n", abs)
	}

	if opts.install {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("定位 home 目录: %w", err)
		}
		dir := filepath.Join(home, ".aws", "sso", "cache")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("创建 %s: %w", dir, err)
		}
		target := filepath.Join(dir, "kiro-auth-token.json")
		if err := os.WriteFile(target, data, 0o600); err != nil {
			return fmt.Errorf("写入 %s: %w", target, err)
		}
		fmt.Printf("已写入本机 Kiro 登录态: %s\n", target)
	}

	fmt.Printf("\nprofileArn : %s\n", file.ProfileARN)
	fmt.Printf("provider   : %s\n", file.Provider)
	fmt.Printf("expiresAt  : %s\n", file.ExpiresAt)
	fmt.Println("\n把这个 JSON 粘进 RingStar 后台「新建 Kiro 账号」即可。")
	return nil
}

func buildHTTPClient(proxy string) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if proxy != "" {
		u, err := url.Parse(proxy)
		if err != nil {
			return nil, fmt.Errorf("代理地址非法: %w", err)
		}
		transport.Proxy = http.ProxyURL(u)
	}
	return &http.Client{Transport: transport}, nil
}

func randomUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func randomBase64URL(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	// Node 的 base64url 不带 padding。
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// machineID 复刻 node-machine-id 的 machineIdSync()：取平台机器标识后做 sha256 十六进制。
// 拿不到时返回扩展的兜底值，保证 User-Agent 仍然合法。
func machineID() string {
	raw := rawMachineID()
	if raw == "" {
		return "UNDETERMINED_MACHINE_ID"
	}
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func rawMachineID() string {
	switch runtime.GOOS {
	case "windows":
		out, err := exec.Command("reg", "QUERY",
			`HKEY_LOCAL_MACHINE\SOFTWARE\Microsoft\Cryptography`, "/v", "MachineGuid").Output()
		if err != nil {
			return ""
		}
		parts := strings.Split(string(out), "REG_SZ")
		if len(parts) == 2 {
			return strings.TrimSpace(parts[1])
		}
	case "darwin":
		out, err := exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
		if err != nil {
			return ""
		}
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "IOPlatformUUID") {
				if _, v, ok := strings.Cut(line, "="); ok {
					return strings.Trim(strings.TrimSpace(v), `"`)
				}
			}
		}
	default:
		for _, p := range []string{"/var/lib/dbus/machine-id", "/etc/machine-id"} {
			if b, err := os.ReadFile(p); err == nil {
				if id := strings.TrimSpace(string(b)); id != "" {
					return id
				}
			}
		}
	}
	return ""
}

// detectKiroVersion 从本机安装的 Kiro 读版本号，只影响 User-Agent。
func detectKiroVersion() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return defaultKiroVersion
	}
	candidates := []string{
		filepath.Join(home, "AppData", "Local", "Programs", "Kiro", "resources", "app", "product.json"),
		"/Applications/Kiro.app/Contents/Resources/app/product.json",
		"/usr/share/kiro/resources/app/product.json",
	}
	for _, p := range candidates {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var product struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(b, &product); err == nil && product.Version != "" {
			return product.Version
		}
	}
	return defaultKiroVersion
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func openBrowser(target string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", target).Start()
	case "darwin":
		return exec.Command("open", target).Start()
	default:
		return exec.Command("xdg-open", target).Start()
	}
}
