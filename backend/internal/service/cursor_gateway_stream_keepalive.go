package service

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	// cursorSSECommentPing 是 Chat Completions / Responses 的 SSE 注释心跳。
	// Codex 等严格 SDK 会把未知 `event: ping` 当成协议错误直接掐流，注释行会被忽略。
	cursorSSECommentPing = ": ping\n\n"
	// cursorSSEAnthropicPing 对齐 Anthropic Messages 的官方 ping 事件。
	cursorSSEAnthropicPing = "event: ping\ndata: {\"type\":\"ping\"}\n\n"

	defaultCursorStreamKeepalive = 10 * time.Second
)

// cursorStreamSink 串行化 Cursor 流式写出：心跳协程与增量写出共用同一把锁，
// 避免在 Agent 长时间只吐 KV、没有文本增量时下游 120s 零字节被 CLI/代理掐成 EOF。
type cursorStreamSink struct {
	mu               sync.Mutex
	c                *gin.Context
	headersWritten   bool
	clientGone       bool
	keepaliveStopped bool
	stopKeepalive    chan struct{}
}

func newCursorStreamSink(c *gin.Context) *cursorStreamSink {
	return &cursorStreamSink{c: c, stopKeepalive: make(chan struct{})}
}

func (s *cursorStreamSink) headersWrittenLocked() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.headersWritten
}

func (s *cursorStreamSink) clientGoneLocked() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clientGone
}

func (s *cursorStreamSink) writeFrame(payload string) error {
	if s == nil {
		return fmt.Errorf("cursor stream sink is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeFrameLocked(payload)
}

func (s *cursorStreamSink) ping(payload string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.keepaliveStopped || s.clientGone {
		return false
	}
	return s.writeFrameLocked(payload) == nil
}

func (s *cursorStreamSink) writeFrameLocked(payload string) error {
	if s.clientGone {
		return fmt.Errorf("cursor stream client gone")
	}
	if s.c == nil || s.c.Writer == nil {
		s.clientGone = true
		return fmt.Errorf("cursor stream writer is nil")
	}
	if !s.headersWritten {
		s.c.Header("Content-Type", "text/event-stream")
		s.c.Header("Cache-Control", "no-cache")
		s.c.Header("Connection", "keep-alive")
		s.c.Header("X-Accel-Buffering", "no")
		s.c.Status(http.StatusOK)
		s.headersWritten = true
	}
	if _, err := fmt.Fprint(s.c.Writer, payload); err != nil {
		s.clientGone = true
		return err
	}
	s.c.Writer.Flush()
	return nil
}

// startKeepalive 在 interval 静默后开始向下游打心跳。首拍故意延迟一个 interval：
// Agent HTTP 握手的硬错误（401/429）通常更快返回，仍走 JSON failover；
// 一旦发出心跳，HTTP 200 已固化，后续失败只能在流内收尾。
func (s *cursorStreamSink) startKeepalive(interval time.Duration, ping string) func() {
	nop := func() {}
	if s == nil || interval <= 0 || ping == "" {
		return nop
	}

	var reqDone <-chan struct{}
	if s.c != nil && s.c.Request != nil {
		reqDone = s.c.Request.Context().Done()
	}

	var once sync.Once
	stop := func() {
		once.Do(func() {
			s.mu.Lock()
			if !s.keepaliveStopped {
				s.keepaliveStopped = true
				close(s.stopKeepalive)
			}
			s.mu.Unlock()
		})
	}

	go func() {
		timer := time.NewTimer(interval)
		defer timer.Stop()
		for {
			select {
			case <-s.stopKeepalive:
				return
			case <-reqDone:
				return
			case <-timer.C:
			}
			if !s.ping(ping) {
				return
			}
			timer.Reset(interval)
		}
	}()
	return stop
}

func (s *CursorGatewayService) cursorStreamKeepaliveInterval() time.Duration {
	if s != nil && s.streamKeepaliveInterval > 0 {
		return s.streamKeepaliveInterval
	}
	return defaultCursorStreamKeepalive
}
