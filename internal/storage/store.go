package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"muse2api/internal/model"
)

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("conflict")

type diskAccounts struct {
	Version   int             `json:"version"`
	Encrypted bool            `json:"encrypted"`
	Payload   string          `json:"payload,omitempty"`
	Accounts  []model.Account `json:"accounts,omitempty"`
}
type diskCredentials struct {
	Cookies      map[string]string `json:"cookies"`
	CookieExpiry map[string]int64  `json:"cookies_exp"`
}

type diskAPIKey struct {
	Version int    `json:"version"`
	Payload string `json:"payload"`
}

type Store struct {
	mu         sync.RWMutex
	dir        string
	box        *sealBox
	insecure   bool
	keepLegacy bool
	accounts   map[string]model.Account
	tasks      map[string]model.Task
	events     []model.Event
}

type Options struct {
	CredentialKey       []byte
	AllowInsecure       bool
	KeepLegacyPlaintext bool
}

func Open(dir string, key []byte, allowInsecure bool) (*Store, error) {
	return OpenWithOptions(dir, Options{CredentialKey: key, AllowInsecure: allowInsecure})
}

func OpenWithOptions(dir string, opts Options) (*Store, error) {
	if len(opts.CredentialKey) == 0 && !opts.AllowInsecure {
		return nil, errors.New("credential key is required; set MUSE2API_CREDENTIAL_KEY or explicitly enable insecure mode")
	}
	if err := os.MkdirAll(filepath.Join(dir, "backups"), 0o700); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, insecure: opts.AllowInsecure, keepLegacy: opts.KeepLegacyPlaintext, accounts: map[string]model.Account{}, tasks: map[string]model.Task{}}
	if len(opts.CredentialKey) > 0 {
		box, err := newSealBox(opts.CredentialKey)
		if err != nil {
			return nil, err
		}
		s.box = box
	}
	securePath := filepath.Join(dir, "accounts.secure.json")
	if _, err := os.Stat(securePath); err == nil {
		if err := s.loadAccounts(securePath); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	} else if err := s.migrateLegacyAccounts(); err != nil {
		return nil, err
	}
	if err := readJSON(filepath.Join(dir, "tasks.json"), &s.tasks); err != nil {
		return nil, fmt.Errorf("读取任务数据: %w", err)
	}
	if s.tasks == nil {
		s.tasks = map[string]model.Task{}
	}
	if err := readJSON(filepath.Join(dir, "events.json"), &s.events); err != nil {
		return nil, err
	}
	for id, task := range s.tasks {
		normalizeTask(&task)
		if task.Status == model.TaskQueued || task.Status == model.TaskRunning {
			task.Status = model.TaskUnknown
			task.Error = "服务重启后无法确认上游结果，未自动重试"
			task.UpdatedAt = time.Now().Unix()
		}
		s.tasks[id] = task
	}
	return s, nil
}

func (s *Store) migrateLegacyAccounts() error {
	path := filepath.Join(s.dir, "accounts.json")
	var legacy []model.Account
	if err := readJSON(path, &legacy); err != nil {
		return fmt.Errorf("迁移旧账号: %w", err)
	}
	if len(legacy) == 0 {
		return nil
	}
	if s.box == nil && !s.insecure {
		return errors.New("检测到旧版明文账号：请设置 MUSE2API_CREDENTIAL_KEY 后迁移；原文件未被修改")
	}
	backupPath, err := backup(path)
	if err != nil {
		return err
	}
	for _, a := range legacy {
		if a.ID == "" {
			a.ID = randomID(6)
		}
		normalizeAccount(&a)
		s.accounts[a.ID] = a
	}
	if err := s.saveAccountsLocked(); err != nil {
		return err
	}
	if s.box != nil && !s.keepLegacy {
		if err := sanitizeLegacyAccounts(path); err != nil {
			_ = s.appendEventLocked(model.Event{Level: "error", Type: "migration.sanitize", Message: "旧版明文账号文件脱敏失败，请手动删除: " + err.Error()})
		}
		if backupPath != "" {
			if err := sanitizeLegacyAccounts(backupPath); err != nil {
				_ = s.appendEventLocked(model.Event{Level: "error", Type: "migration.sanitize", Message: "旧版明文迁移备份脱敏失败，请手动删除: " + err.Error()})
			}
		}
		return s.appendEventLocked(model.Event{Level: "info", Type: "migration", Message: fmt.Sprintf("已加密迁移 %d 个旧版账号并脱敏旧明文文件；回滚请使用升级前备份", len(legacy))})
	}
	return s.appendEventLocked(model.Event{Level: "info", Type: "migration", Message: fmt.Sprintf("已备份并迁移 %d 个旧版账号；原文件保留", len(legacy))})
}

func (s *Store) loadAccounts(path string) error {
	var disk diskAccounts
	if err := readJSON(path, &disk); err != nil {
		return err
	}
	if disk.Encrypted {
		if s.box == nil {
			return errors.New("账号数据已加密，但 MUSE2API_CREDENTIAL_KEY 未设置；不会覆盖数据")
		}
		plaintext, err := s.box.Open(disk.Payload)
		if err != nil {
			return err
		}
		var credentials map[string]diskCredentials
		if err := json.Unmarshal(plaintext, &credentials); err != nil {
			return err
		}
		for _, a := range disk.Accounts {
			c := credentials[a.ID]
			a.Cookies, a.CookieExpiry = c.Cookies, c.CookieExpiry
			normalizeAccount(&a)
			s.accounts[a.ID] = a
		}
		return nil
	}
	if !s.insecure && s.box == nil {
		return errors.New("账号数据为开发用明文格式；请配置加密密钥")
	}
	for _, a := range disk.Accounts {
		normalizeAccount(&a)
		s.accounts[a.ID] = a
	}
	return nil
}

func (s *Store) saveAccountsLocked() error {
	if s.box == nil && !s.insecure {
		return errors.New("refusing to write plaintext credentials without explicit insecure mode")
	}
	disk := diskAccounts{Version: 1, Encrypted: s.box != nil}
	credentials := map[string]diskCredentials{}
	for id, a := range s.accounts {
		if s.box == nil {
			// Insecure development mode deliberately persists plaintext credentials.
			disk.Accounts = append(disk.Accounts, a)
			continue
		}
		public := a
		public.Cookies, public.CookieExpiry = nil, nil
		disk.Accounts = append(disk.Accounts, public)
		credentials[id] = diskCredentials{a.Cookies, a.CookieExpiry}
	}
	sort.Slice(disk.Accounts, func(i, j int) bool { return disk.Accounts[i].CreatedAt < disk.Accounts[j].CreatedAt })
	if s.box != nil {
		plaintext, err := json.Marshal(credentials)
		if err != nil {
			return err
		}
		payload, err := s.box.Seal(plaintext)
		if err != nil {
			return err
		}
		disk.Payload = payload
	}
	return writeJSON(filepath.Join(s.dir, "accounts.secure.json"), disk)
}

func (s *Store) ListAccounts() []model.Account {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.Account, 0, len(s.accounts))
	for _, a := range s.accounts {
		a = cloneAccount(a)
		a.Cookies, a.CookieExpiry = nil, nil
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out
}
func (s *Store) GetAccount(id string) (model.Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.accounts[id]
	if !ok {
		return model.Account{}, ErrNotFound
	}
	return cloneAccount(a), nil
}
func (s *Store) AddAccount(label string, cookies map[string]string, expires map[string]int64) (model.Account, error) {
	if len(cookies) == 0 {
		return model.Account{}, errors.New("cookies required")
	}
	now := time.Now().Unix()
	a := model.Account{ID: randomID(6), Label: label, Cookies: cloneStrings(cookies), CookieExpiry: cloneInt64s(expires), Enabled: true, CreatedAt: now, MaxConcurrency: 1, Status: model.AccountUnknown}
	if a.Label == "" {
		a.Label = a.ID
	}
	normalizeAccount(&a)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accounts[a.ID] = a
	if err := s.saveAccountsLocked(); err != nil {
		delete(s.accounts, a.ID)
		return model.Account{}, err
	}
	if err := s.appendEventLocked(model.Event{Level: "info", Type: "account.import", Message: "账号凭据已导入", AccountID: a.ID}); err != nil {
		return model.Account{}, err
	}
	return sanitizeAccount(a), nil
}
func (s *Store) UpdateAccount(id string, mutate func(*model.Account) error) (model.Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.accounts[id]
	if !ok {
		return model.Account{}, ErrNotFound
	}
	a := cloneAccount(old)
	if err := mutate(&a); err != nil {
		return model.Account{}, err
	}
	normalizeAccount(&a)
	s.accounts[id] = a
	if err := s.saveAccountsLocked(); err != nil {
		s.accounts[id] = old
		return model.Account{}, err
	}
	return sanitizeAccount(a), nil
}
func (s *Store) DeleteAccount(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.accounts[id]
	if !ok {
		return ErrNotFound
	}
	delete(s.accounts, id)
	if err := s.saveAccountsLocked(); err != nil {
		s.accounts[id] = old
		return err
	}
	return s.appendEventLocked(model.Event{Level: "info", Type: "account.delete", Message: "账号已删除", AccountID: id})
}
func (s *Store) AccountStats() map[string]int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[string]int{"total": len(s.accounts)}
	now := time.Now().Unix()
	for _, a := range s.accounts {
		if a.Enabled {
			out["enabled"]++
		} else {
			out["disabled"]++
		}
		if a.Status == model.AccountActive {
			out["healthy"]++
			out["active"]++
		}
		if a.Status == model.AccountExpired || a.Status == model.AccountError {
			out["error"]++
		}
		if a.ExpiresAt > 0 && a.ExpiresAt <= now {
			out["expired"]++
		} else if a.ExpiresAt > 0 && a.ExpiresAt-now < 3*86400 {
			out["expiring"]++
		}
	}
	return out
}

func (s *Store) CreateTask(kind, prompt, apiPrompt, key, hash string) (model.Task, error) {
	t, _, err := s.FindOrCreateTask(model.Task{Kind: kind, Prompt: prompt, APIPrompt: apiPrompt, IdempotencyKey: key, RequestHash: hash})
	return t, err
}

// FindOrCreateTask makes idempotency check + insert one atomic operation.
func (s *Store) FindOrCreateTask(t model.Task) (model.Task, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.IdempotencyKey != "" {
		for _, existing := range s.tasks {
			// Idempotency is scoped by task kind: the same client header may
			// legitimately appear on an image and a video request.
			if existing.Kind == t.Kind && existing.IdempotencyKey == t.IdempotencyKey {
				if existing.RequestHash != t.RequestHash {
					return model.Task{}, false, ErrConflict
				}
				return cloneTask(existing), false, nil
			}
		}
	}
	now := time.Now().Unix()
	if t.ID == "" {
		t.ID = "task_" + randomID(10)
	}
	t.Object = "task"
	t.Status = model.TaskQueued
	t.CreatedAt = now
	t.UpdatedAt = now
	s.tasks[t.ID] = t
	if err := s.saveTasksLocked(); err != nil {
		delete(s.tasks, t.ID)
		return model.Task{}, false, err
	}
	return cloneTask(t), true, nil
}
func (s *Store) UpdateTask(id string, mutate func(*model.Task) error) (model.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.tasks[id]
	if !ok {
		return model.Task{}, ErrNotFound
	}
	t := cloneTask(old)
	if err := mutate(&t); err != nil {
		return model.Task{}, err
	}
	if !model.ValidTaskTransition(old.Status, t.Status) {
		return model.Task{}, fmt.Errorf("%w: invalid task transition %s -> %s", ErrConflict, old.Status, t.Status)
	}
	t.UpdatedAt = time.Now().Unix()
	s.tasks[id] = t
	if err := s.saveTasksLocked(); err != nil {
		s.tasks[id] = old
		return model.Task{}, err
	}
	return cloneTask(t), nil
}
func (s *Store) GetTask(id string) (model.Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tasks[id]
	if !ok {
		return model.Task{}, ErrNotFound
	}
	return cloneTask(t), nil
}

// ClaimTask atomically moves a queued task to running. Two workers that
// receive the same ID (duplicate enqueue, retry, or operator action) cannot
// both claim it, preventing duplicate upstream generation.
func (s *Store) ClaimTask(id string) (model.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.tasks[id]
	if !ok {
		return model.Task{}, ErrNotFound
	}
	if old.Status != model.TaskQueued {
		return model.Task{}, fmt.Errorf("%w: task %s is %s, not queued", ErrConflict, id, old.Status)
	}
	now := time.Now()
	t := cloneTask(old)
	t.Status = model.TaskRunning
	t.StartedAt = now.Unix()
	t.Progress = 5
	t.UpdatedAt = now.Unix()
	s.tasks[id] = t
	if err := s.saveTasksLocked(); err != nil {
		s.tasks[id] = old
		return model.Task{}, err
	}
	return cloneTask(t), nil
}
func (s *Store) ListTasks(limit int) []model.Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.Task, 0, len(s.tasks))
	for _, task := range s.tasks {
		out = append(out, task)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt == out[j].CreatedAt {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt > out[j].CreatedAt
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	// Deep-copy only the rows that leave the store. A dashboard asking for
	// 20 rows must not serialize every historical task result, and list views
	// never need the (potentially tens of MiB) reference payload.
	for i := range out {
		out[i] = cloneTask(out[i])
		out[i].ReferenceImage = ""
		out[i].ReferenceImages = nil
	}
	return out
}

// TaskCounts is a read-only projection for dashboards. It avoids cloning
// result payloads for every historical task.
type TaskCounts struct {
	RequestsToday  int
	Running        int
	Queued         int
	ImageTotal     int
	ImageSucceeded int
	VideoTotal     int
	VideoSucceeded int
}

func (s *Store) TaskCounts(since int64) TaskCounts {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var counts TaskCounts
	for _, task := range s.tasks {
		switch task.Status {
		case model.TaskRunning:
			counts.Running++
		case model.TaskQueued:
			counts.Queued++
		}
		if task.CreatedAt >= since {
			counts.RequestsToday++
		}
		switch task.Kind {
		case "image":
			counts.ImageTotal++
			if task.Status == model.TaskSucceeded {
				counts.ImageSucceeded++
			}
		case "video":
			counts.VideoTotal++
			if task.Status == model.TaskSucceeded {
				counts.VideoSucceeded++
			}
		}
	}
	return counts
}
func (s *Store) DeleteTask(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.tasks[id]
	if !ok {
		return ErrNotFound
	}
	delete(s.tasks, id)
	if err := s.saveTasksLocked(); err != nil {
		s.tasks[id] = old
		return err
	}
	return nil
}
func (s *Store) ClearTasks(keep int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.tasks
	active := make([]model.Task, 0)
	terminal := make([]model.Task, 0)
	for _, t := range old {
		if t.Status == model.TaskRunning {
			active = append(active, t)
		} else {
			terminal = append(terminal, t)
		}
	}
	sort.Slice(terminal, func(i, j int) bool { return terminal[i].CreatedAt > terminal[j].CreatedAt })
	if keep > 0 && len(terminal) > keep {
		terminal = terminal[:keep]
	} else if keep <= 0 {
		terminal = terminal[:0]
	}
	next := make(map[string]model.Task, len(active)+len(terminal))
	for _, t := range active {
		next[t.ID] = t
	}
	for _, t := range terminal {
		next[t.ID] = t
	}
	removed := len(old) - len(next)
	s.tasks = next
	if err := s.saveTasksLocked(); err != nil {
		s.tasks = old
		return 0
	}
	return removed
}
func (s *Store) listTasksLocked(limit int) []model.Task {
	out := make([]model.Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}
func (s *Store) saveTasksLocked() error {
	return writeJSON(filepath.Join(s.dir, "tasks.json"), s.tasks)
}

func (s *Store) AddEvent(e model.Event) { s.mu.Lock(); defer s.mu.Unlock(); _ = s.appendEventLocked(e) }
func (s *Store) appendEventLocked(e model.Event) error {
	if e.ID == "" {
		e.ID = randomID(8)
	}
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	if e.Level == "" {
		e.Level = "info"
	}
	s.events = append([]model.Event{e}, s.events...)
	if len(s.events) > 1000 {
		s.events = s.events[:1000]
	}
	return writeJSON(filepath.Join(s.dir, "events.json"), s.events)
}
func (s *Store) Events(limit int) []model.Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > len(s.events) {
		limit = len(s.events)
	}
	return append([]model.Event(nil), s.events[:limit]...)
}

func (s *Store) Close() error { return nil }
func normalizeAccount(a *model.Account) {
	a.CookieCount = len(a.Cookies)
	a.EssentialOK = a.Cookies["hatch_sess"] != "" && a.Cookies["hatch_vml"] != ""
	if a.MaxConcurrency <= 0 {
		a.MaxConcurrency = 1
	}
	if a.Status == "" {
		if !a.Enabled {
			a.Status = model.AccountDisabled
		} else if a.OK != nil && !*a.OK {
			a.Status = model.AccountError
		} else if a.OK != nil && *a.OK {
			a.Status = model.AccountActive
		} else {
			a.Status = model.AccountUnknown
		}
	}
	if !a.Enabled {
		a.Status = model.AccountDisabled
	}
	if exp := a.CookieExpiry["hatch_vml"]; exp > 0 {
		a.ExpiresAt = exp
		a.ExpiryEstimated = false
	} else if a.ExpiresAt == 0 && a.CreatedAt > 0 {
		a.ExpiresAt = a.CreatedAt + 2*86400
		a.ExpiryEstimated = true
	}
}
func sanitizeAccount(a model.Account) model.Account { a.Cookies = nil; a.CookieExpiry = nil; return a }
func cloneAccount(a model.Account) model.Account {
	a.Cookies = cloneStrings(a.Cookies)
	a.CookieExpiry = cloneInt64s(a.CookieExpiry)
	if a.Quota != nil {
		q := *a.Quota
		if q.ExtraUsedPct != nil {
			value := *q.ExtraUsedPct
			q.ExtraUsedPct = &value
		}
		a.Quota = &q
	}
	if a.OK != nil {
		value := *a.OK
		a.OK = &value
	}
	return a
}
func cloneTask(t model.Task) model.Task {
	if t.ReferenceImages != nil {
		t.ReferenceImages = append([]string(nil), t.ReferenceImages...)
	}
	if t.Result != nil {
		out := make(map[string]any, len(t.Result))
		for key, value := range t.Result {
			out[key] = cloneResultValue(value)
		}
		t.Result = out
	}
	return t
}

func cloneResultValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = cloneResultValue(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = cloneResultValue(item)
		}
		return out
	default:
		return value
	}
}
func normalizeTask(t *model.Task) {
	switch t.Status {
	case "processing":
		t.Status = model.TaskRunning
	case "completed":
		t.Status = model.TaskSucceeded
	case "success", "done":
		t.Status = model.TaskSucceeded
	case "":
		t.Status = model.TaskUnknown
	}
}
func cloneStrings(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
func cloneInt64s(in map[string]int64) map[string]int64 {
	if in == nil {
		return nil
	}
	out := make(map[string]int64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
func randomID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func readJSON(path string, out any) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, out)
}
func writeJSON(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err = tmp.Chmod(0o600); err == nil {
		_, err = tmp.Write(b)
	}
	if err == nil {
		// Flush file data before the rename so a crash cannot leave a
		// zero-length or truncated state file in place of the previous one.
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	syncDir(filepath.Dir(path))
	return nil
}

func syncDir(dir string) {
	handle, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = handle.Sync()
	_ = handle.Close()
}
func backup(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s.%s.bak", filepath.Base(path), time.Now().UTC().Format("20060102T150405Z"))
	dest := filepath.Join(filepath.Dir(path), "backups", name)
	if err := os.WriteFile(dest, b, 0o600); err != nil {
		return "", err
	}
	return dest, nil
}

func sanitizeLegacyAccounts(path string) error {
	var accounts []model.Account
	if err := readJSON(path, &accounts); err != nil {
		return err
	}
	if len(accounts) == 0 {
		return nil
	}
	for i := range accounts {
		accounts[i].Cookies = nil
		accounts[i].CookieExpiry = nil
		accounts[i].CookieCount = 0
		accounts[i].EssentialOK = false
		accounts[i].Note = "legacy plaintext credentials redacted after migration"
	}
	return writeJSON(path, accounts)
}
func (s *Store) Backup(ctx context.Context) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	dir := filepath.Join(s.dir, "backups", "manual-"+time.Now().UTC().Format("20060102T150405Z"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	for _, name := range []string{"accounts.secure.json", "api_key.secure", "tasks.json", "events.json"} {
		b, err := os.ReadFile(filepath.Join(s.dir, name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
			return "", err
		}
	}
	return dir, nil
}
func (s *Store) SaveAPIKey(key string) error {
	if s.box == nil {
		return errors.New("credential key required to persist API key")
	}
	payload, err := s.box.Seal([]byte(key))
	if err != nil {
		return err
	}
	return writeJSON(filepath.Join(s.dir, "api_key.secure"), diskAPIKey{Version: 1, Payload: payload})
}

func (s *Store) LoadAPIKey() (string, error) {
	var disk diskAPIKey
	if err := readJSON(filepath.Join(s.dir, "api_key.secure"), &disk); err != nil {
		return "", err
	}
	if disk.Payload == "" {
		return "", nil
	}
	if s.box == nil {
		return "", errors.New("stored API key is encrypted but credential key is missing")
	}
	plaintext, err := s.box.Open(disk.Payload)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func SafeFilename(name string) bool {
	return name != "" && name == filepath.Base(name) && !strings.Contains(name, "..") && !strings.ContainsAny(name, "/\\\x00")
}
