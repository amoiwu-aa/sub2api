package cursor

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// dashboard 接口认的 cookie 是 "<user_id>::<jwt>"，分隔符必须是 URL 编码的。
// 之前只传裸 JWT，上游一律 401 / 307，看起来就像"Cursor 没有额度接口"。
func TestSessionCookieJoinsUserIDAndToken(t *testing.T) {
	require.Equal(t, "user_1%3A%3Aeyj", SessionCookie("user_1", "eyj"))
	require.Equal(t, "eyj", SessionCookie("", "eyj"), "缺 user_id 时退回裸 token，让上游的 401 暴露问题")
	require.Equal(t, "", SessionCookie("user_1", ""))
	require.Equal(t, "user_1%3A%3Aeyj", SessionCookie("  user_1 ", " eyj "))
}

func TestDashboardGetRejectsEmptyCookie(t *testing.T) {
	client := &stubHTTPClient{}

	var out UsageSummary
	err := dashboardGet(t.Context(), &Options{HTTPClient: client}, "  ", usageSummaryPath, &out)
	require.Error(t, err)
	require.Empty(t, client.requests, "缺 cookie 时不该发出请求")

	var httpErr *HTTPError
	require.ErrorAs(t, err, &httpErr)
	require.Equal(t, http.StatusUnauthorized, httpErr.Status)
}

func TestFetchUsageSummarySendsSessionCookie(t *testing.T) {
	client := &stubHTTPClient{responses: []*http.Response{
		jsonResponse(http.StatusOK, `{"membershipType":"pro","isUnlimited":false}`),
	}}

	summary, err := FetchUsageSummary(t.Context(), &Options{HTTPClient: client}, SessionCookie("user_1", "eyj"))
	require.NoError(t, err)
	require.Equal(t, "pro", summary.MembershipType)

	require.Len(t, client.requests, 1)
	req := client.requests[0]
	require.Equal(t, "https://cursor.com/api/usage-summary", req.URL.String())
	require.Equal(t, "WorkosCursorSessionToken=user_1%3A%3Aeyj", req.Header.Get("Cookie"))
	require.Equal(t, "https://cursor.com", req.Header.Get("Origin"))
}

// 凭证失效时上游 307 到 WorkOS 登录页，而 http.Client 默认跟随重定向，
// 于是我们拿到的是 200 + HTML。必须识别成需要重新授权，
// 否则会退化成一条难懂的 JSON 解析错误。
func TestDashboardGetTreatsLoginPageAsUnauthorized(t *testing.T) {
	client := &stubHTTPClient{responses: []*http.Response{
		jsonResponse(http.StatusOK, `<!DOCTYPE html><html><head></head></html>`),
	}}

	_, err := FetchStripeProfile(t.Context(), &Options{HTTPClient: client}, SessionCookie("user_1", "eyj"))
	require.Error(t, err)

	var httpErr *HTTPError
	require.ErrorAs(t, err, &httpErr)
	require.Equal(t, http.StatusUnauthorized, httpErr.Status)
	require.True(t, httpErr.Unauthorized())
}

func TestIsHTMLBodyDetectsLoginRedirect(t *testing.T) {
	require.True(t, isHTMLBody([]byte("\n  <!DOCTYPE html><html>")))
	require.True(t, isHTMLBody([]byte("<html>")))
	require.False(t, isHTMLBody([]byte(`{"membershipType":"pro"}`)))
	require.False(t, isHTMLBody(nil))
}

func TestUsageSummaryScopePrefersTeamPool(t *testing.T) {
	individual := &PlanUsage{UsedCents: 1}
	team := &PlanUsage{UsedCents: 2}

	personal := &UsageSummary{IndividualUsage: UsageScope{Plan: individual}}
	require.Equal(t, individual, personal.Scope().Plan)

	member := &UsageSummary{
		IndividualUsage: UsageScope{Plan: individual},
		TeamUsage:       UsageScope{Plan: team},
	}
	require.Equal(t, team, member.Scope().Plan)
}

func TestStripeProfileHealthy(t *testing.T) {
	require.True(t, (*StripeProfile)(nil).Healthy())
	require.True(t, (&StripeProfile{SubscriptionStatus: "active"}).Healthy())
	require.True(t, (&StripeProfile{SubscriptionStatus: "trialing"}).Healthy())
	require.False(t, (&StripeProfile{SubscriptionStatus: "past_due"}).Healthy())
	require.False(t, (&StripeProfile{SubscriptionStatus: "active", LastPaymentFailed: true}).Healthy())
}

func TestCycleEndParsesRFC3339(t *testing.T) {
	summary := &UsageSummary{BillingCycleEnd: "2026-08-25T13:35:47.000Z"}
	require.Equal(t, 2026, summary.CycleEnd().Year())
	require.Equal(t, 8, int(summary.CycleEnd().Month()))

	require.True(t, (&UsageSummary{BillingCycleEnd: "not-a-time"}).CycleEnd().IsZero())
	require.True(t, (&UsageSummary{}).CycleEnd().IsZero())
}
