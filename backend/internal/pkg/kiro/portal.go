package kiro

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Kiro 1.0.x 的网页登录（portal）流程。
//
// 协议来源：Kiro 扩展 kiro.kiro-agent 的 packages/kiro-shared/dist/
// portal-auth-provider-*.js，与 backend/cmd/kirologin 同源。
//
// 与 IDE 的差别在于「谁接收回调」：
// IDE 在本机 127.0.0.1 起一个临时 HTTP 服务收 code；服务端做不到这一点——
// portal 只放行 localhost 回调，浏览器会把 code 送到**管理员自己的机器**上，
// 而那里并没有监听。所以这里走本项目其他平台同样的手动模式：
// 回调页打不开，但 code 就在地址栏里，管理员贴回来即可。
// 换 token 那一步（POST /oauth/token）是纯服务端调用，不要求调用方就是收到回调的人。
const (
	// PortalBaseURL 承载 Google / GitHub / BuilderID 各家登录。
	PortalBaseURL = "https://app.kiro.dev"
	// AuthEndpoint 是换 token 的服务端。
	AuthEndpoint = "https://prod.us-east-1.auth.desktop.kiro.dev"
	// DefaultKiroVersion 只用于 User-Agent。
	DefaultKiroVersion = "1.0.212"
)

// DefaultCallbackPort 取扩展 CALLBACK_PORTS 的第一个。
//
// portal 侧只放行固定的这几个 localhost 端口，不能随便选；IDE 是从头往后试
// 第一个能监听的。服务端不监听任何端口，所以固定用第一个即可——它只是一个
// 必须与换 token 时一致的字符串。
const DefaultCallbackPort = 3128

// CallbackPorts 保留完整列表：管理员本机若恰好有 Kiro IDE 在跑，
// 回调可能落到后面的端口上，粘回来的 URL 需要能被识别。
var CallbackPorts = []int{3128, 4649, 6588, 8008, 9091, 49153, 50153, 51153, 52153, 53153}

// PKCE 是一次登录会话的校验参数。
type PKCE struct {
	State     string
	Verifier  string
	Challenge string
}

// NewPKCE 生成 state 与 PKCE 参数。
//
// 注意 challenge 是对 verifier **字符串**做 sha256，而不是对随机字节做——
// 此时 verifier 已经是 base64url 文本了。对齐扩展的
// createHash("sha256").update(codeVerifier)，算错会被 portal 判为 invalid_grant。
func NewPKCE() (*PKCE, error) {
	verifier, err := randomBase64URL(32)
	if err != nil {
		return nil, fmt.Errorf("generate code_verifier: %w", err)
	}
	state, err := randomBase64URL(16)
	if err != nil {
		return nil, fmt.Errorf("generate state: %w", err)
	}
	sum := sha256.Sum256([]byte(verifier))
	return &PKCE{
		State:     state,
		Verifier:  verifier,
		Challenge: base64.RawURLEncoding.EncodeToString(sum[:]),
	}, nil
}

func randomBase64URL(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// CallbackOrigin 是进 portal 时用的 redirect_uri：只有 origin，没有路径。
func CallbackOrigin(port int) string {
	return fmt.Sprintf("http://localhost:%d", port)
}

// BuildPortalURL 组装浏览器要打开的登录地址。
func BuildPortalURL(pkce *PKCE, port int) string {
	params := url.Values{}
	params.Set("state", pkce.State)
	params.Set("code_challenge", pkce.Challenge)
	params.Set("code_challenge_method", "S256")
	params.Set("redirect_uri", CallbackOrigin(port))
	params.Set("redirect_from", "KiroIDE")
	return PortalBaseURL + "/signin?" + params.Encode()
}

// CallbackParams 是从回调地址里解析出来的东西。
//
// 两条链的回调形状不同：social 回调带 code，IdC 回调只是个交接信息，
// 带的是 issuer_url + idc_region，告诉你接着该去哪个 Identity Center 实例。
type CallbackParams struct {
	Code        string
	State       string
	LoginOption string
	Port        int

	// 以下三项只有 IdC 的交接回调（login_option=builderid/awsidc/internal）会带。
	IssuerURL string
	IdCRegion string
	ClientID  string
}

// IsIdCHandoff 报告这是不是 portal 交给 IdC 链的那次回调。
func (p *CallbackParams) IsIdCHandoff() bool {
	return p != nil && IsIdCLoginOption(p.LoginOption)
}

// ParseCallback 从管理员粘回来的内容里取出 code / state / login_option。
//
// 尽量宽容：既接受整条回调 URL（最常见——直接从地址栏复制），
// 也接受只有 query 的片段，还接受光秃秃的一个 code。
//
// IdC 的交接回调不带 code，所以只有非 IdC 的回调才强制要求 code；
// 缺 issuer_url 之类的 IdC 字段由调用方按自己的流程去判断。
func ParseCallback(raw string) (*CallbackParams, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("callback is empty")
	}

	// 不含 = 与 ? 的，按裸 code 处理。
	if !strings.ContainsAny(trimmed, "=?") {
		return &CallbackParams{Code: trimmed, Port: DefaultCallbackPort}, nil
	}

	query := trimmed
	port := DefaultCallbackPort
	if parsed, err := url.Parse(trimmed); err == nil && parsed.RawQuery != "" {
		query = parsed.RawQuery
		if p := parsed.Port(); p != "" {
			if _, convErr := fmt.Sscanf(p, "%d", &port); convErr != nil {
				port = DefaultCallbackPort
			}
		}
	} else {
		query = strings.TrimPrefix(query, "?")
	}

	values, err := url.ParseQuery(query)
	if err != nil {
		return nil, fmt.Errorf("parse callback query: %w", err)
	}
	// portal 出错时会带 error / error_message 回来，直接透传给运营方看。
	if msg := strings.TrimSpace(values.Get("error_message")); msg != "" {
		return nil, fmt.Errorf("kiro portal returned an error: %s", msg)
	}
	if msg := strings.TrimSpace(values.Get("error")); msg != "" {
		return nil, fmt.Errorf("kiro portal returned an error: %s", msg)
	}

	params := &CallbackParams{
		Code:        strings.TrimSpace(values.Get("code")),
		State:       strings.TrimSpace(values.Get("state")),
		LoginOption: strings.TrimSpace(values.Get("login_option")),
		Port:        port,
		IssuerURL:   strings.TrimSpace(values.Get("issuer_url")),
		IdCRegion:   strings.TrimSpace(values.Get("idc_region")),
		ClientID:    strings.TrimSpace(values.Get("client_id")),
	}
	if params.Code == "" && !params.IsIdCHandoff() {
		return nil, fmt.Errorf("no authorization code found in the callback")
	}
	return params, nil
}

// ExchangeRedirectURI 是换 token 时用的 redirect_uri。
//
// 与进 portal 时的形状**不同**：这里必须带上回调路径和 login_option。
// 两边不一致会被判 redirect_uri 不匹配——这是本流程最容易踩的坑。
func ExchangeRedirectURI(port int, loginOption string) string {
	uri := fmt.Sprintf("%s/oauth/callback", CallbackOrigin(port))
	if loginOption != "" {
		uri += "?login_option=" + url.QueryEscape(loginOption)
	}
	return uri
}

// PortalTokenResponse 是 POST /oauth/token 的响应（camelCase）。
type PortalTokenResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ProfileARN   string `json:"profileArn"`
	ExpiresIn    int64  `json:"expiresIn"`
}

// ExchangeAuthorizationCode 用 code + verifier 换 token。请求体 snake_case。
func ExchangeAuthorizationCode(
	ctx context.Context,
	client *http.Client,
	userAgent string,
	code, verifier, redirectURI string,
) (*PortalTokenResponse, error) {
	payload, err := json.Marshal(map[string]string{
		"code":          code,
		"code_verifier": verifier,
		"redirect_uri":  redirectURI,
	})
	if err != nil {
		return nil, fmt.Errorf("encode token request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		AuthEndpoint+"/oauth/token", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read token response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// code 是一次性的且很快过期，走到这一步基本只能重新登录一遍。
		return nil, fmt.Errorf(
			"kiro rejected the authorization code (HTTP %d): %s",
			resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var token PortalTokenResponse
	if err := json.Unmarshal(raw, &token); err != nil {
		return nil, fmt.Errorf("decode token response: %w", err)
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return nil, fmt.Errorf("token response has no accessToken")
	}
	// 没有 refreshToken 的凭证一小时后就废了，建号也活不过第一次续期。
	if strings.TrimSpace(token.RefreshToken) == "" {
		return nil, fmt.Errorf("token response has no refreshToken")
	}
	return &token, nil
}

// PortalUserAgent 复刻 IDE 的 User-Agent 形状。
func PortalUserAgent(version, machineID string) string {
	if strings.TrimSpace(version) == "" {
		version = DefaultKiroVersion
	}
	if strings.TrimSpace(machineID) == "" {
		machineID = DefaultMachineID
	}
	return fmt.Sprintf("KiroIDE-%s-%s", version, machineID)
}

// ExpiresAt 把 expiresIn（秒）换算成绝对时间。
func (t *PortalTokenResponse) ExpiresAt(now time.Time) time.Time {
	if t.ExpiresIn <= 0 {
		return now.Add(time.Hour)
	}
	return now.Add(time.Duration(t.ExpiresIn) * time.Second)
}
