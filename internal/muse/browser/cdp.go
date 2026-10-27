package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// CDP is a serialized Chrome DevTools Protocol connection. Each page has its
// own connection, so one slow evaluation cannot interleave with another call.
type CDP struct {
	conn      *websocket.Conn
	timeout   time.Duration
	mu        sync.Mutex
	nextID    int64
	closed    atomic.Bool
	closeOnce sync.Once
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
		return nil, fmt.Errorf("CDP WebSocket 连接失败: %w", err)
	}
	conn.SetReadLimit(768 << 20)
	return &CDP{conn: conn, timeout: timeout}, nil
}

func (c *CDP) Call(ctx context.Context, method string, params any, out any) error {
	if c.closed.Load() {
		return errors.New("CDP 连接已关闭")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed.Load() {
		return errors.New("CDP 连接已关闭")
	}
	c.nextID++
	id := c.nextID
	deadline := time.Now().Add(c.timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := c.conn.WriteJSON(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return fmt.Errorf("CDP %s 写入失败: %w", method, err)
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("CDP %s 超时", method)
		}
		readDeadline := time.Now().Add(2 * time.Second)
		if deadline.Before(readDeadline) {
			readDeadline = deadline
		}
		_ = c.conn.SetReadDeadline(readDeadline)
		var msg cdpMessage
		if err := c.conn.ReadJSON(&msg); err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				continue
			}
			return fmt.Errorf("CDP %s 读取失败: %w", method, err)
		}
		if msg.ID != id {
			continue // Events are intentionally ignored; state is polled from the DOM.
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
		return errors.New("页面 JavaScript 执行失败")
	}
	if out == nil || len(response.Result.Value) == 0 {
		return nil
	}
	if err := json.Unmarshal(response.Result.Value, out); err != nil {
		return fmt.Errorf("页面 JavaScript 返回值解析失败: %w", err)
	}
	return nil
}

// Close may be called while Call is blocked in ReadJSON. websocket.Conn.Close
// is safe to call concurrently with a reader, so shutdown is not held behind
// an in-flight CDP timeout.
func (c *CDP) Close() error {
	var err error
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		if c.conn != nil {
			err = c.conn.Close()
		}
	})
	return err
}
