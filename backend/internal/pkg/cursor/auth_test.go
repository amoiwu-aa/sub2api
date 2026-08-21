package cursor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type stubHTTPClient struct {
	requests  []*http.Request
	bodies    []string
	responses []*http.Response
	err       error
}

func (c *stubHTTPClient) Do(req *http.Request) (*http.Response, error) {
	body := ""
	if req.Body != nil {
		raw, _ := io.ReadAll(req.Body)
		body = string(raw)
	}
	c.requests = append(c.requests, req)
	c.bodies = append(c.bodies, body)
	if c.err != nil {
		return nil, c.err
	}
	if len(c.responses) == 0 {
		return nil, errors.New("stub has no more responses")
	}
	resp := c.responses[0]
	c.responses = c.responses[1:]
	return resp, nil
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

// fakeClock 让轮询测试既不真的等待，又能触发超时分支。
type fakeClock struct{ now time.Time }

func (c *fakeClock) sleep(context.Context, time.Duration) error {
	c.now = c.now.Add(time.Second)
	return nil
}

func withFakeClock(t *testing.T) *fakeClock {
	t.Helper()
	clock := &fakeClock{now: time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)}
	original := timeNow
	timeNow = func() time.Time { return clock.now }
	t.Cleanup(func() { timeNow = original })
	return clock
}

// makeJWT 造一个只有 payload 有意义的 JWT；本包不验签。
func makeJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	require.NoError(t, err)
	encode := func(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
	return "eyJ" + encode([]byte(`{"alg":"none"}`))[3:] + "." + encode(payload) + ".sig"
}

// 过期时间以 timeNow 为基准，这样装了假时钟的用例造出来的 JWT 也是有效的。
func sessionJWT(t *testing.T) string {
	return makeJWT(t, map[string]any{
		"type": "session", "sub": "auth0|user_01SESSION",
		"exp": timeNow().Add(time.Hour).Unix(),
	})
}

func webJWT(t *testing.T) string {
	return makeJWT(t, map[string]any{
		"type": "web", "sub": "auth0|user_01WEB",
		"exp": timeNow().Add(time.Hour).Unix(),
	})
}

func TestGeneratePKCEProducesVerifiableChallenge(t *testing.T) {
	pkce, err := GeneratePKCE()
	require.NoError(t, err)

	require.NotEmpty(t, pkce.Verifier)
	require.NotEmpty(t, pkce.UUID)
	// challenge 必须是不带 padding 的 base64url，上游对此敏感。
	require.NotContains(t, pkce.Challenge, "=")
	require.NotContains(t, pkce.Challenge, "+")
	require.NotContains(t, pkce.Challenge, "/")

	parsed, err := url.Parse(pkce.LoginURL)
	require.NoError(t, err)
	require.Equal(t, "cursor.com", parsed.Host)
	require.Equal(t, "/loginDeepControl", parsed.Path)
	require.Equal(t, pkce.Challenge, parsed.Query().Get("challenge"))
	require.Equal(t, pkce.UUID, parsed.Query().Get("uuid"))
	require.Equal(t, "login", parsed.Query().Get("mode"))
	require.Equal(t, "cli", parsed.Query().Get("redirectTarget"))

	other, err := GeneratePKCE()
	require.NoError(t, err)
	require.NotEqual(t, pkce.Verifier, other.Verifier)
}

func TestGeneratePKCEForSandUsesSandRedirectTarget(t *testing.T) {
	pkce, err := GeneratePKCEForProfile(AgentProfileSand)
	require.NoError(t, err)

	parsed, err := url.Parse(pkce.LoginURL)
	require.NoError(t, err)
	require.Equal(t, "sand", parsed.Query().Get("redirectTarget"))
}

func TestParseSessionInputAcceptsAllPastedForms(t *testing.T) {
	jwt := sessionJWT(t)
	cases := map[string]string{
		"cookie with name":  "WorkosCursorSessionToken=user_01ABC::" + jwt,
		"user id prefix":    "user_01ABC::" + jwt,
		"url encoded colon": "user_01ABC%3A%3A" + jwt,
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			parsed, err := ParseSessionInput(input)
			require.NoError(t, err)
			require.Equal(t, "user_01ABC", parsed.UserID)
			require.Equal(t, jwt, parsed.JWT)
			require.True(t, parsed.IsSession())
			require.Equal(t, "user_01ABC::"+jwt, parsed.CookieValue())
		})
	}

	// 裸 JWT：userId 从 sub 取，并去掉 auth0| 前缀。
	parsed, err := ParseSessionInput(jwt)
	require.NoError(t, err)
	require.Equal(t, "user_01SESSION", parsed.UserID)
	require.Equal(t, "auth0|user_01SESSION", parsed.Subject)
}

func TestParseSessionInputRejectsBadInput(t *testing.T) {
	for _, input := range []string{"", "   ", "not-a-jwt", "user_01ABC::nope"} {
		_, err := ParseSessionInput(input)
		require.ErrorIs(t, err, ErrNotAJWT, "input=%q", input)
	}

	expired := makeJWT(t, map[string]any{"type": "web", "exp": timeNow().Add(-time.Hour).Unix()})
	_, err := ParseSessionInput(expired)
	require.ErrorIs(t, err, ErrTokenExpired)
}

func TestTokenTypeHelpers(t *testing.T) {
	require.True(t, IsSessionToken(sessionJWT(t)))
	require.False(t, IsWebToken(sessionJWT(t)))
	require.True(t, IsWebToken(webJWT(t)))
	require.False(t, IsSessionToken(webJWT(t)))
	require.False(t, IsSessionToken("garbage"))
	require.True(t, TokenExpiry("garbage").IsZero())
	require.False(t, TokenExpiry(sessionJWT(t)).IsZero())
}

func TestPollOnceTreats404AsPending(t *testing.T) {
	client := &stubHTTPClient{responses: []*http.Response{jsonResponse(http.StatusNotFound, "")}}

	_, err := PollOnce(context.Background(), &Options{HTTPClient: client}, "uuid-1", "verifier-1")
	require.ErrorIs(t, err, ErrPollPending)

	req := client.requests[0]
	require.Equal(t, "https://api2.cursor.sh/auth/poll?uuid=uuid-1&verifier=verifier-1", req.URL.String())
	require.Equal(t, "ide", req.Header.Get("x-cursor-client-type"))
	require.Equal(t, ClientVersion, req.Header.Get("x-cursor-client-version"))
	require.Equal(t, "false", req.Header.Get("x-ghost-mode"))
}

func TestPollOnceSandUsesSandHeaders(t *testing.T) {
	client := &stubHTTPClient{responses: []*http.Response{jsonResponse(http.StatusNotFound, "")}}

	_, err := PollOnce(context.Background(), &Options{
		HTTPClient: client,
		Profile:    AgentProfileSand,
	}, "uuid-1", "verifier-1")
	require.ErrorIs(t, err, ErrPollPending)

	req := client.requests[0]
	require.Equal(t, "sand", req.Header.Get("x-cursor-client-type"))
	require.Equal(t, SandClientVersion, req.Header.Get("x-cursor-client-version"))
	require.Equal(t, "prod", req.Header.Get("x-sand-box-namespace"))
	require.Empty(t, req.Header.Get("x-new-onboarding-completed"))
}

func TestPollOnceReturnsTokens(t *testing.T) {
	client := &stubHTTPClient{responses: []*http.Response{
		jsonResponse(http.StatusOK, `{"accessToken":"a1","refreshToken":"r1","authId":"auth-1","selectedTeamId":42}`),
	}}

	tokens, err := PollOnce(context.Background(), &Options{HTTPClient: client}, "u", "v")
	require.NoError(t, err)
	require.Equal(t, "a1", tokens.AccessToken)
	require.Equal(t, "r1", tokens.RefreshToken)
	require.Equal(t, "auth-1", tokens.AuthID)
	// selectedTeamId 可能是数字也可能是字符串。
	require.Equal(t, "42", tokens.SelectedTeamID)
}

func TestPollOnceAcceptsSnakeCaseAndDefaultsRefreshToAccess(t *testing.T) {
	client := &stubHTTPClient{responses: []*http.Response{
		jsonResponse(http.StatusOK, `{"access_token":"a1"}`),
	}}

	tokens, err := PollOnce(context.Background(), &Options{HTTPClient: client}, "u", "v")
	require.NoError(t, err)
	require.Equal(t, "a1", tokens.AccessToken)
	require.Equal(t, "a1", tokens.RefreshToken)
}

func TestPollOnceSurfacesUnexpectedStatus(t *testing.T) {
	client := &stubHTTPClient{responses: []*http.Response{jsonResponse(http.StatusInternalServerError, "boom")}}

	_, err := PollOnce(context.Background(), &Options{HTTPClient: client}, "u", "v")

	var httpErr *HTTPError
	require.ErrorAs(t, err, &httpErr)
	require.Equal(t, http.StatusInternalServerError, httpErr.Status)
	require.False(t, httpErr.Unauthorized())
}

func TestPollTokensRetriesUntilReady(t *testing.T) {
	clock := withFakeClock(t)
	client := &stubHTTPClient{responses: []*http.Response{
		jsonResponse(http.StatusNotFound, ""),
		jsonResponse(http.StatusNotFound, ""),
		jsonResponse(http.StatusOK, `{"accessToken":"a1","refreshToken":"r1"}`),
	}}

	tokens, err := PollTokens(context.Background(),
		&Options{HTTPClient: client, Sleep: clock.sleep}, "u", "v", time.Minute, time.Second)
	require.NoError(t, err)
	require.Equal(t, "a1", tokens.AccessToken)
	require.Len(t, client.requests, 3)
}

func TestPollTokensTimesOut(t *testing.T) {
	clock := withFakeClock(t)
	client := &stubHTTPClient{}
	for i := 0; i < 20; i++ {
		client.responses = append(client.responses, jsonResponse(http.StatusNotFound, ""))
	}

	_, err := PollTokens(context.Background(),
		&Options{HTTPClient: client, Sleep: clock.sleep}, "u", "v", 5*time.Second, time.Second)
	require.ErrorIs(t, err, ErrPollTimeout)
}

func TestExchangeWebTokenToSessionIsIdempotentForSessionInput(t *testing.T) {
	jwt := sessionJWT(t)
	client := &stubHTTPClient{}

	tokens, err := ExchangeWebTokenToSession(context.Background(), &Options{HTTPClient: client}, jwt, "")
	require.NoError(t, err)
	require.Equal(t, jwt, tokens.AccessToken)
	require.Equal(t, "session_passthrough", tokens.Source)
	// 已经是 session 就不该打任何上游请求。
	require.Empty(t, client.requests)
}

func TestExchangeWebTokenToSessionApprovesThenPolls(t *testing.T) {
	clock := withFakeClock(t)
	web := webJWT(t)
	client := &stubHTTPClient{responses: []*http.Response{
		jsonResponse(http.StatusOK, `{"ok":true}`),
		jsonResponse(http.StatusOK, `{"accessToken":"session-a","refreshToken":"session-r"}`),
	}}

	tokens, err := ExchangeWebTokenToSession(context.Background(),
		&Options{HTTPClient: client, Sleep: clock.sleep}, "user_01ABC::"+web, "team-7")
	require.NoError(t, err)
	require.Equal(t, "session-a", tokens.AccessToken)
	require.Equal(t, "web_session_exchange", tokens.Source)

	approve := client.requests[0]
	require.Equal(t, "https://cursor.com/api/auth/loginDeepCallbackControl", approve.URL.String())
	require.Equal(t, "https://cursor.com", approve.Header.Get("Origin"))
	// cookie 值必须整体 URL 编码，`::` 不能裸传。
	require.Equal(t, "WorkosCursorSessionToken="+url.QueryEscape("user_01ABC::"+web), approve.Header.Get("Cookie"))

	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(client.bodies[0]), &body))
	require.Equal(t, "team-7", body["selectedTeamId"])
	require.NotEmpty(t, body["uuid"])
	require.NotEmpty(t, body["challenge"])

	// 轮询必须用同一次 PKCE 的 uuid，否则永远等不到令牌。
	require.Equal(t, body["uuid"], client.requests[1].URL.Query().Get("uuid"))
}

func TestExchangeWebTokenToSessionOmitsEmptyTeamID(t *testing.T) {
	clock := withFakeClock(t)
	client := &stubHTTPClient{responses: []*http.Response{
		jsonResponse(http.StatusOK, `{}`),
		jsonResponse(http.StatusOK, `{"accessToken":"a"}`),
	}}

	_, err := ExchangeWebTokenToSession(context.Background(),
		&Options{HTTPClient: client, Sleep: clock.sleep}, webJWT(t), "  ")
	require.NoError(t, err)

	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(client.bodies[0]), &body))
	require.NotContains(t, body, "selectedTeamId")
}

func TestRefreshTokensUsesOAuthFirst(t *testing.T) {
	client := &stubHTTPClient{responses: []*http.Response{
		jsonResponse(http.StatusOK, `{"access_token":"new-a","refresh_token":"new-r"}`),
	}}

	tokens, err := RefreshTokens(context.Background(), &Options{HTTPClient: client}, sessionJWT(t), "refresh-1")
	require.NoError(t, err)
	require.Equal(t, "new-a", tokens.AccessToken)
	require.Equal(t, "new-r", tokens.RefreshToken)
	require.Equal(t, "oauth", tokens.Source)

	require.Equal(t, "https://api2.cursor.sh/oauth/token", client.requests[0].URL.String())
	require.JSONEq(t,
		`{"grant_type":"refresh_token","client_id":"`+OAuthClientID+`","refresh_token":"refresh-1"}`,
		client.bodies[0],
	)
}

func TestRefreshTokensFallsBackToExchange(t *testing.T) {
	client := &stubHTTPClient{responses: []*http.Response{
		jsonResponse(http.StatusBadGateway, "upstream down"),
		jsonResponse(http.StatusOK, `{"accessToken":"exchanged-a"}`),
	}}

	tokens, err := RefreshTokens(context.Background(), &Options{HTTPClient: client}, sessionJWT(t), "refresh-1")
	require.NoError(t, err)
	require.Equal(t, "exchanged-a", tokens.AccessToken)
	require.Equal(t, "exchange", tokens.Source)

	exchange := client.requests[1]
	require.Equal(t, "https://api2.cursor.sh/auth/exchange_user_api_key", exchange.URL.String())
	require.Equal(t, "Bearer refresh-1", exchange.Header.Get("Authorization"))
}

func TestRefreshTokensFallsBackToAccessTokenExchange(t *testing.T) {
	access := sessionJWT(t)
	client := &stubHTTPClient{responses: []*http.Response{
		jsonResponse(http.StatusBadGateway, ""),               // oauth with refresh
		jsonResponse(http.StatusUnauthorized, ""),             // exchange with refresh
		jsonResponse(http.StatusOK, `{"accessToken":"last"}`), // exchange with access
	}}

	tokens, err := RefreshTokens(context.Background(), &Options{HTTPClient: client}, access, "refresh-1")
	require.NoError(t, err)
	require.Equal(t, "last", tokens.AccessToken)
	require.Equal(t, "Bearer "+access, client.requests[2].Header.Get("Authorization"))
}

func TestRefreshTokensStopsOnShouldLogout(t *testing.T) {
	client := &stubHTTPClient{responses: []*http.Response{
		jsonResponse(http.StatusOK, `{"shouldLogout":true}`),
	}}

	_, err := RefreshTokens(context.Background(), &Options{HTTPClient: client}, sessionJWT(t), "refresh-1")

	// shouldLogout 是终态，继续试 exchange 只会白打一次请求。
	require.ErrorIs(t, err, ErrShouldLogout)
	require.Len(t, client.requests, 1)
}

func TestRefreshTokensConvertsWebTokenFirst(t *testing.T) {
	clock := withFakeClock(t)
	client := &stubHTTPClient{responses: []*http.Response{
		jsonResponse(http.StatusOK, `{}`),
		jsonResponse(http.StatusOK, `{"accessToken":"session-a","refreshToken":"session-r"}`),
	}}

	tokens, err := RefreshTokens(context.Background(),
		&Options{HTTPClient: client, Sleep: clock.sleep}, webJWT(t), "refresh-1")
	require.NoError(t, err)
	require.Equal(t, "web_session_exchange", tokens.Source)
	// web token 必须先换成 session，不能直接拿去 oauth 刷新。
	require.Equal(t, "https://cursor.com/api/auth/loginDeepCallbackControl", client.requests[0].URL.String())
}

func TestRefreshTokensAggregatesFailures(t *testing.T) {
	client := &stubHTTPClient{responses: []*http.Response{
		jsonResponse(http.StatusBadGateway, "oauth down"),
		jsonResponse(http.StatusBadGateway, "exchange down"),
		jsonResponse(http.StatusBadGateway, "exchange access down"),
	}}

	_, err := RefreshTokens(context.Background(), &Options{HTTPClient: client}, sessionJWT(t), "refresh-1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "oauth:")
	require.Contains(t, err.Error(), "exchange_refresh:")
	require.Contains(t, err.Error(), "exchange_access:")
}

func TestRefreshTokensWithoutCredentials(t *testing.T) {
	_, err := RefreshTokens(context.Background(), &Options{HTTPClient: &stubHTTPClient{}}, "", "")
	require.ErrorIs(t, err, ErrNoCredentials)
}

func TestRequiresInjectedHTTPClient(t *testing.T) {
	// 包内不得回退到 http.DefaultClient：那会绕过账号代理直连上游。
	_, err := PollOnce(context.Background(), &Options{}, "u", "v")
	require.ErrorContains(t, err, "http client is nil")

	_, err = RefreshTokens(context.Background(), &Options{}, sessionJWT(t), "r")
	require.ErrorContains(t, err, "http client is nil")
}
