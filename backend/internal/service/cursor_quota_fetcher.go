package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
)

const (
	cursorQuotaHTTPTimeout   = 30 * time.Second
	cursorQuotaHeaderTimeout = 20 * time.Second
	cursorQuotaFetchDeadline = 30 * time.Second
	// 上游金额一律以美分计，前端统一按美元展示。
	cursorCentsPerDollar = 100
)

// CursorQuotaFetcher 从 Cursor 的 dashboard 接口拉账号额度与订阅状态。
//
// # Cursor 的额度模型和 Claude / OpenAI 不是一回事
//
// Cursor 没有 5h / 7d 滚动窗口，它按自然计费周期结算，周期内分两个维度：
//
//	Auto 用量        自动选模型的消耗，对应 autoPercentUsed
//	包含的 API 用量   指定模型的消耗，对应 apiPercentUsed
//
// 两者都打满之后，超出部分进 onDemand（按量付费），是否允许由账号自己的
// 开关决定。所以这里不该拼任何滚动窗口，直接映射上游的百分比即可。
//
// # 鉴权用的是 cookie，不是 Agent 的 Bearer token
//
// 账号凭证里的 access_token 是 Agent 用的裸 JWT，直接拿去调 dashboard 会被
// WorkOS 弹回登录页。dashboard 认的是 WorkosCursorSessionToken=<user_id>::<jwt>，
// user_id 同样在凭证里，缺了它就查不了额度——CanFetch 会据此拦下。
type CursorQuotaFetcher struct {
	proxyRepo ProxyRepository
}

func NewCursorQuotaFetcher(proxyRepo ProxyRepository) *CursorQuotaFetcher {
	return &CursorQuotaFetcher{proxyRepo: proxyRepo}
}

// CanFetch 报告该账号是否具备查额度的条件。
func (f *CursorQuotaFetcher) CanFetch(account *Account) bool {
	if account == nil || account.Platform != PlatformCursor {
		return false
	}
	return strings.TrimSpace(account.GetCredential("access_token")) != "" &&
		strings.TrimSpace(cursorUserID(account)) != ""
}

// cursorUserID 兼容 user_id / userId 两种落库键名。
func cursorUserID(account *Account) string {
	if account == nil {
		return ""
	}
	if v := strings.TrimSpace(account.GetCredential("user_id")); v != "" {
		return v
	}
	return strings.TrimSpace(account.GetCredential("userId"))
}

// missingCredentialReason 说明缺哪个字段，避免前端只显示一个空横杠。
func cursorMissingCredentialReason(account *Account) string {
	switch {
	case account == nil:
		return "cursor credentials incomplete: account is missing"
	case strings.TrimSpace(account.GetCredential("access_token")) == "":
		return "cursor credentials incomplete: missing access_token"
	case strings.TrimSpace(cursorUserID(account)) == "":
		return "cursor credentials incomplete: missing user_id (required to build the dashboard session cookie)"
	default:
		return "cursor credentials incomplete"
	}
}

// GetProxyURL 解析账号绑定的代理。
//
// 与 KiroQuotaFetcher 一致：配置了代理却取不到代理对象时返回错误而不是空串，
// 绝不能退化成直连——否则该账号的流量会从服务器出口 IP 打到上游。
func (f *CursorQuotaFetcher) GetProxyURL(ctx context.Context, account *Account) (string, error) {
	if account == nil || account.ProxyID == nil {
		return "", nil
	}
	if f.proxyRepo == nil {
		return "", errors.New("cursor quota: proxy repository is not available")
	}
	proxy, err := f.proxyRepo.GetByID(ctx, *account.ProxyID)
	if err != nil || proxy == nil {
		return "", fmt.Errorf("cursor quota: configured proxy %d is unavailable", *account.ProxyID)
	}
	return proxy.URL(), nil
}

// FetchQuota 拉取账号额度并映射成 UsageInfo。
func (f *CursorQuotaFetcher) FetchQuota(ctx context.Context, account *Account, proxyURL string) (*UsageInfo, error) {
	client, err := httpclient.GetClient(httpclient.Options{
		ProxyURL:              proxyURL,
		Timeout:               cursorQuotaHTTPTimeout,
		ResponseHeaderTimeout: cursorQuotaHeaderTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("cursor quota: build http client: %w", err)
	}

	opts := &cursor.Options{HTTPClient: client}
	cookie := cursor.SessionCookie(cursorUserID(account), account.GetCredential("access_token"))

	fetchCtx, cancel := context.WithTimeout(ctx, cursorQuotaFetchDeadline)
	defer cancel()

	summary, err := cursor.FetchUsageSummary(fetchCtx, opts, cookie)
	if err != nil {
		if degraded := cursorDegradedUsage(err); degraded != nil {
			return degraded, nil
		}
		return nil, err
	}

	usage := buildCursorUsageInfo(summary, time.Now())

	// 订阅状态是额外信息，拿不到不该让整个额度面板失败：past_due 这类状态
	// 只影响徽标，用量本身已经查到了。
	if profile, profileErr := cursor.FetchStripeProfile(fetchCtx, opts, cookie); profileErr == nil {
		applyCursorStripeProfile(usage, profile)
	}
	return usage, nil
}

// cursorDegradedUsage 把「账号自身有问题」的上游错误转成降级 UsageInfo。
// 返回 nil 表示这个错误应当照常上抛（例如网络抖动）。
func cursorDegradedUsage(err error) *UsageInfo {
	var apiErr *cursor.HTTPError
	if !errors.As(err, &apiErr) {
		return nil
	}
	now := time.Now()
	usage := &UsageInfo{UpdatedAt: &now, Error: apiErr.Error()}
	switch apiErr.Status {
	case http.StatusUnauthorized, http.StatusBadRequest:
		usage.NeedsReauth = true
		usage.ErrorCode = "unauthenticated"
	case http.StatusForbidden:
		usage.IsForbidden = true
		usage.ErrorCode = "forbidden"
	case http.StatusTooManyRequests:
		usage.ErrorCode = "rate_limited"
	default:
		return nil
	}
	return usage
}

// buildCursorUsageInfo 把 usage-summary 映射成前端用的 UsageInfo。
func buildCursorUsageInfo(summary *cursor.UsageSummary, now time.Time) *UsageInfo {
	usage := &UsageInfo{UpdatedAt: &now, Source: "upstream"}
	if summary == nil {
		return usage
	}

	usage.CursorPlan = strings.TrimSpace(summary.MembershipType)
	usage.CursorIsUnlimited = summary.IsUnlimited
	usage.CursorAutoMessage = strings.TrimSpace(summary.AutoDisplayMessage)
	usage.CursorAPIMessage = strings.TrimSpace(summary.NamedDisplayMessage)

	cycleEnd := summary.CycleEnd()
	if start := summary.CycleStart(); !start.IsZero() {
		usage.CursorBillingCycleStart = &start
	}
	if !cycleEnd.IsZero() {
		usage.CursorBillingCycleEnd = &cycleEnd
	}

	scope := summary.Scope()
	if plan := scope.Plan; plan != nil {
		usage.CursorAutoUsage = cursorProgress(plan.AutoPercentUsed, cycleEnd, now)
		usage.CursorAPIUsage = cursorProgress(plan.APIPercentUsed, cycleEnd, now)
		usage.CursorIncludedUsed = centsToDollars(plan.UsedCents)
		usage.CursorIncludedLimit = centsToDollars(plan.LimitCents)
		usage.CursorPeriodTotal = centsToDollars(plan.Breakdown.TotalCents)
		usage.CursorPeriodBonus = centsToDollars(plan.Breakdown.BonusCents)
	}
	if onDemand := scope.OnDemand; onDemand != nil {
		usage.CursorOnDemandEnabled = onDemand.Enabled
		usage.CursorOnDemandUsed = centsToDollars(onDemand.UsedCents)
		if onDemand.LimitCents != nil {
			usage.CursorOnDemandLimit = centsToDollars(*onDemand.LimitCents)
		}
	}
	return usage
}

// applyCursorStripeProfile 把订阅/扣款状态并进 UsageInfo。
func applyCursorStripeProfile(usage *UsageInfo, profile *cursor.StripeProfile) {
	if usage == nil || profile == nil {
		return
	}
	usage.CursorSubscriptionStatus = strings.TrimSpace(profile.SubscriptionStatus)
	usage.CursorPaymentFailed = profile.LastPaymentFailed
	usage.CursorPaymentAction = strings.TrimSpace(profile.PaymentRecoveryAction)
	if usage.CursorPlan == "" {
		usage.CursorPlan = strings.TrimSpace(profile.MembershipType)
	}
}

// cursorProgress 把上游百分比包成 UsageProgress，重置时间用计费周期结束时间。
func cursorProgress(percent float64, cycleEnd, now time.Time) *UsageProgress {
	progress := &UsageProgress{Utilization: percent}
	if cycleEnd.IsZero() {
		return progress
	}
	progress.ResetsAt = &cycleEnd
	if remaining := int(cycleEnd.Sub(now).Seconds()); remaining > 0 {
		progress.RemainingSeconds = remaining
	}
	return progress
}

func centsToDollars(cents float64) float64 {
	return cents / cursorCentsPerDollar
}
