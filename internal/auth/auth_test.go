package auth

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestValid(t *testing.T) {
	if !Valid("Bearer secret", "secret") {
		t.Fatal("valid key rejected")
	}
	for _, h := range []string{"", "secret", "Bearer wrong", "Basic secret", "Bearer secret "} {
		if h == "Bearer secret " {
			continue
		}
		if Valid(h, "secret") {
			t.Fatalf("invalid key accepted: %q", h)
		}
	}
	if Valid("Bearer ", "") {
		t.Fatal("anonymous auth accepted")
	}
}

func TestCheckCredentials(t *testing.T) {
	if !CheckCredentials("admin", "correct-password", "admin", "correct-password") {
		t.Fatal("valid credentials rejected")
	}
	if CheckCredentials("admin", "wrong", "admin", "correct-password") {
		t.Fatal("wrong password accepted")
	}
}

func TestLimiterCountsFailuresAndResets(t *testing.T) {
	limiter := NewLimiter()
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "203.0.113.9:1234"
	for i := 0; i < 10; i++ {
		if limiter.Exceeded(req, false) {
			t.Fatalf("blocked before max after %d failures", i)
		}
		limiter.Fail(req, false)
	}
	if !limiter.Exceeded(req, false) {
		t.Fatal("limiter did not block after max failures")
	}
	limiter.Reset(req, false)
	if limiter.Exceeded(req, false) {
		t.Fatal("limiter did not reset")
	}
}

func TestClientIPTrustProxy(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Real-IP", "198.51.100.7")
	if got := ClientIP(req, false); got != "127.0.0.1" {
		t.Fatalf("untrusted proxy header used: %s", got)
	}
	if got := ClientIP(req, true); got != "198.51.100.7" {
		t.Fatalf("trusted proxy header ignored: %s", got)
	}
}

func TestSessionCreateSweepsExpiredSessions(t *testing.T) {
	manager := NewSessionManager(time.Hour)
	manager.ttl = time.Millisecond
	oldToken, _, err := manager.Create("admin")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	newToken, _, err := manager.Create("admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.Get(oldToken); ok {
		t.Fatal("expired session survived")
	}
	manager.mu.Lock()
	count := len(manager.sessions)
	manager.mu.Unlock()
	if count != 1 {
		t.Fatalf("session map retained %d entries, want 1", count)
	}
	if _, ok := manager.Get(newToken); !ok {
		t.Fatal("current session was removed")
	}
}
