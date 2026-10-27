package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func testCDP(t *testing.T, handler func(*websocket.Conn)) *CDP {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		handler(conn)
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := DialCDP(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func TestCDPSkipsEventsAndDecodesResult(t *testing.T) {
	conn := testCDP(t, func(ws *websocket.Conn) {
		var request cdpMessage
		if err := ws.ReadJSON(&request); err != nil {
			return
		}
		_ = ws.WriteJSON(map[string]any{"method": "Runtime.consoleAPICalled", "params": map[string]any{}})
		_ = ws.WriteJSON(map[string]any{"id": request.ID, "result": map[string]any{"status": "ready"}})
	})
	var result struct{ Status string }
	if err := conn.Call(context.Background(), "Runtime.evaluate", map[string]any{}, &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "ready" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestCDPJavaScriptValue(t *testing.T) {
	conn := testCDP(t, func(ws *websocket.Conn) {
		var request cdpMessage
		if err := ws.ReadJSON(&request); err != nil {
			return
		}
		_ = ws.WriteJSON(map[string]any{"id": request.ID, "result": map[string]any{"result": map[string]any{"value": "hello"}}})
	})
	var value string
	if err := conn.JS(context.Background(), "'hello'", false, &value); err != nil {
		t.Fatal(err)
	}
	if value != "hello" {
		t.Fatalf("unexpected JS value %q", value)
	}
}

func TestCDPErrorAndTimeout(t *testing.T) {
	t.Run("protocol error", func(t *testing.T) {
		conn := testCDP(t, func(ws *websocket.Conn) {
			var request cdpMessage
			if err := ws.ReadJSON(&request); err != nil {
				return
			}
			_ = ws.WriteJSON(map[string]any{"id": request.ID, "error": map[string]any{"code": -32000, "message": "denied"}})
		})
		if err := conn.Call(context.Background(), "Browser.close", nil, nil); err == nil || !strings.Contains(err.Error(), "denied") {
			t.Fatalf("expected CDP error, got %v", err)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		conn := testCDP(t, func(ws *websocket.Conn) {
			var request cdpMessage
			_ = ws.ReadJSON(&request)
			time.Sleep(300 * time.Millisecond)
		})
		conn.timeout = 40 * time.Millisecond
		if err := conn.Call(context.Background(), "Browser.getVersion", nil, nil); err == nil || !strings.Contains(err.Error(), "超时") {
			t.Fatalf("expected timeout, got %v", err)
		}
	})
}

func TestCDPCloseUnblocksPendingCall(t *testing.T) {
	conn := testCDP(t, func(ws *websocket.Conn) {
		var request cdpMessage
		if err := ws.ReadJSON(&request); err != nil {
			return
		}
		// Block until the client closes the socket.
		_, _, _ = ws.ReadMessage()
	})
	done := make(chan error, 1)
	go func() {
		done <- conn.Call(context.Background(), "Runtime.evaluate", map[string]any{}, nil)
	}()
	time.Sleep(50 * time.Millisecond)
	_ = conn.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("call unexpectedly succeeded after close")
		}
	case <-time.After(time.Second):
		t.Fatal("CDP Close did not unblock the pending call")
	}
}
