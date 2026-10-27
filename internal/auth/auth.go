package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

func Valid(header, expected string) bool {
	const prefix = "bearer "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) || expected == "" {
		return false
	}
	got := strings.TrimSpace(header[len(prefix):])
	return subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1
}

func NewKey() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "m2a_" + hex.EncodeToString(b), nil
}

func CheckCredentials(gotUser, gotPass, wantUser, wantPass string) bool {
	userOK := subtle.ConstantTimeCompare([]byte(gotUser), []byte(wantUser)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(gotPass), []byte(wantPass)) == 1
	return userOK && passOK
}

func ClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if v := strings.TrimSpace(r.Header.Get("X-Real-IP")); v != "" && net.ParseIP(v) != nil {
			return v
		}
		if v := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); v != "" {
			first, _, _ := strings.Cut(v, ",")
			first = strings.TrimSpace(first)
			if net.ParseIP(first) != nil {
				return first
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type Limiter struct {
	mu          sync.Mutex
	attempts    map[string]*attempt
	max         int
	window      time.Duration
	lastCleanup time.Time
}
type attempt struct {
	count int
	reset time.Time
}

func NewLimiter() *Limiter {
	return &Limiter{attempts: map[string]*attempt{}, max: 10, window: time.Minute}
}
func (l *Limiter) Exceeded(r *http.Request, trustProxy bool) bool {
	ip := ClientIP(r, trustProxy)
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cleanupLocked(now)
	a := l.attempts[ip]
	if a == nil || now.After(a.reset) {
		return false
	}
	return a.count >= l.max
}
func (l *Limiter) Fail(r *http.Request, trustProxy bool) {
	ip := ClientIP(r, trustProxy)
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cleanupLocked(now)
	a := l.attempts[ip]
	if a == nil || now.After(a.reset) {
		l.attempts[ip] = &attempt{count: 1, reset: now.Add(l.window)}
		return
	}
	a.count++
}
func (l *Limiter) Reset(r *http.Request, trustProxy bool) {
	ip := ClientIP(r, trustProxy)
	l.mu.Lock()
	delete(l.attempts, ip)
	l.mu.Unlock()
}
func (l *Limiter) cleanupLocked(now time.Time) {
	if now.Sub(l.lastCleanup) < l.window && len(l.attempts) < 10000 {
		return
	}
	l.lastCleanup = now
	for key, a := range l.attempts {
		if now.After(a.reset) {
			delete(l.attempts, key)
		}
	}
	if len(l.attempts) >= 10000 {
		clear(l.attempts)
	}
}
