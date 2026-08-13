//go:build unit

package repository

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func newCacheObservationUsageLog(source *service.CacheUsageSource, forced int) *service.UsageLog {
	return &service.UsageLog{
		UserID:                1,
		APIKeyID:              2,
		AccountID:             3,
		RequestID:             "req-cache-observation",
		Model:                 "claude-sonnet",
		CacheReadTokens:       250,
		CacheUsageSource:      source,
		ForcedCacheReadTokens: forced,
		CreatedAt:             time.Now().UTC(),
	}
}

func TestPrepareUsageLogInsertCacheObservationArgWiring(t *testing.T) {
	source := service.CacheUsageSourceEstimated
	prepared := prepareUsageLogInsert(newCacheObservationUsageLog(&source, 75))

	require.Len(t, usageLogInsertArgTypes, 62)
	require.Len(t, prepared.args, len(usageLogInsertArgTypes))

	sourceArg, ok := prepared.args[len(prepared.args)-2].(sql.NullString)
	require.True(t, ok)
	require.True(t, sourceArg.Valid)
	require.Equal(t, string(source), sourceArg.String)
	require.Equal(t, "text", usageLogInsertArgTypes[len(usageLogInsertArgTypes)-2])

	require.Equal(t, 75, prepared.args[len(prepared.args)-1])
	require.Equal(t, "integer", usageLogInsertArgTypes[len(usageLogInsertArgTypes)-1])
}

func TestPrepareUsageLogInsertDefaultsUnknownCacheSourceToUnavailable(t *testing.T) {
	unknown := service.CacheUsageSource("unknown")
	log := newCacheObservationUsageLog(&unknown, 0)
	prepared := prepareUsageLogInsert(log)
	sourceArg, ok := prepared.args[len(prepared.args)-2].(sql.NullString)
	require.True(t, ok)
	require.True(t, sourceArg.Valid)
	require.Equal(t, string(service.CacheUsageSourceUnavailable), sourceArg.String)
	require.NotNil(t, log.CacheUsageSource)
	require.Equal(t, service.CacheUsageSourceUnavailable, *log.CacheUsageSource)
	require.Equal(t, 0, prepared.args[len(prepared.args)-1])
}

func TestPrepareUsageLogInsertClampsNegativeForcedCacheReadTokens(t *testing.T) {
	source := service.CacheUsageSourceReported
	log := newCacheObservationUsageLog(&source, -9)

	prepared := prepareUsageLogInsert(log)

	require.Zero(t, prepared.args[len(prepared.args)-1])
	require.Zero(t, log.ForcedCacheReadTokens)
}

func TestUsageLogInsertQueriesIncludeCacheObservation(t *testing.T) {
	source := service.CacheUsageSourceReported
	log := newCacheObservationUsageLog(&source, 25)
	prepared := prepareUsageLogInsert(log)
	key := usageLogBatchKey(log.RequestID, log.APIKeyID)

	require.Contains(t, usageLogSelectColumns, "cache_usage_source")
	require.Contains(t, usageLogSelectColumns, "forced_cache_read_tokens")

	batchQuery, batchArgs := buildUsageLogBatchInsertQuery(
		[]string{key},
		map[string]usageLogInsertPrepared{key: prepared},
	)
	require.GreaterOrEqual(t, strings.Count(batchQuery, "cache_usage_source"), 3)
	require.GreaterOrEqual(t, strings.Count(batchQuery, "forced_cache_read_tokens"), 3)
	require.Len(t, batchArgs, len(prepared.args)+1)

	bestEffortQuery, bestEffortArgs := buildUsageLogBestEffortInsertQuery([]usageLogInsertPrepared{prepared})
	require.GreaterOrEqual(t, strings.Count(bestEffortQuery, "cache_usage_source"), 3)
	require.GreaterOrEqual(t, strings.Count(bestEffortQuery, "forced_cache_read_tokens"), 3)
	require.Len(t, bestEffortArgs, len(prepared.args))
}
