package kiro

// IdC（IAM Identity Center：Enterprise / BuilderId / Internal）登录链。
//
// 协议来源：Kiro 1.0.212 扩展的 IDCAuthProvider。
//
// 它**不是**设备码流程——尽管 bundle 里能搜到 StartDeviceAuthorization
// （那是 AWS SDK 自带的命令，Kiro 没用）。实际走的是 authorization_code + PKCE：
//
//	1. RegisterIdCClient  拿 clientId / clientSecret（必须随凭证落库，
//	   否则 access token 一过期就永久失效）
//	2. 浏览器打开 IdCAuthorizeURL
//	3. 回调到 IdCCallbackRedirectURI 拿 code
//	4. CreateIdCToken 用 authorization_code 换 token
//	5. ResolveIdCProfileARN 补上 profileArn
//
// 与 portal（social）链的三处关键差异：
//   - scopes 用逗号分隔（不是 OAuth 常见的空格）
//   - IdC 不返回 profileArn，得再调一次 ListAvailableProfiles
//   - redirect_uri 用 127.0.0.1 而不是 localhost，且进授权页与换 token 时形状一致
//
// 本文件只覆盖协议本身。「谁去接收浏览器回调」由调用方决定：
// CLI 在本机监听 127.0.0.1，服务端则让管理员把回调地址粘回来。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// IdCClientName 与扩展的 registerClient 参数一致。
	IdCClientName = "Kiro IDE"
	// IdCClientType 与扩展的 registerClient 参数一致。
	IdCClientType = "public"
	// IdCRegisteredRedirectURI 是注册客户端时声明的回调地址。
	// 注册用的是不带端口的形式，实际回调再补上本地端口
	// （RFC 8252 允许 loopback 使用任意端口）。
	IdCRegisteredRedirectURI = "http://127.0.0.1/oauth/callback"

	// idcScopePrefix 是 scope 的命名空间前缀。
	//
	// 扩展里 GRANT_SCOPES 存的是裸名（completions / analysis / ...），
	// 真正发出去之前 IDCAuthProvider 的构造函数会逐个加上前缀：
	//
	//	const scopePrefix = config.get("codewhisperer.config.scopePrefix") ?? "codewhisperer";
	//	this.scopes = GRANT_SCOPES.map(s => `${scopePrefix}:${s}`);
	//
	// 少了这个前缀，authorize 会被 AWS 直接回 "Invalid scope provided"——
	// 这是实测踩出来的，不是推断。
	idcScopePrefix = "codewhisperer"

	defaultIdCTimeout = 30 * time.Second
	maxIdCBody        = 1 << 20
)

// idcGrantScopes 对齐扩展的 GRANT_SCOPES（裸名，不含前缀）。
var idcGrantScopes = []string{"completions", "analysis", "conversations", "transformations", "taskassist"}

// QDataPlaneRegions 是 Q 数据面实际提供服务的 region。
//
// **IdC 的 region 与 Q 的 region 是两回事**，不能拿前者去拼后者的端点：
// Identity Center 实例可以开在任意 region（实测某企业账号在 us-east-2），
// 但 Q 数据面只在下面这几个 region 有端点——扩展 bundle 里出现过的全部
// q.* 主机就这些。拿 us-east-2 去连 q.us-east-2.amazonaws.com 会直接 EOF。
//
// 这也正是 Credentials.QRegion() 从 profileArn 推 region、而不是用
// Credentials.Region 的原因。但 profileArn 恰恰是登录最后一步要查的东西，
// 所以只能逐个候选 region 试。
//
// FIPS 那两个 gov 端点不在候选里：那是政府云，普通账号用不到。
var QDataPlaneRegions = []string{"us-east-1", "eu-central-1"}

// ErrNoIdCProfile 表示账号在所有候选 region 上都没有可用 profile。
var ErrNoIdCProfile = errors.New("kiro idc account has no available profile")

// IdCScopes 返回真正发给上游的 scope 列表（含前缀）。
func IdCScopes() []string {
	scopes := make([]string, 0, len(idcGrantScopes))
	for _, s := range idcGrantScopes {
		scopes = append(scopes, idcScopePrefix+":"+s)
	}
	return scopes
}

// IdCOIDCBase 构造并校验 OIDC 端点。
// region 来自 portal 回调，是外部输入，直接拼进 URL 有 SSRF 风险。
func IdCOIDCBase(region string) (string, error) {
	tokenURL, err := OIDCTokenURL(region)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(tokenURL, "/token"), nil
}

// IdCCallbackRedirectURI 是 IdC 链的回调地址。
//
// 注意用的是 127.0.0.1 而不是 portal 链的 localhost，且进授权页与换 token
// 时必须是同一个字符串——两边不一致会被 AWS 判 invalid_grant。
func IdCCallbackRedirectURI(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d/oauth/callback", port)
}

// IdCClientRegistration 是 /client/register 的响应。
//
// clientId / clientSecret 必须随凭证落库：服务器上没有本机的
// ~/.aws/sso/cache，少了这两项 token 过期即永久失效。
type IdCClientRegistration struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

// RegisterIdCClient 向 Identity Center 注册一个客户端。
func RegisterIdCClient(ctx context.Context, client HTTPClient, oidcBase, issuerURL string) (*IdCClientRegistration, error) {
	if strings.TrimSpace(issuerURL) == "" {
		return nil, errors.New("kiro idc issuer url is empty")
	}
	body := map[string]any{
		"clientName":   IdCClientName,
		"clientType":   IdCClientType,
		"scopes":       IdCScopes(),
		"grantTypes":   []string{"authorization_code", "refresh_token"},
		"redirectUris": []string{IdCRegisteredRedirectURI},
		"issuerUrl":    issuerURL,
	}
	raw, err := idcPostJSON(ctx, client, oidcBase+"/client/register", body)
	if err != nil {
		return nil, fmt.Errorf("register kiro idc client: %w", err)
	}
	var reg IdCClientRegistration
	if err := json.Unmarshal(raw, &reg); err != nil {
		return nil, fmt.Errorf("decode kiro idc client registration: %w", err)
	}
	reg.ClientID = strings.TrimSpace(reg.ClientID)
	reg.ClientSecret = strings.TrimSpace(reg.ClientSecret)
	if reg.ClientID == "" || reg.ClientSecret == "" {
		return nil, ErrClientRegistrationMissing
	}
	return &reg, nil
}

// IdCAuthorizeURL 复刻扩展的 authorize 参数。
// 注意 scopes 是逗号分隔，不是 OAuth 常见的空格分隔。
func IdCAuthorizeURL(oidcBase, clientID, redirectURI, state, challenge string) string {
	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", clientID)
	params.Set("redirect_uri", redirectURI)
	params.Set("scopes", strings.Join(IdCScopes(), ","))
	params.Set("state", state)
	params.Set("code_challenge", challenge)
	params.Set("code_challenge_method", "S256")
	return oidcBase + "/authorize?" + params.Encode()
}

// IdCTokenResponse 是 /token 在 authorization_code 下的响应。
type IdCTokenResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresIn    int64  `json:"expiresIn"`
}

// ExpiresAt 把 expiresIn（秒）换算成绝对时间。
func (t *IdCTokenResponse) ExpiresAt(now time.Time) time.Time {
	if t == nil || t.ExpiresIn <= 0 {
		return now.Add(time.Hour)
	}
	return now.Add(time.Duration(t.ExpiresIn) * time.Second)
}

// CreateIdCToken 用授权码换 token。
//
// redirectURI 必须与生成 IdCAuthorizeURL 时用的完全一致，调用方应把它
// 存下来而不是重新拼——端口或形状差一个字符就是 invalid_grant。
func CreateIdCToken(
	ctx context.Context,
	client HTTPClient,
	oidcBase string,
	reg *IdCClientRegistration,
	code, verifier, redirectURI string,
) (*IdCTokenResponse, error) {
	if reg == nil {
		return nil, ErrClientRegistrationMissing
	}
	body := map[string]string{
		"clientId":     reg.ClientID,
		"clientSecret": reg.ClientSecret,
		"grantType":    "authorization_code",
		"code":         code,
		"codeVerifier": verifier,
		"redirectUri":  redirectURI,
	}
	raw, err := idcPostJSON(ctx, client, oidcBase+"/token", body)
	if err != nil {
		return nil, fmt.Errorf("exchange kiro idc authorization code: %w", err)
	}
	var tok IdCTokenResponse
	if err := json.Unmarshal(raw, &tok); err != nil {
		return nil, fmt.Errorf("decode kiro idc token response: %w", err)
	}
	if strings.TrimSpace(tok.AccessToken) == "" {
		return nil, errors.New("kiro idc token response has no accessToken")
	}
	if strings.TrimSpace(tok.RefreshToken) == "" {
		// 没有 refresh token 的 IdC 凭证一小时后就废了。
		return nil, ErrRefreshTokenMissing
	}
	return &tok, nil
}

// ResolveIdCProfileARN 用刚拿到的 token 查可用 profile。
//
// social 登录时 profileArn 由 /oauth/token 直接下发，IdC 这条没有，
// 必须显式查一次。缺了它每个 Q API 请求都发不出去，所以调用方应把
// 这里的失败当硬失败。
//
// 返回选中的 ARN 与该 region 上的完整列表：多个 profile 时取第一个，
// 与 Kiro 的 ProfileArnGuard 一致，列表交给调用方决定要不要展示。
//
// client 必须是账号自己的出网通道（含代理）——这次探测同样会暴露出口 IP。
func ResolveIdCProfileARN(ctx context.Context, client HTTPClient, creds *Credentials) (string, []Profile, error) {
	if creds == nil {
		return "", nil, errors.New("kiro credentials are nil")
	}
	var lastErr error

	// 逐个候选 region 试，第一个查到 profile 的即为准。
	for _, region := range QDataPlaneRegions {
		profiles, err := listIdCProfilesIn(ctx, client, creds, region)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", region, err)
			continue
		}
		if len(profiles) == 0 {
			continue
		}
		return profiles[0].ARN, profiles, nil
	}

	if lastErr != nil {
		return "", nil, fmt.Errorf("%w: probed %v, last error %w", ErrNoIdCProfile, QDataPlaneRegions, lastErr)
	}
	return "", nil, fmt.Errorf("%w: probed %v", ErrNoIdCProfile, QDataPlaneRegions)
}

func listIdCProfilesIn(ctx context.Context, client HTTPClient, creds *Credentials, region string) ([]Profile, error) {
	probe := &Credentials{
		AccessToken: creds.AccessToken,
		AuthMethod:  AuthMethodIdC,
		Region:      creds.Region,
		MachineID:   creds.MachineID,
		KiroVersion: creds.KiroVersion,
		// 占位：只为通过 NewClient 的校验并把端点定到 region 上。
		// 真实的 profileArn 正是本函数要查的东西。
		ProfileARN: fmt.Sprintf("arn:aws:codewhisperer:%s:000000000000:profile/BOOTSTRAP", region),
	}
	qClient, err := NewClient(client, probe)
	if err != nil {
		return nil, fmt.Errorf("build kiro q client: %w", err)
	}
	return qClient.ListAvailableProfiles(ctx)
}

// IsIdCLoginOption 报告 portal 回调是否要转交 IdC 流程。
func IsIdCLoginOption(loginOption string) bool {
	switch strings.ToLower(strings.TrimSpace(loginOption)) {
	case "builderid", "awsidc", "internal":
		return true
	default:
		return false
	}
}

// IdCProviderFromLoginOption 把 portal 的 login_option 映射成凭证里的 provider。
//
// 必须传 **portal 那次回调**的 login_option：IdC 自己那次回调是 AWS OIDC
// 发的，不带 login_option，在那儿取只会永远落到默认值。
func IdCProviderFromLoginOption(loginOption string) string {
	switch strings.ToLower(strings.TrimSpace(loginOption)) {
	case "builderid":
		return "BuilderId"
	case "internal":
		return ProviderInternal
	case "awsidc":
		return "Enterprise"
	default:
		return "BuilderId"
	}
}

func idcPostJSON(ctx context.Context, client HTTPClient, endpoint string, body any) ([]byte, error) {
	if client == nil {
		return nil, errors.New("kiro http client is nil")
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}

	reqCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, defaultIdCTimeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxIdCBody))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// 保留状态码，让调用方能区分「凭证被拒」与「上游抖动」。
		return nil, &RefreshError{Status: resp.StatusCode, Body: string(raw)}
	}
	return raw, nil
}
