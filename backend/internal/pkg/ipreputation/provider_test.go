package ipreputation

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Fixtures below are trimmed captures of live responses for 54.91.239.213
// (a plain AWS EC2 address) so the parsers stay pinned to the real shapes.

const ipapiIsAWSFixture = `{
  "ip": "54.91.239.213",
  "is_bogon": false,
  "is_mobile": false,
  "is_datacenter": true,
  "is_tor": false,
  "is_proxy": false,
  "is_vpn": false,
  "is_abuser": false,
  "datacenter": {"datacenter": "Amazon Data Services Northern Virginia"},
  "company": {"name": "Amazon", "abuser_score": "0.0124 (Elevated)", "type": "hosting"},
  "asn": {"asn": 14618, "abuser_score": "0.0049 (Low)", "org": "Amazon.com, Inc.", "type": "hosting"}
}`

func newTestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

func TestIPAPIIsProviderParsesLiveShape(t *testing.T) {
	var gotQuery string
	server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		_, _ = w.Write([]byte(ipapiIsAWSFixture))
	})

	provider := NewIPAPIIsProvider(server.Client(), "")
	provider.baseURL = server.URL + "/"

	verdict := provider.Lookup(context.Background(), "54.91.239.213")

	require.True(t, verdict.OK, verdict.Error)
	require.Equal(t, "54.91.239.213", gotQuery)
	require.Equal(t, SourceIPAPIIs, verdict.Source)
	require.True(t, *verdict.Datacenter)
	require.False(t, *verdict.VPN)
	require.False(t, *verdict.Abuser)
	require.Equal(t, "Amazon Data Services Northern Virginia", verdict.Hoster)
	require.Equal(t, "AS14618", verdict.ASN)
	require.Equal(t, "hosting", verdict.UsageType)
	// The block-level score is preferred over the ASN-level one.
	require.Equal(t, "0.0124 (Elevated)", verdict.RiskLabel)
	require.Equal(t, 35, *verdict.RiskScore)
}

func TestIPAPIIsProviderSendsAPIKeyWhenConfigured(t *testing.T) {
	var gotKey string
	server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.URL.Query().Get("key")
		_, _ = w.Write([]byte(ipapiIsAWSFixture))
	})

	provider := NewIPAPIIsProvider(server.Client(), "secret-key")
	provider.baseURL = server.URL + "/"

	require.True(t, provider.Lookup(context.Background(), "54.91.239.213").OK)
	require.Equal(t, "secret-key", gotKey)
}

func TestParseAbuserScore(t *testing.T) {
	tests := map[string]struct {
		want int
		ok   bool
	}{
		"0 (Very Low)":      {0, true},
		"0.0012 (Low)":      {10, true},
		"0.0124 (Elevated)": {35, true},
		"0.4 (High)":        {70, true},
		"0.9 (Very High)":   {90, true},
		"0.03":              {0, false},
		"0.03 (Unheard Of)": {0, false},
	}
	for raw, want := range tests {
		t.Run(raw, func(t *testing.T) {
			got, ok := parseAbuserScore(raw)
			require.Equal(t, want.ok, ok)
			if want.ok {
				require.Equal(t, want.want, got)
			}
		})
	}
}

func TestProxyCheckProviderParsesLiveShape(t *testing.T) {
	server := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok","54.91.239.213":{"asn":"AS14618","provider":"Amazon.com, Inc.","organisation":"Amazon Technologies Inc.","proxy":"yes","type":"VPN","risk":66}}`))
	})

	provider := NewProxyCheckProvider(server.Client(), "")
	provider.baseURL = server.URL + "/"

	verdict := provider.Lookup(context.Background(), "54.91.239.213")

	require.True(t, verdict.OK, verdict.Error)
	require.True(t, *verdict.Proxy)
	require.True(t, *verdict.VPN)
	require.True(t, *verdict.Datacenter)
	require.Equal(t, 66, *verdict.RiskScore)
	require.Equal(t, "AS14618", verdict.ASN)
}

func TestProxyCheckProviderMapsBusinessAsNonDatacenter(t *testing.T) {
	server := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok","145.79.90.121":{"asn":"AS2914","provider":"NTT America, Inc.","proxy":"no","type":"Business","risk":0}}`))
	})

	provider := NewProxyCheckProvider(server.Client(), "")
	provider.baseURL = server.URL + "/"

	verdict := provider.Lookup(context.Background(), "145.79.90.121")

	require.True(t, verdict.OK, verdict.Error)
	require.False(t, *verdict.Proxy)
	require.False(t, *verdict.Datacenter)
	require.Equal(t, 0, *verdict.RiskScore)
}

func TestProxyCheckProviderSurfacesDeniedStatus(t *testing.T) {
	server := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"denied","message":"Daily query limit reached"}`))
	})

	provider := NewProxyCheckProvider(server.Client(), "")
	provider.baseURL = server.URL + "/"

	verdict := provider.Lookup(context.Background(), "54.91.239.213")

	require.False(t, verdict.OK)
	require.Contains(t, verdict.Error, "Daily query limit reached")
}

func TestIPQualityScoreProviderParsesResponse(t *testing.T) {
	server := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"fraud_score":75,"proxy":true,"vpn":true,"tor":false,"recent_abuse":false,"connection_type":"Data Center","ISP":"Amazon","organization":"AWS","ASN":14618}`))
	})

	provider := NewIPQualityScoreProvider(server.Client(), "key", 1)
	provider.baseURL = server.URL + "/"

	verdict := provider.Lookup(context.Background(), "54.91.239.213")

	require.True(t, verdict.OK, verdict.Error)
	require.Equal(t, 75, *verdict.RiskScore)
	require.True(t, *verdict.Datacenter)
	require.Equal(t, "AS14618", verdict.ASN)
}

func TestIPQualityScoreProviderIgnoresPremiumPlaceholderConnectionType(t *testing.T) {
	server := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"fraud_score":0,"proxy":false,"connection_type":"Premium required"}`))
	})

	provider := NewIPQualityScoreProvider(server.Client(), "key", 1)
	provider.baseURL = server.URL + "/"

	verdict := provider.Lookup(context.Background(), "54.91.239.213")

	require.True(t, verdict.OK, verdict.Error)
	require.Nil(t, verdict.Datacenter, "placeholder connection types must not be classified")
}

func TestIPQualityScoreProviderRequiresAPIKey(t *testing.T) {
	verdict := NewIPQualityScoreProvider(http.DefaultClient, "", 1).Lookup(context.Background(), "1.1.1.1")
	require.False(t, verdict.OK)
	require.Contains(t, verdict.Error, "api key")
}

func TestProviderRejectsOversizedBody(t *testing.T) {
	server := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 200)))
	})

	provider := NewIPAPIIsProvider(server.Client(), "")
	provider.baseURL = server.URL + "/"
	provider.maxBody = 64

	verdict := provider.Lookup(context.Background(), "1.1.1.1")

	require.False(t, verdict.OK)
	require.Contains(t, verdict.Error, "exceeds")
}
