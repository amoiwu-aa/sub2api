package ipreputation

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// ErrNoProviders is returned when reputation lookup is enabled but every
// provider is disabled or missing its API key.
var ErrNoProviders = errors.New("ipreputation: no providers configured")

// Cache stores merged reports keyed by exit address.
//
// Keying on the address rather than the proxy id matters: several proxies in a
// pool routinely share one exit, and reputation data barely moves day to day,
// so a batch check over a whole proxy list collapses into a handful of lookups.
type Cache interface {
	Get(ctx context.Context, ip string) (*Report, bool)
	Set(ctx context.Context, ip string, report *Report, ttl time.Duration)
}

// Checker fans a lookup out to every configured provider and merges the answers.
type Checker struct {
	providers []Provider
	cache     Cache
	ttl       time.Duration
	timeout   time.Duration
	group     singleflight.Group
}

// CheckerOptions configures a Checker.
type CheckerOptions struct {
	Providers []Provider
	Cache     Cache
	// TTL is how long a merged report stays valid. Reputation databases update
	// on the order of days, so hours are appropriate here.
	TTL time.Duration
	// Timeout bounds the whole fan-out, not each provider.
	Timeout time.Duration
}

const (
	defaultCheckerTTL     = 24 * time.Hour
	defaultCheckerTimeout = 8 * time.Second
)

func NewChecker(opts CheckerOptions) *Checker {
	ttl := opts.TTL
	if ttl <= 0 {
		ttl = defaultCheckerTTL
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultCheckerTimeout
	}
	return &Checker{
		providers: opts.Providers,
		cache:     opts.Cache,
		ttl:       ttl,
		timeout:   timeout,
	}
}

// Enabled reports whether any provider is available.
func (c *Checker) Enabled() bool { return c != nil && len(c.providers) > 0 }

// Check returns the merged reputation report for ip, serving from cache when
// possible. Concurrent lookups for the same address collapse into one fan-out.
func (c *Checker) Check(ctx context.Context, ip string) (*Report, error) {
	if !c.Enabled() {
		return nil, ErrNoProviders
	}
	ip = strings.TrimSpace(ip)
	if net.ParseIP(ip) == nil {
		return nil, errors.New("ipreputation: invalid ip address")
	}

	if c.cache != nil {
		if report, ok := c.cache.Get(ctx, ip); ok && report != nil {
			cached := *report
			cached.Cached = true
			return &cached, nil
		}
	}

	result, err, _ := c.group.Do(ip, func() (any, error) {
		// Detach from the caller's cancellation: a batch check where the
		// operator navigates away should still populate the cache for the rest
		// of the run instead of leaving a half-finished lookup behind.
		lookupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.timeout)
		defer cancel()

		verdicts := c.fanOut(lookupCtx, ip)
		report := Merge(ip, verdicts)
		if c.cache != nil && hasSuccessfulVerdict(verdicts) {
			c.cache.Set(ctx, ip, report, c.ttl)
		}
		return report, nil
	})
	if err != nil {
		return nil, err
	}
	report, ok := result.(*Report)
	if !ok || report == nil {
		return nil, errors.New("ipreputation: unexpected lookup result")
	}
	clone := *report
	return &clone, nil
}

func (c *Checker) fanOut(ctx context.Context, ip string) []Verdict {
	verdicts := make([]Verdict, len(c.providers))
	var wg sync.WaitGroup
	for i, provider := range c.providers {
		wg.Add(1)
		go func(idx int, p Provider) {
			defer wg.Done()
			verdicts[idx] = p.Lookup(ctx, ip)
		}(i, provider)
	}
	wg.Wait()
	return verdicts
}

// hasSuccessfulVerdict keeps an all-errors round (rate limited, network down)
// out of the cache so the next check retries instead of serving the failure for
// a full TTL.
func hasSuccessfulVerdict(verdicts []Verdict) bool {
	for _, v := range verdicts {
		if v.OK {
			return true
		}
	}
	return false
}
