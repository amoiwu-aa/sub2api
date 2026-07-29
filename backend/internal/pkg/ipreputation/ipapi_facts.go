package ipreputation

import "strings"

// IPAPIFacts holds the classification fields ip-api.com returns alongside the
// geo data on the connectivity probe that already runs for every proxy.
type IPAPIFacts struct {
	Mobile  *bool
	Proxy   *bool
	Hosting *bool
	ISP     string
	Org     string
	ASN     string
	ASName  string
}

// Empty reports whether ip-api answered with none of the classification fields,
// which happens when the fallback probe (ipify) served the request instead.
func (f IPAPIFacts) Empty() bool {
	return f.Mobile == nil && f.Proxy == nil && f.Hosting == nil &&
		strings.TrimSpace(f.ISP) == "" && strings.TrimSpace(f.ASN) == ""
}

// VerdictFromIPAPI turns those fields into a verdict so the report still has a
// source when every remote provider is disabled or out of quota. It carries no
// risk score: ip-api classifies networks but does not track abuse.
func VerdictFromIPAPI(f IPAPIFacts) Verdict {
	verdict := Verdict{
		Source:     SourceIPAPI,
		OK:         true,
		Datacenter: f.Hosting,
		Proxy:      f.Proxy,
		Mobile:     f.Mobile,
		ISP:        strings.TrimSpace(f.ISP),
		ASN:        strings.TrimSpace(f.ASN),
		ASOrg:      strings.TrimSpace(f.ASName),
	}
	if verdict.ASOrg == "" {
		verdict.ASOrg = strings.TrimSpace(f.Org)
	}
	if isTrue(f.Hosting) {
		verdict.UsageType = "hosting"
		verdict.Hoster = strings.TrimSpace(f.Org)
	}
	return verdict
}
