package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildRecordUsageLogPersistsCacheObservation(t *testing.T) {
	result := &ForwardResult{
		RequestID: "req-cache-observation",
		Model:     "claude-sonnet",
		Usage: ClaudeUsage{
			InputTokens:                10,
			OutputTokens:               5,
			CacheReadInputTokens:       130,
			CacheUsageSource:           CacheUsageSourceEstimated,
			ForcedCacheReadInputTokens: 30,
		},
	}

	log := (&GatewayService{}).buildRecordUsageLog(
		context.Background(),
		&recordUsageCoreInput{},
		result,
		&APIKey{ID: 2},
		&User{ID: 1},
		&Account{ID: 3},
		nil,
		result.Model,
		1,
		1,
		1,
		BillingTypeBalance,
		false,
		nil,
		&recordUsageOpts{},
	)

	require.NotNil(t, log.CacheUsageSource)
	require.Equal(t, CacheUsageSourceEstimated, *log.CacheUsageSource)
	require.Equal(t, 30, log.ForcedCacheReadTokens)
	require.Equal(t, 130, log.CacheReadTokens, "existing billed cache-read value must stay compatible")
	require.Zero(t, log.ProviderCacheReadTokens(), "estimated cache usage must not masquerade as provider-reported hits")
}

func TestBuildRecordUsageLogDefaultsMissingSourceToUnavailable(t *testing.T) {
	result := &ForwardResult{RequestID: "req-cache-legacy", Model: "legacy"}

	log := (&GatewayService{}).buildRecordUsageLog(
		context.Background(),
		&recordUsageCoreInput{},
		result,
		&APIKey{ID: 2},
		&User{ID: 1},
		&Account{ID: 3},
		nil,
		result.Model,
		1,
		1,
		1,
		BillingTypeBalance,
		false,
		nil,
		&recordUsageOpts{},
	)

	require.NotNil(t, log.CacheUsageSource)
	require.Equal(t, CacheUsageSourceUnavailable, *log.CacheUsageSource)
	require.Zero(t, log.ForcedCacheReadTokens)
}

func TestProviderCacheReadTokensNeverGoesNegative(t *testing.T) {
	source := CacheUsageSourceReported
	log := &UsageLog{CacheReadTokens: 10, ForcedCacheReadTokens: 20, CacheUsageSource: &source}
	require.Zero(t, log.ProviderCacheReadTokens())
}

func TestProviderCacheReadTokensRequiresReportedSource(t *testing.T) {
	reported := CacheUsageSourceReported
	estimated := CacheUsageSourceEstimated

	require.Equal(t, 100, (&UsageLog{
		CacheReadTokens:       130,
		ForcedCacheReadTokens: 30,
		CacheUsageSource:      &reported,
	}).ProviderCacheReadTokens())
	require.Zero(t, (&UsageLog{
		CacheReadTokens:       130,
		ForcedCacheReadTokens: 30,
		CacheUsageSource:      &estimated,
	}).ProviderCacheReadTokens())
	require.Zero(t, (&UsageLog{
		CacheReadTokens:       130,
		ForcedCacheReadTokens: 30,
	}).ProviderCacheReadTokens())
	require.Zero(t, (&UsageLog{
		CacheReadTokens:       0,
		ForcedCacheReadTokens: -1,
		CacheUsageSource:      &reported,
	}).ProviderCacheReadTokens(), "negative dirty forced tokens must not fabricate a provider hit")
}
