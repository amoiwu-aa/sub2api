package cursor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// 额度与订阅状态走 cursor.com 的 dashboard 接口，鉴权方式与 Agent 那套
// （api2.cursor.sh + Authorization: Bearer）完全不同：这里认的是浏览器 cookie
// WorkosCursorSessionToken，而且值必须是 "<user_id>::<jwt>"。只传裸 JWT 时
// WorkOS 会把请求 307 到登录页或直接回 401，看起来就像"上游没有额度接口"。
const (
	usageSummaryPath  = "/api/usage-summary"
	stripeProfilePath = "/api/auth/stripe"

	sessionCookieName = "WorkosCursorSessionToken"
	// cookie 里的分隔符必须按 URL 编码写，原样的 "::" 会被上游判为无效 cookie。
	sessionCookieSeparator = "%3A%3A"
)

// SessionCookie 拼出 dashboard 接口要的 cookie 值。
// userID 为空时退回裸 token，让调用方能从上游的 401 里看出凭证不完整。
func SessionCookie(userID, accessToken string) string {
	token := strings.TrimSpace(accessToken)
	id := strings.TrimSpace(userID)
	if token == "" || id == "" {
		return token
	}
	return id + sessionCookieSeparator + token
}

// PlanBreakdown 是本计费周期已消耗额度的来源拆分，单位为美分。
// Total 与 /api/dashboard/get-aggregated-usage-events 的 totalCostCents 对得上。
type PlanBreakdown struct {
	IncludedCents float64 `json:"included"`
	BonusCents    float64 `json:"bonus"`
	TotalCents    float64 `json:"total"`
}

// PlanUsage 是订阅内额度的用量。金额字段单位均为美分。
//
// AutoPercentUsed 与 APIPercentUsed 是两个独立维度，不是同一条进度条的两段：
// 前者对应 Auto 模型，后者对应"包含的 API 用量"。Cursor 自己的面板也是分开展示的。
type PlanUsage struct {
	Enabled          bool          `json:"enabled"`
	UsedCents        float64       `json:"used"`
	LimitCents       float64       `json:"limit"`
	RemainingCents   *float64      `json:"remaining"`
	Breakdown        PlanBreakdown `json:"breakdown"`
	AutoPercentUsed  float64       `json:"autoPercentUsed"`
	APIPercentUsed   float64       `json:"apiPercentUsed"`
	TotalPercentUsed float64       `json:"totalPercentUsed"`
}

// OnDemandUsage 是超出订阅额度后的按量消费。Limit 为 nil 表示未设上限。
type OnDemandUsage struct {
	Enabled        bool     `json:"enabled"`
	UsedCents      float64  `json:"used"`
	LimitCents     *float64 `json:"limit"`
	RemainingCents *float64 `json:"remaining"`
}

// UsageScope 区分个人额度与团队额度，个人账号 teamUsage 为空对象。
type UsageScope struct {
	Plan     *PlanUsage     `json:"plan"`
	OnDemand *OnDemandUsage `json:"onDemand"`
}

// UsageSummary 是 GET /api/usage-summary 的响应。
type UsageSummary struct {
	BillingCycleStart string `json:"billingCycleStart"`
	BillingCycleEnd   string `json:"billingCycleEnd"`
	MembershipType    string `json:"membershipType"`
	LimitType         string `json:"limitType"`
	IsUnlimited       bool   `json:"isUnlimited"`

	// 上游已经把百分比渲染成了人话，直接透传给前端当 tooltip，避免我们二次解释错。
	AutoDisplayMessage  string `json:"autoModelSelectedDisplayMessage"`
	NamedDisplayMessage string `json:"namedModelSelectedDisplayMessage"`

	IndividualUsage UsageScope `json:"individualUsage"`
	TeamUsage       UsageScope `json:"teamUsage"`
}

// Scope 返回该账号实际生效的额度口径。
//
// 只有团队池带了 plan 才用团队池。Team / Enterprise 成员常见形态是：
// individualUsage.plan 有座位额度，teamUsage 只有 onDemand、没有 plan。
// 旧逻辑把「teamUsage.onDemand != nil」也当成团队池，会把个人进度条整段丢掉，
// 页面上就只剩 Enterprise 徽标和「查询」。
func (s *UsageSummary) Scope() UsageScope {
	if s == nil {
		return UsageScope{}
	}
	if s.TeamUsage.Plan != nil {
		return s.TeamUsage
	}
	return s.IndividualUsage
}

// CycleEnd 解析计费周期结束时间，解析失败返回零值。
func (s *UsageSummary) CycleEnd() time.Time {
	if s == nil {
		return time.Time{}
	}
	return parseCursorTime(s.BillingCycleEnd)
}

// CycleStart 解析计费周期开始时间，解析失败返回零值。
func (s *UsageSummary) CycleStart() time.Time {
	if s == nil {
		return time.Time{}
	}
	return parseCursorTime(s.BillingCycleStart)
}

func parseCursorTime(raw string) time.Time {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

// StripeProfile 是 GET /api/auth/stripe 的响应，用于订阅状态与扣款失败告警。
type StripeProfile struct {
	MembershipType     string `json:"membershipType"`
	SubscriptionStatus string `json:"subscriptionStatus"`
	LastPaymentFailed  bool   `json:"lastPaymentFailed"`
	// PaymentRecoveryAction 形如 update_payment_method，为空表示无需处理。
	PaymentRecoveryAction   string `json:"paymentRecoveryAction"`
	IsOnBillableAuto        bool   `json:"isOnBillableAuto"`
	IsTeamMember            bool   `json:"isTeamMember"`
	IsYearlyPlan            bool   `json:"isYearlyPlan"`
	PendingCancellationDate string `json:"pendingCancellationDate"`
}

// Healthy 报告订阅是否处于可正常计费的状态。
func (p *StripeProfile) Healthy() bool {
	if p == nil {
		return true
	}
	if p.LastPaymentFailed {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(p.SubscriptionStatus)) {
	case "", "active", "trialing":
		return true
	default:
		return false
	}
}

// FetchUsageSummary 拉取账号的订阅额度用量。
func FetchUsageSummary(ctx context.Context, opts *Options, cookie string) (*UsageSummary, error) {
	var summary UsageSummary
	if err := dashboardGet(ctx, opts, cookie, usageSummaryPath, &summary); err != nil {
		return nil, err
	}
	return &summary, nil
}

// FetchStripeProfile 拉取账号的订阅/扣款状态。
func FetchStripeProfile(ctx context.Context, opts *Options, cookie string) (*StripeProfile, error) {
	var profile StripeProfile
	if err := dashboardGet(ctx, opts, cookie, stripeProfilePath, &profile); err != nil {
		return nil, err
	}
	return &profile, nil
}

// dashboardGet 发一次 dashboard GET 并解码 JSON。
func dashboardGet(ctx context.Context, opts *Options, cookie, path string, out any) error {
	client, err := opts.client()
	if err != nil {
		return err
	}
	if strings.TrimSpace(cookie) == "" {
		return &HTTPError{Status: http.StatusUnauthorized, Operation: "usage " + path, Body: "missing session cookie"}
	}

	req, err := newRequest(ctx, http.MethodGet, LoginHost+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Cookie", sessionCookieName+"="+cookie)
	req.Header.Set("Origin", LoginHost)
	req.Header.Set("Referer", LoginHost+"/dashboard")

	status, body, err := do(client, req)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return &HTTPError{Status: status, Operation: "usage " + path, Body: string(body)}
	}
	// 凭证无效时上游回 307 跳 WorkOS 登录页。http.Client 默认跟随重定向，
	// 于是我们拿到的是 200 + 一坨 HTML。不识别出来的话会退化成一条难懂的
	// JSON 解析错误，把"需要重新授权"误报成"上游格式变了"。
	if isHTMLBody(body) {
		return &HTTPError{
			Status:    http.StatusUnauthorized,
			Operation: "usage " + path,
			Body:      "redirected to the login page; the session cookie is invalid or expired",
		}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode cursor %s response: %w", path, err)
	}
	return nil
}

func isHTMLBody(body []byte) bool {
	trimmed := strings.TrimLeft(string(body), " \t\r\n")
	return strings.HasPrefix(trimmed, "<")
}
