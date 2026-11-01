package hatch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func startFakeGateway(t *testing.T) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		responder := newNoiseXXResponder()
		_, m1, err := ws.ReadMessage()
		if err != nil {
			return
		}
		if _, err := responder.ReadMessage1(m1); err != nil {
			t.Logf("handshake1: %v", err)
			return
		}
		m2, err := responder.WriteMessage2(nil)
		if err != nil {
			return
		}
		if err := ws.WriteMessage(websocket.BinaryMessage, m2); err != nil {
			return
		}
		_, m3, err := ws.ReadMessage()
		if err != nil {
			return
		}
		if _, err := responder.ReadMessage3(m3); err != nil {
			t.Logf("handshake3: %v", err)
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
				t.Logf("decrypt: %v", err)
				return
			}
			payload, complete, err := reasm.feed(plain)
			if err != nil || !complete {
				continue
			}
			fields, err := parseFields(payload)
			if err != nil {
				t.Logf("service request fields: %v", err)
				return
			}
			var frameBytes []byte
			for _, field := range fields {
				if field.num == 2 && field.wire == 2 {
					frameBytes = field.bytes
					break
				}
			}
			if len(frameBytes) == 0 {
				t.Log("service request missing payload")
				return
			}
			frame, err := decodeServiceFrame(frameBytes)
			if err != nil {
				t.Logf("service frame: %v", err)
				return
			}
			if frame.Kind != "request" {
				continue
			}
			_, path, _ := parseTestRequest(frame.Request)
			switch path {
			case "/model":
				sendTestResponse(ws, tx, frame.StreamID, 200, []byte(`{"ok":true}`), true)
			case "/stream":
				sendTestResponse(ws, tx, frame.StreamID, 200, nil, false)
				sendTestBody(ws, tx, frame.StreamID, []byte(`{"n":1}`), false)
				sendTestBody(ws, tx, frame.StreamID, []byte("\n"+`{"n":2}`), true)
			case "/late":
				sendTestResponse(ws, tx, frame.StreamID, 200, []byte("ok"), true)
				sendTestBody(ws, tx, frame.StreamID, []byte("late"), true)
			case "/sink":
				sendTestResponse(ws, tx, frame.StreamID, 200, nil, false)
				for _, part := range []string{"a", "b", "c"} {
					sendTestBody(ws, tx, frame.StreamID, []byte(part), part == "c")
				}
			default:
				sendTestResponse(ws, tx, frame.StreamID, 404, []byte("missing"), true)
			}
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func parseTestRequest(data []byte) (string, string, []byte) {
	fields, _ := parseFields(data)
	verb, path := "", ""
	var body []byte
	for _, field := range fields {
		switch {
		case field.num == 1 && field.wire == 2:
			verb = string(field.bytes)
		case field.num == 2 && field.wire == 2:
			path = string(field.bytes)
		case field.num == 4 && field.wire == 2:
			body = append(body, field.bytes...)
		}
	}
	return verb, path, body
}

func sendTestResponse(ws *websocket.Conn, tx *CipherState, streamID int64, status int, body []byte, end bool) {
	app := fieldVarint(1, uint64(status))
	if len(body) > 0 {
		app = append(app, fieldBytes(3, body)...)
	}
	app = append(app, fieldBool(4, end)...)
	frame := fieldVarint(1, uint64(streamID))
	frame = append(frame, fieldBytes(3, app)...)
	writeTestEnvelope(ws, tx, fieldBytes(1, frame))
}

func sendTestBody(ws *websocket.Conn, tx *CipherState, streamID int64, data []byte, end bool) {
	frame := encodeServiceFrameBody(streamID, data, end)
	writeTestEnvelope(ws, tx, fieldBytes(1, frame))
}

func writeTestEnvelope(ws *websocket.Conn, tx *CipherState, payload []byte) {
	chunks, err := splitNoisePayload(payload)
	if err != nil {
		return
	}
	for _, chunk := range chunks {
		encrypted, err := tx.EncryptWithAD(nil, chunk)
		if err != nil {
			return
		}
		if err := ws.WriteMessage(websocket.BinaryMessage, encrypted); err != nil {
			return
		}
	}
}

func TestConnRPCStreamSinkLateChunkAndClose(t *testing.T) {
	server := startFakeGateway(t)
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/noise"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := Dial(ctx, wsURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	result, err := conn.Request(ctx, http.MethodGet, "/model", nil, time.Second)
	if err != nil || result == nil || string(result.Body) != `{"ok":true}` {
		t.Fatalf("unary: err=%v result=%+v", err, result)
	}

	stream, err := conn.OpenStream(ctx, "/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	records := make([]map[string]any, 0, 2)
	for record := range stream.Events() {
		records = append(records, record)
	}
	if len(records) != 2 || records[0]["n"] != float64(1) || records[1]["n"] != float64(2) {
		t.Fatalf("unexpected stream records: %+v", records)
	}

	var sink strings.Builder
	if _, err := conn.RequestTo(ctx, http.MethodGet, "/sink", nil, time.Second, func(data []byte) error {
		sink.Write(data)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if sink.String() != "abc" {
		t.Fatalf("sink got %q", sink.String())
	}

	// EndBody followed by a late chunk must not close records a second time
	// or panic the read loop.
	if _, err := conn.Request(ctx, http.MethodGet, "/late", nil, time.Second); err != nil {
		t.Fatalf("late chunk request: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
}
