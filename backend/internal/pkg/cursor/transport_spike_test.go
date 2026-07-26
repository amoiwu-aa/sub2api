package cursor

import (
	"bufio"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
)

// 本文件是计划 C.9 要求的可行性 spike，不是 Agent 桥的实现。
//
// 它要回答两个问题，答不上来就不该动手写 Phase 7：
//  1. Go 的 net/http 能不能在 HTTP/2 上做到真正的全双工——请求体还在写的
//     同时就能读到响应？标准库在某些条件下会缓冲请求体，那样 AgentService/Run
//     的双向流就退化成了一问一答。
//  2. 叠加 HTTP 代理之后还成不成立？账号代理是硬要求（见 C.7）。

const spikeTimeout = 10 * time.Second

// echoStreamHandler 每读到一行就立刻回一行，从而暴露任何一侧的缓冲。
func echoStreamHandler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, 2, r.ProtoMajor, "server must see HTTP/2")

		flusher, ok := w.(http.Flusher)
		require.True(t, ok, "HTTP/2 response writer must support flushing")
		w.Header().Set("Content-Type", "application/connect+proto")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		reader := bufio.NewReader(r.Body)
		for {
			line, err := reader.ReadString('\n')
			if len(line) > 0 {
				_, _ = io.WriteString(w, "echo:"+line)
				flusher.Flush()
			}
			if err != nil {
				return
			}
		}
	})
}

// assertFullDuplex 写一帧、读一帧，交替若干轮。任何一侧缓冲都会让它超时。
func assertFullDuplex(t *testing.T, client *http.Client, endpoint string) {
	t.Helper()

	pipeReader, pipeWriter := io.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), spikeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, pipeReader)
	require.NoError(t, err)
	// 不设 Content-Length：请求体长度未知正是双向流的前提。
	req.ContentLength = -1

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, 2, resp.ProtoMajor, "client must negotiate HTTP/2")

	respReader := bufio.NewReader(resp.Body)
	for i := 0; i < 3; i++ {
		payload := "frame-" + string(rune('a'+i)) + "\n"
		_, err := io.WriteString(pipeWriter, payload)
		require.NoError(t, err, "write frame %d", i)

		// 关键断言：请求体还没关闭，就必须能读到这一帧的响应。
		line, err := respReader.ReadString('\n')
		require.NoError(t, err, "read echo %d", i)
		require.Equal(t, "echo:"+payload, line)
	}
	require.NoError(t, pipeWriter.Close())
}

func TestSpikeHTTP2FullDuplexWithStandardTransport(t *testing.T) {
	server := httptest.NewUnstartedServer(echoStreamHandler(t))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()

	assertFullDuplex(t, server.Client(), server.URL)
}

func TestSpikeHTTP2FullDuplexWithExplicitHTTP2Transport(t *testing.T) {
	server := httptest.NewUnstartedServer(echoStreamHandler(t))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()

	// 显式用 http2.Transport 作为退路：万一 net/http 的自动升级在某些
	// 环境下退回 HTTP/1.1，这条路径仍然可用。
	transport := &http2.Transport{
		TLSClientConfig: &tls.Config{
			//nolint:gosec // spike 用的是 httptest 自签证书，生产路径不走这里。
			InsecureSkipVerify: true,
		},
	}
	defer transport.CloseIdleConnections()

	assertFullDuplex(t, &http.Client{Transport: transport}, server.URL)
}

// TestSpikeHTTP2FullDuplexThroughProxy 覆盖 C.9 的第 2 个风险点：
// http2.Transport 对代理的支持不如 http.Transport 成熟，而账号代理是硬要求。
func TestSpikeHTTP2FullDuplexThroughProxy(t *testing.T) {
	server := httptest.NewUnstartedServer(echoStreamHandler(t))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()

	proxy := httptest.NewServer(connectProxyHandler(t))
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	require.NoError(t, err)

	// http.Transport（而非 http2.Transport）负责 CONNECT 隧道，
	// ForceAttemptHTTP2 让它在隧道内继续协商 h2。
	transport := &http.Transport{
		Proxy: http.ProxyURL(proxyURL),
		TLSClientConfig: &tls.Config{
			//nolint:gosec // spike 用的是 httptest 自签证书。
			InsecureSkipVerify: true,
		},
		ForceAttemptHTTP2: true,
	}
	defer transport.CloseIdleConnections()

	assertFullDuplex(t, &http.Client{Transport: transport}, server.URL)
}

// connectProxyHandler 是一个最小的 CONNECT 隧道代理。
func connectProxyHandler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "only CONNECT is supported", http.StatusMethodNotAllowed)
			return
		}
		upstream, err := net.DialTimeout("tcp", r.Host, spikeTimeout)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer func() { _ = upstream.Close() }()

		hijacker, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "hijacking is not supported", http.StatusInternalServerError)
			return
		}
		client, _, err := hijacker.Hijack()
		if err != nil {
			return
		}
		defer func() { _ = client.Close() }()

		if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		done := make(chan struct{}, 2)
		go func() { _, _ = io.Copy(upstream, client); done <- struct{}{} }()
		go func() { _, _ = io.Copy(client, upstream); done <- struct{}{} }()
		<-done
	})
}
