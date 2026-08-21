package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
)

const (
	cursorHTTPTimeout       = 30 * time.Second
	cursorHTTPHeaderTimeout = 20 * time.Second
	// cursorRefreshDeadline 覆盖一次完整刷新，含可能的 web→session 兑换轮询。
	cursorRefreshDeadline = 2 * time.Minute
)

// CursorOAuthService 负责 cursor 账号的登录、导入与刷新。
type CursorOAuthService struct {
	proxyRepo    ProxyRepository
	sessionStore *cursor.SessionStore
}

func NewCursorOAuthService(proxyRepo ProxyRepository) *CursorOAuthService {
	return &CursorOAuthService{
		proxyRepo:    proxyRepo,
		sessionStore: cursor.NewSessionStore(),
	}
}

func (s *CursorOAuthService) Stop() {
	if s != nil && s.sessionStore != nil {
		s.sessionStore.Stop()
	}
}

// CursorLoginStart 是 start 端点的返回值。
//
// 有意不返回 verifier：它是 PKCE 的私密部分，留在服务端就够了，
// 交给浏览器只会多一个可被窃取的凭证。
type CursorLoginStart struct {
	LoginURL  string `json:"login_url"`
	SessionID string `json:"session_id"`
	UUID      string `json:"uuid"`
}

// CursorTokenInfo 是登录/导入/刷新的结果。
type CursorTokenInfo struct {
	AccessToken   string    `json:"access_token"`
	RefreshToken  string    `json:"refresh_token,omitempty"`
	UserID        string    `json:"user_id,omitempty"`
	Email         string    `json:"email,omitempty"`
	TokenType     string    `json:"token_type,omitempty"`
	ExpiresAt     time.Time `json:"expires_at,omitempty"`
	Source        string    `json:"source,omitempty"`
	AgentProfile  string    `json:"agent_profile,omitempty"`
	MachineID     string    `json:"machine_id,omitempty"`
	ClientVersion string    `json:"client_version,omitempty"`
	SandNamespace string    `json:"sand_namespace,omitempty"`
}

// StartLogin 生成 PKCE 与浏览器登录 URL，并把 verifier 留在服务端。
func (s *CursorOAuthService) StartLogin(ctx context.Context, proxyID *int64) (*CursorLoginStart, error) {
	return s.StartLoginForProfile(ctx, proxyID, cursor.AgentProfileIDE)
}

func (s *CursorOAuthService) StartLoginForProfile(ctx context.Context, proxyID *int64, profile cursor.AgentProfile) (*CursorLoginStart, error) {
	profile = cursor.ParseAgentProfile(string(profile))
	// 提前校验代理：等到 poll 阶段才发现代理不可用，用户已经在浏览器里登录过了。
	if _, err := s.proxyURL(ctx, proxyID); err != nil {
		return nil, err
	}

	pkce, err := cursor.GeneratePKCEForProfile(profile)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusInternalServerError, "CURSOR_PKCE_FAILED", "%v", err)
	}
	sessionID, err := cursor.NewSessionID()
	if err != nil {
		return nil, infraerrors.Newf(http.StatusInternalServerError, "CURSOR_SESSION_FAILED", "%v", err)
	}

	s.sessionStore.Set(sessionID, &cursor.LoginSession{
		Verifier:     pkce.Verifier,
		UUID:         pkce.UUID,
		LoginURL:     pkce.LoginURL,
		ProxyID:      proxyID,
		AgentProfile: profile,
		CreatedAt:    time.Now(),
	})

	return &CursorLoginStart{LoginURL: pkce.LoginURL, SessionID: sessionID, UUID: pkce.UUID}, nil
}

// ErrCursorLoginPending 表示浏览器侧尚未完成登录。
var ErrCursorLoginPending = errors.New("cursor login is still pending")

// PollLogin 查询一次登录状态。未完成时返回 ErrCursorLoginPending。
func (s *CursorOAuthService) PollLogin(ctx context.Context, sessionID string) (*CursorTokenInfo, error) {
	session, ok := s.sessionStore.Get(strings.TrimSpace(sessionID))
	if !ok {
		return nil, infraerrors.New(http.StatusBadRequest, "CURSOR_SESSION_NOT_FOUND", "login session was not found or has expired")
	}

	opts, err := s.options(ctx, session.ProxyID)
	if err != nil {
		return nil, err
	}
	opts.Profile = cursor.ParseAgentProfile(string(session.AgentProfile))

	tokens, err := cursor.PollOnce(ctx, opts, session.UUID, session.Verifier)
	if errors.Is(err, cursor.ErrPollPending) {
		return nil, ErrCursorLoginPending
	}
	if err != nil {
		return nil, cursorUpstreamError(err, "poll")
	}

	// 登录拿到的可能仍是 web 态，Agent 只认 session，这里就地换掉。
	var info *CursorTokenInfo
	if opts.Profile == cursor.AgentProfileSand {
		info = cursorTokenInfoForProfile(tokens, opts.Profile)
	} else {
		info, err = s.ensureSession(ctx, opts, tokens)
	}
	if err != nil {
		return nil, err
	}
	s.sessionStore.Delete(sessionID)
	return info, nil
}

// ImportToken 接受粘贴的 cookie / JWT 并确保换成 session 态。
func (s *CursorOAuthService) ImportToken(ctx context.Context, token string, proxyID *int64, selectedTeamID string) (*CursorTokenInfo, error) {
	return s.ImportTokenForProfile(ctx, token, proxyID, selectedTeamID, cursor.AgentProfileIDE)
}

func (s *CursorOAuthService) ImportTokenForProfile(
	ctx context.Context,
	token string,
	proxyID *int64,
	selectedTeamID string,
	profile cursor.AgentProfile,
) (*CursorTokenInfo, error) {
	profile = cursor.ParseAgentProfile(string(profile))
	if strings.TrimSpace(token) == "" {
		return nil, infraerrors.New(http.StatusBadRequest, "CURSOR_TOKEN_REQUIRED", "token is required")
	}
	opts, err := s.options(ctx, proxyID)
	if err != nil {
		return nil, err
	}
	opts.Profile = profile

	exchangeCtx, cancel := context.WithTimeout(ctx, cursorRefreshDeadline)
	defer cancel()

	tokens, err := cursor.ExchangeWebTokenToSession(exchangeCtx, opts, token, selectedTeamID)
	if err != nil {
		return nil, cursorImportError(err)
	}
	return cursorTokenInfoForProfile(tokens, profile), nil
}

func (s *CursorOAuthService) ImportSandCredentials(
	ctx context.Context,
	accessToken string,
	refreshToken string,
	machineID string,
	clientVersion string,
	namespace string,
	proxyID *int64,
) (*CursorTokenInfo, error) {
	accessToken = strings.TrimSpace(accessToken)
	refreshToken = strings.TrimSpace(refreshToken)
	if accessToken == "" {
		return nil, infraerrors.New(http.StatusBadRequest, "CURSOR_SAND_ACCESS_TOKEN_REQUIRED", "Sand access token is required")
	}
	lower := strings.ToLower(accessToken)
	if strings.HasPrefix(lower, "scoped:v1:") || strings.HasPrefix(lower, "plaintext:v1:") {
		return nil, infraerrors.New(http.StatusBadRequest, "CURSOR_SAND_TOKEN_ENCRYPTED", "sand-secrets.json contains an Electron safeStorage wrapper; paste the decrypted runtime token or use browser login")
	}
	if refreshToken != "" {
		lowerRefresh := strings.ToLower(refreshToken)
		if strings.HasPrefix(lowerRefresh, "scoped:v1:") || strings.HasPrefix(lowerRefresh, "plaintext:v1:") {
			return nil, infraerrors.New(http.StatusBadRequest, "CURSOR_SAND_TOKEN_ENCRYPTED", "sand-secrets.json contains an Electron safeStorage wrapper; paste the decrypted runtime token or use browser login")
		}
	}
	if _, err := s.options(ctx, proxyID); err != nil {
		return nil, err
	}
	info := cursorTokenInfoForProfile(&cursor.Tokens{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		Source:       "sand_import",
	}, cursor.AgentProfileSand)
	info.MachineID = strings.TrimSpace(machineID)
	info.ClientVersion = strings.TrimSpace(clientVersion)
	if info.ClientVersion == "" {
		info.ClientVersion = cursor.SandClientVersion
	}
	info.SandNamespace = strings.TrimSpace(namespace)
	if info.SandNamespace == "" {
		info.SandNamespace = "prod"
	}
	return info, nil
}

// RefreshAccountToken 用账号自己的代理刷新其令牌。
func (s *CursorOAuthService) RefreshAccountToken(ctx context.Context, account *Account) (*CursorTokenInfo, error) {
	if account == nil || account.Platform != PlatformCursor {
		return nil, infraerrors.New(http.StatusBadRequest, "CURSOR_INVALID_ACCOUNT", "account is not a Cursor account")
	}
	if account.Type != AccountTypeOAuth {
		return nil, infraerrors.New(http.StatusBadRequest, "CURSOR_INVALID_ACCOUNT_TYPE", "account is not an OAuth account")
	}

	opts, err := s.options(ctx, account.ProxyID)
	if err != nil {
		return nil, err
	}
	profile := account.CursorAgentProfile()
	opts.Profile = profile
	refreshCtx, cancel := context.WithTimeout(ctx, cursorRefreshDeadline)
	defer cancel()

	tokens, err := cursor.RefreshTokens(refreshCtx, opts,
		account.GetCursorAccessToken(), account.GetCursorRefreshToken())
	if err != nil {
		return nil, cursorRefreshError(err)
	}
	info := cursorTokenInfoForProfile(tokens, profile)
	if profile == cursor.AgentProfileSand {
		info.MachineID = account.CursorMachineID()
		info.ClientVersion = account.CursorClientVersion()
		info.SandNamespace = account.CursorSandNamespace()
	}
	return info, nil
}

// ensureSession 把 auth/poll 拿到的令牌保证成 session 态。
func (s *CursorOAuthService) ensureSession(ctx context.Context, opts *cursor.Options, tokens *cursor.Tokens) (*CursorTokenInfo, error) {
	if cursor.IsSessionToken(tokens.AccessToken) {
		return cursorTokenInfo(tokens), nil
	}
	exchangeCtx, cancel := context.WithTimeout(ctx, cursorRefreshDeadline)
	defer cancel()

	exchanged, err := cursor.ExchangeWebTokenToSession(exchangeCtx, opts, tokens.AccessToken, tokens.SelectedTeamID)
	if err != nil {
		return nil, cursorImportError(err)
	}
	return cursorTokenInfo(exchanged), nil
}

func (s *CursorOAuthService) options(ctx context.Context, proxyID *int64) (*cursor.Options, error) {
	proxyURL, err := s.proxyURL(ctx, proxyID)
	if err != nil {
		return nil, err
	}
	client, err := httpclient.GetClient(httpclient.Options{
		ProxyURL:              proxyURL,
		Timeout:               cursorHTTPTimeout,
		ResponseHeaderTimeout: cursorHTTPHeaderTimeout,
	})
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadRequest, "CURSOR_PROXY_INVALID", "invalid proxy configuration: %v", err)
	}
	return &cursor.Options{HTTPClient: client}, nil
}

func (s *CursorOAuthService) proxyURL(ctx context.Context, proxyID *int64) (string, error) {
	if proxyID == nil {
		return "", nil
	}
	if s.proxyRepo == nil {
		return "", infraerrors.New(http.StatusBadRequest, "CURSOR_PROXY_NOT_AVAILABLE", "proxy repository is not available")
	}
	proxy, err := s.proxyRepo.GetByID(ctx, *proxyID)
	if err != nil {
		if errors.Is(err, ErrProxyNotFound) {
			return "", infraerrors.New(http.StatusBadRequest, "CURSOR_PROXY_NOT_FOUND", "configured proxy was not found")
		}
		return "", infraerrors.New(http.StatusServiceUnavailable, "CURSOR_PROXY_LOOKUP_FAILED", "proxy lookup is temporarily unavailable")
	}
	if proxy == nil {
		return "", infraerrors.New(http.StatusBadRequest, "CURSOR_PROXY_NOT_FOUND", "configured proxy was not found")
	}
	return proxy.URL(), nil
}

// BuildAccountCredentials 把 token info 转成 accounts.credentials 的形状。
func (s *CursorOAuthService) BuildAccountCredentials(info *CursorTokenInfo) map[string]any {
	if info == nil {
		return nil
	}
	credentials := map[string]any{"access_token": info.AccessToken}
	if token := strings.TrimSpace(info.RefreshToken); token != "" {
		credentials["refresh_token"] = token
	}
	if userID := strings.TrimSpace(info.UserID); userID != "" {
		credentials["user_id"] = userID
	}
	if email := strings.TrimSpace(info.Email); email != "" {
		credentials["email"] = email
	}
	if !info.ExpiresAt.IsZero() {
		credentials["expires_at"] = info.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if profile := cursor.ParseAgentProfile(info.AgentProfile); profile == cursor.AgentProfileSand {
		credentials[CursorAgentProfileCredentialKey] = string(profile)
		if value := strings.TrimSpace(info.MachineID); value != "" {
			credentials[CursorMachineIDCredentialKey] = value
		}
		if value := strings.TrimSpace(info.ClientVersion); value != "" {
			credentials[CursorClientVersionCredentialKey] = value
		}
		if value := strings.TrimSpace(info.SandNamespace); value != "" {
			credentials[CursorSandNamespaceCredentialKey] = value
		}
	}
	return credentials
}

func cursorTokenInfo(tokens *cursor.Tokens) *CursorTokenInfo {
	if tokens == nil {
		return nil
	}
	claims := cursor.DecodeJWTClaims(tokens.AccessToken)
	info := &CursorTokenInfo{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		Source:       tokens.Source,
	}
	if claims != nil {
		info.TokenType = claims.Type
		info.ExpiresAt = claims.ExpiresAt
		info.UserID = strings.TrimPrefix(claims.Subject, "auth0|")
	}
	return info
}

func cursorTokenInfoForProfile(tokens *cursor.Tokens, profile cursor.AgentProfile) *CursorTokenInfo {
	info := cursorTokenInfo(tokens)
	if info == nil {
		return nil
	}
	profile = cursor.ParseAgentProfile(string(profile))
	if profile == cursor.AgentProfileSand {
		info.AgentProfile = string(profile)
	}
	return info
}

func cursorImportError(err error) error {
	switch {
	case errors.Is(err, cursor.ErrNotAJWT):
		return infraerrors.New(http.StatusBadRequest, "CURSOR_TOKEN_INVALID",
			"paste the WorkosCursorSessionToken cookie or a JWT starting with eyJ")
	case errors.Is(err, cursor.ErrTokenExpired):
		return infraerrors.New(http.StatusBadRequest, "CURSOR_TOKEN_EXPIRED", "the pasted token has expired; sign in again and copy a fresh one")
	case errors.Is(err, cursor.ErrPollTimeout):
		return infraerrors.New(http.StatusGatewayTimeout, "CURSOR_SESSION_EXCHANGE_TIMEOUT",
			"timed out waiting for Cursor to issue a session token")
	}
	return cursorUpstreamError(err, "session exchange")
}

// cursorRefreshError 区分「凭证已死需重新登录」与「上游抖动可重试」。
func cursorRefreshError(err error) error {
	if errors.Is(err, cursor.ErrShouldLogout) {
		return infraerrors.New(http.StatusUnauthorized, "CURSOR_REAUTH_REQUIRED",
			"Cursor rejected the credentials; sign in again for this account")
	}
	if errors.Is(err, cursor.ErrNoCredentials) {
		return infraerrors.New(http.StatusBadRequest, "CURSOR_NO_CREDENTIALS", "account has no usable Cursor credentials")
	}
	return cursorUpstreamError(err, "token refresh")
}

func cursorUpstreamError(err error, operation string) error {
	var httpErr *cursor.HTTPError
	if errors.As(err, &httpErr) {
		if httpErr.Unauthorized() {
			return infraerrors.Newf(http.StatusUnauthorized, "CURSOR_UNAUTHORIZED",
				"Cursor rejected the credentials during %s: %v", operation, httpErr)
		}
		return infraerrors.Newf(http.StatusServiceUnavailable, "CURSOR_UPSTREAM_ERROR", "%v", httpErr)
	}
	return infraerrors.Newf(http.StatusServiceUnavailable, "CURSOR_REQUEST_FAILED", "cursor %s failed: %v", operation, err)
}
