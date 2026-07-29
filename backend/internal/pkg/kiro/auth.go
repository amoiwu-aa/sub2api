package kiro

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	// AuthMethodSocial 对应 Kiro 的 Google / GitHub 登录。
	AuthMethodSocial = "social"
	// AuthMethodIdC 对应 Enterprise / BuilderId（AWS IAM Identity Center）。
	AuthMethodIdC = "idc"
	// AuthMethodExternalIDP 对应企业自建 IdP。
	//
	// Kiro 有三种 authMethod（social / IdC / external_idp），只有这一种
	// 才发 TokenType: EXTERNAL_IDP 请求头。本包暂不支持用它登录与续期，
	// 定义出来是为了让请求头的判断条件写对——早先把它并进 IdC，导致所有
	// IdC 账号都发了这个头，实测上游会直接 403。
	AuthMethodExternalIDP = "external_idp"

	// SocialRefreshURL 见 kiro-proxy token-reader.js 的 SOCIAL_REFRESH_URL。
	SocialRefreshURL = "https://prod.us-east-1.auth.desktop.kiro.dev/refreshToken"

	// DefaultRegion 是 profileArn 无法推导 region 时的兜底。
	DefaultRegion = "us-east-1"
	// DefaultVersion 用于 User-Agent，对齐本机实测的 Kiro 版本。
	// 这个值会原样出现在发往上游的 UA 里，落后太多等于自报家门。
	DefaultVersion = "1.0.212"

	// DefaultMachineID 是取不到 machineId 时的兜底，取自 Kiro 扩展的
	// DEFAULT_MACHINE_ID。以前这里写的是 "ringstar"——产品名直接出现在
	// UA 里，上游日志中一眼就能把代理流量摘出来。
	//
	// 更好的做法是每个账号带自己的 machineId（kirologin -ringstar 产出的
	// 凭证就有），这里只是最后的兜底。
	DefaultMachineID = "UNDETERMINED_MACHINE_ID"

	// RefreshBuffer 是提前刷新的缓冲期。
	// 10 分钟对齐 Kiro 扩展的 REFRESH_BEFORE_EXPIRY_SECONDS，
	// 比原来的 5 分钟多留一倍余量给代理链路上的重试。
	RefreshBuffer = 10 * time.Minute

	// ProviderInternal 触发 redirect-for-internal 请求头。
	ProviderInternal = "Internal"

	defaultRefreshTimeout = 30 * time.Second
	maxRefreshBody        = 1 << 20
)

var (
	// ErrRefreshTokenMissing 表示凭证里没有 refresh_token，只能重新导入。
	ErrRefreshTokenMissing = errors.New("kiro refresh token is missing")
	// ErrClientRegistrationMissing 表示 IdC 账号缺少 clientId/clientSecret。
	// 服务器上没有 ~/.aws/sso/cache，这两项必须在导入时落库。
	ErrClientRegistrationMissing = errors.New("kiro idc client registration is missing")
	// ErrUnknownAuthMethod 表示 auth_method 既不是 social 也不是 idc。
	ErrUnknownAuthMethod = errors.New("kiro auth method is unknown")
	// ErrAccessTokenMissing 表示导入的 JSON 里没有 accessToken。
	ErrAccessTokenMissing = errors.New("kiro access token is missing")
	// ErrProfileARNMissing 表示缺少 profileArn；Q API 的每个请求都要带它。
	ErrProfileARNMissing = errors.New("kiro profile arn is missing")
)

// RefreshError 携带上游刷新接口的 HTTP 状态码，供调用方区分
// 「凭证已失效需重新登录」（4xx）与「上游抖动可重试」（5xx / 网络错误）。
type RefreshError struct {
	Status int
	Body   string
}

func (e *RefreshError) Error() string {
	body := strings.TrimSpace(e.Body)
	if len(body) > 512 {
		body = body[:512]
	}
	return fmt.Sprintf("kiro token refresh failed (HTTP %d): %s", e.Status, body)
}

// Unauthorized 报告该错误是否意味着凭证本身不可用（而非上游临时故障）。
func (e *RefreshError) Unauthorized() bool {
	return e.Status == http.StatusBadRequest ||
		e.Status == http.StatusUnauthorized ||
		e.Status == http.StatusForbidden
}

// HTTPClient 是本包需要的最小传输面。
//
// 所有出网调用都必须由调用方注入 client：账号可能绑定了代理，包内
// 自建 http.DefaultClient 会让请求从服务器出口 IP 直连上游。
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

// Credentials 是 kiro 账号在 accounts.credentials JSONB 里的形状。
// 字段名与落库的 key 一一对应（snake_case，与 Grok 对齐）。
type Credentials struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	AuthMethod   string
	ProfileARN   string
	Provider     string
	Region       string
	ClientID     string
	ClientSecret string
	MachineID    string
	KiroVersion  string
}

// authTokenFile 兼容 kiro-auth-token.json 的 camelCase 与 RingStar 落库的 snake_case。
// 用户可能直接粘贴 Kiro 写在 ~/.aws/sso/cache 下的原始文件，也可能粘贴我们导出的凭证。
type authTokenFile struct {
	AccessToken   string `json:"accessToken"`
	AccessToken2  string `json:"access_token"`
	RefreshToken  string `json:"refreshToken"`
	RefreshToken2 string `json:"refresh_token"`
	ExpiresAt     string `json:"expiresAt"`
	ExpiresAt2    string `json:"expires_at"`
	AuthMethod    string `json:"authMethod"`
	AuthMethod2   string `json:"auth_method"`
	ProfileARN    string `json:"profileArn"`
	ProfileARN2   string `json:"profile_arn"`
	Provider      string `json:"provider"`
	Region        string `json:"region"`
	ClientID      string `json:"clientId"`
	ClientID2     string `json:"client_id"`
	ClientSecret  string `json:"clientSecret"`
	ClientSecret2 string `json:"client_secret"`
	MachineID     string `json:"machineId"`
	MachineID2    string `json:"machine_id"`
	KiroVersion   string `json:"kiroVersion"`
	KiroVersion2  string `json:"kiro_version"`

	// ClientIDHash 指向本机 SSO cache 文件名。服务器上没有那个文件，
	// 保留它只是为了在报错时告诉用户「这个账号是 IdC，还需要贴 client 注册」。
	ClientIDHash string `json:"clientIdHash"`
}

// ClientRegistration 是 ~/.aws/sso/cache/{clientIdHash}.json 里的 IdC 客户端注册。
type ClientRegistration struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

// ParseAuthToken 解析用户粘贴的 kiro-auth-token.json。
// 只做解析与归一化，不校验完整性——调用方拿到后可能还要合并 client registration，
// 全部字段齐了再调 Validate。
func ParseAuthToken(raw []byte) (*Credentials, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, errors.New("kiro auth token json is empty")
	}
	var file authTokenFile
	if err := json.Unmarshal(trimmed, &file); err != nil {
		return nil, fmt.Errorf("parse kiro auth token json: %w", err)
	}

	creds := &Credentials{
		AccessToken:  firstNonEmpty(file.AccessToken, file.AccessToken2),
		RefreshToken: firstNonEmpty(file.RefreshToken, file.RefreshToken2),
		AuthMethod:   NormalizeAuthMethod(firstNonEmpty(file.AuthMethod, file.AuthMethod2)),
		ProfileARN:   firstNonEmpty(file.ProfileARN, file.ProfileARN2),
		Provider:     strings.TrimSpace(file.Provider),
		Region:       normalizeRegion(file.Region),
		ClientID:     firstNonEmpty(file.ClientID, file.ClientID2),
		ClientSecret: firstNonEmpty(file.ClientSecret, file.ClientSecret2),
		MachineID:    firstNonEmpty(file.MachineID, file.MachineID2),
		KiroVersion:  firstNonEmpty(file.KiroVersion, file.KiroVersion2),
	}
	if expiresAt := firstNonEmpty(file.ExpiresAt, file.ExpiresAt2); expiresAt != "" {
		parsed, err := parseTimestamp(expiresAt)
		if err != nil {
			return nil, fmt.Errorf("parse kiro expiresAt: %w", err)
		}
		creds.ExpiresAt = parsed
	}
	// clientIdHash 存在但没带 client 注册时，auth_method 多半是 IdC 却没写全。
	if creds.AuthMethod == "" && strings.TrimSpace(file.ClientIDHash) != "" {
		creds.AuthMethod = AuthMethodIdC
	}
	return creds, nil
}

// ParseClientRegistration 解析用户粘贴的 IdC client 注册 JSON。
func ParseClientRegistration(raw []byte) (*ClientRegistration, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, errors.New("kiro client registration json is empty")
	}
	var reg ClientRegistration
	if err := json.Unmarshal(trimmed, &reg); err != nil {
		return nil, fmt.Errorf("parse kiro client registration json: %w", err)
	}
	reg.ClientID = strings.TrimSpace(reg.ClientID)
	reg.ClientSecret = strings.TrimSpace(reg.ClientSecret)
	if reg.ClientID == "" || reg.ClientSecret == "" {
		return nil, ErrClientRegistrationMissing
	}
	return &reg, nil
}

// NormalizeAuthMethod 把 Social/social/IdC/idc 归一为本包的两个常量。
func NormalizeAuthMethod(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "social":
		return AuthMethodSocial
	case "idc":
		return AuthMethodIdC
	case "external_idp", "externalidp":
		return AuthMethodExternalIDP
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

// Validate 检查凭证是否足以调用上游并在过期后自行续期。
func (c *Credentials) Validate() error {
	if c == nil {
		return errors.New("kiro credentials are nil")
	}
	if strings.TrimSpace(c.AccessToken) == "" {
		return ErrAccessTokenMissing
	}
	if strings.TrimSpace(c.ProfileARN) == "" {
		return ErrProfileARNMissing
	}
	switch c.AuthMethod {
	case AuthMethodSocial:
		if strings.TrimSpace(c.RefreshToken) == "" {
			return ErrRefreshTokenMissing
		}
	case AuthMethodIdC:
		if strings.TrimSpace(c.RefreshToken) == "" {
			return ErrRefreshTokenMissing
		}
		// 服务器读不到本机 ~/.aws/sso/cache，client 注册必须随凭证落库，
		// 否则 access token 一过期账号就永久失效。
		if strings.TrimSpace(c.ClientID) == "" || strings.TrimSpace(c.ClientSecret) == "" {
			return ErrClientRegistrationMissing
		}
	default:
		return fmt.Errorf("%w: %q", ErrUnknownAuthMethod, c.AuthMethod)
	}
	return nil
}

// NeedsRefresh 报告 access token 是否已过期或在 buffer 内即将过期。
func (c *Credentials) NeedsRefresh(now time.Time, buffer time.Duration) bool {
	if c == nil || strings.TrimSpace(c.AccessToken) == "" {
		return true
	}
	if c.ExpiresAt.IsZero() {
		return true
	}
	if buffer < 0 {
		buffer = 0
	}
	return c.ExpiresAt.Before(now.Add(buffer))
}

// QRegion 返回调用 Q API 应该用的 region。
//
// 注意与 Credentials.Region 的区别：后者是 IdC OIDC 刷新用的 region，
// 二者互相独立（反代 token-reader.js 与 q-client.js 分别使用）。
func (c *Credentials) QRegion() string {
	if c == nil {
		return DefaultRegion
	}
	if region := RegionFromARN(c.ProfileARN); region != "" {
		return region
	}
	return DefaultRegion
}

// OIDCRegion 返回 IdC 刷新用的 region。
func (c *Credentials) OIDCRegion() string {
	if c == nil {
		return DefaultRegion
	}
	if region := normalizeRegion(c.Region); region != "" {
		return region
	}
	return DefaultRegion
}

// UserAgent 是调用 Q / CodeWhisperer 数据面时用的 UA：
// `KiroIDE {version} {machineId}`，空格分隔，对应扩展的 getCustomUserAgent()。
//
// 注意别和 AuthUserAgent 混用：auth 服务那边是连字符格式，两者不通用。
func (c *Credentials) UserAgent() string {
	version, machineID := c.uaParts()
	return "KiroIDE " + version + " " + machineID
}

// AuthUserAgent 是调用 auth 服务（/oauth/token、/refreshToken、/logout）时用的 UA：
// `KiroIDE-{version}-{machineId}`，连字符分隔。
//
// 对应扩展 sso-oidc-client 里的
// `USER_AGENT = KiroIDE-${kiroVersion}-${machineId}`，
// 与数据面那套空格格式是两回事。
func (c *Credentials) AuthUserAgent() string {
	version, machineID := c.uaParts()
	return "KiroIDE-" + version + "-" + machineID
}

func (c *Credentials) uaParts() (version, machineID string) {
	version = DefaultVersion
	if c != nil {
		if v := strings.TrimSpace(c.KiroVersion); v != "" {
			version = v
		}
		machineID = strings.TrimSpace(c.MachineID)
	}
	if machineID == "" {
		machineID = DefaultMachineID
	}
	return version, machineID
}

// IsInternalProvider 报告是否需要 redirect-for-internal 请求头。
func (c *Credentials) IsInternalProvider() bool {
	return c != nil && strings.EqualFold(strings.TrimSpace(c.Provider), ProviderInternal)
}

// ToMap 序列化为 accounts.credentials 的形状。空值不落库，避免用空串
// 覆盖 MergeCredentials 里已有的值。
func (c *Credentials) ToMap() map[string]any {
	if c == nil {
		return map[string]any{}
	}
	out := map[string]any{}
	putNonEmpty(out, "access_token", c.AccessToken)
	putNonEmpty(out, "refresh_token", c.RefreshToken)
	putNonEmpty(out, "auth_method", c.AuthMethod)
	putNonEmpty(out, "profile_arn", c.ProfileARN)
	putNonEmpty(out, "provider", c.Provider)
	putNonEmpty(out, "region", c.Region)
	putNonEmpty(out, "client_id", c.ClientID)
	putNonEmpty(out, "client_secret", c.ClientSecret)
	putNonEmpty(out, "machine_id", c.MachineID)
	putNonEmpty(out, "kiro_version", c.KiroVersion)
	if !c.ExpiresAt.IsZero() {
		out["expires_at"] = c.ExpiresAt.UTC().Format(time.RFC3339)
	}
	return out
}

// CredentialsFromMap 从 accounts.credentials 反序列化。
func CredentialsFromMap(m map[string]any) *Credentials {
	creds := &Credentials{
		AccessToken:  mapString(m, "access_token"),
		RefreshToken: mapString(m, "refresh_token"),
		AuthMethod:   NormalizeAuthMethod(mapString(m, "auth_method")),
		ProfileARN:   firstNonEmpty(mapString(m, "profile_arn"), mapString(m, "profileArn")),
		Provider:     mapString(m, "provider"),
		Region:       normalizeRegion(mapString(m, "region")),
		ClientID:     mapString(m, "client_id"),
		ClientSecret: mapString(m, "client_secret"),
		MachineID:    mapString(m, "machine_id"),
		KiroVersion:  mapString(m, "kiro_version"),
	}
	if expiresAt := mapString(m, "expires_at"); expiresAt != "" {
		if parsed, err := parseTimestamp(expiresAt); err == nil {
			creds.ExpiresAt = parsed
		}
	}
	return creds
}

// refreshResponse 是 Social 与 IdC 两条链共用的响应形状。
type refreshResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresIn    int64  `json:"expiresIn"`
	ProfileARN   string `json:"profileArn"`
}

// Refresh 按 auth_method 分派到 Social 或 IdC 刷新链，返回更新后的凭证副本。
// 入参不会被修改。
func Refresh(ctx context.Context, client HTTPClient, creds *Credentials) (*Credentials, error) {
	if creds == nil {
		return nil, errors.New("kiro credentials are nil")
	}
	if client == nil {
		return nil, errors.New("kiro http client is nil")
	}
	if strings.TrimSpace(creds.RefreshToken) == "" {
		return nil, ErrRefreshTokenMissing
	}

	switch creds.AuthMethod {
	case AuthMethodSocial:
		return refreshSocial(ctx, client, creds)
	case AuthMethodIdC:
		return refreshIdC(ctx, client, creds)
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownAuthMethod, creds.AuthMethod)
	}
}

func refreshSocial(ctx context.Context, client HTTPClient, creds *Credentials) (*Credentials, error) {
	body := map[string]string{"refreshToken": creds.RefreshToken}
	resp, err := postJSON(ctx, client, SocialRefreshURL, body, creds.AuthUserAgent())
	if err != nil {
		return nil, err
	}
	return creds.applyRefresh(resp), nil
}

func refreshIdC(ctx context.Context, client HTTPClient, creds *Credentials) (*Credentials, error) {
	if strings.TrimSpace(creds.ClientID) == "" || strings.TrimSpace(creds.ClientSecret) == "" {
		return nil, ErrClientRegistrationMissing
	}
	endpoint, err := OIDCTokenURL(creds.OIDCRegion())
	if err != nil {
		return nil, err
	}
	body := map[string]string{
		"clientId":     creds.ClientID,
		"clientSecret": creds.ClientSecret,
		"grantType":    "refresh_token",
		"refreshToken": creds.RefreshToken,
	}
	resp, err := postJSON(ctx, client, endpoint, body, creds.AuthUserAgent())
	if err != nil {
		return nil, err
	}
	return creds.applyRefresh(resp), nil
}

// applyRefresh 把刷新响应合并进凭证副本。
// refreshToken 与 profileArn 只在上游返回了新值时才覆盖——两条链都可能省略它们。
func (c *Credentials) applyRefresh(resp *refreshResponse) *Credentials {
	updated := *c
	updated.AccessToken = resp.AccessToken
	if token := strings.TrimSpace(resp.RefreshToken); token != "" {
		updated.RefreshToken = token
	}
	if arn := strings.TrimSpace(resp.ProfileARN); arn != "" {
		updated.ProfileARN = arn
	}
	expiresIn := resp.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	updated.ExpiresAt = timeNow().Add(time.Duration(expiresIn) * time.Second).UTC()
	return &updated
}

func postJSON(ctx context.Context, client HTTPClient, endpoint string, body any, userAgent string) (*refreshResponse, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode kiro refresh request: %w", err)
	}

	reqCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, defaultRefreshTimeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build kiro refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("kiro refresh request: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxRefreshBody))
		_ = resp.Body.Close()
	}()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxRefreshBody))
	if err != nil {
		return nil, fmt.Errorf("read kiro refresh response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &RefreshError{Status: resp.StatusCode, Body: string(raw)}
	}

	var parsed refreshResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("decode kiro refresh response: %w", err)
	}
	if strings.TrimSpace(parsed.AccessToken) == "" {
		return nil, errors.New("kiro refresh response has no accessToken")
	}
	return &parsed, nil
}

// regionPattern 限制 region 只能是 AWS 风格的小写标识。
// region 来自用户粘贴的 JSON，直接拼进 URL 会带来 SSRF 风险。
var regionPattern = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-\d+$`)

// OIDCTokenURL 构造 IdC 刷新端点，并校验 region 合法。
func OIDCTokenURL(region string) (string, error) {
	normalized := normalizeRegion(region)
	if !regionPattern.MatchString(normalized) {
		return "", fmt.Errorf("kiro region %q is not a valid aws region", region)
	}
	return "https://oidc." + normalized + ".amazonaws.com/token", nil
}

// QEndpoint 构造 Amazon Q 的服务端点，并校验 region 合法。
func QEndpoint(region string) (string, error) {
	normalized := normalizeRegion(region)
	if !regionPattern.MatchString(normalized) {
		return "", fmt.Errorf("kiro region %q is not a valid aws region", region)
	}
	return "https://q." + normalized + ".amazonaws.com", nil
}

// RegionFromARN 取 ARN 的第 4 段作为 region：arn:aws:codewhisperer:us-east-1:...
func RegionFromARN(arn string) string {
	parts := strings.Split(strings.TrimSpace(arn), ":")
	if len(parts) < 4 {
		return ""
	}
	return normalizeRegion(parts[3])
}

// timeNow 可在测试中替换。
var timeNow = time.Now

func normalizeRegion(region string) string {
	return strings.ToLower(strings.TrimSpace(region))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func putNonEmpty(target map[string]any, key, value string) {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		target[key] = trimmed
	}
}

func mapString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	value, ok := m[key]
	if !ok {
		return ""
	}
	str, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(str)
}

// parseTimestamp 接受 RFC3339 与 JS `new Date().toISOString()` 的输出。
func parseTimestamp(value string) (time.Time, error) {
	trimmed := strings.TrimSpace(value)
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.000Z"} {
		if parsed, err := time.Parse(layout, trimmed); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized timestamp %q", trimmed)
}
