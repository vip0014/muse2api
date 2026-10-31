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
	"time"

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
	MediaDir      string
	PublicBase    string
	ChatTimeout   time.Duration
	ImageTimeout  time.Duration
	VideoTimeout  time.Duration
	MaxMediaBytes int64
	HTTPClient    *http.Client
}

// Driver implements protocol.Driver over the Noise/protobuf gateway.
type Driver struct {
	cfg    DriverConfig
	store  AccountStore
	client *Client
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
	return &Driver{cfg: cfg, store: store, client: NewClient(cfg.SiteURL, cfg.HTTPClient)}
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
	return protocol.AccountCredentials{ID: account.ID, Cookies: account.Cookies, CookieExpiry: account.CookieExpiry}, nil
}

func (d *Driver) persist(creds protocol.AccountCredentials, opErr error) {
	if creds.ID == "" {
		return
	}
	_, _ = d.store.UpdateAccount(creds.ID, func(a *model.Account) error {
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
		if opErr == nil {
			a.Status = model.AccountActive
			a.LastError = ""
		} else if errors.Is(opErr, protocol.ErrSessionExpired) {
			a.Status = model.AccountExpired
			a.LastError = "上游 Session 已失效，请重新认证"
		} else {
			a.Note = "最近执行未确认成功；保留会话状态"
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
		stream, streamErr := d.client.ChatStream(ctx, creds.Cookies, request.Prompt, d.cfg.ChatTimeout)
		if streamErr != nil {
			d.persist(creds, streamErr)
			select {
			case out <- protocol.ChatEvent{Err: streamErr}:
			case <-ctx.Done():
			}
			return
		}
		var finalErr error
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
				break
			}
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
	text, media, err := d.client.ChatWithItems(ctx, creds.Cookies, prompt, items, true, d.cfg.ImageTimeout)
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
	// Upstream accepts one starting frame for video turns.
	items, err := referenceItems(protocol.ReferenceList(request.ReferenceImage, request.ReferenceImages), 1)
	if err != nil {
		return nil, err
	}
	prompt := protocol.BuildVideoPrompt(request)
	text, media, err := d.client.ChatWithItems(ctx, creds.Cookies, prompt, items, true, d.cfg.VideoTimeout)
	d.persist(creds, err)
	if err != nil {
		return nil, err
	}
	result, err := d.pickMedia(ctx, creds, media, text, "video")
	if err != nil {
		return nil, err
	}
	return &protocol.VideoTask{
		ID: result.Filename, Status: "succeeded", Progress: 100, URL: result.URL,
		DurationMS: result.DurationMS, Width: result.Width, Height: result.Height,
	}, nil
}

func (d *Driver) pickMedia(ctx context.Context, creds protocol.AccountCredentials, media []MediaRef, text, expect string) (*protocol.ImageResult, error) {
	var lastErr error
	for _, ref := range media {
		if ref.Kind != expect {
			continue
		}
		data, mimeType, err := d.client.Download(ctx, creds.Cookies, ref)
		if err != nil {
			lastErr = err
			continue
		}
		result, err := d.saveMedia(data, mimeType, expect)
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
	opts := SessionOptions{BaseURL: d.cfg.SiteURL, Cookies: credentials.Cookies, Client: d.cfg.HTTPClient}
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
		CookieExpiry: credentials.CookieExpiry,
		CheckedAt:    time.Now().Unix(),
	}, nil
}

// QueryQuota reads the subscription/usage snapshot through the settings server
// action; no VM wake and no browser are involved.
func (d *Driver) QueryQuota(ctx context.Context, credentials protocol.AccountCredentials) (*model.Quota, error) {
	return d.client.QueryQuota(ctx, credentials)
}

func (d *Driver) Close() error {
	d.client.forgetConnection()
	return nil
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

func (d *Driver) saveMedia(data []byte, mimeType, expect string) (*protocol.ImageResult, error) {
	if len(data) == 0 || int64(len(data)) > d.cfg.MaxMediaBytes {
		return nil, errors.New("媒体内容为空或超过大小限制")
	}
	kind, detectedMIME, ext := detectMediaType(data)
	if kind == "" {
		return nil, errors.New("无法识别媒体类型，拒绝交付不可验证的文件")
	}
	if expect != "" && kind != expect {
		return nil, fmt.Errorf("期望生成 %s，但提取结果为 %s，拒绝交付错误类型", expect, kind)
	}
	if mimeType == "" {
		mimeType = detectedMIME
	}
	name, err := randomMediaName(ext)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(d.cfg.MediaDir, 0o700); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(d.cfg.MediaDir, ".download-*")
	if err != nil {
		return nil, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(tmpName, filepath.Join(d.cfg.MediaDir, name)); err != nil {
		return nil, err
	}
	base := strings.TrimRight(d.cfg.PublicBase, "/")
	result := &protocol.ImageResult{Filename: name, Kind: kind, URL: base + "/v1/media/" + name, Bytes: int64(len(data))}
	if kind == "video" {
		if durationMS, width, height, ok := probeMP4Metadata(data); ok {
			result.DurationMS, result.Width, result.Height = durationMS, width, height
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
