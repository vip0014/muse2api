package storage

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"muse2api/internal/model"
)

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("conflict")

// diskAccounts is the legacy JSON envelope. It is kept only for one-way
// migration; new state lives in SQLite.
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

type Options struct {
	CredentialKey       []byte
	AllowInsecure       bool
	KeepLegacyPlaintext bool
}

// Store is a SQLite-backed repository. One connection is used intentionally:
// SQLite serializes writes, and the upstream gateway is the real concurrency
// bottleneck. The schema has indexes for the paths that matter (idempotency,
// queue ordering, task history).
type Store struct {
	mu         sync.Mutex
	dir        string
	db         *sql.DB
	box        *sealBox
	insecure   bool
	keepLegacy bool
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
	var box *sealBox
	if len(opts.CredentialKey) > 0 {
		var err error
		box, err = newSealBox(opts.CredentialKey)
		if err != nil {
			return nil, err
		}
	}
	if err := applyStagedRestore(dir); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(dir, "muse2api.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	_ = os.Chmod(dbPath, 0o600)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	s := &Store{dir: dir, db: db, box: box, insecure: opts.AllowInsecure, keepLegacy: opts.KeepLegacyPlaintext}
	if err := s.configure(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.initSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.migrateLegacy(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.reconcileTasks(); err != nil {
		_ = db.Close()
		return nil, err
	}
	// SQLite creates the database lazily during the first PRAGMA/schema write.
	// Tighten it (and existing WAL sidecars) after the file exists.
	_ = os.Chmod(dbPath, 0o600)
	_ = os.Chmod(dbPath+"-wal", 0o600)
	_ = os.Chmod(dbPath+"-shm", 0o600)
	return s, nil
}

func (s *Store) configure() error {
	stmts := []string{
		`PRAGMA busy_timeout = 5000`,
		`PRAGMA foreign_keys = ON`,
		`PRAGMA synchronous = FULL`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	var mode string
	if err := s.db.QueryRow(`PRAGMA journal_mode = WAL`).Scan(&mode); err != nil {
		return err
	}
	return nil
}

func (s *Store) initSchema() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS meta (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS accounts (
  id TEXT PRIMARY KEY,
  label TEXT NOT NULL,
  status TEXT NOT NULL,
  enabled INTEGER NOT NULL,
  essential_ok INTEGER NOT NULL,
  cookie_count INTEGER NOT NULL,
  created_at INTEGER NOT NULL,
  last_used INTEGER NOT NULL DEFAULT 0,
  last_checked INTEGER NOT NULL DEFAULT 0,
  last_keepalive INTEGER NOT NULL DEFAULT 0,
  use_count INTEGER NOT NULL DEFAULT 0,
  expires_at INTEGER NOT NULL DEFAULT 0,
  expiry_estimated INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  note TEXT NOT NULL DEFAULT '',
  max_concurrency INTEGER NOT NULL DEFAULT 1,
  failure_count INTEGER NOT NULL DEFAULT 0,
  cooldown_until INTEGER NOT NULL DEFAULT 0,
  credentials_version INTEGER NOT NULL DEFAULT 0,
  quota_json TEXT NOT NULL DEFAULT '',
  ok INTEGER,
  synced_at INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS account_credentials (
  account_id TEXT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
  payload TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS tasks (
  id TEXT PRIMARY KEY,
  object TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL,
  prompt TEXT NOT NULL,
  api_prompt TEXT NOT NULL,
  status TEXT NOT NULL,
  progress INTEGER NOT NULL DEFAULT 0,
  account_id TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  started_at INTEGER NOT NULL DEFAULT 0,
  completed_at INTEGER NOT NULL DEFAULT 0,
  elapsed REAL NOT NULL DEFAULT 0,
  error TEXT NOT NULL DEFAULT '',
  result_json TEXT NOT NULL DEFAULT '',
  url TEXT NOT NULL DEFAULT '',
  size TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL DEFAULT '',
  aspect_ratio TEXT NOT NULL DEFAULT '',
  resolution TEXT NOT NULL DEFAULT '',
  duration INTEGER NOT NULL DEFAULT 0,
  response_format TEXT NOT NULL DEFAULT '',
  idempotency_key TEXT NOT NULL DEFAULT '',
  request_hash TEXT NOT NULL DEFAULT '',
  cancel_requested INTEGER NOT NULL DEFAULT 0,
  reference_image TEXT NOT NULL DEFAULT '',
  reference_images_json TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_tasks_idempotency
  ON tasks(kind, idempotency_key) WHERE idempotency_key <> '';
CREATE INDEX IF NOT EXISTS idx_tasks_created ON tasks(created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status, created_at);
CREATE TABLE IF NOT EXISTS events (
  id TEXT PRIMARY KEY,
  time_ns INTEGER NOT NULL,
  level TEXT NOT NULL,
  type TEXT NOT NULL,
  message TEXT NOT NULL,
  request_id TEXT NOT NULL DEFAULT '',
  task_id TEXT NOT NULL DEFAULT '',
  account_id TEXT NOT NULL DEFAULT '',
  upstream_status INTEGER NOT NULL DEFAULT 0,
  duration_ms INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_events_time ON events(time_ns DESC);
CREATE TABLE IF NOT EXISTS api_key (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  payload TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS media (
  name TEXT PRIMARY KEY,
  task_id TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL DEFAULT '',
  mime TEXT NOT NULL DEFAULT '',
  bytes INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_media_created ON media(created_at DESC);
`)
	if err != nil {
		return err
	}
	// Add columns introduced after the first SQLite schema version. SQLite
	// has no IF NOT EXISTS for ADD COLUMN, so inspect the table first.
	accountColumns := []struct{ name, ddl string }{
		{"failure_count", `ALTER TABLE accounts ADD COLUMN failure_count INTEGER NOT NULL DEFAULT 0`},
		{"cooldown_until", `ALTER TABLE accounts ADD COLUMN cooldown_until INTEGER NOT NULL DEFAULT 0`},
		{"credentials_version", `ALTER TABLE accounts ADD COLUMN credentials_version INTEGER NOT NULL DEFAULT 0`},
	}
	for _, column := range accountColumns {
		var count int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('accounts') WHERE name = ?`, column.name).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			if _, err := s.db.Exec(column.ddl); err != nil {
				return err
			}
		}
	}
	for _, column := range []struct{ name, ddl string }{
		{"reference_image", `ALTER TABLE tasks ADD COLUMN reference_image TEXT NOT NULL DEFAULT ''`},
		{"reference_images_json", `ALTER TABLE tasks ADD COLUMN reference_images_json TEXT NOT NULL DEFAULT ''`},
	} {
		var count int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('tasks') WHERE name = ?`, column.name).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			if _, err := s.db.Exec(column.ddl); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

// ---------------------------------------------------------------------------
// legacy migration
// ---------------------------------------------------------------------------

func (s *Store) migrateLegacy() error {
	var marker string
	_ = s.db.QueryRow(`SELECT value FROM meta WHERE key = 'legacy_imported'`).Scan(&marker)
	if marker == "1" {
		return nil
	}
	var accountCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM accounts`).Scan(&accountCount); err != nil {
		return err
	}
	if accountCount > 0 {
		return s.setMeta("legacy_imported", "1")
	}

	accountsPath := filepath.Join(s.dir, "accounts.secure.json")
	legacyPath := filepath.Join(s.dir, "accounts.json")
	tasksPath := filepath.Join(s.dir, "tasks.json")
	eventsPath := filepath.Join(s.dir, "events.json")
	apiKeyPath := filepath.Join(s.dir, "api_key.secure")

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var legacyBackup string
	if _, err := os.Stat(accountsPath); err == nil {
		if err := s.importSecureAccounts(tx, accountsPath); err != nil {
			return err
		}
	} else if _, err := os.Stat(legacyPath); err == nil {
		legacyBackup, err = s.importLegacyAccounts(tx, legacyPath)
		if err != nil {
			return err
		}
	}
	if _, err := os.Stat(tasksPath); err == nil {
		if err := s.importLegacyTasks(tx, tasksPath); err != nil {
			return err
		}
	}
	if _, err := os.Stat(eventsPath); err == nil {
		if err := s.importLegacyEvents(tx, eventsPath); err != nil {
			return err
		}
	}
	if _, err := os.Stat(apiKeyPath); err == nil {
		var disk diskAPIKey
		if err := readJSON(apiKeyPath, &disk); err != nil {
			return err
		}
		if disk.Payload != "" {
			if _, err := tx.Exec(`INSERT OR REPLACE INTO api_key(id, payload, updated_at) VALUES (1, ?, ?)`, disk.Payload, time.Now().Unix()); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// Only redact legacy plaintext after the whole import commits. Doing it
	// inside the transaction could destroy the only copy if a later task/event
	// import failed and rolled back.
	if legacyBackup != "" && s.box != nil && !s.keepLegacy {
		if err := sanitizeLegacyAccounts(legacyPath); err != nil {
			return err
		}
		if err := sanitizeLegacyAccounts(legacyBackup); err != nil {
			return err
		}
	}
	return s.setMeta("legacy_imported", "1")
}

func (s *Store) importSecureAccounts(tx *sql.Tx, path string) error {
	var disk diskAccounts
	if err := readJSON(path, &disk); err != nil {
		return fmt.Errorf("迁移账号数据: %w", err)
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
			if err := s.insertAccountTx(tx, a); err != nil {
				return err
			}
		}
		return nil
	}
	if !s.insecure && s.box == nil {
		return errors.New("账号数据为开发用明文格式；请配置加密密钥")
	}
	for _, a := range disk.Accounts {
		normalizeAccount(&a)
		if err := s.insertAccountTx(tx, a); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) importLegacyAccounts(tx *sql.Tx, path string) (string, error) {
	var legacy []model.Account
	if err := readJSON(path, &legacy); err != nil {
		return "", fmt.Errorf("迁移旧账号: %w", err)
	}
	if len(legacy) == 0 {
		return "", nil
	}
	if s.box == nil && !s.insecure {
		return "", errors.New("检测到旧版明文账号：请设置 MUSE2API_CREDENTIAL_KEY 后迁移；原文件未被修改")
	}
	backupPath, err := backupFile(path)
	if err != nil {
		return "", err
	}
	for _, a := range legacy {
		if a.ID == "" {
			a.ID = randomID(6)
		}
		normalizeAccount(&a)
		if err := s.insertAccountTx(tx, a); err != nil {
			return "", err
		}
	}
	return backupPath, nil
}

func (s *Store) importLegacyTasks(tx *sql.Tx, path string) error {
	var tasks map[string]model.Task
	if err := readJSON(path, &tasks); err != nil {
		return err
	}
	for id, task := range tasks {
		if task.ID == "" {
			task.ID = id
		}
		if err := insertTaskTx(tx, task); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) importLegacyEvents(tx *sql.Tx, path string) error {
	var events []model.Event
	if err := readJSON(path, &events); err != nil {
		return err
	}
	for _, event := range events {
		if event.ID == "" {
			event.ID = randomID(8)
		}
		if event.Time.IsZero() {
			event.Time = time.Now()
		}
		if event.Level == "" {
			event.Level = "info"
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO events(id, time_ns, level, type, message, request_id, task_id, account_id, upstream_status, duration_ms)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, event.ID, event.Time.UnixNano(), event.Level, event.Type, event.Message, event.RequestID, event.TaskID, event.AccountID, event.Status, event.DurationMS); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) reconcileTasks() error {
	// Preserve the old safety rule for this phase: a task that was queued or
	// running when the process stopped is uncertain. Phase 5 will replace this
	// with a durable queue and sub-state reconciliation.
	// A running row with no account never acquired an account lease, so it was
	// never submitted upstream. It is safe to put back in the durable queue.
	if _, err := s.db.Exec(`UPDATE tasks SET status = ?, progress = 0, started_at = 0, completed_at = 0, error = '', updated_at = ? WHERE status = ? AND account_id = ''`,
		string(model.TaskQueued), time.Now().Unix(), string(model.TaskRunning)); err != nil {
		return err
	}
	// A running row with an account may have been submitted and charged before
	// the process stopped; keep it undecided instead of replaying it.
	_, err := s.db.Exec(`UPDATE tasks SET status = ?, error = ?, updated_at = ? WHERE status = ?`,
		string(model.TaskUnknown), "服务重启后无法确认上游结果，未自动重试", time.Now().Unix(),
		string(model.TaskRunning))
	return err
}

func (s *Store) setMeta(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO meta(key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// ---------------------------------------------------------------------------
// accounts
// ---------------------------------------------------------------------------

func randomID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func SafeFilename(name string) bool {
	return name != "" && name == filepath.Base(name) && !strings.Contains(name, "..") && !strings.ContainsAny(name, "/\\\x00")
}
