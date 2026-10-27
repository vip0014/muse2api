package protocol

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"muse2api/internal/muse/transport"
)

func TestCheckSession(t *testing.T) {
	var gotCookie string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/session" {
			t.Errorf("path %s", r.URL.Path)
		}
		gotCookie = r.Header.Get("Cookie")
		http.SetCookie(w, &http.Cookie{Name: "hatch_vml", Value: "new", Expires: time.Now().Add(time.Hour)})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"assigned","vm_id":"vm","vm_state":"RUNNING"}`))
	}))
	defer ts.Close()
	tr, _ := transport.NewHTTP(ts.URL, time.Second, "")
	d := NewHTTPDriver(tr)
	s, err := d.CheckSession(context.Background(), AccountCredentials{Cookies: map[string]string{"hatch_sess": "old"}})
	if err != nil {
		t.Fatal(err)
	}
	if gotCookie != "hatch_sess=old" || s.Cookies["hatch_vml"] != "new" || !s.Valid {
		t.Fatalf("bad session: %+v cookie=%q", s, gotCookie)
	}
}
func TestWakeVMRechecksSession(t *testing.T) {
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/hatch/vm/wake":
			if r.Method != http.MethodPost {
				t.Errorf("wake method %s", r.Method)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["vm_id"] != "vm-1" || body["retry_count"] != float64(0) || body["connect_attempt_id"] == "" {
				t.Errorf("unexpected wake body: %+v", body)
			}
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/api/session":
			_, _ = w.Write([]byte(`{"status":"assigned","vm_id":"vm-1","vm_state":"RUNNING"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	tr, _ := transport.NewHTTP(ts.URL, time.Second, "")
	d := NewHTTPDriver(tr)
	session, err := d.WakeVM(context.Background(), AccountCredentials{Cookies: map[string]string{"hatch_sess": "cookie"}}, &Session{VMID: "vm-1"})
	if err != nil || !session.Valid || session.VMState != "RUNNING" || calls != 2 {
		t.Fatalf("wake failed: session=%+v calls=%d err=%v", session, calls, err)
	}
}

func TestSessionErrorsAndCapability(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer ts.Close()
	tr, _ := transport.NewHTTP(ts.URL, time.Second, "")
	d := NewHTTPDriver(tr)
	if _, err := d.CheckSession(context.Background(), AccountCredentials{Cookies: map[string]string{"a": "b"}}); err != ErrSessionExpired {
		t.Fatalf("expected expired, got %v", err)
	}
	if _, err := d.Chat(context.Background(), ChatRequest{}); !IsCapability(err) {
		t.Fatalf("expected unavailable, got %v", err)
	}
}
