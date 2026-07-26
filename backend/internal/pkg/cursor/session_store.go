package cursor

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// SessionTTL 是一次浏览器登录流程的存活时间。
const SessionTTL = 15 * time.Minute

// LoginSession 保存一次 start→poll 之间需要留在服务端的状态。
//
// verifier 不返回给前端：它是 PKCE 的私密部分，返回出去等于把「谁都能拿这个
// uuid 去换令牌」的能力交给了浏览器。前端只需要 session_id 与 login_url。
type LoginSession struct {
	Verifier  string
	UUID      string
	LoginURL  string
	ProxyID   *int64
	CreatedAt time.Time
}

// SessionStore 是登录会话的内存存储，带 TTL 清理。
type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*LoginSession
	stopOnce sync.Once
	stopCh   chan struct{}
}

func NewSessionStore() *SessionStore {
	store := &SessionStore{
		sessions: make(map[string]*LoginSession),
		stopCh:   make(chan struct{}),
	}
	go store.cleanup()
	return store
}

// NewSessionID 生成一个不可猜测的会话标识。
func NewSessionID() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func (s *SessionStore) Set(sessionID string, session *LoginSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sessionID] = session
}

// Get 返回未过期的会话。
func (s *SessionStore) Get(sessionID string) (*LoginSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.sessions[sessionID]
	if !ok || timeNow().Sub(session.CreatedAt) > SessionTTL {
		return nil, false
	}
	return session, true
}

func (s *SessionStore) Delete(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sessionID)
}

func (s *SessionStore) Stop() {
	s.stopOnce.Do(func() { close(s.stopCh) })
}

func (s *SessionStore) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.mu.Lock()
			for id, session := range s.sessions {
				if timeNow().Sub(session.CreatedAt) > SessionTTL {
					delete(s.sessions, id)
				}
			}
			s.mu.Unlock()
		}
	}
}
