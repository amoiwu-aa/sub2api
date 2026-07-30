//go:build unit

package repository

import (
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func newUpstreamCreditsUsageLog(credits float64) *service.UsageLog {
	return &service.UsageLog{
		UserID:          1,
		APIKeyID:        2,
		AccountID:       3,
		RequestID:       "req-upstream-credits",
		Model:           "kiro/auto",
		InputTokens:     42366,
		OutputTokens:    32,
		TotalCost:       0.127578,
		ActualCost:      0.127578,
		UpstreamCredits: credits,
		CreatedAt:       time.Now().UTC(),
	}
}

// TestPrepareUsageLogInsert_UpstreamCreditsArgWiring pins upstream_credits to the
// tail of the arg slice.
//
// Position matters beyond bookkeeping: several INSERT paths in this package spell
// out $1..$N by hand, so the column was deliberately appended after created_at to
// leave every existing placeholder untouched. A future column must be appended
// the same way rather than inserted mid-list.
func TestPrepareUsageLogInsert_UpstreamCreditsArgWiring(t *testing.T) {
	prepared := prepareUsageLogInsert(newUpstreamCreditsUsageLog(0.148231))

	require.Len(t, prepared.args, len(usageLogInsertArgTypes),
		"prepared args must match the arg-type table length")

	last := len(prepared.args) - 1
	credits, ok := prepared.args[last].(float64)
	require.True(t, ok, "upstream_credits arg should be a float64, got %T", prepared.args[last])
	require.InDelta(t, 0.148231, credits, 1e-9)

	require.Equal(t, "numeric", usageLogInsertArgTypes[last],
		"upstream_credits arg type must be numeric")
}

// TestPrepareUsageLogInsert_UpstreamCreditsDefaultsToZero covers every non-Kiro
// platform: nothing sets the field, and it must land as a plain 0 (the column is
// NOT NULL and callers sum it without a nil guard).
func TestPrepareUsageLogInsert_UpstreamCreditsDefaultsToZero(t *testing.T) {
	prepared := prepareUsageLogInsert(newUpstreamCreditsUsageLog(0))
	require.Equal(t, 0.0, prepared.args[len(prepared.args)-1])
}

// TestUsageLogInsertQueries_IncludeUpstreamCredits guards that every generated
// INSERT path and the SELECT column list reference the column. Missing it in any
// one path would silently drop the value for that path only -- the failure mode
// is a column that is populated for some requests and zero for others.
func TestUsageLogInsertQueries_IncludeUpstreamCredits(t *testing.T) {
	require.Contains(t, usageLogSelectColumns, "upstream_credits",
		"SELECT column list must include upstream_credits")

	log := newUpstreamCreditsUsageLog(0.082131)
	prepared := prepareUsageLogInsert(log)
	key := usageLogBatchKey(log.RequestID, log.APIKeyID)

	batchQuery, batchArgs := buildUsageLogBatchInsertQuery([]string{key},
		map[string]usageLogInsertPrepared{key: prepared})
	// CTE definition + INSERT column list + SELECT ... FROM input.
	require.GreaterOrEqual(t, strings.Count(batchQuery, "upstream_credits"), 3)
	require.Len(t, batchArgs, len(prepared.args)+1,
		"batch args include the synthetic input_index before usage-log values")

	bestEffortQuery, bestEffortArgs := buildUsageLogBestEffortInsertQuery([]usageLogInsertPrepared{prepared})
	require.GreaterOrEqual(t, strings.Count(bestEffortQuery, "upstream_credits"), 3)
	require.Len(t, bestEffortArgs, len(prepared.args))
}
