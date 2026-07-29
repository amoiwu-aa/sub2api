package repository

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ipreputation"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const (
	ipReputationKeyPrefix    = "proxy:ip_reputation:"
	ipReputationHTTPTimeout  = 10 * time.Second
	ipReputationResponseWait = 8 * time.Second
)

func ipReputationKey(ip string) string {
	return ipReputationKeyPrefix + ip
}

// NewIPReputationChecker builds the reputation checker from config.
//
// Providers that are disabled or missing a required API key are simply left
// out, so the returned checker reports Enabled() == false when nothing is
// configured and the quality check falls back to the ip-api classification it
// already gets for free.
func NewIPReputationChecker(cfg *config.Config, rdb *redis.Client) service.IPReputationChecker {
	if cfg == nil || !cfg.Security.IPReputation.Enabled {
		return ipreputation.NewChecker(ipreputation.CheckerOptions{})
	}
	settings := cfg.Security.IPReputation

	client, err := httpclient.GetClient(httpclient.Options{
		Timeout:               ipReputationHTTPTimeout,
		ResponseHeaderTimeout: ipReputationResponseWait,
		ValidateResolvedIP:    cfg.Security.URLAllowlist.Enabled,
		AllowPrivateHosts:     cfg.Security.URLAllowlist.AllowPrivateHosts,
	})
	if err != nil {
		logger.LegacyPrintf("repository.ip_reputation", "Warning: ip reputation client unavailable, lookups disabled: %v", err)
		return ipreputation.NewChecker(ipreputation.CheckerOptions{})
	}

	providers := buildIPReputationProviders(settings, client)
	if len(providers) == 0 {
		return ipreputation.NewChecker(ipreputation.CheckerOptions{})
	}

	names := make([]string, 0, len(providers))
	for _, p := range providers {
		names = append(names, p.Name())
	}
	logger.LegacyPrintf("repository.ip_reputation", "[IPReputation] enabled providers: %s", strings.Join(names, ", "))

	return ipreputation.NewChecker(ipreputation.CheckerOptions{
		Providers: providers,
		Cache:     newIPReputationCache(rdb),
		TTL:       time.Duration(settings.CacheTTLHours) * time.Hour,
		Timeout:   time.Duration(settings.TimeoutSeconds) * time.Second,
	})
}

func buildIPReputationProviders(settings config.IPReputationConfig, client *http.Client) []ipreputation.Provider {
	providers := make([]ipreputation.Provider, 0, 3)
	if settings.IPAPIIs.Enabled {
		providers = append(providers, ipreputation.NewIPAPIIsProvider(client, settings.IPAPIIs.APIKey))
	}
	if settings.ProxyCheck.Enabled {
		providers = append(providers, ipreputation.NewProxyCheckProvider(client, settings.ProxyCheck.APIKey))
	}
	if settings.IPQualityScore.Enabled && strings.TrimSpace(settings.IPQualityScore.APIKey) != "" {
		providers = append(providers, ipreputation.NewIPQualityScoreProvider(
			client,
			settings.IPQualityScore.APIKey,
			settings.IPQualityScore.Strictness,
		))
	}
	return providers
}

// ipReputationCache stores merged reports in Redis keyed by exit address.
// A nil Redis client degrades to no caching rather than failing lookups.
type ipReputationCache struct {
	rdb *redis.Client
}

func newIPReputationCache(rdb *redis.Client) ipreputation.Cache {
	if rdb == nil {
		return nil
	}
	return &ipReputationCache{rdb: rdb}
}

func (c *ipReputationCache) Get(ctx context.Context, ip string) (*ipreputation.Report, bool) {
	payload, err := c.rdb.Get(ctx, ipReputationKey(ip)).Bytes()
	if err != nil {
		return nil, false
	}
	var report ipreputation.Report
	if err := json.Unmarshal(payload, &report); err != nil {
		return nil, false
	}
	return &report, true
}

func (c *ipReputationCache) Set(ctx context.Context, ip string, report *ipreputation.Report, ttl time.Duration) {
	if report == nil || ttl <= 0 {
		return
	}
	payload, err := json.Marshal(report)
	if err != nil {
		return
	}
	// Detach from the caller: the lookup that produced this report already
	// outlives the request context, so the write must not be cancelled with it.
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := c.rdb.Set(writeCtx, ipReputationKey(ip), payload, ttl).Err(); err != nil {
		logger.LegacyPrintf("repository.ip_reputation", "Warning: cache ip reputation for %s failed: %v", ip, err)
	}
}
