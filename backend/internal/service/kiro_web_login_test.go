package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/stretchr/testify/require"
)

// 本文件里的用例全部**不出网**。
//
// IdC 那条链要真跑起来得有一个真实的 IAM Identity Center 账号，协议本身的
// 覆盖放在 internal/pkg/kiro/idc_test.go；这里守的是状态机：会话跨两次回调
// 怎么活、什么时候该转交 IdC、哪些回调要当场拒掉。
//
// 手法与 cmd/kirologin 的 e2e 测试一致——用非法 region 让 IdC 链在构造端点
// 时就停下，既证明「确实进了 IdC 分支」，又保证不发出任何请求。

func newKiroWebLoginService() *KiroOAuthService {
	return NewKiroOAuthService(nil)
}

// startedSession 起一个 portal 阶段的会话，返回 sessionID 与它的 state。
func startedSession(t *testing.T, svc *KiroOAuthService) (string, string) {
	t.Helper()
	start, err := svc.StartWebLogin(context.Background(), nil)
	require.NoError(t, err)
	require.NotEmpty(t, start.SessionID)

	sess, ok := svc.webLogins.sessions[start.SessionID]
	require.True(t, ok, "会话应已落进 store")
	require.Equal(t, kiroPhasePortal, sess.phase)
	return start.SessionID, sess.pkce.State
}

// TestKiroWebLoginIdCHandoffEntersIdCLeg 确认企业账号的第一次回调会被转交
// 给 IdC 链，而不是当成社交登录去 portal 换 token。
//
// 之前这里是硬伤：回调 #1 不带 code，ParseCallback 会直接报「回调缺 code」，
// 企业账号根本走不到第二段。
func TestKiroWebLoginIdCHandoffEntersIdCLeg(t *testing.T) {
	for _, loginOption := range []string{"builderid", "awsidc", "internal"} {
		t.Run(loginOption, func(t *testing.T) {
			svc := newKiroWebLoginService()
			sessionID, state := startedSession(t, svc)

			// 刻意给非法 region：IdC 链在构造 OIDC 端点时就会被挡下，
			// 于是测试不会真去打 oidc.<region>.amazonaws.com。
			callback := "http://localhost:3128/oauth/callback?login_option=" + loginOption +
				"&state=" + state +
				"&issuer_url=https%3A%2F%2Fexample.awsapps.com%2Fstart" +
				"&idc_region=not-a-region"

			_, err := svc.CompleteWebLogin(context.Background(), sessionID, callback)
			require.Error(t, err, "非法 region 应该失败")
			// 错在 region 校验，说明确实进了 IdC 链——社交链根本不看 region。
			require.Contains(t, err.Error(), "region")
		})
	}
}

// TestKiroWebLoginIdCHandoffNeedsIssuerURL 覆盖只贴了半截回调的情况。
// 没有 issuer_url 就不知道该找哪个 Identity Center 实例。
func TestKiroWebLoginIdCHandoffNeedsIssuerURL(t *testing.T) {
	svc := newKiroWebLoginService()
	sessionID, state := startedSession(t, svc)

	_, err := svc.CompleteWebLogin(context.Background(), sessionID,
		"http://localhost:3128/oauth/callback?login_option=awsidc&state="+state)
	require.Error(t, err)
	require.Contains(t, err.Error(), "issuer_url")
}

// TestKiroWebLoginRejectsExternalIDP 守住第三种 login_option。
//
// external_idp 要和企业自建 IdP 再做一次 OAuth，本项目没覆盖。放过去会建出
// 一个 provider 写着 external_idp、必然调不通的死号，不如当场说清楚。
func TestKiroWebLoginRejectsExternalIDP(t *testing.T) {
	svc := newKiroWebLoginService()
	sessionID, state := startedSession(t, svc)

	_, err := svc.CompleteWebLogin(context.Background(), sessionID,
		"http://localhost:3128/oauth/callback?login_option=external_idp&code=c&state="+state)
	require.Error(t, err)
	require.Contains(t, err.Error(), "IdP")
}

// TestKiroWebLoginSocialStillRequiresCode 确认为 IdC 放宽 code 校验之后，
// 社交链没有被顺带放宽。
func TestKiroWebLoginSocialStillRequiresCode(t *testing.T) {
	svc := newKiroWebLoginService()
	sessionID, state := startedSession(t, svc)

	_, err := svc.CompleteWebLogin(context.Background(), sessionID,
		"http://localhost:3128/oauth/callback?login_option=google&state="+state)
	require.Error(t, err)
	require.Contains(t, err.Error(), "code")
}

// TestKiroWebLoginRejectsForeignState 确认 state 不匹配的回调不会被接受。
func TestKiroWebLoginRejectsForeignState(t *testing.T) {
	svc := newKiroWebLoginService()
	sessionID, _ := startedSession(t, svc)

	_, err := svc.CompleteWebLogin(context.Background(), sessionID,
		"http://localhost:3128/oauth/callback?login_option=google&code=c&state=forged")
	require.Error(t, err)
	require.Contains(t, err.Error(), "login session")
}

// TestKiroWebLoginIdCPhaseRoutesToSecondLeg 确认会话进了 idc 阶段之后，
// 下一次 complete 走的是第二段而不是回到 portal 那段。
//
// 这里直接把会话摆成 idc 阶段（第一段要注册客户端，得出网），只验证分派。
func TestKiroWebLoginIdCPhaseRoutesToSecondLeg(t *testing.T) {
	svc := newKiroWebLoginService()
	sessionID, _ := startedSession(t, svc)

	pkce, err := kiro.NewPKCE()
	require.NoError(t, err)
	require.True(t, svc.webLogins.advance(sessionID, func(sess *kiroWebLoginSession) {
		sess.phase = kiroPhaseIdC
		sess.pkce = pkce
		sess.loginOption = "awsidc"
		sess.oidcBase = "https://oidc.us-east-1.amazonaws.com"
		sess.idcRegion = "us-east-1"
		sess.clientID = "cid"
		sess.clientSecret = "secret"
		sess.redirectURI = kiro.IdCCallbackRedirectURI(kiro.DefaultCallbackPort)
	}))

	// AWS 那次回调不带 login_option，所以这里也不带——它不该被当成
	// 「不是 IdC 交接」而掉回 portal 分支。缺 code 时应由第二段拒绝。
	_, err = svc.CompleteWebLogin(context.Background(), sessionID,
		"http://127.0.0.1:3128/oauth/callback?state="+pkce.State)
	require.Error(t, err)
	require.Contains(t, err.Error(), "code")
}

// TestKiroWebLoginAdvanceResetsAttempts 确认进入新阶段时尝试次数清零：
// 第一段用掉的次数不该算在第二段头上。
func TestKiroWebLoginAdvanceResetsAttempts(t *testing.T) {
	store := NewKiroWebLoginStore()
	pkce, err := kiro.NewPKCE()
	require.NoError(t, err)
	store.put("s1", &kiroWebLoginSession{phase: kiroPhasePortal, pkce: pkce, createdAt: time.Now()})

	_, ok := store.begin("s1")
	require.True(t, ok)
	require.Equal(t, 1, store.sessions["s1"].attempts)

	require.True(t, store.advance("s1", func(sess *kiroWebLoginSession) {
		sess.phase = kiroPhaseIdC
	}))
	require.Equal(t, 0, store.sessions["s1"].attempts)
	require.Equal(t, kiroPhaseIdC, store.sessions["s1"].phase)
}

// TestKiroWebLoginSessionSurvivesFailedAttempt 是这次改造的核心行为：
// 会话不再「取出即删」。
//
// IdC 第二段必须能复用第一段注册出来的 clientId/clientSecret；顺带地，
// 一次代理抖动也不该逼着管理员从头再走一遍 portal。
func TestKiroWebLoginSessionSurvivesFailedAttempt(t *testing.T) {
	store := NewKiroWebLoginStore()
	pkce, err := kiro.NewPKCE()
	require.NoError(t, err)
	store.put("s1", &kiroWebLoginSession{phase: kiroPhasePortal, pkce: pkce, createdAt: time.Now()})

	_, ok := store.begin("s1")
	require.True(t, ok)
	_, ok = store.begin("s1")
	require.True(t, ok, "失败一次之后会话应该还在")
}

// TestKiroWebLoginSessionExhaustsAttempts 确认保留会话没有换来一个可被
// 反复试探的口子。
func TestKiroWebLoginSessionExhaustsAttempts(t *testing.T) {
	store := NewKiroWebLoginStore()
	pkce, err := kiro.NewPKCE()
	require.NoError(t, err)
	store.put("s1", &kiroWebLoginSession{phase: kiroPhasePortal, pkce: pkce, createdAt: time.Now()})

	for i := 0; i < kiroWebLoginMaxAttempts; i++ {
		_, ok := store.begin("s1")
		require.Truef(t, ok, "第 %d 次尝试应被放行", i+1)
	}
	_, ok := store.begin("s1")
	require.False(t, ok, "超过上限后会话应作废")
	require.NotContains(t, store.sessions, "s1")
}

// TestKiroWebLoginFinishDeletesSession 确认成功后会话被清掉，
// 授权码只能用一次，会话也没有复用的必要。
func TestKiroWebLoginFinishDeletesSession(t *testing.T) {
	store := NewKiroWebLoginStore()
	pkce, err := kiro.NewPKCE()
	require.NoError(t, err)
	store.put("s1", &kiroWebLoginSession{phase: kiroPhasePortal, pkce: pkce, createdAt: time.Now()})

	store.finish("s1")
	_, ok := store.begin("s1")
	require.False(t, ok)
}

// TestKiroWebLoginBeginRejectsExpiredSession 确认过期会话不会被推进。
func TestKiroWebLoginBeginRejectsExpiredSession(t *testing.T) {
	store := NewKiroWebLoginStore()
	pkce, err := kiro.NewPKCE()
	require.NoError(t, err)
	store.put("s1", &kiroWebLoginSession{
		phase:     kiroPhasePortal,
		pkce:      pkce,
		createdAt: time.Now().Add(-kiroWebLoginTTL - time.Minute),
	})

	_, ok := store.begin("s1")
	require.False(t, ok)
}
