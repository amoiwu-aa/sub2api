// Package ipreputation queries third-party IP reputation databases to tell
// whether a proxy exit address looks like a datacenter range, a known VPN/proxy
// endpoint, or an address with abuse history.
//
// Verdicts are kept per source rather than collapsed into a single number.
// Providers disagree often enough that a merged score would be misleading: a
// plain AWS EC2 address is reported as `type=VPN, risk=66` by proxycheck.io
// while ipapi.is reports `is_vpn=false, is_proxy=false` for the same address.
package ipreputation

import (
	"context"
	"sort"
	"strings"
	"time"
)

// Source identifiers, also used as the config keys that enable each provider.
const (
	SourceIPAPI          = "ip-api"
	SourceIPAPIIs        = "ipapi.is"
	SourceProxyCheck     = "proxycheck.io"
	SourceIPQualityScore = "ipqualityscore"
)

// IP classification, derived from the merged flags.
const (
	IPTypeUnknown     = "unknown"
	IPTypeDatacenter  = "datacenter"
	IPTypeResidential = "residential"
	IPTypeMobile      = "mobile"
)

// Risk bands, derived from the merged score and flags.
const (
	RiskUnknown = "unknown"
	RiskClean   = "clean"
	RiskLow     = "low"
	RiskMedium  = "medium"
	RiskHigh    = "high"
)

// Risk score thresholds for the bands above. Datacenter ranges deliberately do
// not raise the band on their own: most AI upstreams tolerate them, and almost
// every proxy an operator buys is one, so treating them as risky would flag
// everything.
const (
	riskScoreHigh   = 70
	riskScoreMedium = 35
	riskScoreLow    = 10
)

// Verdict is one provider's answer about one address.
//
// Every flag is a pointer because "the provider does not report this" and "the
// provider says no" must stay distinguishable — merging them would silently
// turn missing data into a clean bill of health.
type Verdict struct {
	Source    string `json:"source"`
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
	LatencyMs int64  `json:"latency_ms,omitempty"`

	Datacenter *bool `json:"datacenter,omitempty"`
	Proxy      *bool `json:"proxy,omitempty"`
	VPN        *bool `json:"vpn,omitempty"`
	Tor        *bool `json:"tor,omitempty"`
	Mobile     *bool `json:"mobile,omitempty"`
	Abuser     *bool `json:"abuser,omitempty"`

	// RiskScore is normalized to 0-100, higher means more suspicious.
	RiskScore *int `json:"risk_score,omitempty"`
	// RiskLabel keeps the provider's own wording (e.g. ipapi.is "0.0124
	// (Elevated)") so the raw evidence stays visible next to our normalization.
	RiskLabel string `json:"risk_label,omitempty"`

	UsageType string `json:"usage_type,omitempty"` // hosting / isp / business / education / ...
	ASN       string `json:"asn,omitempty"`
	ASOrg     string `json:"as_org,omitempty"`
	ISP       string `json:"isp,omitempty"`
	Hoster    string `json:"hoster,omitempty"` // datacenter / hosting company name
}

// Flag names used in Report.Conflicts.
const (
	FlagDatacenter = "datacenter"
	FlagProxy      = "proxy"
	FlagVPN        = "vpn"
	FlagTor        = "tor"
	FlagMobile     = "mobile"
	FlagAbuser     = "abuser"
)

// Report is the merged view across all providers that answered.
type Report struct {
	IP        string    `json:"ip"`
	CheckedAt int64     `json:"checked_at"`
	Cached    bool      `json:"cached"`
	Sources   []Verdict `json:"sources"`

	Datacenter *bool `json:"datacenter,omitempty"`
	Proxy      *bool `json:"proxy,omitempty"`
	VPN        *bool `json:"vpn,omitempty"`
	Tor        *bool `json:"tor,omitempty"`
	Mobile     *bool `json:"mobile,omitempty"`
	Abuser     *bool `json:"abuser,omitempty"`

	RiskScore *int   `json:"risk_score,omitempty"`
	RiskLevel string `json:"risk_level"`
	IPType    string `json:"ip_type"`

	ASN    string `json:"asn,omitempty"`
	ASOrg  string `json:"as_org,omitempty"`
	ISP    string `json:"isp,omitempty"`
	Hoster string `json:"hoster,omitempty"`

	// Conflicts lists the flags the providers disagreed on. Surfacing this is
	// the point of keeping per-source verdicts: a contested "datacenter" says
	// something quite different from a unanimous one.
	Conflicts []string `json:"conflicts,omitempty"`
}

// Provider queries a single reputation database.
type Provider interface {
	Name() string
	Lookup(ctx context.Context, ip string) Verdict
}

// Merge folds per-source verdicts into a Report.
//
// Boolean flags merge conservatively: one provider asserting a flag is enough
// to set it, because a false negative (missing a flagged address) costs more
// than a false positive here. Scores take the maximum for the same reason.
func Merge(ip string, verdicts []Verdict) *Report {
	report := &Report{
		IP:        ip,
		CheckedAt: time.Now().Unix(),
		Sources:   verdicts,
		RiskLevel: RiskUnknown,
		IPType:    IPTypeUnknown,
	}

	type flagAccumulator struct {
		name     string
		anyTrue  bool
		anyFalse bool
		target   **bool
	}
	flags := []flagAccumulator{
		{name: FlagDatacenter, target: &report.Datacenter},
		{name: FlagProxy, target: &report.Proxy},
		{name: FlagVPN, target: &report.VPN},
		{name: FlagTor, target: &report.Tor},
		{name: FlagMobile, target: &report.Mobile},
		{name: FlagAbuser, target: &report.Abuser},
	}

	for _, v := range verdicts {
		if !v.OK {
			continue
		}
		values := map[string]*bool{
			FlagDatacenter: v.Datacenter,
			FlagProxy:      v.Proxy,
			FlagVPN:        v.VPN,
			FlagTor:        v.Tor,
			FlagMobile:     v.Mobile,
			FlagAbuser:     v.Abuser,
		}
		for i := range flags {
			value := values[flags[i].name]
			if value == nil {
				continue
			}
			if *value {
				flags[i].anyTrue = true
			} else {
				flags[i].anyFalse = true
			}
		}

		if v.RiskScore != nil && (report.RiskScore == nil || *v.RiskScore > *report.RiskScore) {
			score := *v.RiskScore
			report.RiskScore = &score
		}
		report.ASN = firstNonEmpty(report.ASN, v.ASN)
		report.ASOrg = firstNonEmpty(report.ASOrg, v.ASOrg)
		report.ISP = firstNonEmpty(report.ISP, v.ISP)
		report.Hoster = firstNonEmpty(report.Hoster, v.Hoster)
	}

	for i := range flags {
		switch {
		case flags[i].anyTrue:
			*flags[i].target = boolPtr(true)
		case flags[i].anyFalse:
			*flags[i].target = boolPtr(false)
		}
		if flags[i].anyTrue && flags[i].anyFalse {
			report.Conflicts = append(report.Conflicts, flags[i].name)
		}
	}
	sort.Strings(report.Conflicts)

	report.IPType = classifyIPType(report)
	report.RiskLevel = classifyRisk(report)
	return report
}

// Combine re-merges a report with extra verdicts, preserving the cached flag of
// the original. Used to fold in the locally-derived ip-api verdict without
// invalidating the cached remote lookups it is merged with.
func Combine(report *Report, extra ...Verdict) *Report {
	if report == nil {
		if len(extra) == 0 {
			return nil
		}
		return Merge("", extra)
	}
	if len(extra) == 0 {
		return report
	}
	merged := Merge(report.IP, append(append([]Verdict{}, extra...), report.Sources...))
	merged.Cached = report.Cached
	return merged
}

func classifyIPType(r *Report) string {
	switch {
	case isTrue(r.Mobile):
		return IPTypeMobile
	case isTrue(r.Datacenter):
		return IPTypeDatacenter
	case r.Datacenter != nil:
		// Every provider that answered says it is not a datacenter range.
		return IPTypeResidential
	default:
		return IPTypeUnknown
	}
}

func classifyRisk(r *Report) string {
	// Explicit blocklist hits outrank any numeric score.
	if isTrue(r.Tor) || isTrue(r.Abuser) {
		return RiskHigh
	}

	level := RiskUnknown
	if r.RiskScore != nil {
		switch score := *r.RiskScore; {
		case score >= riskScoreHigh:
			level = RiskHigh
		case score >= riskScoreMedium:
			level = RiskMedium
		case score >= riskScoreLow:
			level = RiskLow
		default:
			level = RiskClean
		}
	}

	// A recognized proxy/VPN endpoint is at least medium regardless of score:
	// these are the ranges upstream providers block first.
	if isTrue(r.Proxy) || isTrue(r.VPN) {
		return maxRisk(level, RiskMedium)
	}
	if level == RiskUnknown && hasAnyFlag(r) {
		return RiskClean
	}
	return level
}

// hasAnyFlag reports whether at least one provider answered any flag, which
// separates "everything came back clean" from "nobody told us anything".
func hasAnyFlag(r *Report) bool {
	return r.Datacenter != nil || r.Proxy != nil || r.VPN != nil ||
		r.Tor != nil || r.Mobile != nil || r.Abuser != nil
}

var riskOrder = map[string]int{
	RiskUnknown: 0,
	RiskClean:   1,
	RiskLow:     2,
	RiskMedium:  3,
	RiskHigh:    4,
}

func maxRisk(a, b string) string {
	if riskOrder[a] >= riskOrder[b] {
		return a
	}
	return b
}

func isTrue(v *bool) bool { return v != nil && *v }

func boolPtr(v bool) *bool { return &v }

func intPtr(v int) *int { return &v }

func firstNonEmpty(current, candidate string) string {
	if strings.TrimSpace(current) != "" {
		return current
	}
	return strings.TrimSpace(candidate)
}

func clampScore(v int) int {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}
