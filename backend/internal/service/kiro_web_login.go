package service

import (
	"context"
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

const (
	// kiroWebLoginTTL 是一次登录会话的有效期。授权码本身寿命更短，
	// 这里给足人工在浏览器里完成登录的时间。
	kiroWebLoginTTL = 15 * time.Minute
	// kiroWebLoginMaxSessions 防止未完成的会话无限堆积。
	kiroWebLoginMaxSessions = 256
)

type kiroWebLoginSession struct {
	pkce      *kiro.PKCE
	port      int
	proxyID   *int64
	createdAt time.Time
}

// KiroWebLoginStore 保存进行中的登录会话。
//
// 与 CursorOAuthService 的会话存储一样放在进程内存里：会话寿命只有几分钟，
// 且必须与生成 PKCE 的那个进程绑定。多副本部署时，登录请求要落到同一个副本
// 才能完成——这是已知取舍，写在这里以免以后被当成 bug 排查。
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

// take 取出并删除会话：授权码只能用一次，会话也就没有复用的必要。
func (s *KiroWebLoginStore) take(id string) (*kiroWebLoginSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evictExpiredLocked()
	sess, ok := s.sessions[id]
	if ok {
		delete(s.sessions, id)
	}
	return sess, ok
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

// StartWebLogin 生成一次 portal 登录会话。
func (s *KiroOAuthService) StartWebLogin(_ context.Context, proxyID *int64) (*KiroWebLoginStart, error) {
	pkce, err := kiro.NewPKCE()
	if err != nil {
		return nil, infraerrors.New(http.StatusInternalServerError, "KIRO_PKCE_FAILED", "failed to prepare the login session").WithCause(err)
	}
	port := kiro.DefaultCallbackPort
	sessionID := uuid.NewString()
	s.webLogins.put(sessionID, &kiroWebLoginSession{
		pkce:      pkce,
		port:      port,
		proxyID:   proxyID,
		createdAt: time.Now(),
	})
	return &KiroWebLoginStart{
		LoginURL:       kiro.BuildPortalURL(pkce, port),
		SessionID:      sessionID,
		CallbackPrefix: kiro.CallbackOrigin(port),
	}, nil
}

// CompleteWebLogin 用粘回来的回调地址换取凭证。
func (s *KiroOAuthService) CompleteWebLogin(ctx context.Context, sessionID, callback string) (*KiroTokenInfo, error) {
	sess, ok := s.webLogins.take(strings.TrimSpace(sessionID))
	if !ok {
		return nil, infraerrors.BadRequest("KIRO_LOGIN_SESSION_EXPIRED",
			"the login session has expired or was already used; start the login again")
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

	port := sess.port
	if params.Port > 0 {
		// 管理员本机若有 Kiro IDE 占用了 3128，回调会落到列表里靠后的端口上。
		// 换 token 的 redirect_uri 必须与浏览器实际访问的一致。
		port = params.Port
	}

	proxy, err := s.proxyURL(ctx, sess.proxyID)
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
	return kiroTokenInfoFrom(creds, false), nil
}

// providerFromLoginOption 把 portal 的 login_option 归一成凭证里的 provider 字段。
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
