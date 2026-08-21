package cursor

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// 协议对照来源：cursor 服务器反代的 login-token.js / cursor-session-token.js /
// token-store.js。硬约束：Agent 只接受 payload.type == "session" 的 JWT，
// type=web 会被上游以 ERROR_NOT_LOGGED_IN 拒绝。

const (
	// LoginHost 承载浏览器登录的 deep-control 页面。
	LoginHost = "https://cursor.com"
	// AuthHost 承载 auth/poll 与两个刷新端点。
	AuthHost = "https://api2.cursor.sh"

	// OAuthClientID 是 Cursor IDE 的固定 client_id（取自反代）。
	OAuthClientID = "KbZUR41cY7W6zRSdpSUJ7I7mLYBKOCmB"
	// ClientVersion 同时用于 x-cursor-client-version 与 User-Agent。
	ClientVersion = "3.12.10"

	// TokenTypeWeb 是网页 cookie 里的 JWT，不能直接调 Agent。
	TokenTypeWeb = "web"
	// TokenTypeSession 是 IDE 存的 JWT，Agent 只认它。
	TokenTypeSession = "session"

	loginDeepControlPath  = "/loginDeepControl"
	loginDeepCallbackPath = "/api/auth/loginDeepCallbackControl"
	authPollPath          = "/auth/poll"
	oauthTokenPath        = "/oauth/token"
	exchangeAPIKeyPath    = "/auth/exchange_user_api_key"

	defaultPollTimeout  = 3 * time.Minute
	defaultPollInterval = 1500 * time.Millisecond
	maxResponseBody     = 1 << 20
)

var (
	// ErrPollPending 表示浏览器侧还没完成登录（上游返回 404）。
	ErrPollPending = errors.New("cursor login is still pending")
	// ErrPollTimeout 表示等待登录超时。
	ErrPollTimeout = errors.New("cursor login poll timed out")
	// ErrShouldLogout 表示上游要求重新登录，刷新链已无路可走。
	ErrShouldLogout = errors.New("cursor upstream requires re-login")
	// ErrNotAJWT 表示输入既不是 JWT 也不是可解析的 cookie。
	ErrNotAJWT = errors.New("cursor token must be a JWT or a WorkosCursorSessionToken cookie")
	// ErrTokenExpired 表示粘贴的 JWT 已经过期。
	ErrTokenExpired = errors.New("cursor token is expired")
	// ErrNoCredentials 表示 access 与 refresh 都不可用。
	ErrNoCredentials = errors.New("cursor has no usable credentials")
)

// HTTPError 携带上游状态码，供调用方区分凭证失效与临时故障。
type HTTPError struct {
	Status    int
	Operation string
	Body      string
}

func (e *HTTPError) Error() string {
	body := strings.TrimSpace(e.Body)
	if len(body) > 512 {
		body = body[:512]
	}
	return fmt.Sprintf("cursor %s failed (HTTP %d): %s", e.Operation, e.Status, body)
}

// Unauthorized 报告是否为凭证问题。
func (e *HTTPError) Unauthorized() bool {
	return e.Status == http.StatusBadRequest ||
		e.Status == http.StatusUnauthorized ||
		e.Status == http.StatusForbidden
}

// HTTPClient 是本包需要的最小传输面；必须由调用方注入以走账号代理。
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

// Options 是所有出网调用的公共依赖。
type Options struct {
	HTTPClient HTTPClient
	Profile    AgentProfile
	// Sleep 可在测试中替换，避免轮询真的等待。
	Sleep func(context.Context, time.Duration) error
}

func (o *Options) client() (HTTPClient, error) {
	if o == nil || o.HTTPClient == nil {
		return nil, errors.New("cursor http client is nil")
	}
	return o.HTTPClient, nil
}

func (o *Options) sleep(ctx context.Context, d time.Duration) error {
	if o != nil && o.Sleep != nil {
		return o.Sleep(ctx, d)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// UserAgent 是所有请求共用的 UA，对齐反代。
func UserAgent() string {
	return "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Cursor/" + ClientVersion
}

// PKCE 是一次浏览器登录流程的参数。
type PKCE struct {
	Verifier  string `json:"verifier"`
	Challenge string `json:"challenge"`
	UUID      string `json:"uuid"`
	LoginURL  string `json:"login_url"`
}

// GeneratePKCE 生成 verifier/challenge/uuid 与浏览器登录 URL。
func GeneratePKCE() (*PKCE, error) {
	return GeneratePKCEForProfile(AgentProfileIDE)
}

func GeneratePKCEForProfile(profile AgentProfile) (*PKCE, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("generate cursor pkce verifier: %w", err)
	}
	verifier := base64URL(raw)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64URL(sum[:])
	loginUUID := uuid.NewString()

	query := url.Values{}
	query.Set("challenge", challenge)
	query.Set("uuid", loginUUID)
	query.Set("mode", "login")
	redirectTarget := "cli"
	if ParseAgentProfile(string(profile)) == AgentProfileSand {
		redirectTarget = "sand"
	}
	query.Set("redirectTarget", redirectTarget)

	return &PKCE{
		Verifier:  verifier,
		Challenge: challenge,
		UUID:      loginUUID,
		LoginURL:  LoginHost + loginDeepControlPath + "?" + query.Encode(),
	}, nil
}

// base64URL 是不带 padding 的 base64url，challenge 计算对它敏感。
func base64URL(raw []byte) string {
	return base64.RawURLEncoding.EncodeToString(raw)
}

// Tokens 是 auth/poll 与刷新链返回的令牌对。
type Tokens struct {
	AccessToken    string `json:"access_token"`
	RefreshToken   string `json:"refresh_token"`
	AuthID         string `json:"auth_id,omitempty"`
	SelectedTeamID string `json:"selected_team_id,omitempty"`
	// Source 记录令牌来自哪条链路，便于排查刷新降级。
	Source string `json:"source,omitempty"`
}

// pollResponse 同时接受 camelCase 与 snake_case（反代两种都见过）。
type pollResponse struct {
	AccessToken    string          `json:"accessToken"`
	AccessToken2   string          `json:"access_token"`
	RefreshToken   string          `json:"refreshToken"`
	RefreshToken2  string          `json:"refresh_token"`
	AuthID         string          `json:"authId"`
	SelectedTeamID json.RawMessage `json:"selectedTeamId"`
	ShouldLogout   bool            `json:"shouldLogout"`
}

// PollOnce 查询一次登录状态。未完成时返回 ErrPollPending。
func PollOnce(ctx context.Context, opts *Options, loginUUID, verifier string) (*Tokens, error) {
	client, err := opts.client()
	if err != nil {
		return nil, err
	}

	query := url.Values{}
	query.Set("uuid", loginUUID)
	query.Set("verifier", verifier)

	req, err := newRequest(ctx, http.MethodGet, AuthHost+authPollPath+"?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-ghost-mode", "false")
	if ParseAgentProfile(string(opts.Profile)) == AgentProfileSand {
		req.Header.Set("x-cursor-client-type", "sand")
		req.Header.Set("x-cursor-client-version", SandClientVersion)
		req.Header.Set("x-sand-box-namespace", "prod")
	} else {
		req.Header.Set("x-new-onboarding-completed", "false")
		req.Header.Set("x-cursor-client-type", "ide")
		req.Header.Set("x-cursor-client-version", ClientVersion)
	}

	status, body, err := do(client, req)
	if err != nil {
		return nil, err
	}
	// 404 是「还在等浏览器」的正常状态，不是错误。
	if status == http.StatusNotFound {
		return nil, ErrPollPending
	}
	if status < 200 || status >= 300 {
		return nil, &HTTPError{Status: status, Operation: "auth/poll", Body: string(body)}
	}

	var parsed pollResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode cursor auth/poll response: %w", err)
	}
	accessToken := firstNonEmpty(parsed.AccessToken, parsed.AccessToken2)
	if accessToken == "" {
		return nil, ErrPollPending
	}
	refreshToken := firstNonEmpty(parsed.RefreshToken, parsed.RefreshToken2)
	if refreshToken == "" {
		refreshToken = accessToken
	}
	return &Tokens{
		AccessToken:    accessToken,
		RefreshToken:   refreshToken,
		AuthID:         strings.TrimSpace(parsed.AuthID),
		SelectedTeamID: decodeSelectedTeamID(parsed.SelectedTeamID),
		Source:         "auth_poll",
	}, nil
}

// PollTokens 反复调用 PollOnce 直到拿到令牌或超时。
func PollTokens(ctx context.Context, opts *Options, loginUUID, verifier string, timeout, interval time.Duration) (*Tokens, error) {
	if timeout <= 0 {
		timeout = defaultPollTimeout
	}
	if interval <= 0 {
		interval = defaultPollInterval
	}
	deadline := timeNow().Add(timeout)

	for timeNow().Before(deadline) {
		tokens, err := PollOnce(ctx, opts, loginUUID, verifier)
		if err == nil {
			return tokens, nil
		}
		if !errors.Is(err, ErrPollPending) {
			return nil, err
		}
		if sleepErr := opts.sleep(ctx, interval); sleepErr != nil {
			return nil, sleepErr
		}
	}
	return nil, ErrPollTimeout
}

// ParsedToken 是解析后的 cookie / JWT。
type ParsedToken struct {
	UserID    string
	JWT       string
	Type      string
	Subject   string
	ExpiresAt time.Time
}

// CookieValue 还原成 WorkosCursorSessionToken 的取值形态。
func (p *ParsedToken) CookieValue() string {
	if p == nil {
		return ""
	}
	if p.UserID != "" {
		return p.UserID + "::" + p.JWT
	}
	return p.JWT
}

// IsSession 报告该 JWT 是否可直接用于 Agent。
func (p *ParsedToken) IsSession() bool { return p != nil && p.Type == TokenTypeSession }

// IsWeb 报告该 JWT 是否是网页态、需要先换成 session。
func (p *ParsedToken) IsWeb() bool { return p != nil && p.Type == TokenTypeWeb }

// ParseSessionInput 解析用户粘贴的 cookie 或 JWT。
//
// 接受三种形态：
//   - WorkosCursorSessionToken=user_01xxx::eyJ...
//   - user_01xxx::eyJ...（含 URL-encoded 的 %3A%3A）
//   - 裸 JWT（userId 从 payload.sub 取，去掉 auth0| 前缀）
func ParseSessionInput(input string) (*ParsedToken, error) {
	value := strings.TrimSpace(input)
	if value == "" {
		return nil, ErrNotAJWT
	}
	if idx := strings.Index(strings.ToLower(value), "workoscursorsessiontoken="); idx == 0 {
		value = strings.TrimSpace(value[len("WorkosCursorSessionToken="):])
	}
	if strings.Contains(strings.ToLower(value), "%3a%3a") {
		if decoded, err := url.QueryUnescape(value); err == nil {
			value = decoded
		}
	}

	userID := ""
	jwt := value
	if idx := strings.Index(value, "::"); idx >= 0 {
		userID = strings.TrimSpace(value[:idx])
		jwt = strings.TrimSpace(value[idx+2:])
	}
	if !strings.HasPrefix(jwt, "eyJ") {
		return nil, ErrNotAJWT
	}

	claims := DecodeJWTClaims(jwt)
	parsed := &ParsedToken{UserID: userID, JWT: jwt}
	if claims != nil {
		parsed.Type = claims.Type
		parsed.Subject = claims.Subject
		parsed.ExpiresAt = claims.ExpiresAt
		if parsed.UserID == "" && claims.Subject != "" {
			parsed.UserID = strings.TrimPrefix(claims.Subject, "auth0|")
		}
	}
	if !parsed.ExpiresAt.IsZero() && !timeNow().Before(parsed.ExpiresAt) {
		return nil, ErrTokenExpired
	}
	return parsed, nil
}

// JWTClaims 是我们关心的那几个 JWT 字段。
type JWTClaims struct {
	Type      string
	Subject   string
	ExpiresAt time.Time
}

// DecodeJWTClaims 只解 payload，不验签——签名由上游校验，本地只需要读类型和过期时间。
func DecodeJWTClaims(token string) *JWTClaims {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) < 2 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return nil
	}
	var raw struct {
		Type    string `json:"type"`
		Subject string `json:"sub"`
		Exp     int64  `json:"exp"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil
	}
	claims := &JWTClaims{Type: raw.Type, Subject: raw.Subject}
	if raw.Exp > 0 {
		claims.ExpiresAt = time.Unix(raw.Exp, 0).UTC()
	}
	return claims
}

// IsSessionToken 报告 JWT 是否为 session 类型。
func IsSessionToken(token string) bool {
	claims := DecodeJWTClaims(token)
	return claims != nil && claims.Type == TokenTypeSession
}

// IsWebToken 报告 JWT 是否为 web 类型。
func IsWebToken(token string) bool {
	claims := DecodeJWTClaims(token)
	return claims != nil && claims.Type == TokenTypeWeb
}

// TokenExpiry 返回 JWT 的过期时间；无 exp 时返回零值。
func TokenExpiry(token string) time.Time {
	claims := DecodeJWTClaims(token)
	if claims == nil {
		return time.Time{}
	}
	return claims.ExpiresAt
}

// ExchangeWebTokenToSession 把网页 cookie/JWT 换成 IDE 的 session 令牌。
//
// 做法是非交互地复刻网页上「Yes, Log In」那个按钮：带着 web cookie 调
// loginDeepCallbackControl 批准一次新的 deep-control 登录，再从 auth/poll 取回令牌。
// 输入已经是 session 时原样返回（幂等）。
func ExchangeWebTokenToSession(ctx context.Context, opts *Options, input, selectedTeamID string) (*Tokens, error) {
	parsed, err := ParseSessionInput(input)
	if err != nil {
		return nil, err
	}
	if parsed.IsSession() {
		return &Tokens{
			AccessToken:  parsed.JWT,
			RefreshToken: parsed.JWT,
			AuthID:       parsed.Subject,
			Source:       "session_passthrough",
		}, nil
	}

	client, err := opts.client()
	if err != nil {
		return nil, err
	}
	profile := AgentProfileIDE
	if opts != nil {
		profile = opts.Profile
	}
	pkce, err := GeneratePKCEForProfile(profile)
	if err != nil {
		return nil, err
	}

	body := map[string]any{"uuid": pkce.UUID, "challenge": pkce.Challenge}
	if teamID := strings.TrimSpace(selectedTeamID); teamID != "" {
		body["selectedTeamId"] = teamID
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode cursor deep callback request: %w", err)
	}

	req, err := newRequest(ctx, http.MethodPost, LoginHost+loginDeepCallbackPath, payload)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", "WorkosCursorSessionToken="+url.QueryEscape(parsed.CookieValue()))
	req.Header.Set("Origin", LoginHost)
	redirectTarget := "cli"
	if ParseAgentProfile(string(profile)) == AgentProfileSand {
		redirectTarget = "sand"
	}
	req.Header.Set("Referer", LoginHost+loginDeepControlPath+
		"?challenge="+url.QueryEscape(pkce.Challenge)+
		"&uuid="+url.QueryEscape(pkce.UUID)+
		"&mode=login&redirectTarget="+redirectTarget+"&supportsSelectedTeamLogin=true")

	status, respBody, err := do(client, req)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, &HTTPError{Status: status, Operation: "loginDeepCallbackControl", Body: string(respBody)}
	}

	tokens, err := PollTokens(ctx, opts, pkce.UUID, pkce.Verifier, time.Minute, time.Second)
	if err != nil {
		return nil, err
	}
	tokens.Source = "web_session_exchange"
	return tokens, nil
}

// RefreshTokens 按反代 token-store.js 的固定顺序尝试刷新链。
//
//  0. access 或 refresh 是 web 态 → 先换成 session
//  1. POST /oauth/token（grant_type=refresh_token）
//  2. POST /auth/exchange_user_api_key（Bearer refresh）
//  3. 仍失败且有 access → 对 access 再试一次 exchange
//
// 全部失败时返回聚合错误，便于运维一眼看出卡在哪一步。
func RefreshTokens(ctx context.Context, opts *Options, accessToken, refreshToken string) (*Tokens, error) {
	accessToken = strings.TrimSpace(accessToken)
	refreshToken = strings.TrimSpace(refreshToken)
	if accessToken == "" && refreshToken == "" {
		return nil, ErrNoCredentials
	}

	var failures []string
	record := func(step string, err error) {
		failures = append(failures, step+": "+err.Error())
	}

	if accessToken != "" && IsWebToken(accessToken) {
		tokens, err := ExchangeWebTokenToSession(ctx, opts, accessToken, "")
		if err == nil {
			return tokens, nil
		}
		record("web_session", err)
	}
	if refreshToken != "" && refreshToken != accessToken && IsWebToken(refreshToken) {
		tokens, err := ExchangeWebTokenToSession(ctx, opts, refreshToken, "")
		if err == nil {
			return tokens, nil
		}
		record("web_session_refresh", err)
	}

	if refreshToken != "" {
		tokens, err := refreshViaOAuth(ctx, opts, refreshToken)
		if err == nil {
			return tokens, nil
		}
		if errors.Is(err, ErrShouldLogout) {
			return nil, err
		}
		record("oauth", err)

		tokens, err = refreshViaExchange(ctx, opts, refreshToken)
		if err == nil {
			return tokens, nil
		}
		record("exchange_refresh", err)
	}

	if accessToken != "" {
		tokens, err := refreshViaExchange(ctx, opts, accessToken)
		if err == nil {
			return tokens, nil
		}
		record("exchange_access", err)
	}

	return nil, fmt.Errorf("cursor token refresh failed: %s", strings.Join(failures, " | "))
}

type oauthTokenResponse struct {
	AccessToken   string `json:"access_token"`
	AccessToken2  string `json:"accessToken"`
	RefreshToken  string `json:"refresh_token"`
	RefreshToken2 string `json:"refreshToken"`
	ShouldLogout  bool   `json:"shouldLogout"`
}

func refreshViaOAuth(ctx context.Context, opts *Options, refreshToken string) (*Tokens, error) {
	client, err := opts.client()
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(map[string]string{
		"grant_type":    "refresh_token",
		"client_id":     OAuthClientID,
		"refresh_token": refreshToken,
	})
	if err != nil {
		return nil, fmt.Errorf("encode cursor oauth request: %w", err)
	}

	req, err := newRequest(ctx, http.MethodPost, AuthHost+oauthTokenPath, payload)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	status, body, err := do(client, req)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, &HTTPError{Status: status, Operation: "oauth/token", Body: string(body)}
	}

	var parsed oauthTokenResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode cursor oauth response: %w", err)
	}
	// shouldLogout 是终态：继续往下试 exchange 只会浪费一次请求。
	if parsed.ShouldLogout {
		return nil, ErrShouldLogout
	}
	accessToken := firstNonEmpty(parsed.AccessToken, parsed.AccessToken2)
	if accessToken == "" {
		return nil, errors.New("cursor oauth/token response has no access token")
	}
	return &Tokens{
		AccessToken:  accessToken,
		RefreshToken: firstNonEmpty(parsed.RefreshToken, parsed.RefreshToken2, refreshToken),
		Source:       "oauth",
	}, nil
}

func refreshViaExchange(ctx context.Context, opts *Options, bearerToken string) (*Tokens, error) {
	client, err := opts.client()
	if err != nil {
		return nil, err
	}
	req, err := newRequest(ctx, http.MethodPost, AuthHost+exchangeAPIKeyPath, []byte{})
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+bearerToken)
	req.Header.Set("Content-Type", "application/json")

	status, body, err := do(client, req)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, &HTTPError{Status: status, Operation: "auth/exchange_user_api_key", Body: string(body)}
	}

	var parsed oauthTokenResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode cursor exchange response: %w", err)
	}
	accessToken := firstNonEmpty(parsed.AccessToken2, parsed.AccessToken)
	if accessToken == "" {
		return nil, errors.New("cursor exchange_user_api_key response has no access token")
	}
	return &Tokens{
		AccessToken:  accessToken,
		RefreshToken: firstNonEmpty(parsed.RefreshToken2, parsed.RefreshToken, bearerToken),
		Source:       "exchange",
	}, nil
}

// newRequest 不自带超时：单次请求的时限由调用方注入的 http.Client.Timeout
// 与 ctx 的 deadline 共同决定。在这里加 context.WithTimeout 会让 cancel
// 的生命周期跨出函数边界，要么泄漏计时器，要么提前掐断响应读取。
func newRequest(ctx context.Context, method, endpoint string, body []byte) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, fmt.Errorf("build cursor request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", UserAgent())
	return req, nil
}

func do(client HTTPClient, req *http.Request) (int, []byte, error) {
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("cursor request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("read cursor response: %w", err)
	}
	return resp.StatusCode, body, nil
}

// decodeSelectedTeamID 接受字符串与数字两种形态。
func decodeSelectedTeamID(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	if trimmed[0] == '"' {
		var value string
		if err := json.Unmarshal(raw, &value); err == nil {
			return value
		}
		return ""
	}
	return trimmed
}

// timeNow 可在测试中替换。
var timeNow = time.Now

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
