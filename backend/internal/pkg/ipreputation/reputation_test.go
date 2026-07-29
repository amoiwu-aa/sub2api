package ipreputation

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMergeConservativeFlags(t *testing.T) {
	report := Merge("1.2.3.4", []Verdict{
		{Source: "a", OK: true, Datacenter: boolPtr(false), Proxy: boolPtr(false)},
		{Source: "b", OK: true, Datacenter: boolPtr(true), Proxy: boolPtr(false)},
	})

	require.NotNil(t, report.Datacenter)
	require.True(t, *report.Datacenter, "one source asserting the flag must be enough")
	require.NotNil(t, report.Proxy)
	require.False(t, *report.Proxy)
	require.Equal(t, []string{FlagDatacenter}, report.Conflicts)
	require.Equal(t, IPTypeDatacenter, report.IPType)
}

func TestMergeIgnoresFailedVerdicts(t *testing.T) {
	report := Merge("1.2.3.4", []Verdict{
		{Source: "a", OK: false, Error: "timeout", Datacenter: boolPtr(true)},
		{Source: "b", OK: true, Datacenter: boolPtr(false)},
	})

	require.NotNil(t, report.Datacenter)
	require.False(t, *report.Datacenter)
	require.Empty(t, report.Conflicts)
	require.Equal(t, IPTypeResidential, report.IPType)
}

func TestMergeKeepsUnknownDistinctFromNo(t *testing.T) {
	report := Merge("1.2.3.4", []Verdict{
		{Source: "a", OK: true, ASN: "AS14618"},
	})

	require.Nil(t, report.Datacenter, "a source that does not report the flag must not imply false")
	require.Equal(t, IPTypeUnknown, report.IPType)
	require.Equal(t, RiskUnknown, report.RiskLevel)
}

func TestMergeTakesHighestRiskScore(t *testing.T) {
	report := Merge("1.2.3.4", []Verdict{
		{Source: "a", OK: true, RiskScore: intPtr(12)},
		{Source: "b", OK: true, RiskScore: intPtr(66)},
	})

	require.NotNil(t, report.RiskScore)
	require.Equal(t, 66, *report.RiskScore)
	require.Equal(t, RiskMedium, report.RiskLevel)
}

func TestRiskClassification(t *testing.T) {
	tests := []struct {
		name    string
		verdict Verdict
		want    string
	}{
		{"tor outranks a clean score", Verdict{OK: true, Tor: boolPtr(true), RiskScore: intPtr(0)}, RiskHigh},
		{"abuse history outranks a clean score", Verdict{OK: true, Abuser: boolPtr(true), RiskScore: intPtr(0)}, RiskHigh},
		{"known vpn floors at medium", Verdict{OK: true, VPN: boolPtr(true), RiskScore: intPtr(0)}, RiskMedium},
		{"high score", Verdict{OK: true, RiskScore: intPtr(80)}, RiskHigh},
		{"low score", Verdict{OK: true, RiskScore: intPtr(15)}, RiskLow},
		{"clean score", Verdict{OK: true, RiskScore: intPtr(0)}, RiskClean},
		{"datacenter alone stays clean", Verdict{OK: true, Datacenter: boolPtr(true)}, RiskClean},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, Merge("1.2.3.4", []Verdict{tc.verdict}).RiskLevel)
		})
	}
}

func TestMobileWinsOverDatacenterForIPType(t *testing.T) {
	report := Merge("1.2.3.4", []Verdict{
		{Source: "a", OK: true, Mobile: boolPtr(true), Datacenter: boolPtr(true)},
	})
	require.Equal(t, IPTypeMobile, report.IPType)
}

func TestCombineFoldsExtraVerdictsAndKeepsCachedFlag(t *testing.T) {
	cached := Merge("1.2.3.4", []Verdict{{Source: "remote", OK: true, Datacenter: boolPtr(false)}})
	cached.Cached = true

	combined := Combine(cached, Verdict{Source: SourceIPAPI, OK: true, Datacenter: boolPtr(true)})

	require.True(t, combined.Cached)
	require.Len(t, combined.Sources, 2)
	require.Equal(t, []string{FlagDatacenter}, combined.Conflicts)
}

func TestCombineWithNilReport(t *testing.T) {
	require.Nil(t, Combine(nil))

	combined := Combine(nil, Verdict{Source: SourceIPAPI, OK: true, Datacenter: boolPtr(true)})
	require.NotNil(t, combined)
	require.Len(t, combined.Sources, 1)
}

func TestVerdictFromIPAPI(t *testing.T) {
	verdict := VerdictFromIPAPI(IPAPIFacts{
		Hosting: boolPtr(true),
		Proxy:   boolPtr(false),
		Mobile:  boolPtr(false),
		ISP:     "Amazon.com, Inc.",
		Org:     "AWS EC2 (us-east-1)",
		ASN:     "AS14618 Amazon.com, Inc.",
		ASName:  "AMAZON-AES",
	})

	require.Equal(t, SourceIPAPI, verdict.Source)
	require.True(t, verdict.OK)
	require.True(t, *verdict.Datacenter)
	require.Equal(t, "hosting", verdict.UsageType)
	require.Equal(t, "AWS EC2 (us-east-1)", verdict.Hoster)
	require.Equal(t, "AMAZON-AES", verdict.ASOrg)
	require.Nil(t, verdict.RiskScore, "ip-api classifies networks but tracks no abuse history")
}

func TestIPAPIFactsEmptyWhenFallbackProbeAnswered(t *testing.T) {
	require.True(t, IPAPIFacts{}.Empty())
	require.False(t, IPAPIFacts{Hosting: boolPtr(false)}.Empty())
	require.False(t, IPAPIFacts{ISP: "NTT America, Inc."}.Empty())
}
