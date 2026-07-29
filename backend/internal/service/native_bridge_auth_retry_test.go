//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newAuthRetryTestContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req, _ := http.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request = req
	return c
}

func TestShouldRetryNativeBridgeAuth(t *testing.T) {
	noop := func(context.Context) error { return nil }

	t.Run("401 before any output retries once", func(t *testing.T) {
		c := newAuthRetryTestContext()
		err := &UpstreamFailoverError{StatusCode: http.StatusUnauthorized}
		require.True(t, shouldRetryNativeBridgeAuth(
			context.Background(), c, c.Writer.Size(), err, noop, "test", 1))
	})

	t.Run("403 also retries", func(t *testing.T) {
		c := newAuthRetryTestContext()
		err := &UpstreamFailoverError{StatusCode: http.StatusForbidden}
		require.True(t, shouldRetryNativeBridgeAuth(
			context.Background(), c, c.Writer.Size(), err, noop, "test", 1))
	})

	t.Run("non-auth failures are left to normal failover", func(t *testing.T) {
		c := newAuthRetryTestContext()
		for _, status := range []int{http.StatusTooManyRequests, http.StatusBadGateway, http.StatusInternalServerError} {
			err := &UpstreamFailoverError{StatusCode: status}
			require.Falsef(t, shouldRetryNativeBridgeAuth(
				context.Background(), c, c.Writer.Size(), err, noop, "test", 1),
				"status %d must not trigger an auth retry", status)
		}
	})

	t.Run("only retries once", func(t *testing.T) {
		c := newAuthRetryTestContext()
		err := &UpstreamFailoverError{StatusCode: http.StatusUnauthorized}
		ctx := markNativeBridgeAuthRetried(context.Background())
		require.False(t, shouldRetryNativeBridgeAuth(ctx, c, c.Writer.Size(), err, noop, "test", 1))
	})

	// 已经写出去的字节收不回来，重试会让客户端看到两段拼接的响应。
	t.Run("never retries after bytes reached the client", func(t *testing.T) {
		c := newAuthRetryTestContext()
		sizeBefore := c.Writer.Size()
		_, _ = c.Writer.Write([]byte("event: message_start\n\n"))
		err := &UpstreamFailoverError{StatusCode: http.StatusUnauthorized}
		require.False(t, shouldRetryNativeBridgeAuth(
			context.Background(), c, sizeBefore, err, noop, "test", 1))
	})

	// 作废失败意味着缓存里还是那个坏 token，重试必然拿到同样的 401。
	t.Run("does not retry when the cache could not be invalidated", func(t *testing.T) {
		c := newAuthRetryTestContext()
		err := &UpstreamFailoverError{StatusCode: http.StatusUnauthorized}
		failing := func(context.Context) error { return errors.New("redis down") }
		require.False(t, shouldRetryNativeBridgeAuth(
			context.Background(), c, c.Writer.Size(), err, failing, "test", 1))
	})

	t.Run("plain errors and nil are ignored", func(t *testing.T) {
		c := newAuthRetryTestContext()
		require.False(t, shouldRetryNativeBridgeAuth(
			context.Background(), c, c.Writer.Size(), nil, noop, "test", 1))
		require.False(t, shouldRetryNativeBridgeAuth(
			context.Background(), c, c.Writer.Size(), errors.New("boom"), noop, "test", 1))
	})
}
