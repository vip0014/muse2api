package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadAndValidate(t *testing.T) {
	t.Setenv("MUSE2API_KEY", "test-secret")
	t.Setenv("MUSE2API_CREDENTIAL_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("MUSE2API_ADMIN_PASSWORD", "test-admin-pass")
	t.Setenv("MUSE2API_CORS_ORIGINS", "")
	t.Setenv("MUSE2API_PROXY", "")
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != 18610 || c.Concurrency != 2 || c.QueueSize != 64 {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	if !c.BrowserEnabled || c.CDPPort != 19210 || c.LoginWait.Seconds() != 60 {
		t.Fatalf("unexpected browser defaults: %+v", c)
	}
	if err := c.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
}

func TestBrowserCanBeDisabled(t *testing.T) {
	t.Setenv("MUSE2API_KEY", "test-secret")
	t.Setenv("MUSE2API_CREDENTIAL_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("MUSE2API_ADMIN_PASSWORD", "test-admin-pass")
	t.Setenv("MUSE2API_BROWSER", "0")
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if c.BrowserEnabled {
		t.Fatal("MUSE2API_BROWSER=0 did not disable browser execution")
	}
}

func TestRequiresCredentialAndAdminPassword(t *testing.T) {
	t.Setenv("MUSE2API_KEY", "test-secret")
	t.Setenv("MUSE2API_CREDENTIAL_KEY", "")
	t.Setenv("MUSE2API_ADMIN_PASSWORD", "test-admin-pass")
	if _, err := Load(t.TempDir()); err == nil {
		t.Fatal("missing credential key accepted")
	}
	t.Setenv("MUSE2API_CREDENTIAL_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("MUSE2API_ADMIN_PASSWORD", "")
	if _, err := Load(t.TempDir()); err == nil {
		t.Fatal("missing admin password accepted")
	}
	t.Setenv("MUSE2API_ADMIN_PASSWORD", "short")
	if _, err := Load(t.TempDir()); err == nil {
		t.Fatal("short admin password accepted")
	}
	t.Setenv("MUSE2API_ADMIN_PASSWORD", "long-enough-admin-password")
	if _, err := Load(t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

func TestRejectsAnonymousAndBadKey(t *testing.T) {
	t.Setenv("MUSE2API_KEY", "")
	if _, err := Load(t.TempDir()); err == nil {
		t.Fatal("empty API key accepted")
	}
	t.Setenv("MUSE2API_KEY", "secret")
	t.Setenv("MUSE2API_CREDENTIAL_KEY", "bad")
	if _, err := Load(t.TempDir()); err == nil {
		t.Fatal("bad credential key accepted")
	}
}

func TestSecretsFromFilesTakePrecedence(t *testing.T) {
	dir := t.TempDir()
	write := func(name, value string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	credential := base64.StdEncoding.EncodeToString([]byte("01234567890123456789012345678901"))
	t.Setenv("MUSE2API_KEY", "environment-key")
	t.Setenv("MUSE2API_ADMIN_PASSWORD", "environment-password")
	t.Setenv("MUSE2API_CREDENTIAL_KEY", "environment-credential")
	t.Setenv("MUSE2API_KEY_FILE", write("api_key", "file-key\n"))
	t.Setenv("MUSE2API_ADMIN_PASSWORD_FILE", write("admin_password", "file-password\r\n"))
	t.Setenv("MUSE2API_CREDENTIAL_KEY_FILE", write("credential_key", credential+"\n"))
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if c.APIKey != "file-key" || c.AdminPassword != "file-password" {
		t.Fatalf("file secrets did not take precedence: %+v", c)
	}
	if string(c.CredentialKey) != "01234567890123456789012345678901" {
		t.Fatal("credential file decoded incorrectly")
	}
}

func TestSecretFileErrors(t *testing.T) {
	t.Setenv("MUSE2API_KEY_FILE", filepath.Join(t.TempDir(), "missing"))
	if _, err := Load(t.TempDir()); err == nil || !strings.Contains(err.Error(), "MUSE2API_KEY_FILE") {
		t.Fatalf("missing secret file error = %v", err)
	}
}

func TestEnsureDirsTightensExistingPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix directory modes")
	}
	home := t.TempDir()
	cfg := Config{Home: home}
	if err := os.MkdirAll(cfg.DataDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(cfg.DataDir())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("data dir mode = %o, want 700", info.Mode().Perm())
	}
}
