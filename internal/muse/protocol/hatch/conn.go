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

const (
	maxUnaryBody    = 64 << 20
	maxStreamRecord = 8 << 20
	streamQueueSize = 512
)

var (
	errProtocol         = errors.New("protocol error")
	errSlowConsumer     = errors.New("stream consumer stalled")
	errResponseTooLarge = errors.New("response body too large")
	errStreamDropped    = errors.New("stream dropped")
)

// RpcStatusError preserves an application-level HTTP status returned by the
// gateway. Callers can inspect Status for expected 404 fallbacks while still
// treating every non-2xx as an error instead of an empty success.
type RpcStatusError struct {
	Status int
	Body   []byte
}

func (e *RpcStatusError) Error() string {
	return fmt.Sprintf("upstream RPC HTTP %d: %s", e.Status, truncate(e.Body, 180))
}

// HTTPStatus lets callers classify gateway status codes without importing the
// hatch transport implementation.
func (e *RpcStatusError) HTTPStatus() int { return e.Status }

type stream struct {
	id      int64
	status  int
	headers []Header
	body    []byte
	done    chan struct{}

	mu      sync.Mutex
	closed  bool
	err     error
	records chan map[string]any // consumer-visible channel, closed by pump
	input   chan map[string]any // read-loop -> pump, never closed by producer
	buf     []byte
	bodyCap int
	sink    func([]byte) error
}

func newStream(id int64, streaming bool) *stream {
	s := &stream{id: id, done: make(chan struct{}), bodyCap: maxUnaryBody}
	if streaming {
		s.input = make(chan map[string]any, streamQueueSize)
		s.records = make(chan map[string]any, streamQueueSize)
		go s.pump()
	}
	return s
}

// pump is the only goroutine allowed to close records. This makes a late
// body_chunk, a reset, or a concurrent Close safe: producers select on done
// instead of racing a close on the consumer channel.
func (s *stream) pump() {
	defer close(s.records)
	for {
		select {
		case record := <-s.input:
			// Prefer the consumer channel over done here. Once a record has
			// been accepted from the read loop, dropping it on a concurrent
			// finish is exactly the final-record loss observed in the fake
			// gateway test.
			select {
			case s.records <- record:
				continue
			default:
				select {
				case <-s.done:
					return
				default:
					s.finish(errSlowConsumer)
					return
				}
			}
		case <-s.done:
			for {
				select {
				case record := <-s.input:
					select {
					case s.records <- record:
					default:
						return
					}
				default:
					return
				}
			}
		}
	}
}

func (s *stream) finish(err error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.err = err
	s.mu.Unlock()
	close(s.done)
}

func (s *stream) terminalError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *stream) setSink(sink func([]byte) error) {
	s.mu.Lock()
	s.sink = sink
	s.mu.Unlock()
}

func (s *stream) getSink() func([]byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sink
}

// feedBody accumulates plain bodies and parses line-delimited JSON for
// streaming RPCs (chat.subscribe emits NDJSON or self-contained JSON chunks).
func (s *stream) feedBody(data []byte) {
	if len(data) == 0 {
		return
	}
	select {
	case <-s.done:
		return
	default:
	}
	if s.input == nil {
		sink := s.getSink()
		if sink != nil && s.status >= 200 && s.status < 300 {
			if err := sink(data); err != nil {
				s.finish(err)
			}
			return
		}
		if len(s.body)+len(data) > s.bodyCap {
			s.finish(fmt.Errorf("upstream response body exceeded %d bytes; %w", s.bodyCap, errResponseTooLarge))
			return
		}
		s.body = append(s.body, data...)
		return
	}
	if len(s.buf)+len(data) > maxStreamRecord {
		s.finish(fmt.Errorf("stream record exceeded %d bytes; %w", maxStreamRecord, errProtocol))
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
		if !s.feedRecord(line) {
			return
		}
	}
	tail := bytes.TrimSpace(s.buf)
	if len(tail) > 0 && (tail[0] == '{' || tail[0] == '[') {
		var record map[string]any
		if json.Unmarshal(tail, &record) == nil {
			s.buf = nil
			s.feedRecord(tail)
		}
	}
}

func (s *stream) feedRecord(line []byte) bool {
	select {
	case <-s.done:
		return false
	default:
	}
	var record map[string]any
	if json.Unmarshal(line, &record) != nil {
		// A non-JSON record is a protocol error, not a silent end-of-stream.
		s.finish(fmt.Errorf("invalid stream record: %w", errProtocol))
		return false
	}
	select {
	case s.input <- record:
		return true
	case <-s.done:
		return false
	default:
		s.finish(fmt.Errorf("stream consumer stalled; %w", errSlowConsumer))
		return false
	}
}

// Conn is one Noise-encrypted hatch gateway connection.
type Conn struct {
	ws      *websocket.Conn
	tx      *CipherState
	rx      *CipherState
	reasm   *reassembler
	wmu     sync.Mutex
	mu      sync.Mutex
	streams map[int64]*stream
	nextID  int64
	closed  bool
	err     error
	done    chan struct{}
}

const (
	handshakeTimeout = 20 * time.Second
	readTimeout      = 120 * time.Second
	writeTimeout     = 30 * time.Second
	pingInterval     = 30 * time.Second
)

// Dial opens the WebSocket, performs the XX handshake and starts the read loop.
func Dial(ctx context.Context, wsURL string) (*Conn, error) {
	return DialWithDialer(ctx, wsURL, nil)
}

// DialWithDialer is the test/proxy seam. A nil dialer keeps the production
// defaults.
func DialWithDialer(ctx context.Context, wsURL string, dialer *websocket.Dialer) (*Conn, error) {
	if dialer == nil {
		dialer = &websocket.Dialer{HandshakeTimeout: handshakeTimeout, ReadBufferSize: 1 << 20, WriteBufferSize: 1 << 20}
	}
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
	_ = ws.SetReadDeadline(time.Now().Add(readTimeout))
	ws.SetPongHandler(func(string) error {
		return ws.SetReadDeadline(time.Now().Add(readTimeout))
	})
	c := &Conn{ws: ws, tx: tx, rx: rx, reasm: newReassembler(), streams: map[int64]*stream{}, nextID: 1, done: make(chan struct{})}
	go c.heartbeat()
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
	close(c.done)
	c.mu.Unlock()
	for _, s := range pending {
		s.finish(err)
	}
	_ = c.ws.Close()
}

func (c *Conn) heartbeat() {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			if err := c.ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)); err != nil {
				c.shutdown(fmt.Errorf("gateway ping: %w", err))
				return
			}
		}
	}
}

func (c *Conn) readLoop() {
	for {
		msgType, raw, err := c.ws.ReadMessage()
		if err != nil {
			c.shutdown(fmt.Errorf("gateway read: %w", err))
			return
		}
		_ = c.ws.SetReadDeadline(time.Now().Add(readTimeout))
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
			c.shutdown(fmt.Errorf("gateway service response: %w", err))
			return
		}
		frame, err := decodeServiceFrame(serviceFrameBytes)
		if err != nil {
			c.shutdown(fmt.Errorf("gateway service frame: %w", err))
			return
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
			s.finish(fmt.Errorf("response frame missing application payload: %w", errProtocol))
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

func (c *Conn) newStreamState(streaming bool) (*stream, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, errors.New("gateway connection closed")
	}
	id := c.nextID
	c.nextID++
	s := newStream(id, streaming)
	c.streams[id] = s
	return s, nil
}

func (c *Conn) dropStream(id int64) {
	c.mu.Lock()
	s := c.streams[id]
	delete(c.streams, id)
	c.mu.Unlock()
	if s != nil {
		s.finish(errStreamDropped)
	}
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
	_ = c.ws.SetWriteDeadline(time.Now().Add(writeTimeout))
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
	return c.request(ctx, method, path, params, timeout, nil)
}

// RequestTo performs a unary RPC while streaming a successful body to sink.
// The gateway may deliver media in many body_chunks; this keeps large media
// out of process memory. Non-2xx bodies are still captured (bounded) for the
// error path.
func (c *Conn) RequestTo(ctx context.Context, method, path string, params map[string]any, timeout time.Duration, sink func([]byte) error) (*RpcResult, error) {
	return c.request(ctx, method, path, params, timeout, sink)
}

func (c *Conn) request(ctx context.Context, method, path string, params map[string]any, timeout time.Duration, sink func([]byte) error) (*RpcResult, error) {
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

	s, err := c.newStreamState(false)
	if err != nil {
		return nil, err
	}
	s.setSink(sink)
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
		if err := s.terminalError(); err != nil {
			return nil, err
		}
		if s.status >= 400 {
			return nil, &RpcStatusError{Status: s.status, Body: append([]byte(nil), s.body...)}
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
	s, err := c.newStreamState(true)
	if err != nil {
		return nil, err
	}
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
