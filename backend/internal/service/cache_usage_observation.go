package service

// CacheUsageSource describes whether cache-token fields came from the upstream
// response. It is deliberately independent from billing adjustments.
type CacheUsageSource string

const (
	CacheUsageSourceReported    CacheUsageSource = "reported"
	CacheUsageSourceEstimated   CacheUsageSource = "estimated"
	CacheUsageSourceUnavailable CacheUsageSource = "unavailable"
)

func (source CacheUsageSource) IsValid() bool {
	switch source {
	case CacheUsageSourceReported, CacheUsageSourceEstimated, CacheUsageSourceUnavailable:
		return true
	default:
		return false
	}
}

func optionalCacheUsageSource(source CacheUsageSource) *CacheUsageSource {
	if !source.IsValid() {
		return nil
	}
	value := source
	return &value
}

// ApplyForceCacheBilling moves ordinary input into the cache-read billing bucket
// while retaining the amount of the accounting-only adjustment.
func ApplyForceCacheBilling(usage *ClaudeUsage) {
	if usage == nil || usage.InputTokens <= 0 {
		return
	}
	adjusted := usage.InputTokens
	usage.CacheReadInputTokens += adjusted
	usage.ForcedCacheReadInputTokens += adjusted
	usage.InputTokens = 0
}
