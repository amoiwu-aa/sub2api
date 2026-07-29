package ipreputation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// IPQualityScoreProvider queries ipqualityscore.com, the source of the 0-100
// "fraud score" most IP-quality tools quote. It always requires an API key;
// the free tier allows 5000 lookups/month.
type IPQualityScoreProvider struct {
	httpProvider
	baseURL string
	apiKey  string
	// strictness maps to IPQS's own 0-3 knob: higher catches more proxies at
	// the cost of more false positives.
	strictness int
}

func NewIPQualityScoreProvider(client *http.Client, apiKey string, strictness int) *IPQualityScoreProvider {
	if strictness < 0 || strictness > 3 {
		strictness = 1
	}
	return &IPQualityScoreProvider{
		httpProvider: httpProvider{name: SourceIPQualityScore, client: client, maxBody: defaultMaxBodyBytes},
		baseURL:      "https://www.ipqualityscore.com/api/json/ip/",
		apiKey:       strings.TrimSpace(apiKey),
		strictness:   strictness,
	}
}

func (p *IPQualityScoreProvider) Name() string { return p.name }

type ipQualityScoreResponse struct {
	Success        bool   `json:"success"`
	Message        string `json:"message"`
	FraudScore     *int   `json:"fraud_score"`
	Proxy          *bool  `json:"proxy"`
	VPN            *bool  `json:"vpn"`
	Tor            *bool  `json:"tor"`
	RecentAbuse    *bool  `json:"recent_abuse"`
	BotStatus      *bool  `json:"bot_status"`
	Mobile         *bool  `json:"mobile"`
	ConnectionType string `json:"connection_type"`
	ISP            string `json:"ISP"`
	Organization   string `json:"organization"`
	ASN            *int   `json:"ASN"`
}

func (p *IPQualityScoreProvider) Lookup(ctx context.Context, ip string) Verdict {
	if p.apiKey == "" {
		return failedVerdict(p.name, 0, fmt.Errorf("api key not configured"))
	}

	endpoint := p.baseURL + url.PathEscape(p.apiKey) + "/" + url.PathEscape(ip) +
		"?strictness=" + strconv.Itoa(p.strictness)

	body, latency, err := p.fetch(ctx, endpoint)
	if err != nil {
		return failedVerdict(p.name, latency, err)
	}

	var parsed ipQualityScoreResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return failedVerdict(p.name, latency, fmt.Errorf("parse response: %w", err))
	}
	if !parsed.Success {
		message := parsed.Message
		if message == "" {
			message = "lookup rejected"
		}
		return failedVerdict(p.name, latency, fmt.Errorf("ipqualityscore: %s", message))
	}

	verdict := Verdict{
		Source:    p.name,
		OK:        true,
		LatencyMs: latency,
		Proxy:     parsed.Proxy,
		VPN:       parsed.VPN,
		Tor:       parsed.Tor,
		Mobile:    parsed.Mobile,
		Abuser:    parsed.RecentAbuse,
		UsageType: strings.ToLower(strings.TrimSpace(parsed.ConnectionType)),
		ISP:       strings.TrimSpace(parsed.ISP),
		ASOrg:     strings.TrimSpace(parsed.Organization),
	}
	if parsed.ASN != nil && *parsed.ASN > 0 {
		verdict.ASN = "AS" + strconv.Itoa(*parsed.ASN)
	}
	if parsed.FraudScore != nil {
		verdict.RiskScore = intPtr(clampScore(*parsed.FraudScore))
	}

	// connection_type reads "Premium required" on plans that do not include the
	// field, so only the known values may be mapped.
	switch verdict.UsageType {
	case "data center", "datacenter":
		verdict.Datacenter = boolPtr(true)
	case "residential", "corporate", "education":
		verdict.Datacenter = boolPtr(false)
	case "mobile":
		verdict.Datacenter = boolPtr(false)
		verdict.Mobile = boolPtr(true)
	}
	return verdict
}
