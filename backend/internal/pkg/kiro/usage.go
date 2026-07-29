package kiro

// GetUsageLimits 是账号池最需要的那个接口：一次调用就能知道
// 某个 Kiro 账号还剩多少额度、什么时候重置、是不是已经超了。
//
// 协议来源：Kiro 1.0.212 的 kiro.kiro-agent 扩展。
// schema 里的 HTTP 绑定是 `["GET", "/getUsageLimits", 200]`，
// 注意路径首字母小写，与同一 client 上的 `/ListAvailableModels` 风格不一致。
//
// 它挂在 CodeWhispererRuntimeClient 上，也就是和 GenerateAssistantResponse、
// ListAvailableModels 同一个 client——用同样的 q.<region> 端点和 bearer 凭证，
// 不需要另建连接。实测不带任何查询参数即可返回 200。
//
// （扩展里还有一份 KiroControlPlaneBearerService 版本的 GetUsageLimits，
// 走独立的 control plane 端点。既然 runtime 这条能通，就不必再引一套。）

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	getUsageLimitsPath = "/getUsageLimits"
	// listAvailableProfilesPath 是 POST，与同 client 上 GET 的两个接口不同。
	listAvailableProfilesPath = "/ListAvailableProfiles"
	maxUsageLimitsBody        = 1 << 20

	// ResourceTypeCredit 是目前唯一观测到的计量维度。
	ResourceTypeCredit = "CREDIT"

	// OverageStatusEnabled 表示账号允许超额消费。
	OverageStatusEnabled = "ENABLED"
	// FreeTrialStatusExpired 表示试用额度已用完或过期。
	FreeTrialStatusExpired = "EXPIRED"
)

// UsageLimits 是 GET /getUsageLimits 的响应。
type UsageLimits struct {
	DaysUntilReset int `json:"daysUntilReset"`
	// NextDateReset 是 Unix 秒，上游用浮点数下发（例如 1.7855424E9）。
	NextDateReset        float64              `json:"nextDateReset"`
	SubscriptionInfo     SubscriptionInfo     `json:"subscriptionInfo"`
	OverageConfiguration OverageConfiguration `json:"overageConfiguration"`
	UsageBreakdownList   []UsageBreakdown     `json:"usageBreakdownList"`
	UserInfo             UserInfo             `json:"userInfo"`
}

type SubscriptionInfo struct {
	// SubscriptionTitle 是给人看的套餐名，例如 "KIRO FREE"。
	SubscriptionTitle string `json:"subscriptionTitle"`
	// Type 是机器可读的套餐标识，例如 "Q_DEVELOPER_STANDALONE_FREE"。
	Type string `json:"type"`
	// OverageCapability 取 OVERAGE_CAPABLE / OVERAGE_INCAPABLE。
	OverageCapability string `json:"overageCapability"`
	// UpgradeCapability 取 UPGRADE_CAPABLE / UPGRADE_INCAPABLE。
	UpgradeCapability            string `json:"upgradeCapability"`
	SubscriptionManagementTarget string `json:"subscriptionManagementTarget"`
}

type OverageConfiguration struct {
	// OverageStatus 取 ENABLED / DISABLED。
	OverageStatus string   `json:"overageStatus"`
	OverageLimit  *float64 `json:"overageLimit"`
}

type UserInfo struct {
	UserID string `json:"userId"`
	Email  string `json:"email"`
}

// UsageBreakdown 是按资源类型细分的用量。
//
// 每个数值都有「整数版」和「WithPrecision 版」两套字段，且**必须优先用后者**：
// 实测账号已消耗 0.01 credit 时，currentUsage 是 0 而
// currentUsageWithPrecision 是 0.01——只看整数版会把「已用一点」误判成
// 「完全没用」。Kiro 自己的映射函数也是 `WithPrecision ?? 整数版 ?? 0`。
type UsageBreakdown struct {
	ResourceType      string `json:"resourceType"`
	Unit              string `json:"unit"`
	DisplayName       string `json:"displayName"`
	DisplayNamePlural string `json:"displayNamePlural"`

	CurrentUsage              int      `json:"currentUsage"`
	CurrentUsageWithPrecision *float64 `json:"currentUsageWithPrecision"`
	UsageLimit                int      `json:"usageLimit"`
	UsageLimitWithPrecision   *float64 `json:"usageLimitWithPrecision"`

	CurrentOverages              int      `json:"currentOverages"`
	CurrentOveragesWithPrecision *float64 `json:"currentOveragesWithPrecision"`
	OverageCap                   int      `json:"overageCap"`
	OverageCapWithPrecision      *float64 `json:"overageCapWithPrecision"`
	OverageRate                  float64  `json:"overageRate"`
	OverageCharges               float64  `json:"overageCharges"`
	Currency                     string   `json:"currency"`

	NextDateReset float64        `json:"nextDateReset"`
	FreeTrialInfo *FreeTrialInfo `json:"freeTrialInfo"`
}

type FreeTrialInfo struct {
	// FreeTrialStatus 取 EXPIRED / ACTIVE 等。
	FreeTrialStatus           string   `json:"freeTrialStatus"`
	FreeTrialExpiry           float64  `json:"freeTrialExpiry"`
	CurrentUsage              int      `json:"currentUsage"`
	CurrentUsageWithPrecision *float64 `json:"currentUsageWithPrecision"`
	UsageLimit                int      `json:"usageLimit"`
	UsageLimitWithPrecision   *float64 `json:"usageLimitWithPrecision"`
}

// GetUsageLimits 查询账号的额度与用量。
func (c *Client) GetUsageLimits(ctx context.Context) (*UsageLimits, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+getUsageLimitsPath, nil)
	if err != nil {
		return nil, fmt.Errorf("build kiro usage limits request: %w", err)
	}
	c.applyCommonHeaders(req)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("kiro usage limits request: %w", err)
	}
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxUsageLimitsBody))
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{Status: resp.StatusCode, Operation: "GetUsageLimits", Body: string(raw)}
	}
	if readErr != nil {
		return nil, fmt.Errorf("read kiro usage limits response: %w", readErr)
	}

	var parsed UsageLimits
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("decode kiro usage limits response: %w", err)
	}
	return &parsed, nil
}

// Profile 是 ListAvailableProfiles 返回的单个 profile。
type Profile struct {
	ARN         string `json:"arn"`
	ProfileName string `json:"profileName,omitempty"`
}

type listAvailableProfilesResponse struct {
	Profiles  []Profile `json:"profiles"`
	NextToken string    `json:"nextToken,omitempty"`
}

// ListAvailableProfiles 查账号可用的 profile 列表。
//
// social 登录时 profileArn 由 auth 服务的 /oauth/token 直接下发，
// 这个接口是给 IdC 用的——那条链不返回 profileArn，必须显式查一次。
//
// HTTP 绑定是 ["POST", "/ListAvailableProfiles", 200]，注意是 POST，
// 与同一 client 上 GET 的 /getUsageLimits、/ListAvailableModels 不同。
func (c *Client) ListAvailableProfiles(ctx context.Context) ([]Profile, error) {
	var all []Profile
	nextToken := ""

	for page := 0; page < maxListModelsPage; page++ {
		body := map[string]any{}
		if nextToken != "" {
			body["nextToken"] = nextToken
		}
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode kiro list profiles request: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			c.endpoint+listAvailableProfilesPath, bytes.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("build kiro list profiles request: %w", err)
		}
		c.applyCommonHeaders(req)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("kiro list profiles request: %w", err)
		}
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxUsageLimitsBody))
		_ = resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, &APIError{Status: resp.StatusCode, Operation: "ListAvailableProfiles", Body: string(raw)}
		}
		if readErr != nil {
			return nil, fmt.Errorf("read kiro list profiles response: %w", readErr)
		}

		var parsed listAvailableProfilesResponse
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return nil, fmt.Errorf("decode kiro list profiles response: %w", err)
		}
		all = append(all, parsed.Profiles...)
		nextToken = strings.TrimSpace(parsed.NextToken)
		if nextToken == "" {
			break
		}
	}
	return all, nil
}

// RateMultiplierByModel 把模型目录压成 modelId -> 计费倍率的表。
//
// 倍率跨度很大（实测 0.05 ~ 2.4，相差 48 倍），选模型是最直接的省钱手段，
// 这张表让路由与计费能用上游的真实倍率而不是估算。
//
// 但**不能拿它在请求发出前算准成本**：credit 消耗同时取决于输出长度。
// 早先一组实验里五次请求的 credit 逐位相同，让人以为计费是输入的确定性
// 函数——那只是因为都要求「用一个词回答」，输出长度恰好一样。换成长度
// 不定的回答后，同样输入的 credit 就不同了（实测 0.082114 vs 0.110121）。
// 真实成本仍以 meteringEvent 为准。
func RateMultiplierByModel(models []AvailableModel) map[string]float64 {
	if len(models) == 0 {
		return nil
	}
	out := make(map[string]float64, len(models))
	for _, m := range models {
		if m.ModelID == "" || m.RateMultiplier <= 0 {
			continue
		}
		out[m.ModelID] = m.RateMultiplier
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Used 返回已消耗量，优先取高精度字段。
func (b *UsageBreakdown) Used() float64 {
	if b == nil {
		return 0
	}
	if b.CurrentUsageWithPrecision != nil {
		return *b.CurrentUsageWithPrecision
	}
	return float64(b.CurrentUsage)
}

// Limit 返回额度上限，优先取高精度字段。
func (b *UsageBreakdown) Limit() float64 {
	if b == nil {
		return 0
	}
	if b.UsageLimitWithPrecision != nil {
		return *b.UsageLimitWithPrecision
	}
	return float64(b.UsageLimit)
}

// Remaining 返回剩余额度；已超额时返回 0 而不是负数。
func (b *UsageBreakdown) Remaining() float64 {
	remaining := b.Limit() - b.Used()
	if remaining < 0 {
		return 0
	}
	return remaining
}

// UsedPercent 返回已用百分比；没有上限时返回 0。
func (b *UsageBreakdown) UsedPercent() float64 {
	limit := b.Limit()
	if limit <= 0 {
		return 0
	}
	return b.Used() / limit * 100
}

// Exhausted 报告额度是否已经用尽。
func (b *UsageBreakdown) Exhausted() bool {
	return b.Limit() > 0 && b.Used() >= b.Limit()
}

// Breakdown 按 resourceType 取某一维度的用量，找不到返回 nil。
func (u *UsageLimits) Breakdown(resourceType string) *UsageBreakdown {
	if u == nil {
		return nil
	}
	for i := range u.UsageBreakdownList {
		if u.UsageBreakdownList[i].ResourceType == resourceType {
			return &u.UsageBreakdownList[i]
		}
	}
	return nil
}

// Credits 是 CREDIT 维度的快捷方式——目前账号池只关心这一个。
func (u *UsageLimits) Credits() *UsageBreakdown {
	return u.Breakdown(ResourceTypeCredit)
}

// ResetAt 把 Unix 秒的重置时间转成 time.Time；上游没给则返回零值。
func (u *UsageLimits) ResetAt() time.Time {
	if u == nil || u.NextDateReset <= 0 {
		return time.Time{}
	}
	sec, frac := int64(u.NextDateReset), u.NextDateReset-float64(int64(u.NextDateReset))
	return time.Unix(sec, int64(frac*float64(time.Second))).UTC()
}

// OverageEnabled 报告账号是否开启了超额消费。
// 没开的话额度用尽就直接不可用，账号池应当及时轮换。
func (u *UsageLimits) OverageEnabled() bool {
	return u != nil && u.OverageConfiguration.OverageStatus == OverageStatusEnabled
}

// Exhausted 报告账号是否已经不可用：credit 用尽且没有超额兜底。
func (u *UsageLimits) Exhausted() bool {
	if u == nil {
		return false
	}
	credits := u.Credits()
	if credits == nil {
		return false
	}
	return credits.Exhausted() && !u.OverageEnabled()
}
