package main

// IdC（IAM Identity Center：Enterprise / BuilderId / Internal）登录。
//
// 协议来源：Kiro 1.0.212 扩展的 IDCAuthProvider。
//
// 注意它**不是**设备码流程——尽管 bundle 里能搜到 StartDeviceAuthorization
// （那是 AWS SDK 自带的命令，Kiro 没用）。实际走的是 authorization_code + PKCE，
// 和 portal 那条链同构，所以本文件复用了同一个本地回调服务器：
//
//	1. RegisterClient  拿 clientId / clientSecret（IdC 账号必须落库，否则
//	   access token 一过期就永久失效）
//	2. 浏览器打开 https://oidc.{region}.amazonaws.com/authorize?...
//	3. 回调到 http://127.0.0.1:{port}/oauth/callback 拿 code
//	4. CreateToken 用 authorization_code 换 token
//
// 与 portal 链的两处关键差异：
//   - scopes 用逗号分隔（不是 OAuth 常见的空格）
//   - IdC 不返回 profileArn，得再调一次 ListAvailableProfiles

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

	"github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
)

// idcScopes 对齐扩展的 GRANT_SCOPES。
var idcScopes = []string{"completions", "analysis", "conversations", "transformations", "taskassist"}

const (
	// idcClientName 与扩展的 registerClient 参数一致。
	idcClientName = "Kiro IDE"
	idcClientType = "public"
	// idcRedirectURI 是注册客户端时声明的回调地址。
	// 注册用的是不带端口的形式，实际回调再补上本地端口。
	idcRegisteredRedirectURI = "http://127.0.0.1/oauth/callback"
)

type idcClientRegistration struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

type idcTokenResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresIn    int64  `json:"expiresIn"`
}

// loginIdC 跑完整条 IdC 链，产出可直接导入 RingStar 的凭证。
//
// issuerURL 与 region 来自 portal 回调（login_option=builderid/awsidc/internal），
// 也可以由用户直接指定。
func loginIdC(opts options, client *http.Client, open func(string) error, issuerURL, region string) (authTokenFile, error) {
	var zero authTokenFile

	if strings.TrimSpace(issuerURL) == "" {
		return zero, errors.New("IdC 登录需要 issuer_url")
	}
	oidcBase, err := idcOIDCBase(region)
	if err != nil {
		return zero, err
	}

	// 1. 注册客户端。clientId / clientSecret 必须随凭证落库：
	// 服务器上没有本机的 ~/.aws/sso/cache，少了这两项 token 过期即永久失效。
	reg, err := idcRegisterClient(client, oidcBase, issuerURL)
	if err != nil {
		return zero, err
	}
	fmt.Printf("已注册 IdC 客户端: %s\n", reg.ClientID)

	state, err := randomUUID()
	if err != nil {
		return zero, fmt.Errorf("生成 state: %w", err)
	}
	verifier, err := randomBase64URL(32)
	if err != nil {
		return zero, fmt.Errorf("生成 code_verifier: %w", err)
	}
	challenge := pkceChallenge(verifier)

	srv, err := startCallbackServer(state)
	if err != nil {
		return zero, err
	}
	defer srv.close()

	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/oauth/callback", srv.port)
	authorizeURL := idcAuthorizeURL(oidcBase, reg.ClientID, redirectURI, state, challenge)

	fmt.Printf("本地回调已就绪: %s\n", redirectURI)
	fmt.Printf("登录地址:\n  %s\n\n", authorizeURL)

	if opts.noBrowser {
		fmt.Println("已按 -no-browser 跳过自动打开，请手动在浏览器里打开上面的地址。")
	} else if err := open(authorizeURL); err != nil {
		fmt.Printf("自动打开浏览器失败（%v），请手动打开上面的地址。\n", err)
	}
	fmt.Printf("等待回调，超时 %s...\n", authFlowTimeout)

	cb, err := srv.wait(authFlowTimeout)
	if err != nil {
		return zero, err
	}
	if cb.Code == "" {
		return zero, errors.New("IdC 回调里没有 code")
	}

	// 4. 换 token。
	tok, err := idcCreateToken(client, oidcBase, reg, cb.Code, verifier, redirectURI)
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
		ExpiresAt:    time.Now().UTC().Add(time.Duration(expiresIn) * time.Second).Format("2006-01-02T15:04:05.000Z"),
		AuthMethod:   "idc",
		Provider:     idcProviderFromLoginOption(cb.LoginOption),
		Region:       region,
		ClientID:     reg.ClientID,
		ClientSecret: reg.ClientSecret,
	}
	if opts.ringstar {
		file.MachineID = machineID()
		file.KiroVersion = opts.kiroVersion
	}

	// 5. IdC 不像 social 那样直接给 profileArn，得再查一次。
	// 缺了它每个 Q API 请求都发不出去，所以这里失败就是硬失败。
	profileARN, err := idcResolveProfileARN(client, &file)
	if err != nil {
		return zero, err
	}
	file.ProfileARN = profileARN
	fmt.Printf("已解析 profileArn: %s\n", profileARN)

	return file, nil
}

// idcOIDCBase 构造并校验 OIDC 端点。region 来自外部输入，直接拼进 URL 有 SSRF 风险。
func idcOIDCBase(region string) (string, error) {
	tokenURL, err := kiro.OIDCTokenURL(region)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(tokenURL, "/token"), nil
}

func idcRegisterClient(client *http.Client, oidcBase, issuerURL string) (*idcClientRegistration, error) {
	body := map[string]any{
		"clientName":   idcClientName,
		"clientType":   idcClientType,
		"scopes":       idcScopes,
		"grantTypes":   []string{"authorization_code", "refresh_token"},
		"redirectUris": []string{idcRegisteredRedirectURI},
		"issuerUrl":    issuerURL,
	}
	raw, err := idcPostJSON(client, oidcBase+"/client/register", body)
	if err != nil {
		return nil, fmt.Errorf("注册 IdC 客户端: %w", err)
	}
	var reg idcClientRegistration
	if err := json.Unmarshal(raw, &reg); err != nil {
		return nil, fmt.Errorf("解析客户端注册响应: %w", err)
	}
	if reg.ClientID == "" || reg.ClientSecret == "" {
		return nil, errors.New("客户端注册响应缺少 clientId/clientSecret")
	}
	return &reg, nil
}

// idcAuthorizeURL 复刻扩展的 authorize 参数。
// 注意 scopes 是逗号分隔，不是 OAuth 常见的空格分隔。
func idcAuthorizeURL(oidcBase, clientID, redirectURI, state, challenge string) string {
	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", clientID)
	params.Set("redirect_uri", redirectURI)
	params.Set("scopes", strings.Join(idcScopes, ","))
	params.Set("state", state)
	params.Set("code_challenge", challenge)
	params.Set("code_challenge_method", "S256")
	return oidcBase + "/authorize?" + params.Encode()
}

func idcCreateToken(client *http.Client, oidcBase string, reg *idcClientRegistration, code, verifier, redirectURI string) (*idcTokenResponse, error) {
	body := map[string]string{
		"clientId":     reg.ClientID,
		"clientSecret": reg.ClientSecret,
		"grantType":    "authorization_code",
		"code":         code,
		"codeVerifier": verifier,
		"redirectUri":  redirectURI,
	}
	raw, err := idcPostJSON(client, oidcBase+"/token", body)
	if err != nil {
		return nil, fmt.Errorf("IdC 换 token: %w", err)
	}
	var tok idcTokenResponse
	if err := json.Unmarshal(raw, &tok); err != nil {
		return nil, fmt.Errorf("解析 IdC token 响应: %w", err)
	}
	if strings.TrimSpace(tok.AccessToken) == "" {
		return nil, errors.New("IdC token 响应里没有 accessToken")
	}
	if strings.TrimSpace(tok.RefreshToken) == "" {
		// 没有 refresh token 的 IdC 凭证一小时后就废了。
		return nil, errors.New("IdC token 响应里没有 refreshToken")
	}
	return &tok, nil
}

// idcResolveProfileARN 用刚拿到的 token 查可用 profile。
//
// social 登录时 profileArn 由 /oauth/token 直接下发，IdC 这条没有，
// 必须显式查一次。多个 profile 时取第一个，与 Kiro 的 ProfileArnGuard 一致。
func idcResolveProfileARN(client *http.Client, file *authTokenFile) (string, error) {
	// 这一步要用 Q 数据面，而端点是从 profileArn 推的——此刻还没有。
	// 先按 region 直连，拿到 profile 后再回填。
	creds := &kiro.Credentials{
		AccessToken: file.AccessToken,
		AuthMethod:  kiro.AuthMethodIdC,
		Region:      file.Region,
		MachineID:   file.MachineID,
		KiroVersion: file.KiroVersion,
		// 占位：仅为通过 NewClient 的校验，真实值就是本函数要查的东西。
		ProfileARN: fmt.Sprintf("arn:aws:codewhisperer:%s:000000000000:profile/BOOTSTRAP", credsRegion(file.Region)),
	}

	qClient, err := kiro.NewClient(client, creds)
	if err != nil {
		return "", fmt.Errorf("构造 Q 客户端: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), verifyTimeout)
	defer cancel()

	profiles, err := qClient.ListAvailableProfiles(ctx)
	if err != nil {
		return "", fmt.Errorf("查询可用 profile: %w", err)
	}
	if len(profiles) == 0 {
		return "", errors.New("该账号没有任何可用 profile，请联系管理员开通")
	}
	if len(profiles) > 1 {
		fmt.Printf("该账号有 %d 个 profile，取第一个：\n", len(profiles))
		for _, p := range profiles {
			fmt.Printf("  - %s %s\n", p.ARN, p.ProfileName)
		}
	}
	return profiles[0].ARN, nil
}

func credsRegion(region string) string {
	if strings.TrimSpace(region) == "" {
		return kiro.DefaultRegion
	}
	return region
}

// isIdCLoginOption 报告 portal 回调是否要转交 IdC 流程。
func isIdCLoginOption(loginOption string) bool {
	switch loginOption {
	case "builderid", "awsidc", "internal":
		return true
	default:
		return false
	}
}

// idcProviderFromLoginOption 把 portal 的 login_option 映射成凭证里的 provider。
func idcProviderFromLoginOption(loginOption string) string {
	switch loginOption {
	case "builderid":
		return "BuilderId"
	case "internal":
		return "Internal"
	case "awsidc":
		return "Enterprise"
	default:
		return "BuilderId"
	}
}

func idcPostJSON(client *http.Client, endpoint string, body any) ([]byte, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("编码请求: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), exchangeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("构造请求: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return nil, fmt.Errorf("读取响应: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}
