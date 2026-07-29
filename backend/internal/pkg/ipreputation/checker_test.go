package ipreputation

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type stubProvider struct {
	name    string
	calls   atomic.Int32
	verdict Verdict
	delay   time.Duration
}

func (p *stubProvider) Name() string { return p.name }

func (p *stubProvider) Lookup(ctx context.Context, _ string) Verdict {
	p.calls.Add(1)
	if p.delay > 0 {
		select {
		case <-time.After(p.delay):
		case <-ctx.Done():
			return failedVerdict(p.name, 0, ctx.Err())
		}
	}
	verdict := p.verdict
	verdict.Source = p.name
	return verdict
}

type memoryCache struct {
	mu      sync.Mutex
	entries map[string]*Report
	sets    int
}

func newMemoryCache() *memoryCache {
	return &memoryCache{entries: map[string]*Report{}}
}

func (c *memoryCache) Get(_ context.Context, ip string) (*Report, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	report, ok := c.entries[ip]
	return report, ok
}

func (c *memoryCache) Set(_ context.Context, ip string, report *Report, _ time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[ip] = report
	c.sets++
}

func TestCheckerDisabledWithoutProviders(t *testing.T) {
	checker := NewChecker(CheckerOptions{})
	require.False(t, checker.Enabled())

	_, err := checker.Check(context.Background(), "1.1.1.1")
	require.ErrorIs(t, err, ErrNoProviders)
}

func TestCheckerRejectsInvalidIP(t *testing.T) {
	checker := NewChecker(CheckerOptions{Providers: []Provider{&stubProvider{name: "a"}}})

	_, err := checker.Check(context.Background(), "not-an-ip")
	require.Error(t, err)
}

func TestCheckerServesFromCacheAndMarksIt(t *testing.T) {
	provider := &stubProvider{name: "a", verdict: Verdict{OK: true, Datacenter: boolPtr(true)}}
	cache := newMemoryCache()
	checker := NewChecker(CheckerOptions{Providers: []Provider{provider}, Cache: cache})

	first, err := checker.Check(context.Background(), "1.1.1.1")
	require.NoError(t, err)
	require.False(t, first.Cached)

	second, err := checker.Check(context.Background(), "1.1.1.1")
	require.NoError(t, err)
	require.True(t, second.Cached)
	require.EqualValues(t, 1, provider.calls.Load(), "cached address must not be looked up again")
}

func TestCheckerDoesNotCacheAllFailedRound(t *testing.T) {
	provider := &stubProvider{name: "a", verdict: Verdict{OK: false, Error: "rate limited"}}
	cache := newMemoryCache()
	checker := NewChecker(CheckerOptions{Providers: []Provider{provider}, Cache: cache})

	_, err := checker.Check(context.Background(), "1.1.1.1")
	require.NoError(t, err)
	_, err = checker.Check(context.Background(), "1.1.1.1")
	require.NoError(t, err)

	require.Zero(t, cache.sets, "a fully failed round must not be cached for a whole TTL")
	require.EqualValues(t, 2, provider.calls.Load())
}

func TestCheckerCollapsesConcurrentLookups(t *testing.T) {
	provider := &stubProvider{
		name:    "a",
		verdict: Verdict{OK: true, Datacenter: boolPtr(false)},
		delay:   80 * time.Millisecond,
	}
	checker := NewChecker(CheckerOptions{Providers: []Provider{provider}, Cache: newMemoryCache()})

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := checker.Check(context.Background(), "1.1.1.1")
			require.NoError(t, err)
		}()
	}
	wg.Wait()

	require.EqualValues(t, 1, provider.calls.Load(),
		"a batch check over proxies sharing one exit must spend a single lookup")
}

func TestCheckerMergesAcrossProviders(t *testing.T) {
	checker := NewChecker(CheckerOptions{Providers: []Provider{
		&stubProvider{name: "a", verdict: Verdict{OK: true, Datacenter: boolPtr(false), RiskScore: intPtr(0)}},
		&stubProvider{name: "b", verdict: Verdict{OK: true, Datacenter: boolPtr(true), RiskScore: intPtr(66)}},
	}})

	report, err := checker.Check(context.Background(), "1.1.1.1")
	require.NoError(t, err)
	require.Len(t, report.Sources, 2)
	require.Equal(t, 66, *report.RiskScore)
	require.Equal(t, []string{FlagDatacenter}, report.Conflicts)
}

func TestCheckerSurvivesCallerCancellation(t *testing.T) {
	provider := &stubProvider{
		name:    "a",
		verdict: Verdict{OK: true, Datacenter: boolPtr(true)},
		delay:   30 * time.Millisecond,
	}
	checker := NewChecker(CheckerOptions{Providers: []Provider{provider}})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	report, err := checker.Check(ctx, "1.1.1.1")
	require.NoError(t, err)
	require.True(t, report.Sources[0].OK, "lookup must not inherit the caller's cancellation")
}

func TestCheckerTimeoutProducesFailedVerdict(t *testing.T) {
	provider := &stubProvider{name: "a", delay: time.Second}
	checker := NewChecker(CheckerOptions{
		Providers: []Provider{provider},
		Timeout:   20 * time.Millisecond,
	})

	report, err := checker.Check(context.Background(), "1.1.1.1")
	require.NoError(t, err)
	require.False(t, report.Sources[0].OK, "a provider that outruns the fan-out budget must fail, not block")
	require.Equal(t, RiskUnknown, report.RiskLevel)
}
