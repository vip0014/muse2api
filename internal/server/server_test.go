package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"muse2api/internal/config"
	"muse2api/internal/model"
	"muse2api/internal/muse/capabilities"
	"muse2api/internal/muse/protocol"
	"muse2api/internal/storage"
)

type fakeDriver struct{ unavailable bool }

func (f *fakeDriver) Capabilities(context.Context) []capabilities.Capability {
	return capabilities.Current()
}
func (f *fakeDriver) Chat(ctx context.Context, r protocol.ChatRequest) (<-chan protocol.ChatEvent, error) {
	if f.unavailable {
		return nil, protocol.Unavailable("chat")
	}
	ch := make(chan protocol.ChatEvent, 2)
	ch <- protocol.ChatEvent{Delta: "hello " + r.Prompt}
	ch <- protocol.ChatEvent{Done: true}
	close(ch)
	return ch, nil
}
func (f *fakeDriver) GenerateImage(context.Context, protocol.ImageRequest) (*protocol.ImageResult, error) {
	return nil, protocol.Unavailable("image")
}
func (f *fakeDriver) EditImage(context.Context, protocol.ImageRequest) (*protocol.ImageResult, error) {
	return nil, protocol.Unavailable("image")
}
func (f *fakeDriver) CreateVideo(context.Context, protocol.VideoRequest) (*protocol.VideoTask, error) {
	return nil, protocol.Unavailable("video")
}
func (f *fakeDriver) GetVideoTask(context.Context, string) (*protocol.VideoTask, error) {
	return nil, protocol.Unavailable("video")
}
func (f *fakeDriver) CheckSession(context.Context, protocol.AccountCredentials) (*protocol.Session, error) {
	return &protocol.Session{Valid: true, Status: "assigned", Cookies: map[string]string{}, CookieExpiry: map[string]int64{}, CheckedAt: time.Now().Unix()}, nil
}
func (f *fakeDriver) Close() error { return nil }

func testServer(t *testing.T, driver protocol.Driver) (*Server, http.Handler) {
	t.Helper()
	home := t.TempDir()
	cfg := config.Config{Home: home, APIKey: "test-secret", AdminUsername: "admin", AdminPassword: "test-admin-pass", SessionTTL: time.Hour, Concurrency: 1, QueueSize: 4, ImageTimeout: time.Second, VideoTimeout: time.Second, ChatTimeout: time.Second, HTTPTimeout: time.Second, MaxUploadBytes: 1 << 20, CredentialKey: []byte("01234567890123456789012345678901")}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(cfg.DataDir(), cfg.CredentialKey, false)
	if err != nil {
		t.Fatal(err)
	}
	account, err := store.AddAccount("test", map[string]string{"hatch_sess": "s", "hatch_vml": "v"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateAccount(account.ID, func(a *model.Account) error { a.Status = model.AccountActive; return nil }); err != nil {
		t.Fatal(err)
	}
	s := New(cfg, store, driver, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	t.Cleanup(s.Close)
	return s, s.Handler()
}
func request(handler http.Handler, method, path, body, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
func adminLogin(t *testing.T, handler http.Handler) *http.Cookie {
	t.Helper()
	rec := request(handler, "POST", "/admin/login", `{"username":"admin","password":"test-admin-pass"}`, "")
	if rec.Code != 200 {
		t.Fatalf("login %d %s", rec.Code, rec.Body.String())
	}
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == adminSessionCookie {
			return cookie
		}
	}
	t.Fatal("admin session cookie missing")
	return nil
}

func adminRequest(handler http.Handler, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestAuthenticationAndOpenAIErrors(t *testing.T) {
	_, h := testServer(t, &fakeDriver{})
	if rec := request(h, "GET", "/v1/models", "", "wrong"); rec.Code != 401 {
		t.Fatalf("status %d", rec.Code)
	}
	rec := request(h, "POST", "/v1/chat/completions", `{"messages":[{"role":"user","content":"hi"}],"tools":[{}]}`, "test-secret")
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "capability_unavailable") {
		t.Fatalf("unexpected %d %s", rec.Code, rec.Body.String())
	}
}
func TestChatAndResponses(t *testing.T) {
	_, h := testServer(t, &fakeDriver{})
	rec := request(h, "POST", "/v1/chat/completions", `{"messages":[{"role":"user","content":"hi"}]}`, "test-secret")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "hello hi") {
		t.Fatalf("chat %d %s", rec.Code, rec.Body.String())
	}
	rec = request(h, "POST", "/v1/chat/completions", `{"messages":[{"role":"user","content":"hi"}],"stream":true}`, "test-secret")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "data: [DONE]") || !strings.Contains(rec.Body.String(), "chat.completion.chunk") {
		t.Fatalf("stream %d %s", rec.Code, rec.Body.String())
	}
	rec = request(h, "POST", "/v1/responses", `{"input":"hi","instructions":"be brief"}`, "test-secret")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "response") || !strings.Contains(rec.Body.String(), "hello") {
		t.Fatalf("responses %d %s", rec.Code, rec.Body.String())
	}
}
func TestUnavailableIsHonest(t *testing.T) {
	_, h := testServer(t, &fakeDriver{unavailable: true})
	rec := request(h, "POST", "/v1/chat/completions", `{"messages":[{"role":"user","content":"hi"}]}`, "test-secret")
	if rec.Code != 503 || strings.Contains(rec.Body.String(), "chatcmpl") {
		t.Fatalf("fake success returned: %d %s", rec.Code, rec.Body.String())
	}
}
func TestImageIdempotency(t *testing.T) {
	_, h := testServer(t, &fakeDriver{})
	body := `{"prompt":"a house"}`
	req := httptest.NewRequest("POST", "/v1/images/tasks", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-secret")
	req.Header.Set("Idempotency-Key", "same")
	first := httptest.NewRecorder()
	h.ServeHTTP(first, req)
	if first.Code != 202 {
		t.Fatalf("first %d %s", first.Code, first.Body.String())
	}
	var created map[string]any
	_ = json.Unmarshal(first.Body.Bytes(), &created)
	req = httptest.NewRequest("POST", "/v1/images/tasks", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-secret")
	req.Header.Set("Idempotency-Key", "same")
	second := httptest.NewRecorder()
	h.ServeHTTP(second, req)
	if second.Code != 202 || !strings.Contains(second.Body.String(), created["id"].(string)) {
		t.Fatalf("duplicate %d %s", second.Code, second.Body.String())
	}
	req = httptest.NewRequest("POST", "/v1/images/tasks", strings.NewReader(`{"prompt":"different"}`))
	req.Header.Set("Authorization", "Bearer test-secret")
	req.Header.Set("Idempotency-Key", "same")
	conflict := httptest.NewRecorder()
	h.ServeHTTP(conflict, req)
	if conflict.Code != 409 {
		t.Fatalf("conflict %d", conflict.Code)
	}
}
func TestVideoIdempotency(t *testing.T) {
	_, h := testServer(t, &fakeDriver{})
	body := `{"prompt":"a short film","duration":6}`
	req := httptest.NewRequest("POST", "/v1/videos", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-secret")
	req.Header.Set("Idempotency-Key", "video-same")
	first := httptest.NewRecorder()
	h.ServeHTTP(first, req)
	if first.Code != 202 {
		t.Fatalf("first %d %s", first.Code, first.Body.String())
	}
	var created map[string]any
	_ = json.Unmarshal(first.Body.Bytes(), &created)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("missing task id: %s", first.Body.String())
	}
	req = httptest.NewRequest("POST", "/v1/videos", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-secret")
	req.Header.Set("Idempotency-Key", "video-same")
	second := httptest.NewRecorder()
	h.ServeHTTP(second, req)
	if second.Code != 202 || !strings.Contains(second.Body.String(), id) {
		t.Fatalf("duplicate %d %s", second.Code, second.Body.String())
	}
	req = httptest.NewRequest("POST", "/v1/videos", strings.NewReader(`{"prompt":"different film","duration":6}`))
	req.Header.Set("Authorization", "Bearer test-secret")
	req.Header.Set("Idempotency-Key", "video-same")
	conflict := httptest.NewRecorder()
	h.ServeHTTP(conflict, req)
	if conflict.Code != 409 {
		t.Fatalf("conflict %d %s", conflict.Code, conflict.Body.String())
	}
}

func TestIdempotencyKeysAreScopedByTaskKind(t *testing.T) {
	_, h := testServer(t, &fakeDriver{})
	post := func(path, body string) map[string]any {
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer test-secret")
		req.Header.Set("Idempotency-Key", "shared-key")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 202 {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		return payload
	}
	image := post("/v1/images/tasks", `{"prompt":"same-key image"}`)
	video := post("/v1/videos", `{"prompt":"same-key video"}`)
	if image["id"] == video["id"] || image["id"] == "" || video["id"] == "" {
		t.Fatalf("image and video shared an idempotency task: %v %v", image["id"], video["id"])
	}
}

func TestLoginRateLimitIsSeparateFromAPIAuth(t *testing.T) {
	_, h := testServer(t, &fakeDriver{})
	for i := 0; i < 10; i++ {
		rec := request(h, "POST", "/admin/login", `{"username":"admin","password":"wrong"}`, "")
		if rec.Code != 401 {
			t.Fatalf("attempt %d: %d %s", i+1, rec.Code, rec.Body.String())
		}
	}
	if rec := request(h, "POST", "/admin/login", `{"username":"admin","password":"wrong"}`, ""); rec.Code != 429 {
		t.Fatalf("expected login throttle, got %d", rec.Code)
	}
	// A successful API request must not clear the administrator login throttle.
	if rec := request(h, "GET", "/v1/models", "", "test-secret"); rec.Code != 200 {
		t.Fatalf("api auth failed: %d", rec.Code)
	}
	if rec := request(h, "POST", "/admin/login", `{"username":"admin","password":"wrong"}`, ""); rec.Code != 429 {
		t.Fatalf("api success reset login throttle: %d", rec.Code)
	}
}

func TestMediaCacheAndHiddenTempFiles(t *testing.T) {
	s, h := testServer(t, &fakeDriver{})
	png := []byte("\x89PNG\r\n\x1a\ncontent")
	if err := os.WriteFile(filepath.Join(s.cfg.MediaDir(), "picture.png"), png, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.cfg.MediaDir(), ".download-temp"), png, 0o600); err != nil {
		t.Fatal(err)
	}
	rec := request(h, "GET", "/v1/media/picture.png", "", "test-secret")
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Cache-Control"), "private") {
		t.Fatalf("media response %d cache=%q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	if rec := request(h, "GET", "/v1/media/.download-temp", "", "test-secret"); rec.Code != 400 {
		t.Fatalf("hidden temp file served: %d", rec.Code)
	}
	cookie := adminLogin(t, h)
	listed := adminRequest(h, "GET", "/admin/media", "", cookie)
	if listed.Code != 200 || strings.Contains(listed.Body.String(), ".download-temp") || !strings.Contains(listed.Body.String(), "picture.png") {
		t.Fatalf("media listing %d %s", listed.Code, listed.Body.String())
	}
}

func TestAccountCredentialsNeverReturned(t *testing.T) {
	_, h := testServer(t, &fakeDriver{})
	cookie := adminLogin(t, h)
	rec := adminRequest(h, "POST", "/admin/accounts", `{"label":"main","cookie_header":"hatch_sess=very-secret; hatch_vml=v; hatch_native_auth_device=d"}`, cookie)
	if rec.Code != 201 || strings.Contains(rec.Body.String(), "very-secret") {
		t.Fatalf("credential leaked: %d %s", rec.Code, rec.Body.String())
	}
	rec = adminRequest(h, "GET", "/admin/accounts", "", cookie)
	if strings.Contains(rec.Body.String(), "very-secret") || !strings.Contains(rec.Body.String(), "cookie_count") {
		t.Fatalf("list leaked: %s", rec.Body.String())
	}
}

type recordingDriver struct {
	fakeDriver
	imageAccount string
	editAccount  string
	editSize     string
	imageRefs    []string
	editRefs     []string
	videoRefs    []string
	session      *protocol.Session
}

func (d *recordingDriver) GenerateImage(_ context.Context, req protocol.ImageRequest) (*protocol.ImageResult, error) {
	d.imageAccount = req.AccountID
	d.imageRefs = append([]string(nil), req.ReferenceImages...)
	return &protocol.ImageResult{Filename: "x.png", Kind: "image", URL: "/v1/media/x.png", Bytes: 1}, nil
}
func (d *recordingDriver) EditImage(_ context.Context, req protocol.ImageRequest) (*protocol.ImageResult, error) {
	d.editAccount = req.AccountID
	d.editSize = req.Size
	d.editRefs = append([]string(nil), req.ReferenceImages...)
	return &protocol.ImageResult{Filename: "x.png", Kind: "image", URL: "/v1/media/x.png", Bytes: 1}, nil
}
func (d *recordingDriver) CreateVideo(_ context.Context, req protocol.VideoRequest) (*protocol.VideoTask, error) {
	d.videoRefs = append([]string(nil), req.ReferenceImages...)
	return &protocol.VideoTask{ID: "video-1", Status: "succeeded", Progress: 100, URL: "/v1/media/video-1.mp4"}, nil
}
func (d *recordingDriver) CheckSession(context.Context, protocol.AccountCredentials) (*protocol.Session, error) {
	if d.session != nil {
		return d.session, nil
	}
	return d.fakeDriver.CheckSession(context.Background(), protocol.AccountCredentials{})
}

func TestAdminLoginSessionAndLogout(t *testing.T) {
	_, h := testServer(t, &fakeDriver{})
	if rec := request(h, "POST", "/admin/login", `{"username":"admin","password":"wrong"}`, ""); rec.Code != 401 {
		t.Fatalf("bad login %d", rec.Code)
	}
	cookie := adminLogin(t, h)
	rec := adminRequest(h, "GET", "/admin/accounts", "", cookie)
	if rec.Code != 200 {
		t.Fatalf("admin request %d %s", rec.Code, rec.Body.String())
	}
	session := adminRequest(h, "GET", "/admin/session", "", cookie)
	if !strings.Contains(session.Body.String(), `"authenticated":true`) {
		t.Fatalf("session not authenticated: %s", session.Body.String())
	}
	logout := adminRequest(h, "POST", "/admin/logout", "", cookie)
	if logout.Code != 200 {
		t.Fatalf("logout %d", logout.Code)
	}
	rec = adminRequest(h, "GET", "/admin/accounts", "", cookie)
	if rec.Code != 401 {
		t.Fatalf("session survived logout: %d", rec.Code)
	}
}

func TestAccountTestDoesNotLeakCookies(t *testing.T) {
	driver := &recordingDriver{session: &protocol.Session{Valid: true, Status: "assigned", Cookies: map[string]string{"hatch_sess": "LEAKED-SECRET"}}}
	s, h := testServer(t, driver)
	cookie := adminLogin(t, h)
	id := s.store.ListAccounts()[0].ID
	rec := adminRequest(h, "POST", "/admin/accounts/"+id+"/test", "", cookie)
	if strings.Contains(rec.Body.String(), "LEAKED-SECRET") {
		t.Fatalf("session cookies leaked: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("test response unexpected: %s", rec.Body.String())
	}
}

func TestSyncImagesUseAccountLease(t *testing.T) {
	driver := &recordingDriver{}
	s, h := testServer(t, driver)
	accountID := s.store.ListAccounts()[0].ID
	rec := request(h, "POST", "/v1/images/generations", `{"prompt":"x"}`, "test-secret")
	if rec.Code != 200 || driver.imageAccount != accountID {
		t.Fatalf("generation account=%q status=%d body=%s", driver.imageAccount, rec.Code, rec.Body.String())
	}
	rec = request(h, "POST", "/v1/images/edits", `{"prompt":"x","size":"1024x1024","image":"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="}`, "test-secret")
	if rec.Code != 200 || driver.editAccount != accountID || driver.editSize != "1024x1024" {
		t.Fatalf("edit account=%q size=%q status=%d body=%s", driver.editAccount, driver.editSize, rec.Code, rec.Body.String())
	}
}

func TestAdminRoutesRequireSessionNotAPIKey(t *testing.T) {
	_, h := testServer(t, &fakeDriver{})
	rec := request(h, "GET", "/admin/accounts", "", "test-secret")
	if rec.Code != 401 {
		t.Fatalf("API key accepted for admin route: %d", rec.Code)
	}
}

func TestUnknownAPIPathReturnsJSON404(t *testing.T) {
	_, h := testServer(t, &fakeDriver{})
	rec := request(h, "GET", "/v1/does-not-exist", "", "test-secret")
	if rec.Code != 404 || strings.Contains(rec.Body.String(), "<html") {
		t.Fatalf("unknown API path not 404 JSON: %d %s", rec.Code, rec.Body.String())
	}
}

func TestOverviewDoesNotLeakReferenceImage(t *testing.T) {
	s, h := testServer(t, &fakeDriver{})
	task, _ := s.store.CreateTask("image", "p", "p", "", "")
	_, _ = s.store.UpdateTask(task.ID, func(t *model.Task) error {
		t.ReferenceImage = "data:image/png;base64,SUPERSECRET"
		t.ReferenceImages = []string{"SUPERSECRET2"}
		return nil
	})
	cookie := adminLogin(t, h)
	rec := adminRequest(h, "GET", "/admin/overview", "", cookie)
	if strings.Contains(rec.Body.String(), "SUPERSECRET") || strings.Contains(rec.Body.String(), "SUPERSECRET2") {
		t.Fatalf("overview leaked reference image: %s", rec.Body.String())
	}
}

func TestAPIKeyRotation(t *testing.T) {
	s, h := testServer(t, &fakeDriver{})
	cookie := adminLogin(t, h)
	rec := adminRequest(h, "POST", "/admin/apikey/rotate", "", cookie)
	if rec.Code != 200 {
		t.Fatalf("rotate %d %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		APIKey string `json:"api_key"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &payload)
	if payload.APIKey == "" || payload.APIKey == "test-secret" {
		t.Fatalf("unexpected rotated key: %+v", payload)
	}
	if stored, err := s.store.LoadAPIKey(); err != nil || stored != payload.APIKey {
		t.Fatalf("rotated key not persisted: %q %v", stored, err)
	}
	if rec := request(h, "GET", "/v1/models", "", payload.APIKey); rec.Code != 200 {
		t.Fatalf("new key rejected: %d", rec.Code)
	}
	if rec := request(h, "GET", "/v1/models", "", "test-secret"); rec.Code != 401 {
		t.Fatalf("old key accepted: %d", rec.Code)
	}
}

func TestRevealAPIKeyRequiresPassword(t *testing.T) {
	_, h := testServer(t, &fakeDriver{})
	cookie := adminLogin(t, h)
	rec := adminRequest(h, "GET", "/admin/apikey", "", cookie)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "test-secret") {
		t.Fatalf("masked key leaked: %d %s", rec.Code, rec.Body.String())
	}
	rec = adminRequest(h, "POST", "/admin/apikey/reveal", `{"password":"wrong"}`, cookie)
	if rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), "test-secret") {
		t.Fatalf("wrong password: %d %s", rec.Code, rec.Body.String())
	}
	rec = adminRequest(h, "POST", "/admin/apikey/reveal", `{"password":"test-admin-pass"}`, cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "test-secret") {
		t.Fatalf("reveal: %d %s", rec.Code, rec.Body.String())
	}
}

func TestVideoDurationPolicy(t *testing.T) {
	for _, duration := range []int{-1, 0, 1, 5, 6, 7, 8, 9, 10} {
		if _, err := normalizeVideoDuration(duration); err != nil {
			t.Fatalf("duration %d rejected: %v", duration, err)
		}
	}
	for _, duration := range []int{11, 60} {
		if _, err := normalizeVideoDuration(duration); err == nil {
			t.Fatalf("duration %d should be rejected", duration)
		}
	}
}

func TestStaticConsole(t *testing.T) {
	_, h := testServer(t, &fakeDriver{})
	rec := request(h, "GET", "/", "", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<html") {
		t.Fatalf("console %d", rec.Code)
	}
}

type quotaErrorDriver struct {
	fakeDriver
	err        error
	panicValue any
}

func (d *quotaErrorDriver) QueryQuota(context.Context, protocol.AccountCredentials) (*model.Quota, error) {
	if d.panicValue != nil {
		panic(d.panicValue)
	}
	return nil, d.err
}

func TestQuotaErrorIsExposed(t *testing.T) {
	s, h := testServer(t, &quotaErrorDriver{err: errors.New("找不到设置入口")})
	cookie := adminLogin(t, h)
	id := s.store.ListAccounts()[0].ID
	rec := adminRequest(h, "POST", "/admin/accounts/"+id+"/quota", "", cookie)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "找不到设置入口") {
		t.Fatalf("unexpected %d %s", rec.Code, rec.Body.String())
	}
}

func TestQuotaPanicIsRecovered(t *testing.T) {
	s, h := testServer(t, &quotaErrorDriver{panicValue: "boom"})
	cookie := adminLogin(t, h)
	id := s.store.ListAccounts()[0].ID
	rec := adminRequest(h, "POST", "/admin/accounts/"+id+"/quota", "", cookie)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "额度查询内部错误") {
		t.Fatalf("unexpected %d %s", rec.Code, rec.Body.String())
	}
}

type autoQuotaDriver struct{ fakeDriver }

func (d *autoQuotaDriver) CheckSession(context.Context, protocol.AccountCredentials) (*protocol.Session, error) {
	return &protocol.Session{
		Valid: true, Status: "assigned", CheckedAt: time.Now().Unix(),
		Cookies:      map[string]string{"hatch_sess": "s", "hatch_vml": "v", "hatch_native_auth_device": "d"},
		CookieExpiry: map[string]int64{"hatch_vml": time.Now().Add(time.Hour).Unix()},
	}, nil
}

func (d *autoQuotaDriver) QueryQuota(context.Context, protocol.AccountCredentials) (*model.Quota, error) {
	return &model.Quota{Found: true, Plan: "Free plan", CheckedAt: time.Now().Unix()}, nil
}

func TestImportAutoChecksAndRefreshesQuota(t *testing.T) {
	s, h := testServer(t, &autoQuotaDriver{})
	cookie := adminLogin(t, h)
	rec := adminRequest(h, "POST", "/admin/accounts", `{"label":"imported","cookie_header":"hatch_sess=s; hatch_vml=v; hatch_native_auth_device=d"}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("import %d %s", rec.Code, rec.Body.String())
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, account := range s.store.ListAccounts() {
			if account.Label == "imported" && account.Quota != nil && account.Quota.Plan == "Free plan" {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("imported account was not auto-checked and refreshed")
}

const tinyImageBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

func tinyImageURL() string { return "data:image/png;base64," + tinyImageBase64 }

func TestImageGenerationAcceptsOrderedReferences(t *testing.T) {
	driver := &recordingDriver{}
	_, h := testServer(t, driver)
	body := fmt.Sprintf(`{"prompt":"x","images":[{"url":%q},{"url":%q}]}`, tinyImageURL(), tinyImageURL())
	rec := request(h, "POST", "/v1/images/generations", body, "test-secret")
	if rec.Code != 200 || len(driver.imageRefs) != 2 {
		t.Fatalf("status=%d refs=%d body=%s", rec.Code, len(driver.imageRefs), rec.Body.String())
	}
	for _, ref := range driver.imageRefs {
		if !strings.HasPrefix(ref, "data:image/png;base64,") {
			t.Fatalf("unexpected normalized reference: %s", ref)
		}
	}
}

func TestImageGenerationRejectsTooManyAndMixedReferences(t *testing.T) {
	driver := &recordingDriver{}
	_, h := testServer(t, driver)
	ref := tinyImageURL()
	tooMany := fmt.Sprintf(`{"prompt":"x","images":[%q,%q,%q,%q,%q]}`, ref, ref, ref, ref, ref)
	if rec := request(h, "POST", "/v1/images/generations", tooMany, "test-secret"); rec.Code != 400 {
		t.Fatalf("too many status=%d body=%s", rec.Code, rec.Body.String())
	}
	mixed := fmt.Sprintf(`{"prompt":"x","reference_image":%q,"images":[%q]}`, ref, ref)
	if rec := request(h, "POST", "/v1/images/generations", mixed, "test-secret"); rec.Code != 400 {
		t.Fatalf("mixed status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestImageEditsAcceptsMultipleMultipartImages(t *testing.T) {
	driver := &recordingDriver{}
	_, h := testServer(t, driver)
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	if err := writer.WriteField("prompt", "combine"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="image"; filename="ref-%d.png"`, i))
		header.Set("Content-Type", "image/png")
		part, err := writer.CreatePart(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte("png-content")); err != nil {
			t.Fatal(err)
		}

	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/v1/images/edits", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer test-secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || len(driver.editRefs) != 2 {
		t.Fatalf("status=%d refs=%d body=%s", rec.Code, len(driver.editRefs), rec.Body.String())
	}
}

func TestVideoAcceptsOrderedReferences(t *testing.T) {
	driver := &recordingDriver{}
	_, h := testServer(t, driver)
	ref := tinyImageURL()
	body := fmt.Sprintf(`{"prompt":"move","images":[{"url":%q},{"url":%q}]}`, ref, ref)
	rec := request(h, "POST", "/v1/videos", body, "test-secret")
	if rec.Code != 202 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(driver.videoRefs) != 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(driver.videoRefs) != 2 {
		t.Fatalf("video refs=%d body=%s", len(driver.videoRefs), rec.Body.String())
	}
}

func TestImageTaskRejectsBadReferenceBeforeQueue(t *testing.T) {
	s, h := testServer(t, &recordingDriver{})
	rec := request(h, "POST", "/v1/images/tasks", `{"prompt":"x","images":["http://127.0.0.1:1/x.png"]}`, "test-secret")
	if rec.Code != 400 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if tasks := s.store.ListTasks(1); len(tasks) != 0 {
		t.Fatalf("task created before reference validation: %+v", tasks)
	}
}
