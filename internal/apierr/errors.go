package apierr

import (
	"encoding/json"
	"errors"
	"net/http"
)

type Error struct {
	Status  int    `json:"-"`
	Message string `json:"message"`
	Type    string `json:"type"`
	Param   any    `json:"param"`
	Code    any    `json:"code"`
}

func (e *Error) Error() string { return e.Message }
func New(status int, typ, message string) *Error {
	return &Error{Status: status, Type: typ, Message: message, Code: status}
}
func Capability(message string) *Error {
	return New(http.StatusServiceUnavailable, "capability_unavailable", message)
}
func QueueFull() *Error {
	return New(http.StatusTooManyRequests, "rate_limit_error", "任务队列已满，请稍后重试")
}
func NotFound(message string) *Error { return New(http.StatusNotFound, "not_found_error", message) }
func BadRequest(message string) *Error {
	return New(http.StatusBadRequest, "invalid_request_error", message)
}

func Write(w http.ResponseWriter, r *http.Request, err error) {
	var e *Error
	if !errors.As(err, &e) {
		e = New(500, "server_error", "内部错误")
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(e.Status)
	if len(r.URL.Path) >= 4 && r.URL.Path[:4] == "/v1/" {
		_ = json.NewEncoder(w).Encode(map[string]any{"error": e})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"detail": e.Message, "error": e})
}
