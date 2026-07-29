package ipreputation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// ProxyCheckProvider queries proxycheck.io. It works without a key at 100
// lookups/day; a free key raises that to 1000. It is the only source here that
// returns a plain 0-100 risk score without a paid plan.
//
// Its verdicts skew aggressive — a bare EC2 address comes back as
// `type=VPN, risk=66` — which is why Merge keeps it beside the others instead
// of letting any single source decide.
type ProxyCheckProvider struct {
	httpProvider
	baseURL string
	apiKey  string
}

func NewProxyCheckProvider(client *http.Client, apiKey string) *ProxyCheckProvider {
	return &ProxyCheckProvider{
		httpProvider: httpProvider{name: SourceProxyCheck, client: client, maxBody: defaultMaxBodyBytes},
		baseURL:      "https://proxycheck.io/v2/",
		apiKey:       strings.TrimSpace(apiKey),
	}
}

func (p *ProxyCheckProvider) Name() string { return p.name }

type proxyCheckEntry struct {
	ASN          string `json:"asn"`
	Provider     string `json:"provider"`
	Organisation string `json:"organisation"`
	Proxy        string `json:"proxy"`
	Type         string `json:"type"`
	Risk         *int   `json:"risk"`
}

func (p *ProxyCheckProvider) Lookup(ctx context.Context, ip string) Verdict {
	endpoint := p.baseURL + url.PathEscape(ip) + "?vpn=1&asn=1&risk=1"
	if p.apiKey != "" {
		endpoint += "&key=" + url.QueryEscape(p.apiKey)
	}

	body, latency, err := p.fetch(ctx, endpoint)
	if err != nil {
		return failedVerdict(p.name, latency, err)
	}

	// The response is a flat object keyed by the queried address, so the entry
	// has to be pulled out before it can be decoded into a struct.
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return failedVerdict(p.name, latency, fmt.Errorf("parse response: %w", err))
	}
	if status, ok := envelope["status"]; ok {
		var value string
		if json.Unmarshal(status, &value) == nil && value != "ok" {
			message := value
			if raw, ok := envelope["message"]; ok {
				var text string
				if json.Unmarshal(raw, &text) == nil && text != "" {
					message = text
				}
			}
			return failedVerdict(p.name, latency, fmt.Errorf("proxycheck: %s", message))
		}
	}
	raw, ok := envelope[ip]
	if !ok {
		return failedVerdict(p.name, latency, fmt.Errorf("no result for %s", ip))
	}
	var entry proxyCheckEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return failedVerdict(p.name, latency, fmt.Errorf("parse entry: %w", err))
	}

	connType := strings.ToLower(strings.TrimSpace(entry.Type))
	verdict := Verdict{
		Source:    p.name,
		OK:        true,
		LatencyMs: latency,
		UsageType: connType,
		ASN:       strings.TrimSpace(entry.ASN),
		ASOrg:     strings.TrimSpace(entry.Organisation),
		ISP:       strings.TrimSpace(entry.Provider),
	}

	switch strings.ToLower(strings.TrimSpace(entry.Proxy)) {
	case "yes":
		verdict.Proxy = boolPtr(true)
	case "no":
		verdict.Proxy = boolPtr(false)
	}
	if entry.Risk != nil {
		verdict.RiskScore = intPtr(clampScore(*entry.Risk))
	}

	switch connType {
	case "vpn":
		verdict.VPN = boolPtr(true)
		verdict.Datacenter = boolPtr(true)
	case "tor":
		verdict.Tor = boolPtr(true)
	case "mobile":
		verdict.Mobile = boolPtr(true)
		verdict.Datacenter = boolPtr(false)
	case "residential", "business", "education", "government":
		verdict.Datacenter = boolPtr(false)
	case "hosting", "datacenter", "compromised server":
		verdict.Datacenter = boolPtr(true)
	}
	return verdict
}
