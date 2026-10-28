package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"muse2api/internal/muse/protocol"
)

type Config struct {
	Chromium      string
	SiteURL       string
	PublicBase    string
	ProfileDir    string
	DownloadDir   string
	MediaDir      string
	CDPPort       int
	LoginWait     time.Duration
	ChatTimeout   time.Duration
	ImageTimeout  time.Duration
	VideoTimeout  time.Duration
	MaxMediaBytes int64
}

type Engine struct {
	cfg      Config
	opMu     sync.Mutex
	lifeMu   sync.Mutex
	browser  *CDP
	cmd      *exec.Cmd
	procDone chan struct{}
	logFile  *os.File
	closed   bool
}

type Page struct {
	conn      *CDP
	contextID string
	targetID  string
	engine    *Engine
}

func NewEngine(cfg Config) *Engine {
	if cfg.LoginWait <= 0 {
		cfg.LoginWait = 60 * time.Second
	}
	if cfg.ChatTimeout <= 0 {
		cfg.ChatTimeout = 300 * time.Second
	}
	if cfg.ImageTimeout <= 0 {
		cfg.ImageTimeout = 240 * time.Second
	}
	if cfg.VideoTimeout <= 0 {
		cfg.VideoTimeout = 600 * time.Second
	}
	if cfg.MaxMediaBytes <= 0 {
		cfg.MaxMediaBytes = 512 << 20
	}
	return &Engine{cfg: cfg}
}

func chromiumEnv(home string) []string {
	out := make([]string, 0, len(os.Environ())+1)
	for _, item := range os.Environ() {
		name, _, ok := strings.Cut(item, "=")
		if !ok || name == "HOME" || sensitiveEnvName(name) {
			continue
		}
		out = append(out, item)
	}
	// Alpine's non-root account has no physical home; crashpad still requires
	// a writable HOME even though Chromium has an explicit user-data-dir.
	return append(out, "HOME="+home)
}

func sensitiveEnvName(name string) bool {
	upper := strings.ToUpper(name)
	for _, marker := range []string{"KEY", "SECRET", "PASSWORD", "PASSWD", "TOKEN", "CREDENTIAL", "PROXY"} {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}

func (e *Engine) debugURL() string {
	return "http://127.0.0.1:" + strconv.Itoa(e.cfg.CDPPort) + "/json/version"
}

func (e *Engine) connectExisting(ctx context.Context) (*CDP, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.debugURL(), nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("CDP 调试端点不可用")
	}
	var version struct {
		WebSocket string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&version); err != nil || version.WebSocket == "" {
		return nil, errors.New("CDP 版本响应无效")
	}
	return DialCDP(ctx, version.WebSocket, 180*time.Second)
}

func (e *Engine) start(ctx context.Context) error {
	e.lifeMu.Lock()
	defer e.lifeMu.Unlock()
	if e.closed {
		return errors.New("浏览器执行器已关闭")
	}
	if e.browser != nil {
		checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := e.browser.Call(checkCtx, "Browser.getVersion", map[string]any{}, nil)
		cancel()
		if err == nil {
			return nil
		}
		// Any failed liveness check means the old socket must never be reused.
		_ = e.browser.Close()
		e.browser = nil
	}
	if conn, err := e.connectExisting(ctx); err == nil {
		e.browser = conn
		return nil
	}
	e.stopStartedLocked()
	for _, dir := range []string{e.cfg.ProfileDir, e.cfg.DownloadDir, e.cfg.MediaDir, filepath.Dir(e.cfg.MediaDir)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return err
		}
	}
	for _, name := range []string{"SingletonLock", "SingletonSocket", "SingletonCookie"} {
		_ = os.Remove(filepath.Join(e.cfg.ProfileDir, name))
	}
	logPath := filepath.Join(filepath.Dir(e.cfg.MediaDir), "chromium.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	args := []string{
		"--headless=new", "--no-sandbox", "--disable-gpu", "--disable-dev-shm-usage",
		"--disable-background-networking", "--no-first-run", "--no-default-browser-check",
		"--autoplay-policy=no-user-gesture-required", "--window-size=1440,2400",
		"--remote-debugging-address=127.0.0.1", "--remote-debugging-port=" + strconv.Itoa(e.cfg.CDPPort),
		"--user-data-dir=" + e.cfg.ProfileDir, "about:blank",
	}
	cmd := exec.Command(e.cfg.Chromium, args...)
	cmd.Env = chromiumEnv(e.cfg.ProfileDir)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("%w: Chromium 启动失败: %v", ErrBrowserUnavailable, err)
	}
	e.cmd = cmd
	e.logFile = logFile
	e.procDone = make(chan struct{})
	procDone := e.procDone
	go func() {
		defer close(procDone)
		_ = cmd.Wait()
	}()
	for attempt := 0; attempt < 90; attempt++ {
		if err := ctx.Err(); err != nil {
			e.stopStartedLocked()
			return err
		}
		conn, err := e.connectExisting(ctx)
		if err == nil {
			e.browser = conn
			return nil
		}
		select {
		case <-ctx.Done():
			e.stopStartedLocked()
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	e.stopStartedLocked()
	return fmt.Errorf("%w: Chromium 已启动但 CDP 在 90 秒内未就绪", ErrBrowserUnavailable)
}

// stopStartedLocked terminates a Chromium process started by this engine and
// releases its log descriptor. It waits for the child to exit so a relaunch
// cannot race the old process for the CDP port. A failed start must not leave
// either behind.
func (e *Engine) stopStartedLocked() {
	if e.cmd != nil && e.cmd.Process != nil {
		_ = e.cmd.Process.Kill()
		if e.procDone != nil {
			select {
			case <-e.procDone:
			case <-time.After(5 * time.Second):
			}
		}
	}
	e.cmd = nil
	e.procDone = nil
	if e.logFile != nil {
		_ = e.logFile.Close()
		e.logFile = nil
	}
}

func (e *Engine) openPage(ctx context.Context) (*Page, error) {
	if err := e.start(ctx); err != nil {
		return nil, err
	}
	var contextResult struct {
		ID string `json:"browserContextId"`
	}
	if err := e.browser.Call(ctx, "Target.createBrowserContext", map[string]any{}, &contextResult); err != nil {
		return nil, err
	}
	if contextResult.ID == "" {
		return nil, errors.New("Chromium 未返回 BrowserContext")
	}
	page := &Page{contextID: contextResult.ID, engine: e}
	cleanup := true
	defer func() {
		if cleanup {
			page.close()
		}
	}()
	var targetResult struct {
		ID string `json:"targetId"`
	}
	if err := e.browser.Call(ctx, "Target.createTarget", map[string]any{"url": "about:blank", "browserContextId": contextResult.ID}, &targetResult); err != nil {
		return nil, err
	}
	page.targetID = targetResult.ID
	wsURL := "ws://127.0.0.1:" + strconv.Itoa(e.cfg.CDPPort) + "/devtools/page/" + targetResult.ID
	conn, err := DialCDP(ctx, wsURL, 180*time.Second)
	if err != nil {
		return nil, err
	}
	page.conn = conn
	for _, method := range []string{"Network.enable", "Page.enable", "Runtime.enable"} {
		if err := conn.Call(ctx, method, map[string]any{}, nil); err != nil {
			return nil, err
		}
	}
	_ = conn.Call(ctx, "Browser.setDownloadBehavior", map[string]any{"behavior": "allow", "downloadPath": e.cfg.DownloadDir}, nil)
	cleanup = false
	return page, nil
}

func (p *Page) close() {
	if p.conn != nil {
		_ = p.conn.Close()
		p.conn = nil
	}
	if p.engine != nil && p.engine.browser != nil && p.contextID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = p.engine.browser.Call(ctx, "Target.disposeBrowserContext", map[string]any{"browserContextId": p.contextID}, nil)
		cancel()
	}
}

func (e *Engine) threadURL() string {
	u, err := url.Parse(e.cfg.SiteURL)
	if err != nil {
		return strings.TrimRight(e.cfg.SiteURL, "/") + "/thread/new"
	}
	u.Path = "/thread/new"
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func (p *Page) applyCookies(ctx context.Context, credentials protocol.AccountCredentials) error {
	_ = p.conn.Call(ctx, "Network.clearBrowserCookies", map[string]any{}, nil)
	u, err := url.Parse(p.engine.cfg.SiteURL)
	if err != nil {
		return err
	}
	now := time.Now()
	for name, value := range credentials.Cookies {
		if value == "" {
			continue
		}
		expires := now.Add(7 * 24 * time.Hour).Unix()
		if exp := credentials.CookieExpiry[name]; exp > now.Add(time.Hour).Unix() {
			expires = exp
		}
		essential := name == "hatch_sess" || name == "hatch_vml"
		params := map[string]any{"name": name, "value": value, "path": "/", "secure": u.Scheme == "https", "expires": expires}
		hostname := strings.TrimPrefix(u.Hostname(), ".")
		if strings.HasPrefix(name, "__Host-") || hostname == "localhost" || net.ParseIP(hostname) != nil {
			params["url"] = u.Scheme + "://" + u.Host
		} else {
			params["domain"] = "." + hostname
		}
		var result struct {
			Success bool `json:"success"`
		}
		if err := p.conn.Call(ctx, "Network.setCookie", params, &result); err != nil {
			if essential {
				return fmt.Errorf("注入账号 Cookie %s 失败: %w", name, err)
			}
			continue
		}
		if !result.Success {
			if essential {
				return fmt.Errorf("Chromium 拒绝核心账号 Cookie %s", name)
			}
			continue
		}
	}
	return nil
}

func (p *Page) waitReady(ctx context.Context) error {
	deadline := time.Now().Add(p.engine.cfg.LoginWait)
	for time.Now().Before(deadline) {
		var state string
		err := p.conn.JS(ctx, `(function(){
			if (document.readyState !== 'complete') return 'loading';
			var ta = document.querySelector('textarea');
			if (!ta) return 'no-ta';
			var h = document.querySelector('[data-hatch-shell-hydration-state]');
			if (h && h.getAttribute('data-hatch-shell-hydration-state') !== 'hydrated') return 'hydrating';
			var b = document.body ? (document.body.innerText || '') : '';
			if (b.indexOf('Connecting...') !== -1) return 'connecting';
			if (ta.disabled) return 'input-disabled';
			return 'ready';
		})()`, false, &state)
		if err == nil && state == "ready" {
			return nil
		}
		if err != nil && IsConnectionError(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	var body string
	readCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_ = p.conn.JS(readCtx, `document.body ? document.body.innerText.slice(0,1200) : ''`, false, &body)
	cancel()
	lower := strings.ToLower(body)
	if strings.Contains(lower, "log in") || strings.Contains(lower, "sign in") || strings.Contains(body, "登录") || strings.Contains(lower, "use another account") {
		return fmt.Errorf("网页会话已登出: %w", protocol.ErrSessionExpired)
	}
	if strings.Contains(lower, "connecting...") {
		return errors.New("页面网关连接超时 (Connecting...)")
	}
	return errors.New("muse.ai 页面加载超时，未出现可用聊天输入框")
}

func (p *Page) readCredentials(ctx context.Context, current protocol.AccountCredentials) protocol.AccountCredentials {
	out := cloneCredentials(current)
	var result struct {
		Cookies []struct {
			Name    string  `json:"name"`
			Value   string  `json:"value"`
			Expires float64 `json:"expires"`
		} `json:"cookies"`
	}
	if err := p.conn.Call(ctx, "Network.getCookies", map[string]any{"urls": []string{p.engine.cfg.SiteURL}}, &result); err != nil {
		return out
	}
	if out.Cookies == nil {
		out.Cookies = map[string]string{}
	}
	if out.CookieExpiry == nil {
		out.CookieExpiry = map[string]int64{}
	}
	for _, cookie := range result.Cookies {
		if cookie.Name == "" || cookie.Value == "" {
			continue
		}
		out.Cookies[cookie.Name] = cookie.Value
		if cookie.Expires > 0 {
			out.CookieExpiry[cookie.Name] = int64(cookie.Expires)
		}
	}
	return out
}

func cloneCredentials(in protocol.AccountCredentials) protocol.AccountCredentials {
	out := protocol.AccountCredentials{ID: in.ID, Cookies: map[string]string{}, CookieExpiry: map[string]int64{}}
	for k, v := range in.Cookies {
		out.Cookies[k] = v
	}
	for k, v := range in.CookieExpiry {
		out.CookieExpiry[k] = v
	}
	return out
}

func (e *Engine) authenticatedPage(ctx context.Context, credentials protocol.AccountCredentials) (*Page, error) {
	return e.authenticatedPageWith(ctx, credentials, func(page *Page) error {
		return page.waitReady(ctx)
	})
}

// authenticatedPageForQuota only waits for the account menu. It does not wait
// for a usable chat textarea, which is unnecessary for reading the quota panel
// and can be much slower while the upstream VM is still warming up.
func (e *Engine) authenticatedPageForQuota(ctx context.Context, credentials protocol.AccountCredentials) (*Page, error) {
	return e.authenticatedPageWith(ctx, credentials, func(page *Page) error {
		return page.waitQuotaReady(ctx)
	})
}

func (e *Engine) authenticatedPageWith(ctx context.Context, credentials protocol.AccountCredentials, ready func(*Page) error) (*Page, error) {
	page, err := e.openPage(ctx)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			page.close()
		}
	}()
	if err := page.applyCookies(ctx, credentials); err != nil {
		return nil, err
	}
	if err := page.conn.Call(ctx, "Page.navigate", map[string]any{"url": e.threadURL()}, nil); err != nil {
		return nil, err
	}
	if err := ready(page); err != nil {
		return nil, err
	}
	ok = true
	return page, nil
}

func (e *Engine) BrowserRunning() bool {
	e.lifeMu.Lock()
	defer e.lifeMu.Unlock()
	if e.browser == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return e.browser.Call(ctx, "Browser.getVersion", map[string]any{}, nil) == nil
}

func (e *Engine) Close() error {
	e.opMu.Lock()
	defer e.opMu.Unlock()
	e.lifeMu.Lock()
	defer e.lifeMu.Unlock()
	e.closed = true
	if e.browser != nil {
		if e.cmd != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = e.browser.Call(ctx, "Browser.close", map[string]any{}, nil)
			cancel()
		}
		_ = e.browser.Close()
		e.browser = nil
	}
	e.stopStartedLocked()
	return nil
}
