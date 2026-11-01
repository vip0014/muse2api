package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"muse2api/internal/model"
)

func (s *Store) StageRestore(ctx context.Context, backupDir string) error {
	src := filepath.Join(backupDir, "muse2api.db")
	if info, statErr := os.Stat(src); statErr != nil || !info.Mode().IsRegular() {
		return errors.New("备份数据库不存在")
	}
	check, err := sql.Open("sqlite", src)
	if err != nil {
		return fmt.Errorf("打开备份数据库失败: %w", err)
	}
	var integrity string
	if err := check.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil {
		_ = check.Close()
		return fmt.Errorf("校验备份数据库失败: %w", err)
	}
	if !strings.EqualFold(integrity, "ok") {
		_ = check.Close()
		return fmt.Errorf("备份数据库完整性检查失败: %s", integrity)
	}
	var tables int
	if err := check.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='accounts'`).Scan(&tables); err != nil || tables == 0 {
		_ = check.Close()
		return errors.New("备份数据库缺少 accounts 表")
	}
	_ = check.Close()
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()
	staged := filepath.Join(s.dir, "muse2api.db.restore")
	tmp, err := os.CreateTemp(s.dir, ".restore-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := io.Copy(tmp, srcFile); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, staged); err != nil {
		return err
	}
	syncDir(s.dir)
	return nil
}

func applyStagedRestore(dir string) error {
	staged := filepath.Join(dir, "muse2api.db.restore")
	if _, err := os.Stat(staged); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	current := filepath.Join(dir, "muse2api.db")
	if _, err := os.Stat(current); err == nil {
		backupName := fmt.Sprintf("%s.before-restore-%s", current, time.Now().UTC().Format("20060102T150405.000000000Z"))
		if err := os.Rename(current, backupName); err != nil {
			return err
		}
	}
	// A stale WAL from the replaced database must not be applied to the
	// restored snapshot.
	_ = os.Remove(current + "-wal")
	_ = os.Remove(current + "-shm")
	if err := os.Rename(staged, current); err != nil {
		return err
	}
	syncDir(dir)
	return nil
}

func (s *Store) Backup(ctx context.Context) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Join(s.dir, "backups", "manual-"+time.Now().UTC().Format("20060102T150405.000000000Z"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	target := filepath.Join(dir, "muse2api.db")
	escaped := strings.ReplaceAll(target, "'", "''")
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO '"+escaped+"'"); err != nil {
		return "", err
	}
	if err := os.Chmod(target, 0o600); err != nil {
		return "", err
	}
	syncDir(dir)
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
	_, err = s.db.Exec(`INSERT INTO api_key(id, payload, updated_at) VALUES (1, ?, ?)
ON CONFLICT(id) DO UPDATE SET payload = excluded.payload, updated_at = excluded.updated_at`, payload, time.Now().Unix())
	return err
}

func (s *Store) LoadAPIKey() (string, error) {
	var payload string
	if err := s.db.QueryRow(`SELECT payload FROM api_key WHERE id = 1`).Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	if payload == "" {
		return "", nil
	}
	if s.box == nil {
		return "", errors.New("stored API key is encrypted but credential key is missing")
	}
	plaintext, err := s.box.Open(payload)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

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

func syncDir(dir string) {
	handle, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = handle.Sync()
	_ = handle.Close()
}

func backupFile(path string) (string, error) {
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
	b, err := json.MarshalIndent(accounts, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o600)
}
