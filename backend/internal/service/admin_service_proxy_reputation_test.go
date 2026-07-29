package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ipreputation"
	"github.com/stretchr/testify/require"
)

type stubIPReputationChecker struct {
	enabled bool
	report  *ipreputation.Report
	err     error
	calls   int
}

func (s *stubIPReputationChecker) Enabled() bool { return s.enabled }

func (s *stubIPReputationChecker) Check(_ context.Context, _ string) (*ipreputation.Report, error) {
	s.calls++
	return s.report, s.err
}

func TestLookupIPReputation_UsesIPAPIFactsWhenCheckerDisabled(t *testing.T) {
	svc := &adminServiceImpl{ipReputationChecker: &stubIPReputationChecker{enabled: false}}

	report := svc.lookupIPReputation(context.Background(), &ProxyExitInfo{
		IP:      "54.91.239.213",
		Hosting: boolPtr(true),
		Proxy:   boolPtr(false),
		ISP:     "Amazon.com, Inc.",
		ASN:     "AS14618 Amazon.com, Inc.",
	})

	require.NotNil(t, report, "the free ip-api classification must survive on its own")
	require.Len(t, report.Sources, 1)
	require.Equal(t, ipreputation.SourceIPAPI, report.Sources[0].Source)
	require.Equal(t, ipreputation.IPTypeDatacenter, report.IPType)
}

func TestLookupIPReputation_NilWhenNothingKnown(t *testing.T) {
	svc := &adminServiceImpl{ipReputationChecker: &stubIPReputationChecker{enabled: false}}

	// The ipify fallback probe answers with an address and nothing else.
	require.Nil(t, svc.lookupIPReputation(context.Background(), &ProxyExitInfo{IP: "54.91.239.213"}))
	require.Nil(t, svc.lookupIPReputation(context.Background(), nil))
	require.Nil(t, svc.lookupIPReputation(context.Background(), &ProxyExitInfo{}))
}

func TestLookupIPReputation_MergesRemoteAndLocalVerdicts(t *testing.T) {
	remote := ipreputation.Merge("54.91.239.213", []ipreputation.Verdict{
		{Source: ipreputation.SourceProxyCheck, OK: true, Datacenter: boolPtr(false), RiskScore: intPtrForTest(66)},
	})
	checker := &stubIPReputationChecker{enabled: true, report: remote}
	svc := &adminServiceImpl{ipReputationChecker: checker}

	report := svc.lookupIPReputation(context.Background(), &ProxyExitInfo{
		IP:      "54.91.239.213",
		Hosting: boolPtr(true),
	})

	require.Equal(t, 1, checker.calls)
	require.Len(t, report.Sources, 2)
	require.Equal(t, []string{ipreputation.FlagDatacenter}, report.Conflicts,
		"disagreement between sources must stay visible instead of being averaged away")
	require.Equal(t, 66, *report.RiskScore)
}

func TestLookupIPReputation_DegradesWhenCheckerFails(t *testing.T) {
	checker := &stubIPReputationChecker{enabled: true, err: errors.New("rate limited")}
	svc := &adminServiceImpl{ipReputationChecker: checker}

	report := svc.lookupIPReputation(context.Background(), &ProxyExitInfo{
		IP:      "54.91.239.213",
		Hosting: boolPtr(true),
	})

	require.NotNil(t, report, "a failed lookup must not drop the free classification")
	require.Len(t, report.Sources, 1)
	require.Equal(t, ipreputation.IPTypeDatacenter, report.IPType)
}

func TestSaveProxyQualitySnapshotCopiesReputationSummary(t *testing.T) {
	cache := &recordingProxyLatencyCache{}
	svc := &adminServiceImpl{proxyLatencyCache: cache}

	result := &ProxyQualityCheckResult{
		Score:     100,
		Grade:     "A",
		CheckedAt: 1700000000,
		Reputation: ipreputation.Merge("54.91.239.213", []ipreputation.Verdict{
			{Source: ipreputation.SourceIPAPI, OK: true, Datacenter: boolPtr(true), RiskScore: intPtrForTest(35)},
		}),
	}

	svc.saveProxyQualitySnapshot(context.Background(), 7, result, &ProxyExitInfo{IP: "54.91.239.213"})

	require.NotNil(t, cache.saved)
	require.Equal(t, ipreputation.IPTypeDatacenter, cache.saved.IPType)
	require.Equal(t, ipreputation.RiskMedium, cache.saved.IPRiskLevel)
	require.Equal(t, 35, *cache.saved.IPRiskScore)
}

func TestSaveProxyLatencyDropsReputationWhenExitIPChanges(t *testing.T) {
	previous := 35
	cache := &recordingProxyLatencyCache{existing: map[int64]*ProxyLatencyInfo{
		7: {
			IPAddress:   "54.91.239.213",
			IPType:      ipreputation.IPTypeDatacenter,
			IPRiskLevel: ipreputation.RiskMedium,
			IPRiskScore: &previous,
		},
	}}
	svc := &adminServiceImpl{proxyLatencyCache: cache}

	svc.saveProxyLatency(context.Background(), 7, &ProxyLatencyInfo{Success: true, IPAddress: "203.0.113.9"})

	require.Empty(t, cache.saved.IPType, "a rotated exit must not inherit the previous address's verdict")
	require.Empty(t, cache.saved.IPRiskLevel)
	require.Nil(t, cache.saved.IPRiskScore)
}

func TestSaveProxyLatencyKeepsReputationForSameExitIP(t *testing.T) {
	previous := 35
	cache := &recordingProxyLatencyCache{existing: map[int64]*ProxyLatencyInfo{
		7: {
			IPAddress:   "54.91.239.213",
			IPType:      ipreputation.IPTypeDatacenter,
			IPRiskLevel: ipreputation.RiskMedium,
			IPRiskScore: &previous,
		},
	}}
	svc := &adminServiceImpl{proxyLatencyCache: cache}

	svc.saveProxyLatency(context.Background(), 7, &ProxyLatencyInfo{Success: true, IPAddress: "54.91.239.213"})

	require.Equal(t, ipreputation.IPTypeDatacenter, cache.saved.IPType)
	require.Equal(t, 35, *cache.saved.IPRiskScore)
}

type recordingProxyLatencyCache struct {
	existing map[int64]*ProxyLatencyInfo
	saved    *ProxyLatencyInfo
}

func (c *recordingProxyLatencyCache) GetProxyLatencies(_ context.Context, ids []int64) (map[int64]*ProxyLatencyInfo, error) {
	out := make(map[int64]*ProxyLatencyInfo, len(ids))
	for _, id := range ids {
		if info, ok := c.existing[id]; ok {
			out[id] = info
		}
	}
	return out, nil
}

func (c *recordingProxyLatencyCache) SetProxyLatency(_ context.Context, _ int64, info *ProxyLatencyInfo) error {
	c.saved = info
	return nil
}
