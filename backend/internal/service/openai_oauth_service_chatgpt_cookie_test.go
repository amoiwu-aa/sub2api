package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/imroc/req/v3"
)

func TestExchangeChatGPTCookieUsesIsolatedBrowserClient(t *testing.T) {
	expiry := time.Now().UTC().Add(time.Hour)
	accessToken := buildChatGPTCookieTestJWT(t, expiry)
	const submittedSession = "submitted-session"
	const pooledSession = "must-not-leak-from-pool"

	var gotCookie string
	reqClient := req.C()
	reqClient.GetClient().Transport = chatGPTCookieRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotCookie = r.Header.Get("Cookie")
		body, _ := json.Marshal(map[string]any{
			"accessToken": accessToken,
			"user":        map[string]any{"email": "cookie@example.com"},
		})
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(string(body))),
			Request:    r,
		}, nil
	})
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	target, _ := url.Parse("https://chatgpt.com/")
	jar.SetCookies(target, []*http.Cookie{{
		Name:  "__Secure-next-auth.session-token",
		Value: pooledSession,
		Path:  "/",
	}})
	reqClient.GetClient().Jar = jar

	svc := NewOpenAIOAuthService(nil, nil)
	svc.SetPrivacyClientFactory(func(proxyURL string) (*req.Client, error) {
		if proxyURL != "" {
			t.Fatalf("proxyURL = %q, want direct", proxyURL)
		}
		return reqClient, nil
	})

	result, err := svc.ExchangeChatGPTCookie(context.Background(), &ChatGPTCookieExchangeInput{
		Content:   "__Secure-next-auth.session-token=" + submittedSession,
		UserAgent: "test-browser",
	})
	if err != nil {
		t.Fatalf("ExchangeChatGPTCookie: %v", err)
	}
	if gotCookie != "__Secure-next-auth.session-token="+submittedSession {
		t.Fatalf("Cookie = %q; pooled jar cookie leaked or submitted cookie changed", gotCookie)
	}
	if strings.Contains(gotCookie, pooledSession) {
		t.Fatalf("pooled cookie leaked: %s", gotCookie)
	}
	if result.Credential.AccessToken != accessToken {
		t.Fatalf("access token mismatch")
	}
	if result.Credential.User["email"] != "cookie@example.com" {
		t.Fatalf("user = %#v", result.Credential.User)
	}
}

func TestExchangeChatGPTCookieMapsUpstreamAuthFailureToAdminSafeBadRequest(t *testing.T) {
	reqClient := req.C()
	reqClient.GetClient().Transport = chatGPTCookieRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"error":"contains-no-credential"}`)),
			Request:    r,
		}, nil
	})

	svc := NewOpenAIOAuthService(nil, nil)
	svc.SetPrivacyClientFactory(func(string) (*req.Client, error) { return reqClient, nil })
	_, err := svc.ExchangeChatGPTCookie(context.Background(), &ChatGPTCookieExchangeInput{
		Content: "__Secure-next-auth.session-token=expired-session",
	})
	if err == nil {
		t.Fatal("upstream 401 should fail")
	}
	if got := infraerrors.Code(err); got != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 so admin auth interceptors do not log out", got)
	}
	if got := infraerrors.Reason(err); got != "CHATGPT_COOKIE_REJECTED" {
		t.Fatalf("reason = %q", got)
	}
	if strings.Contains(infraerrors.Message(err), "expired-session") {
		t.Fatalf("error leaked submitted session: %v", err)
	}
}

type chatGPTCookieRoundTripFunc func(*http.Request) (*http.Response, error)

func (f chatGPTCookieRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func buildChatGPTCookieTestJWT(t *testing.T, expiry time.Time) string {
	t.Helper()
	header, err := json.Marshal(map[string]any{"alg": "none", "typ": "JWT"})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := json.Marshal(map[string]any{"exp": expiry.Unix()})
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(claims) + ".signature"
}
