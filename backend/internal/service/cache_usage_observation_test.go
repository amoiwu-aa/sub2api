package service

import "testing"

func TestCacheUsageSourcePreservesExplicitZeroObservation(t *testing.T) {
	usage := ClaudeUsage{
		InputTokens:      1200,
		CacheUsageSource: CacheUsageSourceReported,
	}

	if usage.CacheUsageSource != CacheUsageSourceReported {
		t.Fatalf("CacheUsageSource = %q, want %q", usage.CacheUsageSource, CacheUsageSourceReported)
	}
}

func TestApplyForceCacheBillingTracksAdjustmentSeparately(t *testing.T) {
	usage := ClaudeUsage{
		InputTokens:                1000,
		CacheReadInputTokens:       500,
		CacheUsageSource:           CacheUsageSourceReported,
		ForcedCacheReadInputTokens: 0,
	}

	ApplyForceCacheBilling(&usage)

	if usage.InputTokens != 0 {
		t.Fatalf("InputTokens = %d, want 0", usage.InputTokens)
	}
	if usage.CacheReadInputTokens != 1500 {
		t.Fatalf("CacheReadInputTokens = %d, want 1500", usage.CacheReadInputTokens)
	}
	if usage.ForcedCacheReadInputTokens != 1000 {
		t.Fatalf("ForcedCacheReadInputTokens = %d, want 1000", usage.ForcedCacheReadInputTokens)
	}
	if usage.CacheUsageSource != CacheUsageSourceReported {
		t.Fatalf("CacheUsageSource changed to %q", usage.CacheUsageSource)
	}
}
