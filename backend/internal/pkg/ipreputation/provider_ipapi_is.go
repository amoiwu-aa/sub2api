package ipreputation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// IPAPIIsProvider queries api.ipapi.is, which answers without an API key at
// 1000 lookups/day and is the only free source here that reports Tor exits and
// abuse history alongside the datacenter flag.
type IPAPIIsProvider struct {
	httpProvider
	baseURL string
	apiKey  string
}

func NewIPAPIIsProvider(client *http.Client, apiKey string) *IPAPIIsProvider {
	return &IPAPIIsProvider{
		httpProvider: httpProvider{name: SourceIPAPIIs, client: client, maxBody: defaultMaxBodyBytes},
		baseURL:      "https://api.ipapi.is/",
		apiKey:       strings.TrimSpace(apiKey),
	}
}

func (p *IPAPIIsProvider) Name() string { return p.name }

type ipapiIsResponse struct {
	IP           string `json:"ip"`
	IsBogon      bool   `json:"is_bogon"`
	IsMobile     bool   `json:"is_mobile"`
	IsCrawler    bool   `json:"is_crawler"`
	IsDatacenter bool   `json:"is_datacenter"`
	IsTor        bool   `json:"is_tor"`
	IsProxy      bool   `json:"is_proxy"`
	IsVPN        bool   `json:"is_vpn"`
	IsAbuser     bool   `json:"is_abuser"`
	Datacenter   struct {
		Datacenter string `json:"datacenter"`
	} `json:"datacenter"`
	Company struct {
		Name        string `json:"name"`
		AbuserScore string `json:"abuser_score"`
		Type        string `json:"type"`
	} `json:"company"`
	ASN struct {
		ASN         int    `json:"asn"`
		AbuserScore string `json:"abuser_score"`
		Org         string `json:"org"`
		Type        string `json:"type"`
	} `json:"asn"`
}

func (p *IPAPIIsProvider) Lookup(ctx context.Context, ip string) Verdict {
	endpoint := p.baseURL + "?q=" + url.QueryEscape(ip)
	if p.apiKey != "" {
		endpoint += "&key=" + url.QueryEscape(p.apiKey)
	}

	body, latency, err := p.fetch(ctx, endpoint)
	if err != nil {
		return failedVerdict(p.name, latency, err)
	}

	var parsed ipapiIsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return failedVerdict(p.name, latency, fmt.Errorf("parse response: %w", err))
	}
	if parsed.IP == "" {
		return failedVerdict(p.name, latency, fmt.Errorf("no result for %s", ip))
	}

	verdict := Verdict{
		Source:     p.name,
		OK:         true,
		LatencyMs:  latency,
		Datacenter: boolPtr(parsed.IsDatacenter),
		Proxy:      boolPtr(parsed.IsProxy),
		VPN:        boolPtr(parsed.IsVPN),
		Tor:        boolPtr(parsed.IsTor),
		Mobile:     boolPtr(parsed.IsMobile),
		Abuser:     boolPtr(parsed.IsAbuser),
		UsageType:  strings.ToLower(strings.TrimSpace(parsed.Company.Type)),
		ASOrg:      strings.TrimSpace(parsed.ASN.Org),
		Hoster:     strings.TrimSpace(parsed.Datacenter.Datacenter),
	}
	if parsed.ASN.ASN > 0 {
		verdict.ASN = "AS" + strconv.Itoa(parsed.ASN.ASN)
	}
	if verdict.UsageType == "" {
		verdict.UsageType = strings.ToLower(strings.TrimSpace(parsed.ASN.Type))
	}

	// The address block's own abuse history is a tighter signal than the whole
	// ASN's, so prefer it and only fall back to the ASN-level figure.
	label := firstNonEmpty(parsed.Company.AbuserScore, parsed.ASN.AbuserScore)
	if label != "" {
		verdict.RiskLabel = label
		if score, ok := parseAbuserScore(label); ok {
			verdict.RiskScore = intPtr(score)
		}
	}
	return verdict
}

// abuserScoreBands maps ipapi.is's qualitative label onto our 0-100 scale. The
// numeric part of the field is a raw abuse ratio (0.0124 still means
// "Elevated"), so the label is the only part that carries usable severity.
var abuserScoreBands = map[string]int{
	"very low":  0,
	"low":       10,
	"elevated":  35,
	"high":      70,
	"very high": 90,
}

var abuserScoreLabelPattern = regexp.MustCompile(`\(([^)]+)\)`)

// parseAbuserScore reads labels shaped like "0.0124 (Elevated)".
func parseAbuserScore(raw string) (int, bool) {
	match := abuserScoreLabelPattern.FindStringSubmatch(raw)
	if len(match) != 2 {
		return 0, false
	}
	band, ok := abuserScoreBands[strings.ToLower(strings.TrimSpace(match[1]))]
	if !ok {
		return 0, false
	}
	return band, true
}
