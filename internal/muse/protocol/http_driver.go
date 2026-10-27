package protocol

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"muse2api/internal/muse/capabilities"
	"muse2api/internal/muse/transport"
)

type HTTPDriver struct{ http *transport.HTTP }

func NewHTTPDriver(t *transport.HTTP) *HTTPDriver { return &HTTPDriver{http: t} }
func (d *HTTPDriver) Capabilities(context.Context) []capabilities.Capability {
	return capabilities.Current()
}
func (d *HTTPDriver) Chat(context.Context, ChatRequest) (<-chan ChatEvent, error) {
	return nil, Unavailable("chat")
}
func (d *HTTPDriver) GenerateImage(context.Context, ImageRequest) (*ImageResult, error) {
	return nil, Unavailable("image generation")
}
func (d *HTTPDriver) EditImage(context.Context, ImageRequest) (*ImageResult, error) {
	return nil, Unavailable("image edit")
}
func (d *HTTPDriver) CreateVideo(context.Context, VideoRequest) (*VideoTask, error) {
	return nil, Unavailable("video generation")
}
func (d *HTTPDriver) GetVideoTask(context.Context, string) (*VideoTask, error) {
	return nil, Unavailable("video task query")
}
func (d *HTTPDriver) Close() error { d.http.Close(); return nil }

func (d *HTTPDriver) CheckSession(ctx context.Context, credentials AccountCredentials) (*Session, error) {
	if len(credentials.Cookies) == 0 {
		return nil, &UpstreamError{Operation: "session", Message: "账号没有 Cookie"}
	}
	req, err := d.http.NewRequest(ctx, http.MethodGet, "/api/session", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Origin", d.http.BaseURL.Scheme+"://"+d.http.BaseURL.Host)
	req.Header.Set("Referer", d.http.BaseURL.String()+"/thread/new")
	req.Header.Set("User-Agent", "Muse2API-Go/2.0")
	req.Header.Set("Cookie", cookieHeader(credentials.Cookies))
	resp, err := d.http.Client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &UpstreamError{Operation: "session", Retryable: true, Message: "网络请求失败，未改变账号会话状态"}
	}
	defer resp.Body.Close()
	defer io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrSessionExpired
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &UpstreamError{Operation: "session", Status: resp.StatusCode, Retryable: resp.StatusCode == 429 || resp.StatusCode >= 500, Message: "会话状态未确认"}
	}
	var body struct {
		Status  string `json:"status"`
		VMID    string `json:"vm_id"`
		VMState string `json:"vm_state"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return nil, &UpstreamError{Operation: "session", Message: "上游返回非 JSON，会话状态未确认"}
	}
	if body.Status != "assigned" {
		return nil, &UpstreamError{Operation: "session", Message: "上游未返回 assigned 会话"}
	}
	session := &Session{Valid: true, Status: body.Status, VMID: body.VMID, VMState: body.VMState, Cookies: clone(credentials.Cookies), CookieExpiry: cloneExpiry(credentials.CookieExpiry), CheckedAt: time.Now().Unix()}
	for _, c := range resp.Cookies() {
		if c.Value != "" {
			session.Cookies[c.Name] = c.Value
		}
		if !c.Expires.IsZero() {
			session.CookieExpiry[c.Name] = c.Expires.Unix()
		}
	}
	return session, nil
}

func (d *HTTPDriver) WakeVM(ctx context.Context, credentials AccountCredentials, session *Session) (*Session, error) {
	if session == nil || session.VMID == "" {
		return nil, &UpstreamError{Operation: "vm wake", Message: "缺少 VM ID"}
	}
	attempt := make([]byte, 16)
	if _, err := rand.Read(attempt); err != nil {
		return nil, err
	}
	payload, _ := json.Marshal(map[string]any{"vm_id": session.VMID, "retry_count": 0, "connect_attempt_id": hex.EncodeToString(attempt)})
	req, err := d.http.NewRequest(ctx, http.MethodPost, "/api/hatch/vm/wake", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", d.http.BaseURL.Scheme+"://"+d.http.BaseURL.Host)
	req.Header.Set("Referer", strings.TrimRight(d.http.BaseURL.String(), "/")+"/thread/new")
	req.Header.Set("User-Agent", "Muse2API-Go/2.0")
	req.Header.Set("Cookie", cookieHeader(credentials.Cookies))
	resp, err := d.http.Client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &UpstreamError{Operation: "vm wake", Retryable: true, Message: "VM 唤醒请求失败"}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, &UpstreamError{Operation: "vm wake", Status: resp.StatusCode, Retryable: resp.StatusCode == 429 || resp.StatusCode >= 500, Message: "VM 唤醒未确认"}
	}
	return d.CheckSession(ctx, credentials)
}

func cookieHeader(cookies map[string]string) string {
	keys := make([]string, 0, len(cookies))
	for k, v := range cookies {
		if v != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+cookies[k])
	}
	return strings.Join(parts, "; ")
}
func clone(in map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		out[k] = v
	}
	return out
}
func cloneExpiry(in map[string]int64) map[string]int64 {
	out := map[string]int64{}
	for k, v := range in {
		out[k] = v
	}
	return out
}
func IsCapability(err error) bool { return errors.Is(err, ErrCapabilityUnavailable) }
