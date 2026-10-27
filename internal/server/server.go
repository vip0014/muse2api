package server

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"muse2api/internal/apierr"
	"muse2api/internal/auth"
	"muse2api/internal/config"
	"muse2api/internal/model"
	"muse2api/internal/muse/protocol"
	"muse2api/internal/scheduler"
	"muse2api/internal/storage"
	"muse2api/internal/task"
)

//go:embed static/*
var staticFiles embed.FS

type Server struct {
	cfg          config.Config
	store        *storage.Store
	driver       protocol.Driver
	tasks        *task.Manager
	pool         *scheduler.Pool
	log          *slog.Logger
	authLimiter  *auth.Limiter
	loginLimiter *auth.Limiter
	sessions     *auth.SessionManager
	adminUser    string
	adminPass    string
	keyMu        sync.RWMutex
	apiKey       string
}

func New(cfg config.Config, store *storage.Store, driver protocol.Driver, log *slog.Logger) *Server {
	s := &Server{cfg: cfg, store: store, driver: driver, log: log, authLimiter: auth.NewLimiter(), loginLimiter: auth.NewLimiter(), sessions: auth.NewSessionManager(cfg.SessionTTL), adminUser: cfg.AdminUsername, adminPass: cfg.AdminPassword, apiKey: cfg.APIKey}
	s.pool = scheduler.NewPool(store, cfg.Concurrency)
	s.tasks = task.NewManager(store, task.ProcessorFunc(s.processTask), cfg.Concurrency, cfg.QueueSize)
	return s
}

// Close bounds task shutdown. A provider that ignores context cannot hold the
// process forever, but the driver still gets a bounded chance to release its
// Chromium process instead of being skipped entirely.
func (s *Server) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.tasks.Shutdown(ctx); err != nil {
		s.log.Warn("任务管理器关闭超时", "error", err)
	}
	driverDone := make(chan struct{})
	go func() {
		_ = s.driver.Close()
		close(driverDone)
	}()
	select {
	case <-driverDone:
	case <-time.After(3 * time.Second):
		s.log.Warn("执行器关闭超时，交由进程退出清理")
	}
	_ = s.store.Close()
}
func (s *Server) currentKey() string { s.keyMu.RLock(); defer s.keyMu.RUnlock(); return s.apiKey }
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /readyz", s.ready)
	mux.HandleFunc("POST /admin/login", s.adminLogin)
	mux.HandleFunc("POST /admin/logout", s.adminLogout)
	mux.HandleFunc("GET /admin/session", s.adminSessionStatus)
	mux.HandleFunc("GET /v1/models", s.models)
	mux.HandleFunc("POST /v1/chat/completions", s.chatCompletions)
	mux.HandleFunc("POST /v1/responses", s.responses)
	mux.HandleFunc("POST /v1/images/generations", s.imageGenerations)
	mux.HandleFunc("POST /v1/images/edits", s.imageEdits)
	mux.HandleFunc("POST /v1/images/tasks", s.imageTasks)
	mux.HandleFunc("GET /v1/images/tasks/{id}", s.getImageTask)
	mux.HandleFunc("POST /v1/videos", s.createVideo)
	mux.HandleFunc("POST /v1/videos/generations", s.createVideo)
	mux.HandleFunc("GET /v1/videos/{id}", s.getVideo)
	mux.HandleFunc("GET /v1/videos/generations/{id}", s.getVideo)
	mux.HandleFunc("GET /v1/media/{name}", s.getMedia)
	mux.HandleFunc("GET /admin/status", s.adminStatus)
	mux.HandleFunc("GET /admin/overview", s.adminStatus)
	mux.HandleFunc("GET /admin/accounts", s.listAccounts)
	mux.HandleFunc("POST /admin/accounts", s.addAccounts)
	mux.HandleFunc("PATCH /admin/accounts/{id}", s.patchAccount)
	mux.HandleFunc("DELETE /admin/accounts/{id}", s.deleteAccount)
	mux.HandleFunc("POST /admin/accounts/{id}/test", s.testAccount)
	mux.HandleFunc("POST /admin/accounts/{id}/relogin", s.testAccount)
	mux.HandleFunc("POST /admin/accounts/{id}/cookies", s.updateCookies)
	mux.HandleFunc("POST /admin/accounts/{id}/quota", s.quota)
	mux.HandleFunc("POST /admin/quota", s.quotaAny)
	mux.HandleFunc("POST /admin/accounts/bulk", s.bulkAccounts)
	mux.HandleFunc("GET /admin/tasks", s.listTasks)
	mux.HandleFunc("DELETE /admin/tasks/{id}", s.deleteTask)
	mux.HandleFunc("POST /admin/tasks/clear", s.clearTasks)
	mux.HandleFunc("POST /admin/tasks/{id}/cancel", s.cancelTask)
	mux.HandleFunc("GET /admin/media", s.listMedia)
	mux.HandleFunc("DELETE /admin/media/{name}", s.deleteMedia)
	mux.HandleFunc("POST /admin/media/delete", s.deleteMediaBatch)
	mux.HandleFunc("GET /admin/models", s.adminModels)
	mux.HandleFunc("GET /admin/settings", s.settings)
	mux.HandleFunc("POST /admin/settings/backup", s.backup)
	mux.HandleFunc("POST /admin/settings/restore", s.restore)
	mux.HandleFunc("GET /admin/apikey", s.apiKeyInfo)
	mux.HandleFunc("POST /admin/apikey/rotate", s.rotateAPIKey)
	mux.HandleFunc("GET /admin/logs", s.logs)
	mux.HandleFunc("GET /admin/logs/export", s.exportLogs)
	mux.HandleFunc("GET /admin/capabilities", s.adminModels)
	mux.HandleFunc("/", s.static)
	return s.recoverMiddleware(s.requestMiddleware(mux))
}

func (s *Server) recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.log.Error("request panic", "path", r.URL.Path, "panic", recovered, "stack", string(debug.Stack()))
				apierr.Write(w, r, errors.New("panic"))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type requestIDKey struct{}

func requestIDFrom(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey{}).(string)
	return value
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(body)
}

// ReadFrom preserves sendfile-style file serving used by http.ServeContent.
func (w *statusWriter) ReadFrom(reader io.Reader) (int64, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if readerFrom, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		return readerFrom.ReadFrom(reader)
	}
	return io.Copy(w.ResponseWriter, reader)
}

// Flush keeps SSE streaming working through the status wrapper.
func (w *statusWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (s *Server) requestMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" || len(requestID) > 128 {
			requestID = newRequestID()
		}
		w.Header().Set("X-Request-ID", requestID)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		if strings.HasPrefix(r.URL.Path, "/v1/") || strings.HasPrefix(r.URL.Path, "/admin/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		sw := &statusWriter{ResponseWriter: w}
		w = sw
		r = r.WithContext(context.WithValue(r.Context(), requestIDKey{}, requestID))
		if r.Method == http.MethodOptions {
			s.cors(w, r)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		s.cors(w, r)
		protected := strings.HasPrefix(r.URL.Path, "/admin/") || strings.HasPrefix(r.URL.Path, "/v1/")
		publicAuth := r.URL.Path == "/admin/login" || r.URL.Path == "/admin/logout" || r.URL.Path == "/admin/session"
		if publicAuth || r.URL.Path == "/admin/" {
			protected = false
		}
		isAPI := strings.HasPrefix(r.URL.Path, "/v1/")
		if strings.HasPrefix(r.URL.Path, "/v1/media/") && s.cfg.PublicMedia {
			protected = false
		}
		if protected {
			authorized := s.hasAdminSession(r)
			if !authorized && isAPI {
				authorized = auth.Valid(r.Header.Get("Authorization"), s.currentKey())
			}
			if !authorized {
				if s.authLimiter.Exceeded(r, s.cfg.TrustProxy) {
					apierr.Write(w, r, apierr.New(429, "rate_limit_error", "认证尝试过于频繁"))
					return
				}
				s.authLimiter.Fail(r, s.cfg.TrustProxy)
				apierr.Write(w, r, apierr.New(401, "authentication_error", "缺少或无效的认证凭据"))
				return
			}
			s.authLimiter.Reset(r, s.cfg.TrustProxy)
		}
		if protected && r.Method != http.MethodGet && r.Method != http.MethodHead {
			origin := r.Header.Get("Origin")
			if origin != "" && !sameOrigin(origin, r) {
				apierr.Write(w, r, apierr.New(403, "csrf_rejected", "跨站管理请求已拒绝"))
				return
			}
		}
		r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxUploadBytes)
		next.ServeHTTP(w, r)
		status := sw.status
		if status == 0 {
			status = http.StatusOK
		}
		s.log.Info("http request", "method", r.Method, "path", r.URL.Path, "status", status, "request_id", requestID, "duration_ms", time.Since(start).Milliseconds())
	})
}
func (s *Server) cors(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return
	}
	for _, allowed := range s.cfg.CORSOrigins {
		if origin == allowed {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, X-Request-ID")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Expose-Headers", "X-Request-ID")
			return
		}
	}
}
func sameOrigin(origin string, r *http.Request) bool {
	return origin == "http://"+r.Host || origin == "https://"+r.Host
}

const adminSessionCookie = "muse2api_admin"

func (s *Server) hasAdminSession(r *http.Request) bool {
	cookie, err := r.Cookie(adminSessionCookie)
	if err != nil {
		return false
	}
	_, ok := s.sessions.Get(cookie.Value)
	return ok
}

func (s *Server) adminSession(r *http.Request) (auth.Session, bool) {
	cookie, err := r.Cookie(adminSessionCookie)
	if err != nil {
		return auth.Session{}, false
	}
	return s.sessions.Get(cookie.Value)
}

func (s *Server) secureCookie(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if s.cfg.TrustProxy && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return true
	}
	return strings.HasPrefix(s.cfg.PublicBase, "https://")
}

func (s *Server) setAdminCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	maxAge := int(time.Until(expires).Seconds())
	if maxAge < 1 {
		maxAge = 1
	}
	http.SetCookie(w, &http.Cookie{
		Name:     adminSessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   s.secureCookie(r),
		SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) clearAdminCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     adminSessionCookie,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.secureCookie(r),
		SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) adminLogin(w http.ResponseWriter, r *http.Request) {
	if origin := r.Header.Get("Origin"); origin != "" && !sameOrigin(origin, r) {
		apierr.Write(w, r, apierr.New(403, "csrf_rejected", "跨站登录请求已拒绝"))
		return
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		apierr.Write(w, r, apierr.BadRequest("登录 JSON 无效"))
		return
	}
	if len(in.Username) > 256 || len(in.Password) > 1024 {
		apierr.Write(w, r, apierr.BadRequest("用户名或密码过长"))
		return
	}
	if !auth.CheckCredentials(in.Username, in.Password, s.adminUser, s.adminPass) {
		if s.loginLimiter.Exceeded(r, s.cfg.TrustProxy) {
			apierr.Write(w, r, apierr.New(429, "rate_limit_error", "登录尝试过于频繁"))
			return
		}
		s.loginLimiter.Fail(r, s.cfg.TrustProxy)
		apierr.Write(w, r, apierr.New(401, "authentication_error", "用户名或密码错误"))
		return
	}
	s.loginLimiter.Reset(r, s.cfg.TrustProxy)
	token, expires, err := s.sessions.Create(s.adminUser)
	if err != nil {
		apierr.Write(w, r, err)
		return
	}
	s.setAdminCookie(w, r, token, expires)
	s.store.AddEvent(model.Event{Level: "info", Type: "admin.login", Message: "管理员登录成功", RequestID: requestIDFrom(r.Context())})
	writeJSON(w, 200, map[string]any{"ok": true, "username": s.adminUser, "expires_at": expires.Unix()})
}

func (s *Server) adminLogout(w http.ResponseWriter, r *http.Request) {
	if origin := r.Header.Get("Origin"); origin != "" && !sameOrigin(origin, r) {
		apierr.Write(w, r, apierr.New(403, "csrf_rejected", "跨站登出请求已拒绝"))
		return
	}
	if cookie, err := r.Cookie(adminSessionCookie); err == nil {
		s.sessions.Delete(cookie.Value)
	}
	s.clearAdminCookie(w, r)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) adminSessionStatus(w http.ResponseWriter, r *http.Request) {
	if session, ok := s.adminSession(r); ok {
		writeJSON(w, 200, map[string]any{"authenticated": true, "username": session.Username, "expires_at": session.Expires.Unix()})
		return
	}
	writeJSON(w, 200, map[string]any{"authenticated": false})
}
func newRequestID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "req_unknown"
	}
	return "req_" + hex.EncodeToString(b)
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func decodeJSON(w http.ResponseWriter, r *http.Request, out any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return apierr.BadRequest("请求 JSON 无效或包含不支持的字段")
	}
	return nil
}
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"status": "ok", "time": time.Now().Unix(), "version": "2.0.0"})
}

type browserStatus interface {
	BrowserRunning() bool
}

func (s *Server) browserRunning() bool {
	driver, ok := s.driver.(browserStatus)
	return ok && driver.BrowserRunning()
}

func (s *Server) protocolName() string {
	if s.cfg.BrowserEnabled {
		return "go-chromium-cdp"
	}
	return "native-http-unavailable"
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	stats := s.store.AccountStats()
	status := "ready"
	if stats["enabled"] == 0 {
		status = "no_account"
	}
	writeJSON(w, 200, map[string]any{"status": status, "accounts": stats["enabled"], "browser_running": s.browserRunning(), "browser_enabled": s.cfg.BrowserEnabled, "protocol": s.protocolName()})
}
func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.NotFound(w, r)
		return
	}
	if r.URL.Path == "/v1" || ((strings.HasPrefix(r.URL.Path, "/v1/") || strings.HasPrefix(r.URL.Path, "/admin/")) && r.URL.Path != "/admin/") {
		apierr.Write(w, r, apierr.NotFound("接口不存在"))
		return
	}
	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		http.Error(w, "static unavailable", 500)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" || path == "admin" || path == "admin/" {
		path = "index.html"
	}
	if _, err := fs.Stat(sub, path); err != nil {
		path = "index.html"
	}
	switch {
	case path == "index.html":
		w.Header().Set("Cache-Control", "no-cache")
	case strings.HasPrefix(path, "assets/"):
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	default:
		w.Header().Set("Cache-Control", "public, max-age=3600")
	}
	http.ServeFileFS(w, r, sub, path)
}
func (s *Server) processTask(ctx context.Context, t model.Task) (task.Result, error) {
	acquireCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	lease, err := s.pool.Acquire(acquireCtx)
	cancel()
	if err != nil {
		return task.Result{}, err
	}
	defer lease.Release()
	if _, err := s.store.UpdateTask(t.ID, func(current *model.Task) error { current.AccountID = lease.Account.ID; return nil }); err != nil {
		s.store.AddEvent(model.Event{Level: "warning", Type: "task.account.bind", Message: "任务账号绑定持久化失败", TaskID: t.ID, AccountID: lease.Account.ID})
	}
	switch t.Kind {
	case "image":
		res, err := s.driver.GenerateImage(ctx, protocol.ImageRequest{Prompt: t.APIPrompt, Model: t.Model, Size: t.Size, AspectRatio: t.AspectRatio, ResponseFormat: t.ResponseFormat, ReferenceImage: t.ReferenceImage, AccountID: lease.Account.ID})
		if err != nil {
			return task.Result{}, err
		}
		return task.Result{MediaURL: res.URL, Data: map[string]any{"filename": res.Filename, "kind": res.Kind, "bytes": res.Bytes, "url": res.URL}}, nil
	case "video":
		created, err := s.driver.CreateVideo(ctx, protocol.VideoRequest{Prompt: t.APIPrompt, Model: t.Model, Size: t.Size, AspectRatio: t.AspectRatio, Resolution: t.Resolution, ReferenceImage: t.ReferenceImage, Duration: t.Duration, AccountID: lease.Account.ID})
		if err != nil {
			return task.Result{}, err
		}
		if created.Status != "succeeded" && created.Status != "completed" {
			return task.Result{}, errors.New("上游任务尚未成功，不能标记完成")
		}
		return task.Result{MediaURL: created.URL, Data: map[string]any{"url": created.URL}}, nil
	default:
		return task.Result{}, errors.New("不支持的任务类型")
	}
}
func isNotFound(err error) bool { return errors.Is(err, storage.ErrNotFound) }
