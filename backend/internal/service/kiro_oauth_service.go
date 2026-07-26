package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
)

const (
	kiroHTTPTimeout           = 30 * time.Second
	kiroHTTPHeaderTimeout     = 20 * time.Second
	kiroImportRefreshDeadline = 5 * time.Minute
)

// KiroOAuthService 负责 kiro 账号的凭证导入与刷新。
//
// Kiro 没有可服务端拉起的 OAuth 流程（反代同样没有），凭证只能由运营方
// 从本机 Kiro 的 ~/.aws/sso/cache/kiro-auth-token.json 复制粘贴进来。
type KiroOAuthService struct {
	proxyRepo ProxyRepository
}

func NewKiroOAuthService(proxyRepo ProxyRepository) *KiroOAuthService {
	return &KiroOAuthService{proxyRepo: proxyRepo}
}

// KiroImportInput 是后台粘贴式建号的入参。
type KiroImportInput struct {
	// TokenJSON 是 kiro-auth-token.json 的原文。
	TokenJSON string
	// ClientRegistrationJSON 是 IdC 账号的 {clientId, clientSecret}。
	// 服务器上没有本机 SSO cache，IdC 必须显式提供，否则 token 过期即永久失效。
	ClientRegistrationJSON string
	ProxyID                *int64
}

// KiroTokenInfo 是导入/刷新的结果，供 handler 组装 accounts.credentials。
type KiroTokenInfo struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
	AuthMethod   string    `json:"auth_method"`
	ProfileARN   string    `json:"profile_arn"`
	Provider     string    `json:"provider,omitempty"`
	Region       string    `json:"region,omitempty"`
	QRegion      string    `json:"q_region"`
	ClientID     string    `json:"client_id,omitempty"`
	ClientSecret string    `json:"client_secret,omitempty"`
	MachineID    string    `json:"machine_id,omitempty"`
	KiroVersion  string    `json:"kiro_version,omitempty"`
	// Refreshed 报告本次导入是否顺带跑通了一次真实刷新。
	Refreshed bool `json:"refreshed"`
}

// Import 解析粘贴的凭证并做一次连通性验证。
//
// 已过期或临近过期的 token 会就地刷新一次：这既让新建的账号立刻可用，
// 也顺带验证了刷新链在服务器出网路径（含账号代理）上确实能走通。
func (s *KiroOAuthService) Import(ctx context.Context, input KiroImportInput) (*KiroTokenInfo, error) {
	creds, err := kiro.ParseAuthToken([]byte(input.TokenJSON))
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadRequest, "KIRO_TOKEN_INVALID_JSON", "%v", err)
	}

	if registration := strings.TrimSpace(input.ClientRegistrationJSON); registration != "" {
		reg, regErr := kiro.ParseClientRegistration([]byte(registration))
		if regErr != nil {
			return nil, infraerrors.Newf(http.StatusBadRequest, "KIRO_CLIENT_REGISTRATION_INVALID", "%v", regErr)
		}
		creds.ClientID = reg.ClientID
		creds.ClientSecret = reg.ClientSecret
	}

	if err := creds.Validate(); err != nil {
		return nil, kiroValidationError(err)
	}

	info := kiroTokenInfoFrom(creds, false)
	if !creds.NeedsRefresh(time.Now(), kiro.RefreshBuffer) {
		return info, nil
	}

	refreshed, err := s.refresh(ctx, creds, input.ProxyID)
	if err != nil {
		return nil, err
	}
	return kiroTokenInfoFrom(refreshed, true), nil
}

// RefreshAccountToken 用账号自己的代理刷新其 access token。
func (s *KiroOAuthService) RefreshAccountToken(ctx context.Context, account *Account) (*KiroTokenInfo, error) {
	if account == nil || account.Platform != PlatformKiro {
		return nil, infraerrors.New(http.StatusBadRequest, "KIRO_INVALID_ACCOUNT", "account is not a Kiro account")
	}
	if account.Type != AccountTypeOAuth {
		return nil, infraerrors.New(http.StatusBadRequest, "KIRO_INVALID_ACCOUNT_TYPE", "account is not an OAuth account")
	}
	// 配置了代理却取不到代理对象，属于硬凭证失败：绝不能退化成直连，
	// 否则该账号的流量会从服务器出口 IP 打到上游。
	if account.ProxyID != nil && account.Proxy == nil && s.proxyRepo == nil {
		return nil, infraerrors.New(http.StatusBadRequest, "KIRO_PROXY_NOT_AVAILABLE", "configured proxy is not available")
	}

	creds := kiro.CredentialsFromMap(account.Credentials)
	if strings.TrimSpace(creds.RefreshToken) == "" {
		return nil, infraerrors.New(http.StatusBadRequest, "KIRO_NO_REFRESH_TOKEN", "no refresh token available")
	}

	refreshed, err := s.refresh(ctx, creds, account.ProxyID)
	if err != nil {
		return nil, err
	}
	return kiroTokenInfoFrom(refreshed, true), nil
}

func (s *KiroOAuthService) refresh(ctx context.Context, creds *kiro.Credentials, proxyID *int64) (*kiro.Credentials, error) {
	proxyURL, err := s.proxyURL(ctx, proxyID)
	if err != nil {
		return nil, err
	}
	client, err := httpclient.GetClient(httpclient.Options{
		ProxyURL:              proxyURL,
		Timeout:               kiroHTTPTimeout,
		ResponseHeaderTimeout: kiroHTTPHeaderTimeout,
	})
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadRequest, "KIRO_PROXY_INVALID", "invalid proxy configuration: %v", err)
	}

	refreshCtx, cancel := context.WithTimeout(ctx, kiroImportRefreshDeadline)
	defer cancel()

	refreshed, err := kiro.Refresh(refreshCtx, client, creds)
	if err != nil {
		return nil, kiroRefreshError(err)
	}
	return refreshed, nil
}

func (s *KiroOAuthService) proxyURL(ctx context.Context, proxyID *int64) (string, error) {
	if proxyID == nil {
		return "", nil
	}
	if s.proxyRepo == nil {
		return "", infraerrors.New(http.StatusBadRequest, "KIRO_PROXY_NOT_AVAILABLE", "proxy repository is not available")
	}
	proxy, err := s.proxyRepo.GetByID(ctx, *proxyID)
	if err != nil {
		if errors.Is(err, ErrProxyNotFound) {
			return "", infraerrors.New(http.StatusBadRequest, "KIRO_PROXY_NOT_FOUND", "configured proxy was not found")
		}
		return "", infraerrors.New(http.StatusServiceUnavailable, "KIRO_PROXY_LOOKUP_FAILED", "proxy lookup is temporarily unavailable")
	}
	if proxy == nil {
		return "", infraerrors.New(http.StatusBadRequest, "KIRO_PROXY_NOT_FOUND", "configured proxy was not found")
	}
	return proxy.URL(), nil
}

// BuildAccountCredentials 把 token info 转成 accounts.credentials 的形状。
func (s *KiroOAuthService) BuildAccountCredentials(info *KiroTokenInfo) map[string]any {
	if info == nil {
		return nil
	}
	return (&kiro.Credentials{
		AccessToken:  info.AccessToken,
		RefreshToken: info.RefreshToken,
		ExpiresAt:    info.ExpiresAt,
		AuthMethod:   info.AuthMethod,
		ProfileARN:   info.ProfileARN,
		Provider:     info.Provider,
		Region:       info.Region,
		ClientID:     info.ClientID,
		ClientSecret: info.ClientSecret,
		MachineID:    info.MachineID,
		KiroVersion:  info.KiroVersion,
	}).ToMap()
}

func kiroTokenInfoFrom(creds *kiro.Credentials, refreshed bool) *KiroTokenInfo {
	if creds == nil {
		return nil
	}
	return &KiroTokenInfo{
		AccessToken:  creds.AccessToken,
		RefreshToken: creds.RefreshToken,
		ExpiresAt:    creds.ExpiresAt,
		AuthMethod:   creds.AuthMethod,
		ProfileARN:   creds.ProfileARN,
		Provider:     creds.Provider,
		Region:       creds.Region,
		QRegion:      creds.QRegion(),
		ClientID:     creds.ClientID,
		ClientSecret: creds.ClientSecret,
		MachineID:    creds.MachineID,
		KiroVersion:  creds.KiroVersion,
		Refreshed:    refreshed,
	}
}

func kiroValidationError(err error) error {
	switch {
	case errors.Is(err, kiro.ErrClientRegistrationMissing):
		return infraerrors.New(http.StatusBadRequest, "KIRO_CLIENT_REGISTRATION_REQUIRED",
			"IdC accounts must include clientId and clientSecret; the server cannot read the local AWS SSO cache")
	case errors.Is(err, kiro.ErrAccessTokenMissing):
		return infraerrors.New(http.StatusBadRequest, "KIRO_ACCESS_TOKEN_REQUIRED", "accessToken is missing from the pasted token JSON")
	case errors.Is(err, kiro.ErrRefreshTokenMissing):
		return infraerrors.New(http.StatusBadRequest, "KIRO_REFRESH_TOKEN_REQUIRED", "refreshToken is missing from the pasted token JSON")
	case errors.Is(err, kiro.ErrProfileARNMissing):
		return infraerrors.New(http.StatusBadRequest, "KIRO_PROFILE_ARN_REQUIRED", "profileArn is missing; copy it from profile.json if the token file does not carry it")
	case errors.Is(err, kiro.ErrUnknownAuthMethod):
		return infraerrors.Newf(http.StatusBadRequest, "KIRO_AUTH_METHOD_UNSUPPORTED", "%v", err)
	default:
		return infraerrors.Newf(http.StatusBadRequest, "KIRO_TOKEN_INVALID", "%v", err)
	}
}

// kiroRefreshError 把上游状态码翻译成「凭证失效」与「稍后重试」两类，
// 让后台刷新与运营侧能区分要不要重新导入凭证。
func kiroRefreshError(err error) error {
	var refreshErr *kiro.RefreshError
	if errors.As(err, &refreshErr) {
		if refreshErr.Unauthorized() {
			return infraerrors.Newf(http.StatusUnauthorized, "KIRO_REFRESH_UNAUTHORIZED",
				"kiro credentials were rejected by the upstream; re-import the token: %v", refreshErr)
		}
		return infraerrors.Newf(http.StatusServiceUnavailable, "KIRO_REFRESH_UPSTREAM_ERROR", "%v", refreshErr)
	}
	if errors.Is(err, kiro.ErrClientRegistrationMissing) {
		return infraerrors.New(http.StatusBadRequest, "KIRO_CLIENT_REGISTRATION_REQUIRED",
			"IdC accounts must include clientId and clientSecret; the server cannot read the local AWS SSO cache")
	}
	if errors.Is(err, kiro.ErrRefreshTokenMissing) {
		return infraerrors.New(http.StatusBadRequest, "KIRO_NO_REFRESH_TOKEN", "no refresh token available")
	}
	if errors.Is(err, kiro.ErrUnknownAuthMethod) {
		return infraerrors.Newf(http.StatusBadRequest, "KIRO_AUTH_METHOD_UNSUPPORTED", "%v", err)
	}
	return infraerrors.Newf(http.StatusServiceUnavailable, "KIRO_REFRESH_FAILED", "kiro token refresh failed: %v", err)
}
