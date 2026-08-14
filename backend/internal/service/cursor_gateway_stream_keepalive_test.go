package service

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCursorStreamKeepaliveWritesCommentPing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	sink := newCursorStreamSink(c)
	stop := sink.startKeepalive(15*time.Millisecond, cursorSSECommentPing)
	time.Sleep(80 * time.Millisecond)
	stop()

	require.True(t, sink.headersWrittenLocked())
	require.Contains(t, rec.Body.String(), ": ping")
	require.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestCursorStreamKeepaliveStopsBeforeContentWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	sink := newCursorStreamSink(c)
	stop := sink.startKeepalive(time.Hour, cursorSSECommentPing)
	stop()
	require.NoError(t, sink.writeFrame("data: {\"ok\":true}\n\n"))
	require.NotContains(t, rec.Body.String(), ": ping")
	require.Contains(t, rec.Body.String(), `"ok":true`)
}

func TestCursorStreamKeepaliveIntervalDefaultsToTenSeconds(t *testing.T) {
	require.Equal(t, 10*time.Second, (&CursorGatewayService{}).cursorStreamKeepaliveInterval())
	require.Equal(t, 5*time.Second, (&CursorGatewayService{streamKeepaliveInterval: 5 * time.Second}).cursorStreamKeepaliveInterval())
}
