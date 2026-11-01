package hatch

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"muse2api/internal/model"
	"muse2api/internal/muse/capabilities"
	"muse2api/internal/muse/protocol"
	"muse2api/internal/refimage"
)

// AccountStore is the minimal storage surface the driver needs.
type AccountStore interface {
	GetAccount(string) (model.Account, error)
	UpdateAccount(string, func(*model.Account) error) (model.Account, error)
}

// DriverConfig configures the pure-protocol driver.
type DriverConfig struct {
	SiteURL       *url.URL
	WSURL         string
	MediaDir      string
	PublicBase    string
	ChatTimeout   time.Duration
	ImageTimeout  time.Duration
	VideoTimeout  time.Duration
	MaxMediaBytes int64
	HTTPClient    *http.Client
	WSDialer      *websocket.Dialer
}

// Driver implements protocol.Driver over the Noise/protobuf gateway.
type Driver struct {
	cfg   DriverConfig
	store AccountStore

	// One Client per account keeps each account's VM connection (and its
	// serialized conversation) isolated. The scheduler already caps
	// per-account concurrency, so a shared client would silently run one
	// account's turn on another account's connection.
	clientsMu sync.Mutex
	clients   map[string]*Client
}

func NewDriver(cfg DriverConfig, store AccountStore) *Driver {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 25 * time.Second}
	}
	if cfg.ChatTimeout <= 0 {
		cfg.ChatTimeout = 5 * time.Minute
	}
	if cfg.ImageTimeout <= 0 {
		cfg.ImageTimeout = 4 * time.Minute
	}
	if cfg.VideoTimeout <= 0 {
		cfg.VideoTimeout = 10 * time.Minute
	}
	if cfg.MaxMediaBytes <= 0 {
		cfg.MaxMediaBytes = 512 << 20
	}
	return &Driver{cfg: cfg, store: store, clients: map[string]*Client{}}
}

// clientFor returns the per-account protocol client, creating it on first use.
func (d *Driver) clientFor(accountID string) *Client {
	d.clientsMu.Lock()
	defer d.clientsMu.Unlock()
	if client := d.clients[accountID]; client != nil {
		return client
	}
	client := NewClientWithWSURL(d.cfg.SiteURL, d.cfg.HTTPClient, d.cfg.WSURL)
	client.SetWSDialer(d.cfg.WSDialer)
	d.clients[accountID] = client
	return client
}

// forgetAccount drops one account's client and its live gateway connection,
// e.g. after the operator replaces the cookies.
func (d *Driver) forgetAccount(accountID string) {
	d.clientsMu.Lock()
	client := d.clients[accountID]
	delete(d.clients, accountID)
	d.clientsMu.Unlock()
	if client != nil {
		client.invalidate()
	}
}

func (d *Driver) Capabilities(context.Context) []capabilities.Capability {
	return capabilities.Protocol()
}

func (d *Driver) credentials(accountID string) (protocol.AccountCredentials, error) {
	if accountID == "" {
		return protocol.AccountCredentials{}, errors.New("没有指定上游账号")
	}
	account, err := d.store.GetAccount(accountID)
	if err != nil {
		return protocol.AccountCredentials{}, errors.New("上游账号不存在")
	}
	if len(account.Cookies) == 0 {
		return protocol.AccountCredentials{}, errors.New("账号没有 Cookie")
	}
	return protocol.AccountCredentials{ID: account.ID, Cookies: account.Cookies, CookieExpiry: account.CookieExpiry, Version: account.CredentialsVersion}, nil
}

func (d *Driver) persist(creds protocol.AccountCredentials, opErr error) {
	if creds.ID == "" {
		return
	}
	_, _ = d.store.UpdateAccount(creds.ID, func(a *model.Account) error {
		if a.CredentialsVersion != creds.Version {
			return errors.New("账号凭据已被其他操作更新，丢弃过期快照")
		}
		cookies := make(map[string]string, len(creds.Cookies))
		for key, value := range creds.Cookies {
			cookies[key] = value
		}
		a.Cookies = cookies
		if creds.CookieExpiry != nil {
			expiry := make(map[string]int64, len(creds.CookieExpiry))
			for key, value := range creds.CookieExpiry {
				expiry[key] = value
			}
			a.CookieExpiry = expiry
		}
		a.LastUsed = time.Now().Unix()
		a.CredentialsVersion++
		switch {
		case opErr == nil:
			a.Status = model.AccountActive
			a.LastError = ""
			a.FailureCount = 0
			a.CooldownUntil = 0
			a.UseCount++
		case errors.Is(opErr, protocol.ErrSessionExpired):
			a.Status = model.AccountExpired
			a.LastError = "上游 Session 已失效，请重新认证"
			a.FailureCount = 0
			a.CooldownUntil = 0
		case errors.Is(opErr, protocol.ErrModelRefusal), errors.Is(opErr, context.Canceled):
			a.Note = "最近执行未确认成功；保留会话状态"
		default:
			a.FailureCount++
			cooldown := 30 * time.Second
			for i := 1; i < a.FailureCount && cooldown < time.Hour; i++ {
				cooldown *= 2
			}
			if cooldown > time.Hour {
				cooldown = time.Hour
			}
			a.Status = model.AccountCooldown
			a.CooldownUntil = time.Now().Add(cooldown).Unix()
			a.LastError = truncate([]byte(opErr.Error()), 300)
		}
		return nil
	})
}

func (d *Driver) Chat(ctx context.Context, request protocol.ChatRequest) (<-chan protocol.ChatEvent, error) {
	creds, err := d.credentials(request.AccountID)
	if err != nil {
		return nil, err
	}
	out := make(chan protocol.ChatEvent, 32)
	go func() {
		defer close(out)
		stream, streamErr := d.clientFor(request.AccountID).ChatStream(ctx, creds.Cookies, request.Prompt, d.cfg.ChatTimeout)
		if streamErr != nil {
			d.persist(creds, streamErr)
			select {
			case out <- protocol.ChatEvent{Err: streamErr}:
			case <-ctx.Done():
			}
			return
		}
		var finalErr error
		doneSeen := false
		for delta := range stream {
			if delta.Err != nil {
				finalErr = delta.Err
				break
			}
			if delta.Text != "" {
				select {
				case out <- protocol.ChatEvent{Delta: delta.Text}:
				case <-ctx.Done():
					finalErr = ctx.Err()
				}
				if finalErr != nil {
					break
				}
			}
			if delta.Done {
				doneSeen = true
				break
			}
		}
		if finalErr == nil && !doneSeen {
			finalErr = fmt.Errorf("上游对话流在完成前结束: %w", protocol.ErrUpstreamUncertain)
		}
		if finalErr != nil {
			d.persist(creds, finalErr)
			select {
			case out <- protocol.ChatEvent{Err: finalErr}:
			case <-ctx.Done():
			}
			return
		}
		d.persist(creds, nil)
		select {
		case out <- protocol.ChatEvent{Done: true}:
		case <-ctx.Done():
		}
	}()
	return out, nil
}

func (d *Driver) GenerateImage(ctx context.Context, request protocol.ImageRequest) (*protocol.ImageResult, error) {
	creds, err := d.credentials(request.AccountID)
	if err != nil {
		return nil, err
	}
	items, err := referenceItems(protocol.ReferenceList(request.ReferenceImage, request.ReferenceImages), refimage.MaxImages)
	if err != nil {
		return nil, err
	}
	prompt := protocol.BuildImagePrompt(request)
	text, media, err := d.clientFor(creds.ID).ChatWithItems(ctx, creds.Cookies, prompt, items, true, d.cfg.ImageTimeout)
	d.persist(creds, err)
	if err != nil {
		return nil, err
	}
	result, err := d.pickMedia(ctx, creds, media, text, "image")
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (d *Driver) EditImage(ctx context.Context, request protocol.ImageRequest) (*protocol.ImageResult, error) {
	if len(protocol.ReferenceList(request.ReferenceImage, request.ReferenceImages)) == 0 {
		return nil, errors.New("图像编辑需要参考图片")
	}
	return d.GenerateImage(ctx, request)
}

func (d *Driver) CreateVideo(ctx context.Context, request protocol.VideoRequest) (*protocol.VideoTask, error) {
	creds, err := d.credentials(request.AccountID)
	if err != nil {
		return nil, err
	}
	// Keep the attachment set and prompt description consistent. The previous
	// implementation silently truncated the list to one item while the prompt
	// still said "N ordered reference images".
	items, err := referenceItems(protocol.ReferenceList(request.ReferenceImage, request.ReferenceImages), refimage.MaxImages)
	if err != nil {
		return nil, err
	}
	prompt := protocol.BuildVideoPrompt(request)
	text, media, err := d.clientFor(creds.ID).ChatWithItems(ctx, creds.Cookies, prompt, items, true, d.cfg.VideoTimeout)
	d.persist(creds, err)
	if err != nil {
		return nil, err
	}
	result, err := d.pickMedia(ctx, creds, media, text, "video")
	if err != nil {
		return nil, err
	}
	return &protocol.VideoTask{
		ID: result.Filename, Filename: result.Filename, Status: "succeeded", Progress: 100, URL: result.URL,
		DurationMS: result.DurationMS, Width: result.Width, Height: result.Height,
	}, nil
}

func (d *Driver) pickMedia(ctx context.Context, creds protocol.AccountCredentials, media []MediaRef, text, expect string) (*protocol.ImageResult, error) {
	var lastErr error
	for _, ref := range media {
		if ref.Kind != expect {
			continue
		}
		path, mimeType, err := d.clientFor(creds.ID).DownloadToFile(ctx, creds.Cookies, ref, d.cfg.MediaDir, d.cfg.MaxMediaBytes)
		if err != nil {
			lastErr = err
			continue
		}
		result, err := d.saveMediaFile(path, mimeType, expect)
		if err != nil {
			lastErr = err
			continue
		}
		return result, nil
	}
	kind := "图片"
	if expect == "video" {
		kind = "视频"
	}
	reason := truncate([]byte(text), 200)
	if lastErr != nil {
		return nil, fmt.Errorf("上游未产出可用%s（%v）；回复：%s", kind, lastErr, reason)
	}
	return nil, fmt.Errorf("上游未产出可用%s；回复：%s", kind, reason)
}

func (d *Driver) GetVideoTask(context.Context, string) (*protocol.VideoTask, error) {
	return nil, protocol.Unavailable("上游视频任务查询由本地任务存储负责")
}

func (d *Driver) CheckSession(ctx context.Context, credentials protocol.AccountCredentials) (*protocol.Session, error) {
	opts := SessionOptions{
		BaseURL:      d.cfg.SiteURL,
		Cookies:      credentials.Cookies,
		CookieExpiry: credentials.CookieExpiry,
		Client:       d.cfg.HTTPClient,
	}
	info, err := opts.requestJSON(ctx, http.MethodGet, "/api/session", nil)
	if err != nil {
		return nil, err
	}
	return &protocol.Session{
		Valid:        true,
		Status:       asString(info["status"]),
		VMID:         asString(info["vm_id"]),
		VMState:      asString(info["vm_state"]),
		Cookies:      opts.Cookies,
		CookieExpiry: opts.CookieExpiry,
		CheckedAt:    time.Now().Unix(),
	}, nil
}

// QueryQuota reads the subscription/usage snapshot through the settings server
// action; no VM wake and no browser are involved.
func (d *Driver) QueryQuota(ctx context.Context, credentials protocol.AccountCredentials) (*model.Quota, error) {
	return d.clientFor(credentials.ID).QueryQuota(ctx, credentials)
}

func (d *Driver) Close() error {
	d.clientsMu.Lock()
	clients := make([]*Client, 0, len(d.clients))
	for _, client := range d.clients {
		clients = append(clients, client)
	}
	d.clients = map[string]*Client{}
	d.clientsMu.Unlock()
	for _, client := range clients {
		client.invalidate()
	}
	return nil
}

// InvalidateAccount drops the cached gateway connection for one account. The
// next task re-bootstraps with the current cookies.
func (d *Driver) InvalidateAccount(accountID string) {
	d.forgetAccount(accountID)
}

// referenceItems converts canonical data URLs into chat.stream items[] image
// entries, matching the web client's attachment shape.
func referenceItems(refs []string, limit int) ([]ChatItem, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	if limit > 0 && len(refs) > limit {
		refs = refs[:limit]
	}
	out := make([]ChatItem, 0, len(refs))
	for index, raw := range refs {
		data, mimeType, err := refimage.DecodeDataURL(raw)
		if err != nil {
			return nil, fmt.Errorf("第 %d 张参考图：%w", index+1, err)
		}
		out = append(out, ChatItem{
			Type:       "image",
			MimeType:   mimeType,
			DataBase64: base64.StdEncoding.EncodeToString(data),
			Filename:   "reference" + referenceExtension(mimeType),
		})
	}
	return out, nil
}

func referenceExtension(mimeType string) string {
	switch strings.ToLower(strings.SplitN(mimeType, ";", 2)[0]) {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ".png"
	}
}

func (d *Driver) saveMediaFile(path, mimeType, expect string) (*protocol.ImageResult, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("媒体临时文件不存在")
	}
	if info.Size() <= 0 || info.Size() > d.cfg.MaxMediaBytes {
		_ = os.Remove(path)
		return nil, errors.New("媒体内容为空或超过大小限制")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	head := make([]byte, 512)
	n, _ := file.Read(head)
	_ = file.Close()
	kind, detectedMIME, ext := detectMediaType(head[:n])
	if kind == "" {
		_ = os.Remove(path)
		return nil, errors.New("无法识别媒体类型，拒绝交付不可验证的文件")
	}
	if expect != "" && kind != expect {
		_ = os.Remove(path)
		return nil, fmt.Errorf("期望生成 %s，但提取结果为 %s，拒绝交付错误类型", expect, kind)
	}
	if mimeType == "" {
		mimeType = detectedMIME
	}
	name, err := randomMediaName(ext)
	if err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	if err := os.MkdirAll(d.cfg.MediaDir, 0o700); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	final := filepath.Join(d.cfg.MediaDir, name)
	if err := os.Rename(path, final); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	base := strings.TrimRight(d.cfg.PublicBase, "/")
	result := &protocol.ImageResult{Filename: name, Kind: kind, URL: base + "/v1/media/" + name, Bytes: info.Size()}
	if kind == "video" && info.Size() <= 128<<20 {
		if data, readErr := os.ReadFile(final); readErr == nil {
			if durationMS, width, height, ok := probeMP4Metadata(data); ok {
				result.DurationMS, result.Width, result.Height = durationMS, width, height
			}
		}
	}
	return result, nil
}

func randomMediaName(ext string) (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf) + ext, nil
}

func detectMediaType(data []byte) (kind, mimeType, ext string) {
	switch {
	case len(data) >= 8 && bytes.Equal(data[:8], []byte("\x89PNG\r\n\x1a\n")):
		return "image", "image/png", ".png"
	case len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff:
		return "image", "image/jpeg", ".jpg"
	case len(data) >= 6 && (bytes.Equal(data[:6], []byte("GIF87a")) || bytes.Equal(data[:6], []byte("GIF89a"))):
		return "image", "image/gif", ".gif"
	case len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return "image", "image/webp", ".webp"
	case len(data) >= 12 && bytes.Equal(data[4:8], []byte("ftyp")):
		brand := string(data[8:12])
		if brand == "avif" || brand == "avis" {
			return "image", "image/avif", ".avif"
		}
		return "video", "video/mp4", ".mp4"
	case len(data) >= 4 && bytes.Equal(data[:4], []byte{0x1a, 0x45, 0xdf, 0xa3}):
		return "video", "video/webm", ".webm"
	}
	return "", "", ""
}
