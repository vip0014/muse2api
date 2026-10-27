package protocol

import (
	"context"
	"errors"
	"fmt"

	"muse2api/internal/muse/capabilities"
)

var ErrCapabilityUnavailable = errors.New("capability unavailable")
var ErrSessionExpired = errors.New("session expired")

type ChatRequest struct {
	Prompt    string
	Model     string
	AccountID string
}
type ChatEvent struct {
	Delta string
	Done  bool
	Err   error
}
type ImageRequest struct{ Prompt, Model, Size, AspectRatio, ResponseFormat, ReferenceImage, AccountID string }
type ImageResult struct {
	Filename, Kind, URL string
	Bytes               int64
	Data                []byte
}
type VideoRequest struct {
	Prompt, Model, Size, AspectRatio, Resolution, ReferenceImage, AccountID string
	Duration                                                                int
}
type VideoTask struct {
	ID       string
	Status   string
	Progress int
	URL      string
	Error    string
}
type Session struct {
	Valid                 bool
	Status, VMID, VMState string
	Cookies               map[string]string
	CookieExpiry          map[string]int64
	CheckedAt             int64
}
type AccountCredentials struct {
	ID           string
	Cookies      map[string]string
	CookieExpiry map[string]int64
}

type Driver interface {
	Capabilities(context.Context) []capabilities.Capability
	Chat(context.Context, ChatRequest) (<-chan ChatEvent, error)
	GenerateImage(context.Context, ImageRequest) (*ImageResult, error)
	EditImage(context.Context, ImageRequest) (*ImageResult, error)
	CreateVideo(context.Context, VideoRequest) (*VideoTask, error)
	GetVideoTask(context.Context, string) (*VideoTask, error)
	CheckSession(context.Context, AccountCredentials) (*Session, error)
	Close() error
}

type UpstreamError struct {
	Operation string
	Status    int
	Retryable bool
	Message   string
}

func (e *UpstreamError) Error() string {
	if e.Status > 0 {
		return fmt.Sprintf("%s: 上游 HTTP %d", e.Operation, e.Status)
	}
	return e.Operation + ": " + e.Message
}
func Unavailable(operation string) error {
	return fmt.Errorf("%s: %w；需要可用的 Chromium/CDP 执行能力，请确认 MUSE2API_BROWSER=1 且浏览器可用", operation, ErrCapabilityUnavailable)
}
