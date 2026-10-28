package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"

	"muse2api/internal/apierr"
	"muse2api/internal/auth"
	"muse2api/internal/config"
	"muse2api/internal/model"
	"muse2api/internal/muse/protocol"
	"muse2api/internal/storage"
)

type overviewTaskStats struct {
	RequestsToday  int `json:"requests_today"`
	ImageTotal     int `json:"image_total"`
	ImageSucceeded int `json:"image_succeeded"`
	VideoTotal     int `json:"video_total"`
	VideoSucceeded int `json:"video_succeeded"`
	Running        int `json:"running"`
	Queued         int `json:"queued"`
}

func (s *Server) adminStatus(w http.ResponseWriter, r *http.Request) {
	// Counters read a projection and the recent list is limited at the store
	// boundary, so a large task history is not cloned or JSON-serialized on
	// every dashboard poll.
	today := time.Now().Unix() - 86400
	counts := s.store.TaskCounts(today)
	stats := overviewTaskStats{
		RequestsToday: counts.RequestsToday, Running: counts.Running, Queued: counts.Queued,
		ImageTotal: counts.ImageTotal, ImageSucceeded: counts.ImageSucceeded,
		VideoTotal: counts.VideoTotal, VideoSucceeded: counts.VideoSucceeded,
	}
	rawTasks := s.store.ListTasks(20)
	recent := make([]model.Task, 0, len(rawTasks))
	for _, task := range rawTasks {
		recent = append(recent, legacyTask(task))
	}
	writeJSON(w, 200, map[string]any{"status": "ok", "version": "2.1.0", "accounts": s.store.ListAccounts(), "account_stats": s.store.AccountStats(), "stats": stats, "tasks": recent, "task_stats": stats, "media_count": s.mediaCount(), "events": s.store.Events(10), "browser_running": s.browserRunning(), "browser_enabled": s.cfg.BrowserEnabled, "base_url": s.cfg.PublicBase + "/v1", "config": publicConfig(s.cfg)})
}
func (s *Server) listAccounts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"accounts": s.store.ListAccounts(), "stats": s.store.AccountStats()})
}

type accountInput struct {
	Label        string            `json:"label"`
	Cookies      map[string]string `json:"cookies"`
	CookieHeader string            `json:"cookie_header"`
	Batch        string            `json:"batch"`
	Expires      map[string]int64  `json:"expires"`
}

func (s *Server) addAccounts(w http.ResponseWriter, r *http.Request) {
	var in accountInput
	if err := decodeJSON(w, r, &in); err != nil {
		apierr.Write(w, r, err)
		return
	}
	type entry struct {
		label   string
		cookies map[string]string
		expires map[string]int64
	}
	entries := []entry{}
	if in.Batch != "" {
		for _, line := range strings.Split(in.Batch, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			label, body := "", line
			if head, tail, ok := strings.Cut(line, "|"); ok && !strings.Contains(head, "=") {
				label, body = strings.TrimSpace(head), tail
			}
			if cookies := parseCookies(body); len(cookies) > 0 {
				entries = append(entries, entry{label: label, cookies: cookies})
			}
		}
	}
	cookies := parseCookies(in.CookieHeader)
	for k, v := range in.Cookies {
		cookies[k] = v
	}
	if len(cookies) > 0 {
		entries = append(entries, entry{in.Label, cookies, in.Expires})
	}
	if len(entries) == 0 {
		apierr.Write(w, r, apierr.BadRequest("未解析到任何 Cookie"))
		return
	}
	added := []model.Account{}
	missing := map[string]bool{}
	for _, e := range entries {
		a, err := s.store.AddAccount(e.label, e.cookies, e.expires)
		if err != nil {
			apierr.Write(w, r, err)
			return
		}
		added = append(added, a)
		for _, name := range []string{"hatch_sess", "hatch_vml"} {
			if e.cookies[name] == "" {
				missing[name] = true
			}
		}
	}
	for _, a := range added {
		s.enqueueAutoCheck(a.ID)
	}
	s.pool.Wake()
	miss := []string{}
	for k := range missing {
		miss = append(miss, k)
	}
	sort.Strings(miss)
	writeJSON(w, 201, map[string]any{"added": added, "count": len(added), "essential_missing": miss, "auto_check": len(added) > 0, "warning": map[bool]string{true: "部分必要 Cookie 缺失，该账号可能无法使用", false: ""}[len(miss) > 0]})
}
func parseCookies(text string) map[string]string {
	out := map[string]string{}
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "{") {
		_ = json.Unmarshal([]byte(text), &out)
		return out
	}
	for _, part := range strings.FieldsFunc(text, func(r rune) bool { return r == ';' || r == '\n' }) {
		if k, v, ok := strings.Cut(strings.TrimSpace(part), "="); ok && strings.TrimSpace(k) != "" {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}
func (s *Server) patchAccount(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Label          *string `json:"label"`
		Enabled        *bool   `json:"enabled"`
		MaxConcurrency *int    `json:"max_concurrency"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		apierr.Write(w, r, err)
		return
	}
	a, err := s.store.UpdateAccount(r.PathValue("id"), func(a *model.Account) error {
		if in.Label != nil {
			if strings.TrimSpace(*in.Label) == "" {
				return errors.New("标签不能为空")
			}
			a.Label = strings.TrimSpace(*in.Label)
		}
		if in.Enabled != nil {
			a.Enabled = *in.Enabled
			if !a.Enabled {
				a.Status = model.AccountDisabled
			} else {
				a.Status = model.AccountUnknown
			}
		}
		if in.MaxConcurrency != nil {
			if *in.MaxConcurrency < 1 || *in.MaxConcurrency > 32 {
				return errors.New("并发上限须为 1..32")
			}
			a.MaxConcurrency = *in.MaxConcurrency
		}
		return nil
	})
	if err != nil {
		apierr.Write(w, r, err)
		return
	}
	s.pool.Wake()
	writeJSON(w, 200, a)
}
func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteAccount(r.PathValue("id")); err != nil {
		apierr.Write(w, r, apierr.NotFound("账号不存在"))
		return
	}
	s.pool.Wake()
	writeJSON(w, 200, map[string]any{"deleted": true, "id": r.PathValue("id")})
}
func (s *Server) bulkAccounts(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs    []string `json:"ids"`
		Action string   `json:"action"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		apierr.Write(w, r, err)
		return
	}
	if len(in.IDs) > 100 {
		apierr.Write(w, r, apierr.BadRequest("单次最多操作 100 个账号"))
		return
	}
	count := 0
	for _, id := range in.IDs {
		var err error
		switch in.Action {
		case "enable", "disable":
			enabled := in.Action == "enable"
			_, err = s.store.UpdateAccount(id, func(a *model.Account) error {
				a.Enabled = enabled
				if enabled {
					a.Status = model.AccountUnknown
				} else {
					a.Status = model.AccountDisabled
				}
				return nil
			})
		case "delete":
			err = s.store.DeleteAccount(id)
		default:
			apierr.Write(w, r, apierr.BadRequest("不支持的批量操作"))
			return
		}
		if err == nil {
			count++
		}
	}
	s.pool.Wake()
	writeJSON(w, 200, map[string]any{"updated": count})
}
func (s *Server) updateCookies(w http.ResponseWriter, r *http.Request) {
	var in accountInput
	if err := decodeJSON(w, r, &in); err != nil {
		apierr.Write(w, r, err)
		return
	}
	cookies := parseCookies(in.CookieHeader)
	for k, v := range in.Cookies {
		cookies[k] = v
	}
	if len(cookies) == 0 {
		apierr.Write(w, r, apierr.BadRequest("未解析到 Cookie"))
		return
	}
	a, err := s.store.UpdateAccount(r.PathValue("id"), func(a *model.Account) error {
		a.Cookies = cookies
		a.CookieExpiry = in.Expires
		a.OK = nil
		a.Status = model.AccountUnknown
		a.LastError = ""
		a.Note = "凭据已更新，等待验证"
		a.ExpiresAt = 0
		return nil
	})
	if err == nil {
		s.store.AddEvent(model.Event{Level: "info", Type: "account.credentials.update", Message: "账号凭据已更新", RequestID: requestIDFrom(r.Context()), AccountID: a.ID})
	}
	if err != nil {
		apierr.Write(w, r, err)
		return
	}
	s.enqueueAutoCheck(a.ID)
	s.pool.Wake()
	writeJSON(w, 200, map[string]any{"ok": true, "id": a.ID, "cookie_count": a.CookieCount, "expires_at": a.ExpiresAt, "auto_check": true})
}

type sessionCheckResult struct {
	OK      bool
	Session *protocol.Session
	Message string
}

func (s *Server) checkAndStoreSession(ctx context.Context, account model.Account) sessionCheckResult {
	session, checkErr := s.driver.CheckSession(ctx, protocol.AccountCredentials{ID: account.ID, Cookies: account.Cookies, CookieExpiry: account.CookieExpiry})
	ok := checkErr == nil && session != nil && session.Valid
	if _, err := s.store.UpdateAccount(account.ID, func(a *model.Account) error {
		a.LastChecked = time.Now().Unix()
		if ok {
			a.Status = model.AccountActive
			a.OK = boolPtr(true)
			a.LastError = ""
			a.Note = "会话有效"
			if len(session.Cookies) > 0 {
				a.Cookies = session.Cookies
				a.CookieExpiry = session.CookieExpiry
			}
			if exp := session.CookieExpiry["hatch_vml"]; exp > 0 {
				a.ExpiresAt = exp
				a.ExpiryEstimated = false
			}
			a.LastKeepalive = time.Now().Unix()
		} else if errors.Is(checkErr, protocol.ErrSessionExpired) {
			a.Status = model.AccountExpired
			a.OK = boolPtr(false)
			a.LastError = "会话已失效，请重新认证"
		} else {
			a.LastError = safeUpstreamError(checkErr)
			a.Note = "检测未确认，保留此前有效状态"
		}
		return nil
	}); err != nil {
		s.log.Warn("会话检测写入失败", "account", account.ID, "error", err)
	}
	message := safeUpstreamError(checkErr)
	if ok {
		message = "会话有效"
	} else if message == "" {
		message = "会话状态未确认"
	}
	return sessionCheckResult{OK: ok, Session: session, Message: message}
}

func (s *Server) testAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a, err := s.store.GetAccount(id)
	if err != nil {
		apierr.Write(w, r, apierr.NotFound("账号不存在"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.HTTPTimeout)
	defer cancel()
	result := s.checkAndStoreSession(ctx, a)
	s.pool.Wake()
	var sessionView map[string]any
	if result.Session != nil {
		sessionView = map[string]any{
			"valid": result.Session.Valid, "status": result.Session.Status, "vm_id": result.Session.VMID,
			"vm_state": result.Session.VMState, "checked_at": result.Session.CheckedAt,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": result.OK, "message": result.Message, "session": sessionView})
}

type quotaDriver interface {
	QueryQuota(context.Context, protocol.AccountCredentials) (*model.Quota, error)
}

func (s *Server) quota(w http.ResponseWriter, r *http.Request) {
	driver, ok := s.driver.(quotaDriver)
	if !ok {
		apierr.Write(w, r, apierr.Capability("当前执行器不支持额度查询"))
		return
	}
	account, err := s.store.GetAccount(r.PathValue("id"))
	if err != nil {
		apierr.Write(w, r, apierr.NotFound("账号不存在"))
		return
	}
	quota, err := s.queryQuota(r.Context(), driver, protocol.AccountCredentials{ID: account.ID, Cookies: account.Cookies, CookieExpiry: account.CookieExpiry})
	if err != nil {
		s.log.Warn("quota query failed", "account", account.ID, "error", err)
		apierr.Write(w, r, mapQuotaError(err))
		return
	}
	if _, err := s.store.UpdateAccount(account.ID, func(a *model.Account) error { a.Quota = quota; return nil }); err != nil {
		s.log.Warn("quota persist failed", "account", account.ID, "error", err)
	}
	writeJSON(w, http.StatusOK, quota)
}

// queryQuota converts an unexpected panic in browser automation into a normal
// error so the admin UI can show the real failure reason instead of a generic 500.
func (s *Server) queryQuota(ctx context.Context, driver quotaDriver, credentials protocol.AccountCredentials) (quota *model.Quota, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			s.log.Error("quota query panic", "account", credentials.ID, "panic", recovered, "stack", string(debug.Stack()))
			quota = nil
			err = fmt.Errorf("额度查询内部错误: %v", recovered)
		}
	}()
	return driver.QueryQuota(ctx, credentials)
}

func mapQuotaError(err error) error {
	if errors.Is(err, protocol.ErrSessionExpired) {
		return apierr.New(http.StatusUnauthorized, "auth_error", "上游 Session 已失效，请重新导入 Cookie")
	}
	var upstream *protocol.UpstreamError
	if errors.As(err, &upstream) {
		status := http.StatusBadGateway
		if upstream.Status == http.StatusTooManyRequests {
			status = http.StatusTooManyRequests
		}
		return apierr.New(status, "upstream_error", "额度读取失败："+safeUpstreamError(err))
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return apierr.New(http.StatusGatewayTimeout, "upstream_timeout", "额度读取超时，请稍后重试")
	}
	message := strings.TrimSpace(err.Error())
	if message == "" {
		message = "未知错误"
	}
	if runes := []rune(message); len(runes) > 300 {
		message = string(runes[:300]) + "…"
	}
	return apierr.New(http.StatusBadGateway, "quota_error", "额度读取失败："+message)
}
func (s *Server) quotaAny(w http.ResponseWriter, r *http.Request) {
	lease, err := s.pool.Acquire(r.Context())
	if err != nil {
		apierr.Write(w, r, apierr.New(http.StatusServiceUnavailable, "account_unavailable", "没有可用账号"))
		return
	}
	defer lease.Release()
	r.SetPathValue("id", lease.Account.ID)
	s.quota(w, r)
}
func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit == 0 {
		limit = 100
	}
	tasks := s.store.ListTasks(limit)
	for i := range tasks {
		tasks[i] = legacyTask(tasks[i])
	}
	writeJSON(w, 200, map[string]any{"tasks": tasks, "count": len(tasks)})
}
func (s *Server) deleteTask(w http.ResponseWriter, r *http.Request) {
	t, err := s.store.GetTask(r.PathValue("id"))
	if err == nil && (t.Status == model.TaskQueued || t.Status == model.TaskRunning) {
		apierr.Write(w, r, apierr.New(409, "invalid_request_error", "请先取消运行中的任务"))
		return
	}
	if err := s.store.DeleteTask(r.PathValue("id")); err != nil {
		apierr.Write(w, r, apierr.NotFound("任务不存在"))
		return
	}
	writeJSON(w, 200, map[string]any{"deleted": true})
}
func (s *Server) clearTasks(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Keep int `json:"keep"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	if in.Keep < 0 || in.Keep > 10000 {
		apierr.Write(w, r, apierr.BadRequest("keep 无效"))
		return
	}
	writeJSON(w, 200, map[string]any{"removed": s.store.ClearTasks(in.Keep), "message": "仅清理终态与排队任务；运行中的任务保留"})
}
func (s *Server) cancelTask(w http.ResponseWriter, r *http.Request) {
	if err := s.tasks.Cancel(r.PathValue("id")); err != nil {
		switch {
		case errors.Is(err, storage.ErrNotFound):
			apierr.Write(w, r, apierr.NotFound("任务不存在"))
		case errors.Is(err, storage.ErrConflict):
			apierr.Write(w, r, apierr.New(409, "invalid_request_error", err.Error()))
		default:
			apierr.Write(w, r, err)
		}
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "message": "本地取消已处理；不代表上游任务已停止"})
}
func legacyTask(t model.Task) model.Task {
	t.ReferenceImage = ""
	t.ReferenceImages = nil
	switch t.Status {
	case model.TaskRunning:
		t.Status = "processing"
	case model.TaskSucceeded:
		t.Status = "completed"
	}
	return t
}
func safeMediaName(name string) bool {
	return storage.SafeFilename(name) && !strings.HasPrefix(name, ".")
}

func (s *Server) mediaItems() ([]model.Media, error) {
	entries, err := os.ReadDir(s.cfg.MediaDir())
	if err != nil {
		return nil, err
	}
	items := []model.Media{}
	for _, e := range entries {
		if !e.Type().IsRegular() || !safeMediaName(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		kind := "image"
		mime := "image/" + strings.TrimPrefix(ext, ".")
		if ext == ".mp4" || ext == ".webm" || ext == ".mov" {
			kind = "video"
			mime = "video/" + strings.TrimPrefix(ext, ".")
		}
		if ext == ".jpg" {
			mime = "image/jpeg"
		}
		items = append(items, model.Media{Name: e.Name(), URL: "/v1/media/" + e.Name(), Bytes: info.Size(), ModTime: info.ModTime().Unix(), Kind: kind, MIME: mime})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ModTime > items[j].ModTime })
	return items, nil
}

// mediaCount avoids stat-ing every file on each dashboard refresh; the
// overview only needs a count, not sizes or timestamps.
func (s *Server) mediaCount() int {
	entries, err := os.ReadDir(s.cfg.MediaDir())
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if entry.Type().IsRegular() && safeMediaName(entry.Name()) {
			count++
		}
	}
	return count
}

func (s *Server) listMedia(w http.ResponseWriter, r *http.Request) {
	items, err := s.mediaItems()
	if err != nil {
		apierr.Write(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"media": items, "count": len(items)})
}
func (s *Server) deleteMedia(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !safeMediaName(name) {
		apierr.Write(w, r, apierr.BadRequest("非法文件名"))
		return
	}
	if err := os.Remove(filepath.Join(s.cfg.MediaDir(), name)); err != nil {
		apierr.Write(w, r, apierr.NotFound("文件不存在或删除失败"))
		return
	}
	s.store.AddEvent(model.Event{Level: "info", Type: "media.delete", Message: "媒体文件已删除", RequestID: requestIDFrom(r.Context())})
	writeJSON(w, 200, map[string]any{"status": "ok", "deleted": name})
}
func (s *Server) deleteMediaBatch(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Names []string `json:"names"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		apierr.Write(w, r, err)
		return
	}
	removed := 0
	failures := []string{}
	for _, name := range in.Names {
		if !safeMediaName(name) {
			failures = append(failures, "非法文件名")
			continue
		}
		if err := os.Remove(filepath.Join(s.cfg.MediaDir(), name)); err == nil {
			removed++
		} else {
			failures = append(failures, name)
		}
	}
	writeJSON(w, 200, map[string]any{"removed": removed, "errors": failures})
}
func (s *Server) adminModels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"data": s.driver.Capabilities(r.Context())})
}
func publicConfig(c config.Config) map[string]any {
	return map[string]any{"host": c.Host, "port": c.Port, "public_base": c.PublicBase, "site": c.SiteURL, "concurrency": c.Concurrency, "queue_size": c.QueueSize, "image_timeout": int(c.ImageTimeout.Seconds()), "video_timeout": int(c.VideoTimeout.Seconds()), "chat_timeout": int(c.ChatTimeout.Seconds()), "media_dir": "data/media", "media_retention": "manual", "log_level": c.LogLevel, "credential_encryption": len(c.CredentialKey) == 32, "public_media": c.PublicMedia, "browser_runtime": c.BrowserEnabled, "cdp_port": c.CDPPort, "login_wait": int(c.LoginWait.Seconds()), "admin_username": c.AdminUsername, "session_ttl": int(c.SessionTTL.Seconds()), "trust_proxy": c.TrustProxy, "max_upload_bytes": c.MaxUploadBytes, "ref_image_local_files": c.RefImageLocalFiles, "ref_image_private_fetch": c.RefImagePrivateFetch}
}
func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"config": publicConfig(s.cfg), "version": "2.1.0", "restart_required_for": "MUSE2API_* 环境变量"})
}
func (s *Server) backup(w http.ResponseWriter, r *http.Request) {
	path, err := s.store.Backup(r.Context())
	if err != nil {
		apierr.Write(w, r, err)
		return
	}
	writeJSON(w, 201, map[string]any{"ok": true, "backup": filepath.Base(path), "message": "元数据备份完成；媒体文件保留在原目录"})
}
func (s *Server) restore(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name    string `json:"name"`
		Confirm string `json:"confirm"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		apierr.Write(w, r, err)
		return
	}
	if in.Confirm != "RESTORE" || !storage.SafeFilename(in.Name) || !strings.HasPrefix(in.Name, "manual-") {
		apierr.Write(w, r, apierr.BadRequest("恢复需要有效备份名和 confirm=RESTORE"))
		return
	}
	dir := filepath.Join(s.cfg.DataDir(), "backups", in.Name)
	if _, err := s.store.Backup(r.Context()); err != nil {
		apierr.Write(w, r, err)
		return
	}
	for _, name := range []string{"accounts.secure.json", "api_key.secure", "tasks.json", "events.json"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			apierr.Write(w, r, err)
			return
		}
		if err := os.WriteFile(filepath.Join(s.cfg.DataDir(), name), b, 0o600); err != nil {
			apierr.Write(w, r, err)
			return
		}
	}
	writeJSON(w, 200, map[string]any{"ok": true, "restart_required": true})
}
func maskAPIKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 8 {
		return "…"
	}
	return key[:4] + "…" + key[len(key)-4:]
}

func (s *Server) apiKeyInfo(w http.ResponseWriter, r *http.Request) {
	key := s.currentKey()
	writeJSON(w, 200, map[string]any{"configured": key != "", "api_key_masked": maskAPIKey(key), "base_url": s.cfg.PublicBase + "/v1"})
}

// revealAPIKey is intentionally separate from apiKeyInfo: the full secret is
// only returned after re-entering the admin password, is never logged, and is
// not included in the normal settings payload.
func (s *Server) revealAPIKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		apierr.Write(w, r, err)
		return
	}
	if len(in.Password) > 1024 {
		apierr.Write(w, r, apierr.BadRequest("密码过长"))
		return
	}
	if !auth.CheckCredentials(s.adminUser, in.Password, s.adminUser, s.adminPass) {
		if s.loginLimiter.Exceeded(r, s.cfg.TrustProxy) {
			apierr.Write(w, r, apierr.New(429, "rate_limit_error", "验证尝试过于频繁"))
			return
		}
		s.loginLimiter.Fail(r, s.cfg.TrustProxy)
		s.store.AddEvent(model.Event{Level: "warning", Type: "security.key.reveal_denied", Message: "API Key 查看密码验证失败", RequestID: requestIDFrom(r.Context())})
		apierr.Write(w, r, apierr.New(http.StatusUnauthorized, "authentication_error", "管理员密码错误"))
		return
	}
	s.loginLimiter.Reset(r, s.cfg.TrustProxy)
	key := s.currentKey()
	if key == "" {
		apierr.Write(w, r, apierr.NotFound("API Key 未配置"))
		return
	}
	s.store.AddEvent(model.Event{Level: "warning", Type: "security.key.reveal", Message: "API Key 已在管理台显示", RequestID: requestIDFrom(r.Context())})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"api_key": key})
}

func (s *Server) rotateAPIKey(w http.ResponseWriter, r *http.Request) {
	key, err := auth.NewKey()
	if err != nil {
		apierr.Write(w, r, err)
		return
	}
	if err := s.store.SaveAPIKey(key); err != nil {
		apierr.Write(w, r, err)
		return
	}
	s.keyMu.Lock()
	s.apiKey = key
	s.keyMu.Unlock()
	s.store.AddEvent(model.Event{Level: "info", Type: "security.key.rotate", Message: "API Key 已轮换，旧 Key 已失效；新 Key 已加密持久化", RequestID: requestIDFrom(r.Context())})
	writeJSON(w, 200, map[string]any{"ok": true, "api_key": key, "message": "新 Key 仅显示这一次；旧 Key 已失效，管理员会话不受影响"})
}
func (s *Server) logs(w http.ResponseWriter, r *http.Request) {
	events := s.store.Events(500)
	level, q := r.URL.Query().Get("level"), strings.ToLower(r.URL.Query().Get("q"))
	filtered := events[:0]
	for _, e := range events {
		if level != "" && e.Level != level {
			continue
		}
		hay := strings.ToLower(e.Message + " " + e.RequestID + " " + e.TaskID + " " + e.AccountID)
		if q != "" && !strings.Contains(hay, q) {
			continue
		}
		filtered = append(filtered, e)
	}
	writeJSON(w, 200, map[string]any{"events": filtered, "count": len(filtered)})
}
func (s *Server) exportLogs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=muse2api-redacted-logs.ndjson")
	enc := json.NewEncoder(w)
	for _, e := range s.store.Events(1000) {
		_ = enc.Encode(e)
	}
}
func safeUpstreamError(err error) string {
	if err == nil {
		return ""
	}
	var up *protocol.UpstreamError
	if errors.As(err, &up) {
		if up.Status > 0 {
			return fmt.Sprintf("上游 HTTP %d：会话状态未确认", up.Status)
		}
		return up.Message
	}
	if errors.Is(err, protocol.ErrSessionExpired) {
		return "会话已失效，请重新导入 Cookie"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "上游检测超时，未改变会话状态"
	}
	return "上游检测失败，未改变会话状态"
}
func boolPtr(v bool) *bool { return &v }
