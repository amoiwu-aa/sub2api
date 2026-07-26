package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/stretchr/testify/require"
)

func catalogModels() []kiro.AvailableModel {
	return []kiro.AvailableModel{
		{ModelID: "claude-sonnet-4.5", RateMultiplier: 1.3,
			SupportedInputTypes: []string{"TEXT", "IMAGE"},
			TokenLimits:         &kiro.ModelTokenLimits{MaxInputTokens: 200000}},
		{ModelID: "glm-5", RateMultiplier: 0.5,
			SupportedInputTypes: []string{"TEXT"},
			TokenLimits:         &kiro.ModelTokenLimits{MaxInputTokens: 200000}},
	}
}

func kiroCatalogAccount(id int64) *Account {
	return &Account{ID: id, Platform: PlatformKiro, Type: AccountTypeOAuth}
}

// waitFor 等后台刷新落库，避免依赖固定 sleep。
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("等待超时")
}

// TestCatalogCacheNeverBlocks 钉住最重要的那条约束：
// 缓存未命中时必须立刻返回 nil，绝不能等后台拉取——拉一次约 2 秒，
// 放在请求路径上会让每小时的第一个请求平白多等两秒。
func TestCatalogCacheNeverBlocks(t *testing.T) {
	var c kiroCatalogCache
	release := make(chan struct{})
	fetch := func(context.Context) ([]kiro.AvailableModel, error) {
		<-release // 卡住不返回
		return catalogModels(), nil
	}

	start := time.Now()
	got := c.Get(kiroCatalogAccount(1), fetch)
	elapsed := time.Since(start)

	require.Nil(t, got, "未命中时必须返回 nil 而不是等待")
	require.Less(t, elapsed, 200*time.Millisecond, "不得阻塞请求路径")
	close(release)
}

func TestCatalogCacheServesAfterRefresh(t *testing.T) {
	var c kiroCatalogCache
	fetch := func(context.Context) ([]kiro.AvailableModel, error) { return catalogModels(), nil }

	require.Nil(t, c.Get(kiroCatalogAccount(1), fetch))
	waitFor(t, func() bool { return c.Get(kiroCatalogAccount(1), fetch) != nil })

	cat := c.Get(kiroCatalogAccount(1), fetch)
	require.NotNil(t, cat)
	require.Equal(t, 2, cat.Len())
	require.False(t, cat.SupportsImages("glm-5"))
	require.True(t, cat.SupportsImages("claude-sonnet-4.5"))
}

// TestCatalogCacheSingleFlight 确认并发请求只触发一次拉取，
// 否则一个冷账号突然来一批请求会把上游打出一串重复调用。
func TestCatalogCacheSingleFlight(t *testing.T) {
	var c kiroCatalogCache
	var calls int32
	release := make(chan struct{})
	fetch := func(context.Context) ([]kiro.AvailableModel, error) {
		atomic.AddInt32(&calls, 1)
		<-release
		return catalogModels(), nil
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); c.Get(kiroCatalogAccount(7), fetch) }()
	}
	wg.Wait()
	close(release)

	require.EqualValues(t, 1, atomic.LoadInt32(&calls), "并发只应触发一次拉取")
}

// TestCatalogCachePerAccount 目录随账号等级变化，不能全局共用一份。
func TestCatalogCachePerAccount(t *testing.T) {
	var c kiroCatalogCache
	full := func(context.Context) ([]kiro.AvailableModel, error) { return catalogModels(), nil }
	limited := func(context.Context) ([]kiro.AvailableModel, error) {
		return catalogModels()[:1], nil
	}

	c.Get(kiroCatalogAccount(1), full)
	c.Get(kiroCatalogAccount(2), limited)
	waitFor(t, func() bool {
		return c.Get(kiroCatalogAccount(1), full) != nil && c.Get(kiroCatalogAccount(2), limited) != nil
	})

	require.Equal(t, 2, c.Get(kiroCatalogAccount(1), full).Len())
	require.Equal(t, 1, c.Get(kiroCatalogAccount(2), limited).Len())
}

// TestCatalogCacheFailureBackoff 拉取失败后要退避，
// 否则一个坏账号每来一个请求就触发一次后台拉取。
func TestCatalogCacheFailureBackoff(t *testing.T) {
	var c kiroCatalogCache
	var calls int32
	fetch := func(context.Context) ([]kiro.AvailableModel, error) {
		atomic.AddInt32(&calls, 1)
		return nil, errors.New("boom")
	}

	require.Nil(t, c.Get(kiroCatalogAccount(3), fetch))
	waitFor(t, func() bool { return atomic.LoadInt32(&calls) == 1 })

	for i := 0; i < 5; i++ {
		require.Nil(t, c.Get(kiroCatalogAccount(3), fetch))
	}
	time.Sleep(50 * time.Millisecond)
	require.EqualValues(t, 1, atomic.LoadInt32(&calls), "失败后应退避，不得每次都重试")
}

// TestCatalogCacheKeepsStaleOnFailure 刷新失败时保留旧快照。
// 模型能力变化很慢，过期的目录仍然比没有强。
func TestCatalogCacheKeepsStaleOnFailure(t *testing.T) {
	var c kiroCatalogCache
	var fail atomic.Bool
	fetch := func(context.Context) ([]kiro.AvailableModel, error) {
		if fail.Load() {
			return nil, errors.New("boom")
		}
		return catalogModels(), nil
	}

	c.Get(kiroCatalogAccount(4), fetch)
	waitFor(t, func() bool { return c.Get(kiroCatalogAccount(4), fetch) != nil })

	fail.Store(true)
	c.Invalidate(4)
	require.Nil(t, c.Get(kiroCatalogAccount(4), fetch)) // 刚失效，还没有快照
	waitFor(t, func() bool { return true })

	// 再走一遍成功路径，确认失败不会永久毁掉缓存
	fail.Store(false)
	c.Invalidate(4)
	c.Get(kiroCatalogAccount(4), fetch)
	waitFor(t, func() bool { return c.Get(kiroCatalogAccount(4), fetch) != nil })
	require.NotNil(t, c.Get(kiroCatalogAccount(4), fetch))
}

func TestCatalogCacheNilSafe(t *testing.T) {
	var c *kiroCatalogCache
	require.Nil(t, c.Get(kiroCatalogAccount(1), nil))
	require.NotPanics(t, func() { c.Invalidate(1) })

	var real kiroCatalogCache
	require.Nil(t, real.Get(nil, nil))
}
