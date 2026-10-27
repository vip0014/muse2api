package auth

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

type Session struct {
	Username string
	Expires  time.Time
}

type SessionManager struct {
	mu       sync.Mutex
	sessions map[string]Session
	ttl      time.Duration
}

func NewSessionManager(ttl time.Duration) *SessionManager {
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	return &SessionManager{sessions: map[string]Session{}, ttl: ttl}
}

func (m *SessionManager) Create(username string) (string, time.Time, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", time.Time{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	now := time.Now()
	expires := now.Add(m.ttl)
	m.mu.Lock()
	// Login is infrequent; sweeping expired sessions here keeps the map
	// bounded without adding another background goroutine.
	m.cleanupExpiredLocked(now)
	m.sessions[token] = Session{Username: username, Expires: expires}
	m.mu.Unlock()
	return token, expires, nil
}

func (m *SessionManager) Get(token string) (Session, bool) {
	if token == "" {
		return Session{}, false
	}
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[token]
	if !ok {
		return Session{}, false
	}
	if now.After(s.Expires) {
		delete(m.sessions, token)
		return Session{}, false
	}
	return s, true
}

func (m *SessionManager) Delete(token string) {
	if token == "" {
		return
	}
	m.mu.Lock()
	delete(m.sessions, token)
	m.mu.Unlock()
}

func (m *SessionManager) cleanupExpiredLocked(now time.Time) {
	for token, session := range m.sessions {
		if now.After(session.Expires) {
			delete(m.sessions, token)
		}
	}
}
