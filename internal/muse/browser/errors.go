package browser

import "errors"

var (
	// ErrCDPClosed means the CDP connection was closed locally. Calls made after
	// Close return this error instead of touching the socket.
	ErrCDPClosed = errors.New("CDP 连接已关闭")
	// ErrCDPConnectionLost means the CDP socket failed (Chromium exit, page
	// target crash, reset). It is sticky: every later call returns it without
	// reading from the failed connection.
	ErrCDPConnectionLost = errors.New("CDP 连接已失效")
	// ErrBrowserUnavailable means Chromium could not be reached or started.
	// A later attempt may succeed after the old process has exited.
	ErrBrowserUnavailable = errors.New("浏览器不可用")
)

// IsConnectionError reports whether err means the CDP connection can never be
// used again. Polling loops must stop immediately on this class of error.
func IsConnectionError(err error) bool {
	return errors.Is(err, ErrCDPConnectionLost) || errors.Is(err, ErrCDPClosed)
}

// IsRetryable reports whether a failed browser operation deserves one fresh
// attempt. Only connection-level failures qualify; business errors and
// session expiry do not.
func IsRetryable(err error) bool {
	return IsConnectionError(err) || errors.Is(err, ErrBrowserUnavailable)
}
