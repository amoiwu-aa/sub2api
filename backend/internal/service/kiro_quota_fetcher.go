package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
)

const (
	kiroQuotaHTTPTimeout       = 30 * time.Second
	kiroQuotaHeaderTimeout     = 20 * time.Second
	kiroQuotaFetchDeadline     = 30 * time.Second
	kiroCreditsProgressDivisor = 100
)

// KiroQuotaFetcher 从 Kiro 上游拉账号额度。
//
// 依赖面刻意与 AntigravityQuotaFetcher 保持一致（只要 proxyRepo）：
// access token 直接从账号凭证里取，续期由后台的 token refresher 负责。
// token 过期时上游会回 401，FetchQuota 把它翻译成 needs_reauth 而不是硬报错，
// 这样一个待重新授权的账号不会把整个用量面板打挂。
//
// # 额度归属：企业账号是组织共享池，不是这个账号私有的
//
// 拉回来的额度挂在 profileArn 上，而 profileArn 里嵌着 AWS 账号号码：
//
//	免费（social）  arn:aws:codewhisperer:us-east-1:699475941385:profile/XXXX   50 credits
//	企业（IdC）     arn:aws:codewhisperer:us-east-1:317380566887:profile/YYYY   5000 credits
//
// 企业那个 AWS 账号是**整个组织**的，5000 credits 由组织成员共用。实测把一个
// 企业账号导进来后，即使本服务一个请求都没发，currentUsage 也在持续上涨——
// 那是组织里其他人（IDE、其他客户端）在消耗同一个池子。
//
// 对账号池的两点影响：
//
//   - 企业账号进池会和同事抢额度，且 kiro_exhausted 触发时意味着整个组织
//     都用完了，不只是这一个「账号」。运维看到 5000 的池子被打满时，别只查
//     本服务的调用量。
//   - 免费账号那种 50 credits 的独立小池反而更适合池化：各归各的，
//     一个号用尽不影响其他号。
//
// 上游没有按人拆分的接口：GetUsageLimits 的 userInfo 是当前 token 持有者
// 自己，不是消费者名单；schema 里的 ByUserAnalytics 只是个 {toggle, startUrl}
// 开关，按人统计的数据流向 AWS 侧的管理控制台，不回传给客户端。
// 也就是说「谁用掉的」只能去 AWS Console 查，这里查不到。
type KiroQuotaFetcher struct {
	proxyRepo ProxyRepository
}

func NewKiroQuotaFetcher(proxyRepo ProxyRepository) *KiroQuotaFetcher {
	return &KiroQuotaFetcher{proxyRepo: proxyRepo}
}

// CanFetch 报告该账号是否具备查额度的条件。
func (f *KiroQuotaFetcher) CanFetch(account *Account) bool {
	if account == nil || account.Platform != PlatformKiro {
		return false
	}
	// profileArn 决定往哪个 region 打，缺了就构造不出客户端。
	return account.GetCredential("access_token") != "" && account.GetCredential("profile_arn") != ""
}

// GetProxyURL 解析账号绑定的代理。
//
// 与 KiroOAuthService 一致：配置了代理却取不到代理对象时返回错误而不是空串，
// 绝不能退化成直连——否则该账号的流量会从服务器出口 IP 打到上游。
func (f *KiroQuotaFetcher) GetProxyURL(ctx context.Context, account *Account) (string, error) {
	if account == nil || account.ProxyID == nil {
		return "", nil
	}
	if f.proxyRepo == nil {
		return "", errors.New("kiro quota: proxy repository is not available")
	}
	proxy, err := f.proxyRepo.GetByID(ctx, *account.ProxyID)
	if err != nil || proxy == nil {
		return "", fmt.Errorf("kiro quota: configured proxy %d is unavailable", *account.ProxyID)
	}
	return proxy.URL(), nil
}

// FetchQuota 拉取账号额度并映射成 UsageInfo。
func (f *KiroQuotaFetcher) FetchQuota(ctx context.Context, account *Account, proxyURL string) (*UsageInfo, error) {
	creds := kiro.CredentialsFromMap(account.Credentials)

	client, err := httpclient.GetClient(httpclient.Options{
		ProxyURL:              proxyURL,
		Timeout:               kiroQuotaHTTPTimeout,
		ResponseHeaderTimeout: kiroQuotaHeaderTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("kiro quota: build http client: %w", err)
	}

	qClient, err := kiro.NewClient(client, creds)
	if err != nil {
		return nil, fmt.Errorf("kiro quota: build client: %w", err)
	}

	fetchCtx, cancel := context.WithTimeout(ctx, kiroQuotaFetchDeadline)
	defer cancel()

	limits, err := qClient.GetUsageLimits(fetchCtx)
	if err != nil {
		if degraded := kiroDegradedUsage(err); degraded != nil {
			return degraded, nil
		}
		return nil, err
	}
	return buildKiroUsageInfo(limits, time.Now()), nil
}

// kiroDegradedUsage 把「账号自身有问题」的上游错误转成降级 UsageInfo。
// 返回 nil 表示这个错误应当照常上抛（例如网络抖动）。
func kiroDegradedUsage(err error) *UsageInfo {
	var apiErr *kiro.APIError
	if !errors.As(err, &apiErr) {
		return nil
	}
	now := time.Now()
	usage := &UsageInfo{UpdatedAt: &now, Error: apiErr.Error()}
	switch {
	case apiErr.Status == http.StatusUnauthorized:
		usage.NeedsReauth = true
		usage.ErrorCode = "unauthenticated"
	case apiErr.Status == http.StatusForbidden:
		usage.IsForbidden = true
		usage.ErrorCode = "forbidden"
	case apiErr.Status == http.StatusTooManyRequests:
		usage.ErrorCode = "rate_limited"
	default:
		return nil
	}
	return usage
}

// buildKiroUsageInfo 把上游的额度响应映射成前端用的 UsageInfo。
func buildKiroUsageInfo(limits *kiro.UsageLimits, now time.Time) *UsageInfo {
	usage := &UsageInfo{UpdatedAt: &now}
	if limits == nil {
		return usage
	}

	usage.SubscriptionTierRaw = limits.SubscriptionInfo.SubscriptionTitle
	usage.SubscriptionTier = normalizeKiroTier(limits.SubscriptionInfo.Type)
	usage.KiroOverageEnabled = limits.OverageEnabled()
	usage.KiroExhausted = limits.Exhausted()

	credits := limits.Credits()
	if credits == nil {
		return usage
	}

	progress := &UsageProgress{Utilization: credits.UsedPercent()}
	if resetAt := limits.ResetAt(); !resetAt.IsZero() {
		progress.ResetsAt = &resetAt
		if remaining := int(resetAt.Sub(now).Seconds()); remaining > 0 {
			progress.RemainingSeconds = remaining
		}
	}
	usage.KiroCredits = progress
	usage.KiroCreditsUsed = credits.Used()
	usage.KiroCreditsLimit = credits.Limit()
	usage.KiroOverageRate = credits.OverageRate
	usage.KiroCurrency = credits.Currency
	if trial := credits.FreeTrialInfo; trial != nil {
		usage.KiroFreeTrialStatus = trial.FreeTrialStatus
	}
	return usage
}

// normalizeKiroTier 把上游的套餐标识归一成 FREE / PRO / UNKNOWN，
// 与 Antigravity 的 subscription_tier 口径对齐，方便前端统一渲染。
func normalizeKiroTier(rawType string) string {
	upper := strings.ToUpper(strings.TrimSpace(rawType))
	switch {
	case upper == "":
		return "UNKNOWN"
	case strings.Contains(upper, "FREE"):
		return "FREE"
	case strings.Contains(upper, "PRO"):
		return "PRO"
	default:
		return "UNKNOWN"
	}
}
