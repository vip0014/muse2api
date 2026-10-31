package hatch

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// RpcResult is one completed application response.
type RpcResult struct {
	Status  int
	Headers []Header
	Body    []byte
}

// JSON decodes the response body into out.
func (r *RpcResult) JSON(out any) error {
	if len(r.Body) == 0 {
		return errors.New("empty response body")
	}
	return json.Unmarshal(r.Body, out)
}

type stream struct {
	id      int64
	status  int
	headers []Header
	body    []byte
	done    chan struct{}
	err     error

	mu      sync.Mutex
	closed  bool
	records chan map[string]any // non-nil for streaming RPCs
	buf     []byte
}

func (s *stream) finish(err error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.err = err
	records := s.records
	s.mu.Unlock()
	close(s.done)
	if records != nil {
		close(records)
	}
}

// feedBody accumulates plain bodies and parses line-delimited JSON for
// streaming RPCs (chat.subscribe emits NDJSON or self-contained JSON chunks).
func (s *stream) feedBody(data []byte) {
	if len(data) == 0 {
		return
	}
	if s.records == nil {
		s.body = append(s.body, data...)
		return
	}
	s.buf = append(s.buf, data...)
	for {
		idx := bytes.IndexByte(s.buf, '\n')
		if idx < 0 {
			break
		}
		line := bytes.TrimSpace(s.buf[:idx])
		s.buf = s.buf[idx+1:]
		if len(line) == 0 {
			continue
		}
		var record map[string]any
		if json.Unmarshal(line, &record) == nil {
			s.records <- record
		}
	}
	tail := bytes.TrimSpace(s.buf)
	if len(tail) > 0 && (tail[0] == '{' || tail[0] == '[') {
		var record map[string]any
		if json.Unmarshal(tail, &record) == nil {
			s.buf = nil
			s.records <- record
		}
	}
}

// Conn is one Noise-encrypted hatch gateway connection.
type Conn struct {
	ws     *websocket.Conn
	tx     *CipherState
	rx     *CipherState
	reasm  *reassembler
	wmu    sync.Mutex
	mu     sync.Mutex
	streams map[int64]*stream
	nextID int64
	closed bool
	err    error
}

const handshakeTimeout = 20 * time.Second

// Dial opens the WebSocket, performs the XX handshake and starts the read loop.
func Dial(ctx context.Context, wsURL string) (*Conn, error) {
	dialer := websocket.Dialer{HandshakeTimeout: handshakeTimeout, ReadBufferSize: 1 << 20, WriteBufferSize: 1 << 20}
	ws, resp, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		status := ""
		if resp != nil {
			status = resp.Status
		}
		return nil, fmt.Errorf("gateway dial: %w (%s)", err, status)
	}
	ws.SetReadLimit(64 << 20)

	noise := NewNoiseXXInitiator()
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		_ = ws.Close()
		return nil, err
	}
	message1, err := noise.WriteMessage1(append([]byte{0x0a, 0x20}, nonce...))
	if err != nil {
		_ = ws.Close()
		return nil, err
	}
	if err := ws.WriteMessage(websocket.BinaryMessage, message1); err != nil {
		_ = ws.Close()
		return nil, fmt.Errorf("gateway handshake message1: %w", err)
	}

	_ = ws.SetReadDeadline(time.Now().Add(handshakeTimeout))
	msgType, message2, err := ws.ReadMessage()
	if err != nil {
		_ = ws.Close()
		return nil, fmt.Errorf("gateway handshake message2: %w", err)
	}
	_ = ws.SetReadDeadline(time.Time{})
	if msgType != websocket.BinaryMessage {
		_ = ws.Close()
		return nil, errors.New("gateway handshake: expected binary message2")
	}
	if _, err := noise.ReadMessage2(message2); err != nil {
		_ = ws.Close()
		return nil, fmt.Errorf("gateway handshake message2 decode: %w", err)
	}
	message3, err := noise.WriteMessage3(nil)
	if err != nil {
		_ = ws.Close()
		return nil, err
	}
	if err := ws.WriteMessage(websocket.BinaryMessage, message3); err != nil {
		_ = ws.Close()
		return nil, fmt.Errorf("gateway handshake message3: %w", err)
	}
	tx, rx, err := noise.Split()
	if err != nil {
		_ = ws.Close()
		return nil, err
	}
	c := &Conn{ws: ws, tx: tx, rx: rx, reasm: newReassembler(), streams: map[int64]*stream{}, nextID: 1}
	go c.readLoop()
	return c, nil
}

// Close releases the connection; pending calls fail.
func (c *Conn) Close() error {
	c.shutdown(errors.New("connection closed"))
	return nil
}

// IsClosed reports whether the read loop terminated.
func (c *Conn) IsClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *Conn) shutdown(err error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.err = err
	pending := make([]*stream, 0, len(c.streams))
	for _, s := range c.streams {
		pending = append(pending, s)
	}
	c.streams = map[int64]*stream{}
	c.mu.Unlock()
	for _, s := range pending {
		s.finish(err)
	}
	_ = c.ws.Close()
}

func (c *Conn) readLoop() {
	for {
		msgType, raw, err := c.ws.ReadMessage()
		if err != nil {
			c.shutdown(fmt.Errorf("gateway read: %w", err))
			return
		}
		if msgType != websocket.BinaryMessage {
			continue
		}
		plain, err := c.rx.DecryptWithAD(nil, raw)
		if err != nil {
			c.shutdown(fmt.Errorf("gateway decrypt: %w", err))
			return
		}
		payload, complete, err := c.reasm.feed(plain)
		if err != nil {
			c.shutdown(fmt.Errorf("gateway chunk: %w", err))
			return
		}
		if !complete {
			continue
		}
		serviceFrameBytes, err := decodeServiceResponse(payload)
		if err != nil {
			continue
		}
		frame, err := decodeServiceFrame(serviceFrameBytes)
		if err != nil {
			continue
		}
		c.handleFrame(frame)
	}
}

func (c *Conn) handleFrame(frame serviceFrame) {
	c.mu.Lock()
	s := c.streams[frame.StreamID]
	c.mu.Unlock()
	if s == nil {
		return
	}
	switch frame.Kind {
	case "response":
		if frame.Response == nil {
			return
		}
		s.status = frame.Response.Status
		s.headers = frame.Response.Headers
		s.feedBody(frame.Response.Body)
		if frame.Response.EndBody {
			s.finish(nil)
		}
	case "body_chunk":
		s.feedBody(frame.BodyData)
		if frame.BodyEnd {
			s.finish(nil)
		}
	case "reset":
		s.finish(fmt.Errorf("upstream reset: code=%d reason=%s", frame.Reset, frame.Reason))
	}
}

func (c *Conn) newStream() *stream {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.nextID
	c.nextID++
	s := &stream{id: id, done: make(chan struct{})}
	c.streams[id] = s
	return s
}

func (c *Conn) dropStream(id int64) {
	c.mu.Lock()
	delete(c.streams, id)
	c.mu.Unlock()
}

func randomRequestID() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func (c *Conn) writeEncrypted(payload []byte) error {
	chunks, err := splitNoisePayload(payload)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	for _, chunk := range chunks {
		encrypted, err := c.tx.EncryptWithAD(nil, chunk)
		if err != nil {
			return err
		}
		if err := c.ws.WriteMessage(websocket.BinaryMessage, encrypted); err != nil {
			return err
		}
	}
	return nil
}

// Request performs one unary RPC (HTTP-like verb + path over the gateway).
func (c *Conn) Request(ctx context.Context, method, path string, params map[string]any, timeout time.Duration) (*RpcResult, error) {
	if c.IsClosed() {
		return nil, errors.New("gateway connection closed")
	}
	body := []byte(nil)
	jsonBody := false
	if method == "GET" || method == "DELETE" {
		if len(params) > 0 {
			query := url.Values{}
			for key, value := range params {
				query.Set(key, fmt.Sprint(value))
			}
			if encoded := query.Encode(); encoded != "" {
				separator := "?"
				if containsRune(path, '?') {
					separator = "&"
				}
				path += separator + encoded
			}
		}
	} else {
		encoded, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		body = encoded
		jsonBody = true
	}

	headers := []Header{
		{Key: "x-request-id", Value: randomRequestID()},
		{Key: "x-app-id", Value: "hatch-web"},
		{Key: "Accept-Language", Value: "en-US"},
	}
	if jsonBody {
		headers = append([]Header{{Key: "Content-Type", Value: "application/json"}}, headers...)
	}

	s := c.newStream()
	defer c.dropStream(s.id)
	frameBytes := encodeServiceFrameRequest(s.id, ApplicationRequest{
		Verb: method, Path: path, Headers: headers, Body: body, EndBody: true,
	})
	envelope := encodeServiceRequest(serviceDaemon, frameBytes)
	if err := c.writeEncrypted(envelope); err != nil {
		return nil, err
	}

	waitCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		waitCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	select {
	case <-waitCtx.Done():
		// Tell the peer this stream is abandoned.
		resetFrame := encodeServiceFrameReset(s.id, 1, "client aborted")
		_ = c.writeEncrypted(encodeServiceRequest(serviceDaemon, resetFrame))
		return nil, waitCtx.Err()
	case <-s.done:
		if s.err != nil {
			return nil, s.err
		}
		return &RpcResult{Status: s.status, Headers: s.headers, Body: s.body}, nil
	}
}

// StreamHandle is one open streaming RPC (chat.subscribe).
type StreamHandle struct {
	conn   *Conn
	stream *stream
	once   sync.Once
}

// Events yields parsed JSON records until the stream ends. The consumer must
// keep reading; feedBody applies backpressure to the shared connection.
func (h *StreamHandle) Events() <-chan map[string]any {
	return h.stream.records
}

// Err reports the terminal stream error, if any.
func (h *StreamHandle) Err() error {
	h.stream.mu.Lock()
	defer h.stream.mu.Unlock()
	return h.stream.err
}

// Close resets the upstream stream and forgets it.
func (h *StreamHandle) Close() {
	h.once.Do(func() {
		if !h.conn.IsClosed() {
			reset := encodeServiceFrameReset(h.stream.id, 1, "client done")
			_ = h.conn.writeEncrypted(encodeServiceRequest(serviceDaemon, reset))
		}
		h.conn.dropStream(h.stream.id)
	})
}

// OpenStream starts a POST streaming RPC and returns a handle for its records.
func (c *Conn) OpenStream(ctx context.Context, path string, params map[string]any) (*StreamHandle, error) {
	if c.IsClosed() {
		return nil, errors.New("gateway connection closed")
	}
	body, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	s := c.newStream()
	s.records = make(chan map[string]any, 512)
	headers := []Header{
		{Key: "Content-Type", Value: "application/json"},
		{Key: "x-request-id", Value: randomRequestID()},
		{Key: "x-app-id", Value: "hatch-web"},
		{Key: "Accept-Language", Value: "en-US"},
	}
	frameBytes := encodeServiceFrameRequest(s.id, ApplicationRequest{
		Verb: http.MethodPost, Path: path, Headers: headers, Body: body, EndBody: true,
	})
	if err := c.writeEncrypted(encodeServiceRequest(serviceDaemon, frameBytes)); err != nil {
		c.dropStream(s.id)
		return nil, err
	}
	select {
	case <-ctx.Done():
		c.dropStream(s.id)
		return nil, ctx.Err()
	default:
	}
	return &StreamHandle{conn: c, stream: s}, nil
}

func containsRune(s string, r rune) bool {
	for _, c := range s {
		if c == r {
			return true
		}
	}
	return false
}
