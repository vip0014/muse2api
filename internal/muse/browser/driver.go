package browser

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	appconfig "muse2api/internal/config"
	"muse2api/internal/model"
	"muse2api/internal/muse/capabilities"
	"muse2api/internal/muse/protocol"
	"muse2api/internal/muse/transport"
)

type AccountStore interface {
	GetAccount(string) (model.Account, error)
	UpdateAccount(string, func(*model.Account) error) (model.Account, error)
}

type Driver struct {
	engine  *Engine
	http    *protocol.HTTPDriver
	store   AccountStore
	renewMu sync.Mutex
	renewed map[string]time.Time
}

func NewDriver(cfg appconfig.Config, store AccountStore, httpTransport *transport.HTTP) *Driver {
	engine := NewEngine(Config{
		Chromium: cfg.Chromium, SiteURL: cfg.SiteURL, PublicBase: cfg.PublicBase,
		ProfileDir: cfg.ProfileDir, DownloadDir: cfg.DownloadDir, MediaDir: cfg.MediaDir(),
		CDPPort: cfg.CDPPort, LoginWait: cfg.LoginWait, ChatTimeout: cfg.ChatTimeout,
		ImageTimeout: cfg.ImageTimeout, VideoTimeout: cfg.VideoTimeout, MaxMediaBytes: cfg.MaxMediaBytes,
	})
	return &Driver{engine: engine, http: protocol.NewHTTPDriver(httpTransport), store: store, renewed: map[string]time.Time{}}
}

func (d *Driver) Capabilities(context.Context) []capabilities.Capability {
	return capabilities.Browser()
}

func (d *Driver) BrowserRunning() bool {
	return d.engine.BrowserRunning()
}

func (d *Driver) prepare(ctx context.Context, accountID string) (protocol.AccountCredentials, error) {
	if accountID == "" {
		return protocol.AccountCredentials{}, errors.New("没有指定上游账号")
	}
	account, err := d.store.GetAccount(accountID)
	if err != nil {
		return protocol.AccountCredentials{}, errors.New("上游账号不存在")
	}
	credentials := protocol.AccountCredentials{ID: account.ID, Cookies: account.Cookies, CookieExpiry: account.CookieExpiry}
	if len(credentials.Cookies) == 0 {
		return credentials, errors.New("账号没有 Cookie")
	}
	d.renewMu.Lock()
	last := d.renewed[accountID]
	d.renewMu.Unlock()
	recent := account.SyncedAt > 0 && time.Since(time.Unix(account.SyncedAt, 0)) < 10*time.Minute
	if time.Since(last) < 10*time.Minute || (recent && account.OK != nil && *account.OK) {
		return credentials, nil
	}
	session, err := d.http.CheckSession(ctx, credentials)
	if err != nil {
		if errors.Is(err, protocol.ErrSessionExpired) {
			d.persist(accountID, credentials, err)
			return credentials, err
		}
		// A network failure does not prove the cookie invalid; let the page attempt it.
		return credentials, nil
	}
	credentials.Cookies = session.Cookies
	credentials.CookieExpiry = session.CookieExpiry
	if session.VMID != "" && session.VMState != "DISABLED" {
		if awakened, wakeErr := d.http.WakeVM(ctx, credentials, session); wakeErr == nil {
			credentials.Cookies = awakened.Cookies
			credentials.CookieExpiry = awakened.CookieExpiry
		}
	}
	d.renewMu.Lock()
	d.renewed[accountID] = time.Now()
	d.renewMu.Unlock()
	d.persistRenewal(accountID, credentials)
	return credentials, nil
}

func (d *Driver) persistRenewal(accountID string, credentials protocol.AccountCredentials) {
	_, _ = d.store.UpdateAccount(accountID, func(account *model.Account) error {
		account.Cookies = credentials.Cookies
		account.CookieExpiry = credentials.CookieExpiry
		account.SyncedAt = time.Now().Unix()
		account.LastKeepalive = time.Now().Unix()
		account.Status = model.AccountActive
		value := true
		account.OK = &value
		account.LastError = ""
		account.Note = "HTTP Session 有效"
		return nil
	})
}

func (d *Driver) persist(accountID string, credentials protocol.AccountCredentials, operationErr error) {
	if accountID == "" {
		return
	}
	_, _ = d.store.UpdateAccount(accountID, func(account *model.Account) error {
		account.Cookies = credentials.Cookies
		account.CookieExpiry = credentials.CookieExpiry
		account.LastChecked = time.Now().Unix()
		if operationErr == nil {
			account.Status = model.AccountActive
			value := true
			account.OK = &value
			account.LastError = ""
			account.Note = "Go Chromium/CDP 执行成功"
			account.SyncedAt = time.Now().Unix()
			account.LastKeepalive = time.Now().Unix()
			account.LastUsed = time.Now().Unix()
			account.UseCount++
		} else if errors.Is(operationErr, protocol.ErrSessionExpired) {
			account.Status = model.AccountExpired
			value := false
			account.OK = &value
			account.LastError = "上游 Session 已失效，请重新认证"
		} else {
			account.Note = "最近执行未确认成功；保留会话状态"
		}
		return nil
	})
}

func (d *Driver) Chat(ctx context.Context, request protocol.ChatRequest) (<-chan protocol.ChatEvent, error) {
	out := make(chan protocol.ChatEvent, 32)
	go func() {
		defer close(out)
		defer func() {
			if recovered := recover(); recovered != nil {
				select {
				case out <- protocol.ChatEvent{Err: fmt.Errorf("chat execution panic: %v", recovered)}:
				case <-ctx.Done():
				}
			}
		}()
		credentials, err := d.prepare(ctx, request.AccountID)
		if err == nil {
			var updated protocol.AccountCredentials
			updated, err = d.engine.Chat(ctx, credentials, request.Prompt, func(delta string) error {
				select {
				case out <- protocol.ChatEvent{Delta: delta}:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
			d.persist(request.AccountID, updated, err)
		}
		if err != nil {
			select {
			case out <- protocol.ChatEvent{Err: err}:
			case <-ctx.Done():
			}
			return
		}
		select {
		case out <- protocol.ChatEvent{Done: true}:
		case <-ctx.Done():
		}
	}()
	return out, nil
}

func (d *Driver) GenerateImage(ctx context.Context, request protocol.ImageRequest) (*protocol.ImageResult, error) {
	credentials, err := d.prepare(ctx, request.AccountID)
	if err != nil {
		return nil, err
	}
	prompt := buildImagePrompt(request)
	result, updated, err := d.engine.Generate(ctx, credentials, prompt, "image", request.ReferenceImage)
	d.persist(request.AccountID, updated, err)
	return result, err
}

func (d *Driver) EditImage(ctx context.Context, request protocol.ImageRequest) (*protocol.ImageResult, error) {
	if strings.TrimSpace(request.ReferenceImage) == "" {
		return nil, errors.New("图像编辑需要参考图片")
	}
	return d.GenerateImage(ctx, request)
}

func (d *Driver) CreateVideo(ctx context.Context, request protocol.VideoRequest) (*protocol.VideoTask, error) {
	credentials, err := d.prepare(ctx, request.AccountID)
	if err != nil {
		return nil, err
	}
	prompt := buildVideoPrompt(request)
	result, updated, err := d.engine.Generate(ctx, credentials, prompt, "video", request.ReferenceImage)
	d.persist(request.AccountID, updated, err)
	if err != nil {
		return nil, err
	}
	return &protocol.VideoTask{ID: result.Filename, Status: "succeeded", Progress: 100, URL: result.URL}, nil
}

func (d *Driver) GetVideoTask(context.Context, string) (*protocol.VideoTask, error) {
	return nil, protocol.Unavailable("上游视频任务查询由本地任务存储负责")
}

func (d *Driver) CheckSession(ctx context.Context, credentials protocol.AccountCredentials) (*protocol.Session, error) {
	return d.http.CheckSession(ctx, credentials)
}

func (d *Driver) QueryQuota(ctx context.Context, credentials protocol.AccountCredentials) (*model.Quota, error) {
	prepared, err := d.prepare(ctx, credentials.ID)
	if err != nil {
		return nil, err
	}
	quota, updated, err := d.engine.Quota(ctx, prepared)
	d.persist(credentials.ID, updated, err)
	if err != nil {
		return nil, err
	}
	return &quota, nil
}

func (d *Driver) Close() error {
	err := d.engine.Close()
	d.http.Close()
	return err
}
