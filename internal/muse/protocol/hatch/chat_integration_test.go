package hatch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"muse2api/internal/muse/protocol"
)

func startChatGateway(t *testing.T, silent bool) (*httptest.Server, string) {
	t.Helper()
	var upgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	var mu sync.Mutex
	subscribeID := int64(-1)
	subscribeReady := make(chan struct{}, 1)
	handler := func(ws *websocket.Conn) {
		defer ws.Close()
		responder := newNoiseXXResponder()
		_, m1, err := ws.ReadMessage()
		if err != nil {
			return
		}
		if _, err := responder.ReadMessage1(m1); err != nil {
			return
		}
		m2, _ := responder.WriteMessage2(nil)
		_ = ws.WriteMessage(websocket.BinaryMessage, m2)
		_, m3, err := ws.ReadMessage()
		if err != nil {
			return
		}
		if _, err := responder.ReadMessage3(m3); err != nil {
			return
		}
		tx, rx := responder.Split()
		reasm := newReassembler()
		for {
			_, raw, err := ws.ReadMessage()
			if err != nil {
				return
			}
			plain, err := rx.DecryptWithAD(nil, raw)
			if err != nil {
				return
			}
			payload, complete, err := reasm.feed(plain)
			if err != nil || !complete {
				continue
			}
			fields, _ := parseFields(payload)
			var frameBytes []byte
			for _, field := range fields {
				if field.num == 2 && field.wire == 2 {
					frameBytes = field.bytes
				}
			}
			frame, err := decodeServiceFrame(frameBytes)
			if err != nil || frame.Kind != "request" {
				continue
			}
			_, path, body := parseTestRequest(frame.Request)
			basePath := strings.SplitN(path, "?", 2)[0]
			switch basePath {
			case "/api/nodes/register", "/client/register-capabilities":
				sendTestResponse(ws, tx, frame.StreamID, 200, []byte(`{}`), true)
			case "/chat/subscribe":
				mu.Lock()
				subscribeID = frame.StreamID
				mu.Unlock()
				sendTestResponse(ws, tx, frame.StreamID, 200, nil, false)
				select {
				case subscribeReady <- struct{}{}:
				default:
				}
			case "/chat/stream":
				_ = body
				sendTestResponse(ws, tx, frame.StreamID, 200, []byte(`{"message_id":"m1"}`), true)
				if silent {
					continue
				}
				select {
				case <-subscribeReady:
				case <-time.After(time.Second):
					continue
				}
				mu.Lock()
				id := subscribeID
				mu.Unlock()
				now := time.Now().UnixMilli()
				lines := fmt.Sprintf(`{"event":"delta.text_append","payload":{"text":"hel"},"ts_ms":%d}`+"\n"+
					`{"event":"delta.text_append","payload":{"text":"lo"},"ts_ms":%d}`+"\n"+
					`{"event":"delta.message_done","payload":{},"ts_ms":%d}`, now, now, now)
				sendTestBody(ws, tx, id, []byte(lines), true)
			case "/chat/history":
				history := `{"chat_events":[{"message_id":"m1","event_name":"message.user","text":"draw"},{"event_name":"message.assistant","payload":{"kind":"image","path":"sandbox://gen.png","display_text":"done"}}]}`
				sendTestResponse(ws, tx, frame.StreamID, 200, []byte(history), true)
			default:
				if strings.HasPrefix(path, "/media/raw/") || strings.HasPrefix(path, "/fs/raw/") {
					sendTestResponse(ws, tx, frame.StreamID, 200, nil, false)
					for _, part := range []string{"abc", "def"} {
						sendTestBody(ws, tx, frame.StreamID, []byte(part), part == "def")
					}
					continue
				}
				sendTestResponse(ws, tx, frame.StreamID, 404, []byte("missing"), true)
			}
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/session", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"vm_id":"vm1","endpoint_url":"https://vm1","vm_state":"RUNNING"}`))
	})
	mux.HandleFunc("/api/hatch/token", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"token":"token"}`))
	})
	mux.HandleFunc("/api/hatch/noise-notary-token", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"notaryToken":"notary"}`))
	})
	mux.HandleFunc("/v1/noise", func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		handler(ws)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/noise"
	return server, wsURL
}

func TestChatStreamIntegration(t *testing.T) {
	server, wsURL := startChatGateway(t, false)
	client := NewClientWithWSURL(mustURL(t, server.URL), server.Client(), wsURL)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	events, err := client.ChatStream(ctx, map[string]string{"hatch_sess": "s"}, "hi", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	text := ""
	done := false
	for delta := range events {
		if delta.Err != nil {
			t.Fatalf("stream error: %v", delta.Err)
		}
		text += delta.Text
		done = done || delta.Done
	}
	if text != "hello" || !done {
		t.Fatalf("text=%q done=%v", text, done)
	}
}

func TestChatStreamTimeoutIsAnError(t *testing.T) {
	server, wsURL := startChatGateway(t, true)
	client := NewClientWithWSURL(mustURL(t, server.URL), server.Client(), wsURL)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	events, err := client.ChatStream(ctx, map[string]string{"hatch_sess": "s"}, "hi", 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	var streamErr error
	for delta := range events {
		if delta.Err != nil {
			streamErr = delta.Err
		}
	}
	if !errors.Is(streamErr, protocol.ErrUpstreamTimeout) {
		t.Fatalf("timeout stream error = %v", streamErr)
	}
}

func TestChatWithItemsHistoryAndMedia(t *testing.T) {
	server, wsURL := startChatGateway(t, true)
	client := NewClientWithWSURL(mustURL(t, server.URL), server.Client(), wsURL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	text, media, err := client.ChatWithItems(ctx, map[string]string{"hatch_sess": "s"}, "draw", nil, true, 4*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if text != "done" || len(media) != 1 || media[0].Path != "gen.png" {
		t.Fatalf("text=%q media=%+v", text, media)
	}
}

func TestDownloadToFileStreamsGatewayBody(t *testing.T) {
	server, wsURL := startChatGateway(t, true)
	client := NewClientWithWSURL(mustURL(t, server.URL), server.Client(), wsURL)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ref := MediaRef{Kind: "image", Path: "sandbox://x.png"}
	path, _, err := client.DownloadToFile(ctx, map[string]string{"hatch_sess": "s"}, ref, t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "abcdef" {
		t.Fatalf("downloaded %q err=%v", data, err)
	}
	_ = os.Remove(path)
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
