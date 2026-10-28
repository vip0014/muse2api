package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// CDP is a serialized Chrome DevTools Protocol connection. One goroutine owns
// the read side and dispatches responses by request id; callers wait on their
// own channel with a context/deadline. This is important because
// gorilla/websocket treats a read timeout as a permanently failed connection:
// short per-read deadlines would poison the socket and eventually panic with
// "repeated read on failed websocket connection". Here a slow call only times
// out that call; the connection stays healthy.
type CDP struct {
	conn    *websocket.Conn
	timeout time.Duration

	writeMu sync.Mutex
	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan cdpMessage

	closed    atomic.Bool
	closeOnce sync.Once
	errMu     sync.Mutex
	err       error
}

type cdpMessage struct {
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func DialCDP(ctx context.Context, url string, timeout time.Duration) (*CDP, error) {
	if timeout <= 0 {
		timeout = 180 * time.Second
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: CDP WebSocket 连接失败: %v", ErrBrowserUnavailable, err)
	}
	conn.SetReadLimit(768 << 20)
	c := &CDP{conn: conn, timeout: timeout, pending: map[int64]chan cdpMessage{}}
	go c.readLoop()
	return c, nil
}

// readLoop is the only reader of the CDP socket. A read error is permanent for
// gorilla/websocket, so it fails every pending call once and exits. It never
// retries the same connection.
func (c *CDP) readLoop() {
	for {
		var msg cdpMessage
		if err := c.conn.ReadJSON(&msg); err != nil {
			c.fail(fmt.Errorf("%w: %v", ErrCDPConnectionLost, err))
			return
		}
		if msg.ID == 0 {
			continue // Browser event; state is polled from the DOM instead.
		}
		// Send while holding the lock: fail() closes pending channels under the
		// same lock, so a concurrent Close can never close a channel between the
		// lookup and the send.
		c.mu.Lock()
		ch := c.pending[msg.ID]
		if ch != nil {
			select {
			case ch <- msg:
			default:
			}
		}
		c.mu.Unlock()
	}
}

// fail marks the connection permanently unusable. Closing every pending
// channel wakes callers immediately instead of letting them read a dead
// socket. closeOnce guarantees the first cause is preserved.
func (c *CDP) fail(err error) {
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		c.errMu.Lock()
		c.err = err
		c.errMu.Unlock()
		_ = c.conn.Close()

		c.mu.Lock()
		pending := c.pending
		c.pending = map[int64]chan cdpMessage{}
		for _, ch := range pending {
			close(ch)
		}
		c.mu.Unlock()
	})
}

func (c *CDP) connectionError() error {
	c.errMu.Lock()
	defer c.errMu.Unlock()
	if c.err != nil {
		return c.err
	}
	return ErrCDPClosed
}

func (c *CDP) Call(ctx context.Context, method string, params any, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.closed.Load() {
		return fmt.Errorf("CDP %s: %w", method, c.connectionError())
	}

	c.mu.Lock()
	if c.closed.Load() {
		c.mu.Unlock()
		return fmt.Errorf("CDP %s: %w", method, c.connectionError())
	}
	c.nextID++
	id := c.nextID
	ch := make(chan cdpMessage, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	deadline := time.Now().Add(c.timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}

	c.writeMu.Lock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	err := c.conn.WriteJSON(map[string]any{"id": id, "method": method, "params": params})
	c.writeMu.Unlock()
	if err != nil {
		c.fail(fmt.Errorf("%w: %v", ErrCDPConnectionLost, err))
		return fmt.Errorf("CDP %s 写入失败: %w", method, c.connectionError())
	}

	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case msg, ok := <-ch:
		if !ok {
			return fmt.Errorf("CDP %s: %w", method, c.connectionError())
		}
		if msg.Error != nil {
			return fmt.Errorf("CDP %s: %s", method, msg.Error.Message)
		}
		if out != nil && len(msg.Result) > 0 {
			if err := json.Unmarshal(msg.Result, out); err != nil {
				return fmt.Errorf("CDP %s 响应解析失败: %w", method, err)
			}
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("CDP %s 超时", method)
	}
}

func (c *CDP) JS(ctx context.Context, expression string, awaitPromise bool, out any) error {
	var response struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	params := map[string]any{"expression": expression, "returnByValue": true, "awaitPromise": awaitPromise}
	if err := c.Call(ctx, "Runtime.evaluate", params, &response); err != nil {
		return err
	}
	if response.ExceptionDetails != nil {
		return fmt.Errorf("页面 JavaScript 执行失败: %s", response.ExceptionDetails.Text)
	}
	if out == nil || len(response.Result.Value) == 0 {
		return nil
	}
	if err := json.Unmarshal(response.Result.Value, out); err != nil {
		return fmt.Errorf("页面 JavaScript 返回值解析失败: %w", err)
	}
	return nil
}

// Close is safe to call while calls are blocked. It wakes every waiter with a
// permanent connection error; the reader goroutine exits on the closed socket.
func (c *CDP) Close() error {
	c.fail(ErrCDPClosed)
	return nil
}
