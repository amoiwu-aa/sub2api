package kiro

import (
	"context"
	"errors"
	"io"
	"net/http"
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

// freezeTime 把 timeNow 固定住，让 expires_at 断言可预期。
func freezeTime(t *testing.T, at time.Time) {
	t.Helper()
	original := timeNow
	timeNow = func() time.Time { return at }
	t.Cleanup(func() { timeNow = original })
}

const socialTokenFixture = `{
  "accessToken": "aws-access-token",
  "refreshToken": "aws-refresh-token",
  "expiresAt": "2026-07-26T10:00:00.000Z",
  "authMethod": "Social",
  "profileArn": "arn:aws:codewhisperer:eu-west-1:123456789012:profile/ABCDEF",
  "provider": "Google",
  "region": "us-east-1"
}`

const idcTokenFixture = `{
  "accessToken": "idc-access-token",
  "refreshToken": "idc-refresh-token",
  "expiresAt": "2026-07-26T10:00:00.000Z",
  "authMethod": "IdC",
  "profileArn": "arn:aws:codewhisperer:us-east-1:123456789012:profile/XYZ",
  "region": "ap-southeast-1",
  "clientIdHash": "9f86d081884c7d65"
}`

func TestParseAuthTokenSocial(t *testing.T) {
	creds, err := ParseAuthToken([]byte(socialTokenFixture))
	require.NoError(t, err)

	require.Equal(t, "aws-access-token", creds.AccessToken)
	require.Equal(t, "aws-refresh-token", creds.RefreshToken)
	require.Equal(t, AuthMethodSocial, creds.AuthMethod)
	require.Equal(t, "Google", creds.Provider)
	require.Equal(t, time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC), creds.ExpiresAt)

	// Q 的 region 来自 profileArn，与 IdC 刷新用的 region 字段互不影响。
	require.Equal(t, "eu-west-1", creds.QRegion())
	require.Equal(t, "us-east-1", creds.OIDCRegion())
	require.NoError(t, creds.Validate())
}

func TestParseAuthTokenIdCInfersAuthMethodAndRequiresClientRegistration(t *testing.T) {
	creds, err := ParseAuthToken([]byte(idcTokenFixture))
	require.NoError(t, err)
	require.Equal(t, AuthMethodIdC, creds.AuthMethod)

	// 服务器读不到本机 SSO cache，缺 client 注册必须在导入时就拒绝，
	// 否则账号会在 access token 过期后静默死掉。
	require.ErrorIs(t, creds.Validate(), ErrClientRegistrationMissing)

	reg, err := ParseClientRegistration([]byte(`{"clientId":"cid","clientSecret":"secret"}`))
	require.NoError(t, err)
	creds.ClientID = reg.ClientID
	creds.ClientSecret = reg.ClientSecret
	require.NoError(t, creds.Validate())
}

func TestParseAuthTokenAcceptsSnakeCaseRoundTrip(t *testing.T) {
	original, err := ParseAuthToken([]byte(socialTokenFixture))
	require.NoError(t, err)

	stored := original.ToMap()
	require.Equal(t, "2026-07-26T10:00:00Z", stored["expires_at"])

	restored := CredentialsFromMap(stored)
	require.Equal(t, original.AccessToken, restored.AccessToken)
	require.Equal(t, original.RefreshToken, restored.RefreshToken)
	require.Equal(t, original.AuthMethod, restored.AuthMethod)
	require.Equal(t, original.ProfileARN, restored.ProfileARN)
	require.Equal(t, original.ExpiresAt, restored.ExpiresAt)
}

func TestToMapOmitsEmptyValues(t *testing.T) {
	creds := &Credentials{AccessToken: "token", AuthMethod: AuthMethodSocial}
	stored := creds.ToMap()

	// 空值不能落库：MergeCredentials 会用它覆盖掉已有的 client_secret 之类。
	require.NotContains(t, stored, "client_secret")
	require.NotContains(t, stored, "expires_at")
	require.Equal(t, "token", stored["access_token"])
}

func TestValidateRejectsMissingProfileARN(t *testing.T) {
	creds := &Credentials{
		AccessToken:  "token",
		RefreshToken: "refresh",
		AuthMethod:   AuthMethodSocial,
	}
	require.ErrorIs(t, creds.Validate(), ErrProfileARNMissing)
}

func TestNeedsRefresh(t *testing.T) {
	now := time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		creds  *Credentials
		expect bool
	}{
		{name: "nil", creds: nil, expect: true},
		{name: "no access token", creds: &Credentials{ExpiresAt: now.Add(time.Hour)}, expect: true},
		{name: "no expiry", creds: &Credentials{AccessToken: "t"}, expect: true},
		{name: "expired", creds: &Credentials{AccessToken: "t", ExpiresAt: now.Add(-time.Minute)}, expect: true},
		{name: "inside buffer", creds: &Credentials{AccessToken: "t", ExpiresAt: now.Add(time.Minute)}, expect: true},
		{name: "fresh", creds: &Credentials{AccessToken: "t", ExpiresAt: now.Add(time.Hour)}, expect: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.expect, tc.creds.NeedsRefresh(now, RefreshBuffer))
		})
	}
}

func TestRefreshSocial(t *testing.T) {
	freezeTime(t, time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC))
	creds, err := ParseAuthToken([]byte(socialTokenFixture))
	require.NoError(t, err)

	client := &stubHTTPClient{responses: []*http.Response{
		jsonResponse(http.StatusOK, `{"accessToken":"new-access","refreshToken":"new-refresh","expiresIn":3600,"profileArn":"arn:aws:codewhisperer:us-west-2:1:profile/NEW"}`),
	}}

	updated, err := Refresh(context.Background(), client, creds)
	require.NoError(t, err)

	require.Len(t, client.requests, 1)
	require.Equal(t, SocialRefreshURL, client.requests[0].URL.String())
	require.Equal(t, "application/json", client.requests[0].Header.Get("Content-Type"))
	require.Equal(t, "KiroIDE "+DefaultVersion+" ringstar", client.requests[0].Header.Get("User-Agent"))
	require.JSONEq(t, `{"refreshToken":"aws-refresh-token"}`, client.bodies[0])

	require.Equal(t, "new-access", updated.AccessToken)
	require.Equal(t, "new-refresh", updated.RefreshToken)
	require.Equal(t, "arn:aws:codewhisperer:us-west-2:1:profile/NEW", updated.ProfileARN)
	require.Equal(t, time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC), updated.ExpiresAt)

	// 入参不能被就地修改：调用方还要用旧凭证做 proxy 身份比对。
	require.Equal(t, "aws-access-token", creds.AccessToken)
}

func TestRefreshSocialKeepsExistingRefreshTokenWhenOmitted(t *testing.T) {
	freezeTime(t, time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC))
	creds, err := ParseAuthToken([]byte(socialTokenFixture))
	require.NoError(t, err)

	client := &stubHTTPClient{responses: []*http.Response{
		jsonResponse(http.StatusOK, `{"accessToken":"new-access","expiresIn":0}`),
	}}

	updated, err := Refresh(context.Background(), client, creds)
	require.NoError(t, err)
	require.Equal(t, "aws-refresh-token", updated.RefreshToken)
	require.Equal(t, creds.ProfileARN, updated.ProfileARN)
	// expiresIn 缺失或为 0 时回退到 1 小时，与反代一致。
	require.Equal(t, time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC), updated.ExpiresAt)
}

func TestRefreshIdCUsesRegionScopedOIDCEndpoint(t *testing.T) {
	freezeTime(t, time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC))
	creds, err := ParseAuthToken([]byte(idcTokenFixture))
	require.NoError(t, err)
	creds.ClientID = "cid"
	creds.ClientSecret = "secret"

	client := &stubHTTPClient{responses: []*http.Response{
		jsonResponse(http.StatusOK, `{"accessToken":"idc-new","expiresIn":1800}`),
	}}

	updated, err := Refresh(context.Background(), client, creds)
	require.NoError(t, err)

	require.Equal(t, "https://oidc.ap-southeast-1.amazonaws.com/token", client.requests[0].URL.String())
	require.JSONEq(t,
		`{"clientId":"cid","clientSecret":"secret","grantType":"refresh_token","refreshToken":"idc-refresh-token"}`,
		client.bodies[0],
	)
	require.Equal(t, "idc-new", updated.AccessToken)
	require.Equal(t, time.Date(2026, 7, 26, 9, 30, 0, 0, time.UTC), updated.ExpiresAt)
}

func TestRefreshIdCWithoutClientRegistrationFails(t *testing.T) {
	creds, err := ParseAuthToken([]byte(idcTokenFixture))
	require.NoError(t, err)

	client := &stubHTTPClient{}
	_, err = Refresh(context.Background(), client, creds)
	require.ErrorIs(t, err, ErrClientRegistrationMissing)
	require.Empty(t, client.requests)
}

func TestRefreshClassifiesUpstreamStatus(t *testing.T) {
	creds, err := ParseAuthToken([]byte(socialTokenFixture))
	require.NoError(t, err)

	cases := []struct {
		name         string
		status       int
		unauthorized bool
	}{
		{name: "bad request", status: http.StatusBadRequest, unauthorized: true},
		{name: "unauthorized", status: http.StatusUnauthorized, unauthorized: true},
		{name: "forbidden", status: http.StatusForbidden, unauthorized: true},
		{name: "server error", status: http.StatusBadGateway, unauthorized: false},
		{name: "rate limited", status: http.StatusTooManyRequests, unauthorized: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &stubHTTPClient{responses: []*http.Response{jsonResponse(tc.status, `{"message":"nope"}`)}}
			_, err := Refresh(context.Background(), client, creds)

			var refreshErr *RefreshError
			require.ErrorAs(t, err, &refreshErr)
			require.Equal(t, tc.status, refreshErr.Status)
			require.Equal(t, tc.unauthorized, refreshErr.Unauthorized())
		})
	}
}

func TestRefreshRejectsUnknownAuthMethod(t *testing.T) {
	creds := &Credentials{AccessToken: "t", RefreshToken: "r", AuthMethod: "saml"}
	_, err := Refresh(context.Background(), &stubHTTPClient{}, creds)
	require.ErrorIs(t, err, ErrUnknownAuthMethod)
}

func TestRefreshRequiresInjectedClient(t *testing.T) {
	creds := &Credentials{AccessToken: "t", RefreshToken: "r", AuthMethod: AuthMethodSocial}
	// 包内不得回退到 http.DefaultClient：那会绕过账号代理直连上游。
	_, err := Refresh(context.Background(), nil, creds)
	require.Error(t, err)
	require.Contains(t, err.Error(), "http client is nil")
}

func TestRegionFromARN(t *testing.T) {
	cases := map[string]string{
		"arn:aws:codewhisperer:us-east-1:123:profile/A":  "us-east-1",
		"arn:aws:codewhisperer:AP-SOUTH-1:123:profile/B": "ap-south-1",
		"arn:aws:codewhisperer":                          "",
		"":                                               "",
	}
	for arn, expected := range cases {
		require.Equal(t, expected, RegionFromARN(arn), "arn=%s", arn)
	}
}

func TestEndpointBuildersRejectInjectedRegion(t *testing.T) {
	// region 来自用户粘贴的 JSON，拼 URL 前必须校验，否则是个 SSRF 入口。
	malicious := []string{
		"us-east-1.evil.com",
		"us-east-1/../..",
		"us-east-1#",
		"",
		"useast1",
	}
	for _, region := range malicious {
		_, err := OIDCTokenURL(region)
		require.Error(t, err, "region=%q", region)
		_, err = QEndpoint(region)
		require.Error(t, err, "region=%q", region)
	}

	url, err := QEndpoint("EU-WEST-1")
	require.NoError(t, err)
	require.Equal(t, "https://q.eu-west-1.amazonaws.com", url)
}
