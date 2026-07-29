package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
)

const (
	// kiroCatalogTTL 是模型目录的有效期。目录变动很慢（模型上下线、
	// 账号升降级），一小时足够，也把上游调用量压到可忽略。
	kiroCatalogTTL = time.Hour
	// kiroCatalogFetchTimeout 是后台拉取目录的上限。
	kiroCatalogFetchTimeout = 30 * time.Second
)

// kiroCatalogEntry 一经存入就**不可变**。
//
// 并发安全就建立在这一点上：刷新时总是 Store 一个全新的 entry，从不就地
// 修改已存入的那个，所以读侧不需要加锁。改这里时务必保持这个性质——
// 一旦开始原地改字段，Get 那边就会读到撕裂的状态。
type kiroCatalogEntry struct {
	catalog *kiro.Catalog
	// failedAt 记录上次拉取失败的时间，用于失败退避，避免一个坏账号
	// 每来一个请求就触发一次后台拉取。
	failedAt time.Time
}

// kiroCatalogCache 按账号缓存模型目录。
//
// 必须按账号缓存而不是全局共用：目录随账号等级变化，实测免费号 9 个模型、
// 企业号 19 个，倍率与上下文上限也各不相同。
//
// 设计上有一条硬约束：**绝不阻塞请求路径**。拉一次目录约 2 秒，放在请求
// 里同步做会让每小时的第一个请求平白多等两秒。所以缓存未命中时直接放行
// （fail open），同时异步拉取，让后续请求逐渐变聪明。目录只用于「能确定
// 会失败就提前拦掉」，拿不到目录时退回原有行为即可，不影响正确性。
type kiroCatalogCache struct {
	entries  sync.Map // accountID -> *kiroCatalogEntry
	inflight sync.Map // accountID -> struct{}，防止同一账号并发重复拉取
}

// Get 返回该账号的模型目录快照；没有或已过期时返回 nil 并触发一次后台刷新。
//
// 返回 nil 是正常情况，调用方必须能在没有目录时照常工作。
func (c *kiroCatalogCache) Get(account *Account, fetch func(context.Context) ([]kiro.AvailableModel, error)) *kiro.Catalog {
	if c == nil || account == nil {
		return nil
	}

	var entry *kiroCatalogEntry
	if v, ok := c.entries.Load(account.ID); ok {
		entry, _ = v.(*kiroCatalogEntry)
	}

	fresh := entry != nil && entry.catalog != nil &&
		time.Since(entry.catalog.FetchedAt()) < kiroCatalogTTL
	if fresh {
		return entry.catalog
	}

	// 失败退避：上次拉取失败后一分钟内不再重试。
	if entry != nil && !entry.failedAt.IsZero() && time.Since(entry.failedAt) < time.Minute {
		return entry.catalog // 可能是 nil，也可能是过期但仍可用的旧快照
	}

	c.refreshAsync(account.ID, fetch)

	// 过期的旧快照比没有强：模型能力不会频繁变，拿它做前置校验依然合理。
	if entry != nil {
		return entry.catalog
	}
	return nil
}

func (c *kiroCatalogCache) refreshAsync(accountID int64, fetch func(context.Context) ([]kiro.AvailableModel, error)) {
	if _, loaded := c.inflight.LoadOrStore(accountID, struct{}{}); loaded {
		return // 已有同账号的拉取在跑
	}

	go func() {
		defer c.inflight.Delete(accountID)

		// 用独立 context：触发它的那个请求结束后不该把拉取一起取消。
		ctx, cancel := context.WithTimeout(context.Background(), kiroCatalogFetchTimeout)
		defer cancel()

		models, err := fetch(ctx)
		if err != nil {
			var prev *kiro.Catalog
			if v, ok := c.entries.Load(accountID); ok {
				if e, _ := v.(*kiroCatalogEntry); e != nil {
					prev = e.catalog
				}
			}
			c.entries.Store(accountID, &kiroCatalogEntry{catalog: prev, failedAt: time.Now()})
			slog.Warn("kiro.model_catalog_refresh_failed", "account_id", accountID, "error", err)
			return
		}

		c.entries.Store(accountID, &kiroCatalogEntry{catalog: kiro.NewCatalog(models, time.Now())})
		slog.Debug("kiro.model_catalog_refreshed", "account_id", accountID, "models", len(models))
	}()
}

// Invalidate 丢弃某账号的目录缓存，账号凭证变化后调用。
func (c *kiroCatalogCache) Invalidate(accountID int64) {
	if c == nil {
		return
	}
	c.entries.Delete(accountID)
}
