package service

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// quotaPatrolPlatforms 是巡检覆盖的平台：这些平台具备可主动查询的额度数据，
// 并在额度耗尽时可以提前停止调度。
//
// 其余平台要么没有可查的上游额度接口，要么额度耗尽表现为请求失败后由
// failover 自然切走，不需要提前停止调度。
var quotaPatrolPlatforms = []string{PlatformKiro, PlatformCursor, PlatformOpenAI}

// defaultQuotaPatrolInterval 是巡检间隔。
//
// 10 分钟是在「发现得够快」和「别把上游额度接口打爆」之间取的折中：账号数
// 通常是个位数，一轮巡检对每个账号最多一次上游调用，10 分钟一轮即每账号每
// 小时 6 次，远低于任何合理限流；而最坏情况下额度打满后最多多放行 10 分钟
// 的请求。真要更快可以调小，但没必要低于额度缓存的 TTL——那样只是反复读到
// 同一份缓存。
const defaultQuotaPatrolInterval = 10 * time.Minute

// AccountQuotaPatrolService 周期性拉取账号额度，触发耗尽停号与低额预警。
//
// 存在的理由：parkExhaustedKiroAccount 与 parkExhaustedCursorAccount 都挂在
// 用量查询路径上，而该路径原本只有后台面板会触发。也就是说没人打开面板时，
// 额度打满的账号会继续被调度——Kiro 上表现为每次请求都被上游拒绝，Cursor 上
// 更糟，每次请求都计入没有上限的 on-demand 账单。这个巡检去掉了「得有人看
// 面板」这个隐含前提，让停号真正无人值守。
type AccountQuotaPatrolService struct {
	accountRepo  AccountRepository
	usageService *AccountUsageService
	interval     time.Duration

	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

func NewAccountQuotaPatrolService(
	accountRepo AccountRepository,
	usageService *AccountUsageService,
	interval time.Duration,
) *AccountQuotaPatrolService {
	if interval <= 0 {
		interval = defaultQuotaPatrolInterval
	}
	return &AccountQuotaPatrolService{
		accountRepo:  accountRepo,
		usageService: usageService,
		interval:     interval,
		stopCh:       make(chan struct{}),
	}
}

func (s *AccountQuotaPatrolService) Start() {
	if s.accountRepo == nil || s.usageService == nil {
		slog.Warn("quota_patrol.service_disabled", "reason", "missing dependencies")
		return
	}
	s.wg.Add(1)
	go s.loop()
	slog.Info("quota_patrol.service_started",
		"interval", s.interval.String(),
		"platforms", quotaPatrolPlatforms,
	)
}

func (s *AccountQuotaPatrolService) Stop() {
	s.stopOnce.Do(func() {
		close(s.stopCh)
		s.wg.Wait()
	})
}

func (s *AccountQuotaPatrolService) loop() {
	defer s.wg.Done()

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	// 启动即跑一轮：重启后不该留出一整个间隔的空窗。
	s.patrol()

	for {
		select {
		case <-ticker.C:
			s.patrol()
		case <-s.stopCh:
			return
		}
	}
}

// patrol 跑一轮巡检。
//
// 只看可调度的账号：已经被停掉的不必再查，这既省了上游调用，也天然避免了
// 对同一个账号重复写停号日志。
func (s *AccountQuotaPatrolService) patrol() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	accounts, err := s.accountRepo.ListSchedulableByPlatforms(ctx, quotaPatrolPlatforms)
	if err != nil {
		slog.Warn("quota_patrol.list_accounts_failed", "error", err)
		return
	}
	if len(accounts) == 0 {
		return
	}

	checked := 0
	for i := range accounts {
		account := &accounts[i]
		// OpenAI 的配额接口依赖 OAuth 令牌；API Key 账号没有可查询的
		// /wham/usage 端点，不能纳入主动巡检。
		if account.Platform == PlatformOpenAI && account.Type != AccountTypeOAuth {
			continue
		}

		// 停号与预警的判定都在 GetUsage 真正拉取额度那条路径里（见 getKiroUsage /
		// getCursorUsage），这里不重复调用一遍。
		//
		// 之所以能确定判定一定会发生：额度缓存 TTL 是 3 分钟，而巡检间隔 10 分钟，
		// 每一轮都必然穿透缓存走到拉取分支。反过来，万一真命中了缓存，那也只说明
		// 3 分钟内刚有人查过额度，那次查询已经判定过了。两种情况都不会漏。
		// OpenAI's normal path may rely on response-header sampling. The patrol
		// must force an authoritative /wham/usage query so an explicitly
		// exhausted 5h window is removed from scheduling without a dashboard
		// operator having to trigger the check manually.
		forceProbe := account.Platform == PlatformOpenAI
		if _, err := s.usageService.GetUsage(ctx, account.ID, forceProbe); err != nil {
			slog.Warn("quota_patrol.fetch_usage_failed",
				"account_id", account.ID, "platform", account.Platform, "error", err)
			continue
		}
		checked++
	}

	slog.Info("quota_patrol.round_completed", "accounts", len(accounts), "checked", checked)
}
