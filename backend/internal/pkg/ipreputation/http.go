package ipreputation

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// defaultMaxBodyBytes caps reputation responses. ipapi.is returns the largest
// payload of the three providers at roughly 2 KB.
const defaultMaxBodyBytes = int64(64 * 1024)

const userAgent = "RingStar-IPReputation/1.0"

// httpProvider carries the plumbing shared by every provider: an outbound
// client, the response size cap and latency measurement.
//
// Reputation lookups deliberately go out over the server's own connection
// rather than through the proxy being inspected. The exit address is already
// known from the connectivity probe and is passed as a query parameter, so
// tunnelling the lookup would only burn proxy bandwidth and split our rate
// limit across every proxy in the list.
type httpProvider struct {
	name    string
	client  *http.Client
	maxBody int64
}

func (p *httpProvider) fetch(ctx context.Context, url string) ([]byte, int64, error) {
	start := time.Now()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, time.Since(start).Milliseconds(), fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	limit := p.maxBody
	if limit <= 0 {
		limit = defaultMaxBodyBytes
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return nil, latency, fmt.Errorf("read response: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, latency, fmt.Errorf("response exceeds %d bytes", limit)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, latency, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return body, latency, nil
}

func failedVerdict(source string, latencyMs int64, err error) Verdict {
	return Verdict{
		Source:    source,
		OK:        false,
		Error:     err.Error(),
		LatencyMs: latencyMs,
	}
}
