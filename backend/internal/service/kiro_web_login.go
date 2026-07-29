package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/google/uuid"
)

// Kiro 网页登录（portal）。
//
// 为什么不是「点一下就自动完成」：portal 只放行 localhost 回调，浏览器会把
// 授权码送到**管理员自己的机器**上，服务器收不到。所以这里和本项目
// Claude / Gemini / Grok 的 OAuth 一样走手动模式——回调页打不开是预期内的，
// 授权码就在地址栏里，粘回来即可。换 token 是纯服务端调用，不要求
// 调用方就是收到回调的那台机器。
//
// 相比原先只能去翻 ~/.aws/sso/cache/kiro-auth-token.json，这条路不需要
// 本机装 Kiro，也不需要运营方碰文件系统。
//
// # 两条链，两种回调次数
//
// social（Google / GitHub）只有一次回调：portal 直接给授权码，换完 token
// 连 profileArn 都一起下发了。
//
// IdC（Enterprise / BuilderId / Internal）需要两次：
//
//	回调 #1 由 portal 发出，不带授权码，只带 issuer_url + idc_region，
//	         告诉你接着该去哪个 Identity Center 实例；
//	回调 #2 由 AWS OIDC 发出，带真正的授权码，但不带 login_option。
//
// 所以登录会话必须能跨这两次回调存活，CompleteWebLogin 也就不是「一步到位」
// 而是「推进一步」——第一次返回 idc_required 与第二段登录链接，第二次才出凭证。

const (
	// kiroWebLoginTTL 是一次登录会话的有效期。授权码本身寿命更短，
	// 这里给足人工在浏览器里完成登录的时间。IdC 要走两段，所以这个
	// 窗口覆盖的是两次浏览器往返的总时长。
	kiroWebLoginTTL = 15 * time.Minute
	// kiroWebLoginMaxSessions 防止未完成的会话无限堆积。
	kiroWebLoginMaxSessions = 256
	// kiroWebLoginMaxAttempts 限制单个会话可以被推进多少次。
	//
	// 会话不再「取出即删」——IdC 的第二段要复用它，而且注册出来的
	// clientId/clientSecret 可以复用，失败后重开一次授权链接即可，
	// 不必从头再走一遍 portal。但保留会话就得防着被反复试探。
	kiroWebLoginMaxAttempts = 3
)

// kiroWebLoginPhase 标记会话在等哪一次回调。
type kiroWebLoginPhase string

const (
	// kiroPhasePortal 等 portal 的回调 #1。
	kiroPhasePortal kiroWebLoginPhase = "portal"
	// kiroPhaseIdC 等 AWS OIDC 的回调 #2。
	kiroPhaseIdC kiroWebLoginPhase = "idc"
)

// KiroWebLoginStatus 是 CompleteWebLogin 的推进结果。
const (
	// KiroWebLoginStatusCompleted 表示凭证已到手，可以建号了。
	KiroWebLoginStatusCompleted = "completed"
	// KiroWebLoginStatusIdCRequired 表示这是企业账号，还要再登录一次。
	KiroWebLoginStatusIdCRequired = "idc_required"
)

type kiroWebLoginSession struct {
	phase     kiroWebLoginPhase
	pkce      *kiro.PKCE
	proxyID   *int64
	createdAt time.Time
	attempts  int

	// 以下几项只有 idc 阶段有值。
	loginOption  string // 从回调 #1 透传，AWS 那次回调不带
	oidcBase     string
	idcRegion    string
	clientID     string
	clientSecret string
	// redirectURI 是写进第二段授权链接的那一个。
	//
	// 必须存下来而不是换 token 时重新拼：授权链接是服务端自己生成的，
	// 只有它知道当时用的是什么，差一个字符就会被 AWS 判 invalid_grant。
	// portal 那一段不存，因为它换 token 时的形状与进授权页时本来就不同
	// （要补上回调路径和 login_option），由 kiro.ExchangeRedirectURI 现拼。
	redirectURI string
}

// KiroWebLoginStore 保存进行中的登录会话。
//
// 与 CursorOAuthService 的会话存储一样放在进程内存里：会话寿命只有几分钟，
// 且必须与生成 PKCE 的那个进程绑定。多副本部署时，登录请求要落到同一个副本
// 才能完成——这是已知取舍，写在这里以免以后被当成 bug 排查。
//
// IdC 要走两段，这个窗口比 social 更长，多副本下更容易踩到。
type KiroWebLoginStore struct {
	mu       sync.Mutex
	sessions map[string]*kiroWebLoginSession
}

func NewKiroWebLoginStore() *KiroWebLoginStore {
	return &KiroWebLoginStore{sessions: make(map[string]*kiroWebLoginSession)}
}

func (s *KiroWebLoginStore) put(id string, sess *kiroWebLoginSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evictExpiredLocked()
	if len(s.sessions) >= kiroWebLoginMaxSessions {
		// 满了就丢掉最旧的一个，避免无界增长。
		var oldestID string
		var oldest time.Time
		for k, v := range s.sessions {
			if oldestID == "" || v.createdAt.Before(oldest) {
				oldestID, oldest = k, v.createdAt
			}
		}
		delete(s.sessions, oldestID)
	}
	s.sessions[id] = sess
}

// begin 记一次推进尝试并返回会话快照。
//
// 返回的是副本：调用方在网络往返期间持有它，不该看到别的请求的改动。
// 尝试次数用完就地作废，避免保留下来的会话被反复试探。
func (s *KiroWebLoginStore) begin(id string) (*kiroWebLoginSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evictExpiredLocked()
	sess, ok := s.sessions[id]
	if !ok {
		return nil, false
	}
	sess.attempts++
	if sess.attempts > kiroWebLoginMaxAttempts {
		delete(s.sessions, id)
		return nil, false
	}
	snapshot := *sess
	return &snapshot, true
}

// advance 把会话推进到下一阶段。尝试次数一并清零：进入新阶段等于换了一道题。
func (s *KiroWebLoginStore) advance(id string, mutate func(*kiroWebLoginSession)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return false
	}
	mutate(sess)
	sess.attempts = 0
	return true
}

// finish 在登录成功后删除会话。
func (s *KiroWebLoginStore) finish(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
}

func (s *KiroWebLoginStore) evictExpiredLocked() {
	cutoff := time.Now().Add(-kiroWebLoginTTL)
	for k, v := range s.sessions {
		if v.createdAt.Before(cutoff) {
			delete(s.sessions, k)
		}
	}
}

// KiroWebLoginStart 是 StartWebLogin 的返回值。
type KiroWebLoginStart struct {
	// LoginURL 由管理员在浏览器里打开。
	LoginURL string `json:"login_url"`
	// SessionID 在换 token 时回传，用于取回 verifier 与 state。
	SessionID string `json:"session_id"`
	// CallbackPrefix 让前端能提示「登录后地址栏会变成这个开头」。
	CallbackPrefix string `json:"callback_prefix"`
}

// KiroWebLoginResult 是 CompleteWebLogin 的返回值。
//
// Status 决定后面两组字段哪一组有值：completed 出凭证，
// idc_required 出第二段登录链接。
type KiroWebLoginResult struct {
	Status    string `json:"status"`
	SessionID string `json:"session_id"`

	// Status=completed。
	TokenInfo   *KiroTokenInfo `json:"token_info,omitempty"`
	Credentials map[string]any `json:"credentials,omitempty"`
	// Profiles 是 IdC 账号在 Q 上的可用 profile 列表，多于一个时取了第一个。
	// 只作展示用，让运营方知道选中的是哪一个。
	Profiles []KiroProfile `json:"profiles,omitempty"`

	// Status=idc_required。
	NextLoginURL   string `json:"next_login_url,omitempty"`
	CallbackPrefix string `json:"callback_prefix,omitempty"`
	// Provider 让前端能提示这是 Enterprise 还是 BuilderId。
	Provider string `json:"provider,omitempty"`
}

// KiroProfile 是返回给前端展示的 profile。
type KiroProfile struct {
	ARN  string `json:"arn"`
	Name string `json:"name,omitempty"`
}

// StartWebLogin 生成一次 portal 登录会话。
func (s *KiroOAuthService) StartWebLogin(_ context.Context, proxyID *int64) (*KiroWebLoginStart, error) {
	pkce, err := kiro.NewPKCE()
	if err != nil {
		return nil, infraerrors.New(http.StatusInternalServerError, "KIRO_PKCE_FAILED", "failed to prepare the login session").WithCause(err)
	}
	port := kiro.DefaultCallbackPort
	sessionID := uuid.NewString()
	s.webLogins.put(sessionID, &kiroWebLoginSession{
		phase:     kiroPhasePortal,
		pkce:      pkce,
		proxyID:   proxyID,
		createdAt: time.Now(),
	})
	return &KiroWebLoginStart{
		LoginURL:       kiro.BuildPortalURL(pkce, port),
		SessionID:      sessionID,
		CallbackPrefix: kiro.CallbackOrigin(port),
	}, nil
}

// CompleteWebLogin 用粘回来的回调地址把登录推进一步。
//
// social 一次就出凭证；IdC 第一次返回 idc_required 与第二段登录链接，
// 管理员再登录一次、把第二条回调粘回来（用同一个 session_id）才出凭证。
func (s *KiroOAuthService) CompleteWebLogin(ctx context.Context, sessionID, callback string) (*KiroWebLoginResult, error) {
	sessionID = strings.TrimSpace(sessionID)
	sess, ok := s.webLogins.begin(sessionID)
	if !ok {
		return nil, infraerrors.BadRequest("KIRO_LOGIN_SESSION_EXPIRED",
			"the login session has expired, was already used, or has failed too many times; start the login again")
	}

	params, err := kiro.ParseCallback(callback)
	if err != nil {
		return nil, infraerrors.BadRequest("KIRO_CALLBACK_INVALID", err.Error())
	}
	// state 只在回调里带回来时才校验：管理员可能只贴了裸 code。
	if params.State != "" && params.State != sess.pkce.State {
		return nil, infraerrors.BadRequest("KIRO_STATE_MISMATCH",
			"the callback does not belong to this login session; start the login again")
	}

	if sess.phase == kiroPhaseIdC {
		return s.completeIdCLeg(ctx, sessionID, sess, params)
	}
	return s.completePortalLeg(ctx, sessionID, sess, params)
}

// completePortalLeg 处理回调 #1：社交账号就地换 token，IdC 转入第二段。
func (s *KiroOAuthService) completePortalLeg(
	ctx context.Context,
	sessionID string,
	sess *kiroWebLoginSession,
	params *kiro.CallbackParams,
) (*KiroWebLoginResult, error) {
	if params.IsIdCHandoff() {
		return s.startIdCLeg(ctx, sessionID, sess, params)
	}
	// external_idp 要和企业自建 IdP 再做一次 OAuth，本项目没覆盖。
	// 放过去只会建出一个必然不可用的账号，不如在这里说清楚。
	if strings.EqualFold(strings.TrimSpace(params.LoginOption), kiro.AuthMethodExternalIDP) {
		return nil, infraerrors.BadRequest("KIRO_EXTERNAL_IDP_UNSUPPORTED",
			"this Kiro account signs in through a company IdP, which is not supported yet; use a Google / GitHub / IAM Identity Center account instead")
	}

	port := kiro.DefaultCallbackPort
	if params.Port > 0 {
		// 管理员本机若有 Kiro IDE 占用了 3128，回调会落到列表里靠后的端口上。
		// 换 token 的 redirect_uri 必须与浏览器实际访问的一致。
		port = params.Port
	}

	client, err := s.httpClientFor(ctx, sess.proxyID)
	if err != nil {
		return nil, err
	}

	token, err := kiro.ExchangeAuthorizationCode(
		ctx, client,
		kiro.PortalUserAgent("", ""),
		params.Code, sess.pkce.Verifier,
		kiro.ExchangeRedirectURI(port, params.LoginOption),
	)
	if err != nil {
		return nil, infraerrors.BadRequest("KIRO_CODE_EXCHANGE_FAILED", err.Error())
	}

	creds := &kiro.Credentials{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    token.ExpiresAt(time.Now()),
		AuthMethod:   kiro.AuthMethodSocial,
		ProfileARN:   token.ProfileARN,
		Provider:     providerFromLoginOption(params.LoginOption),
	}
	if creds.ProfileARN == "" {
		return nil, infraerrors.BadRequest("KIRO_PROFILE_ARN_MISSING",
			"the login succeeded but Kiro did not return a profileArn; this account cannot call the Q API")
	}

	s.webLogins.finish(sessionID)
	return s.completedResult(sessionID, creds, nil), nil
}

// startIdCLeg 把会话推进到第二段：注册 IdC 客户端并生成新的授权链接。
//
// 这里换一对全新的 PKCE：第二段是另一个授权服务器（AWS OIDC）上的
// 独立 authorization_code 流程，复用 portal 那对既没必要也不安全。
func (s *KiroOAuthService) startIdCLeg(
	ctx context.Context,
	sessionID string,
	sess *kiroWebLoginSession,
	params *kiro.CallbackParams,
) (*KiroWebLoginResult, error) {
	if strings.TrimSpace(params.IssuerURL) == "" {
		return nil, infraerrors.BadRequest("KIRO_IDC_ISSUER_MISSING",
			"this is an IAM Identity Center account but the callback carries no issuer_url; paste the full callback URL from the address bar")
	}
	oidcBase, err := kiro.IdCOIDCBase(params.IdCRegion)
	if err != nil {
		return nil, infraerrors.BadRequest("KIRO_IDC_REGION_INVALID", err.Error())
	}

	client, err := s.httpClientFor(ctx, sess.proxyID)
	if err != nil {
		return nil, err
	}

	registerCtx, cancel := context.WithTimeout(ctx, kiroHTTPTimeout)
	defer cancel()
	reg, err := kiro.RegisterIdCClient(registerCtx, client, oidcBase, params.IssuerURL)
	if err != nil {
		return nil, kiroIdCUpstreamError("KIRO_IDC_REGISTER_FAILED", err)
	}

	pkce, err := kiro.NewPKCE()
	if err != nil {
		return nil, infraerrors.New(http.StatusInternalServerError, "KIRO_PKCE_FAILED", "failed to prepare the login session").WithCause(err)
	}
	redirectURI := kiro.IdCCallbackRedirectURI(kiro.DefaultCallbackPort)

	if !s.webLogins.advance(sessionID, func(target *kiroWebLoginSession) {
		target.phase = kiroPhaseIdC
		target.pkce = pkce
		target.redirectURI = redirectURI
		target.loginOption = params.LoginOption
		target.oidcBase = oidcBase
		target.idcRegion = params.IdCRegion
		target.clientID = reg.ClientID
		target.clientSecret = reg.ClientSecret
	}) {
		return nil, infraerrors.BadRequest("KIRO_LOGIN_SESSION_EXPIRED",
			"the login session expired while registering the Identity Center client; start the login again")
	}

	return &KiroWebLoginResult{
		Status:         KiroWebLoginStatusIdCRequired,
		SessionID:      sessionID,
		NextLoginURL:   kiro.IdCAuthorizeURL(oidcBase, reg.ClientID, redirectURI, pkce.State, pkce.Challenge),
		CallbackPrefix: redirectURI,
		Provider:       kiro.IdCProviderFromLoginOption(params.LoginOption),
	}, nil
}

// completeIdCLeg 处理回调 #2：换 token 并补上 profileArn。
func (s *KiroOAuthService) completeIdCLeg(
	ctx context.Context,
	sessionID string,
	sess *kiroWebLoginSession,
	params *kiro.CallbackParams,
) (*KiroWebLoginResult, error) {
	if strings.TrimSpace(params.Code) == "" {
		return nil, infraerrors.BadRequest("KIRO_CALLBACK_INVALID",
			"no authorization code found in the callback")
	}

	client, err := s.httpClientFor(ctx, sess.proxyID)
	if err != nil {
		return nil, err
	}

	exchangeCtx, cancel := context.WithTimeout(ctx, kiroHTTPTimeout)
	defer cancel()
	// redirectURI 用会话里存的那一个：授权链接是服务端自己生成的，
	// 只有它知道当时写进去的是什么。
	token, err := kiro.CreateIdCToken(exchangeCtx, client, sess.oidcBase,
		&kiro.IdCClientRegistration{ClientID: sess.clientID, ClientSecret: sess.clientSecret},
		params.Code, sess.pkce.Verifier, sess.redirectURI)
	if err != nil {
		return nil, kiroIdCUpstreamError("KIRO_IDC_CODE_EXCHANGE_FAILED", err)
	}

	creds := &kiro.Credentials{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    token.ExpiresAt(time.Now()),
		AuthMethod:   kiro.AuthMethodIdC,
		// provider 必须取 portal 那次回调的 login_option：AWS 这次不带它，
		// 从这里取会把所有企业账号都记成 BuilderId。
		Provider: kiro.IdCProviderFromLoginOption(sess.loginOption),
		Region:   sess.idcRegion,
		// clientId / clientSecret 必须落库：服务器上没有本机的
		// ~/.aws/sso/cache，少了这两项 token 一过期账号就永久失效。
		ClientID:     sess.clientID,
		ClientSecret: sess.clientSecret,
	}

	// IdC 不像 social 那样直接给 profileArn，得再查一次。
	// 缺了它每个 Q API 请求都发不出去，所以这里失败就是硬失败。
	profileCtx, cancelProfile := context.WithTimeout(ctx, kiroImportRefreshDeadline)
	defer cancelProfile()
	profileARN, profiles, err := kiro.ResolveIdCProfileARN(profileCtx, client, creds)
	if err != nil {
		if errors.Is(err, kiro.ErrNoIdCProfile) {
			return nil, infraerrors.Newf(http.StatusBadRequest, "KIRO_IDC_NO_PROFILE",
				"the login succeeded but this account has no Amazon Q profile; ask the organization admin to grant one: %v", err)
		}
		return nil, kiroIdCUpstreamError("KIRO_IDC_PROFILE_LOOKUP_FAILED", err)
	}
	creds.ProfileARN = profileARN

	s.webLogins.finish(sessionID)
	return s.completedResult(sessionID, creds, profiles), nil
}

func (s *KiroOAuthService) completedResult(sessionID string, creds *kiro.Credentials, profiles []kiro.Profile) *KiroWebLoginResult {
	info := kiroTokenInfoFrom(creds, false)
	result := &KiroWebLoginResult{
		Status:      KiroWebLoginStatusCompleted,
		SessionID:   sessionID,
		TokenInfo:   info,
		Credentials: s.BuildAccountCredentials(info),
	}
	for _, p := range profiles {
		result.Profiles = append(result.Profiles, KiroProfile{ARN: p.ARN, Name: p.ProfileName})
	}
	return result
}

// httpClientFor 返回走账号代理的出网客户端。
//
// 登录链上的每一次出网都要用它：注册客户端、换 token、探 profile 都会
// 暴露出口 IP，漏成服务器直连等于把代理白配了。
func (s *KiroOAuthService) httpClientFor(ctx context.Context, proxyID *int64) (*http.Client, error) {
	proxy, err := s.proxyURL(ctx, proxyID)
	if err != nil {
		return nil, err
	}
	client, err := httpclient.GetClient(httpclient.Options{
		ProxyURL:              proxy,
		Timeout:               kiroHTTPTimeout,
		ResponseHeaderTimeout: kiroHTTPHeaderTimeout,
	})
	if err != nil {
		return nil, infraerrors.BadRequest("KIRO_PROXY_INVALID", "invalid proxy configuration")
	}
	return client, nil
}

// kiroIdCUpstreamError 把 AWS 侧的失败分成「授权码/凭证被拒」与「稍后重试」两类。
func kiroIdCUpstreamError(code string, err error) error {
	var refreshErr *kiro.RefreshError
	if errors.As(err, &refreshErr) && !refreshErr.Unauthorized() {
		return infraerrors.Newf(http.StatusServiceUnavailable, "KIRO_IDC_UPSTREAM_ERROR",
			"AWS Identity Center is temporarily unavailable: %v", err)
	}
	return infraerrors.Newf(http.StatusBadRequest, code, "%v", err)
}

// providerFromLoginOption 把 portal 的 login_option 归一成凭证里的 provider。
func providerFromLoginOption(option string) string {
	switch strings.ToLower(strings.TrimSpace(option)) {
	case "":
		return ""
	case "google":
		return "Google"
	case "github":
		return "GitHub"
	default:
		return option
	}
}
