package service

import "context"

// ProxyTrafficRecorder records upstream HTTP payload bytes for account-bound
// proxies. ResolveProxyID snapshots the bound proxy before a request starts so
// later account edits cannot attribute an in-flight request to a new proxy.
type ProxyTrafficRecorder interface {
	ResolveProxyID(ctx context.Context, accountID int64, proxyURL string) int64
	Record(proxyID, uploadBytes, downloadBytes int64)
	Stop()
}
