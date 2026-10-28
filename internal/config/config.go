package config

import (
	"bufio"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Home                     string
	Host                     string
	Port                     int
	APIKey                   string
	PublicBase               string
	SiteURL                  string
	CORSOrigins              []string
	Concurrency              int
	QueueSize                int
	ImageTimeout             time.Duration
	VideoTimeout             time.Duration
	ChatTimeout              time.Duration
	HTTPTimeout              time.Duration
	MaxUploadBytes           int64
	MaxMediaBytes            int64
	RefImageLocalFiles       bool
	RefImagePrivateFetch     bool
	CredentialKey            []byte
	AllowInsecureCredentials bool
	KeepLegacyPlaintext      bool
	AdminUsername            string
	AdminPassword            string
	SessionTTL               time.Duration
	TrustProxy               bool
	PublicMedia              bool
	LogLevel                 string
	ProxyURL                 string
	BrowserEnabled           bool
	Chromium                 string
	CDPPort                  int
	LoginWait                time.Duration
	ProfileDir               string
	DownloadDir              string
}

func Load(homeOverride string) (Config, error) {
	home := env("MUSE2API_HOME", homeOverride)
	if home == "" {
		home = "."
	}
	if err := loadDotEnv(filepath.Join(home, ".env")); err != nil {
		return Config{}, err
	}
	home = env("MUSE2API_HOME", home)
	if home == "" {
		home = "."
	}
	abs, err := filepath.Abs(home)
	if err != nil {
		return Config{}, err
	}
	apiKey, err := secretEnv("MUSE2API_KEY")
	if err != nil {
		return Config{}, err
	}
	adminPassword, err := secretEnv("MUSE2API_ADMIN_PASSWORD")
	if err != nil {
		return Config{}, err
	}
	credentialValue, err := secretEnv("MUSE2API_CREDENTIAL_KEY")
	if err != nil {
		return Config{}, err
	}
	proxyURL, err := secretEnv("MUSE2API_PROXY")
	if err != nil {
		return Config{}, err
	}
	key, err := decodeCredentialKey(credentialValue)
	if err != nil {
		return Config{}, err
	}
	c := Config{
		Home:                     abs,
		Host:                     env("MUSE2API_HOST", "0.0.0.0"),
		APIKey:                   apiKey,
		PublicBase:               strings.TrimRight(os.Getenv("MUSE2API_PUBLIC_BASE"), "/"),
		SiteURL:                  env("MUSE2API_SITE", "https://muse.ai"),
		CORSOrigins:              split(env("MUSE2API_CORS_ORIGINS", "")),
		Concurrency:              2,
		QueueSize:                64,
		MaxUploadBytes:           40 << 20,
		MaxMediaBytes:            512 << 20,
		CredentialKey:            key,
		AllowInsecureCredentials: boolEnv("MUSE2API_ALLOW_INSECURE_CREDENTIALS"),
		KeepLegacyPlaintext:      boolEnv("MUSE2API_KEEP_LEGACY_PLAINTEXT"),
		RefImageLocalFiles:       boolEnv("MUSE2API_REF_IMAGE_LOCAL_FILES"),
		RefImagePrivateFetch:     boolEnv("MUSE2API_REF_IMAGE_PRIVATE_FETCH"),
		AdminUsername:            strings.TrimSpace(env("MUSE2API_ADMIN_USERNAME", "admin")),
		AdminPassword:            adminPassword,
		TrustProxy:               boolEnv("MUSE2API_TRUST_PROXY"),
		PublicMedia:              boolEnv("MUSE2API_PUBLIC_MEDIA"),
		LogLevel:                 env("MUSE2API_LOG_LEVEL", "info"),
		ProxyURL:                 proxyURL,
	}
	if c.AdminUsername == "" {
		c.AdminUsername = "admin"
	}
	c.BrowserEnabled = boolEnvDefault("MUSE2API_BROWSER", true)
	c.Chromium = detectChromium()
	profileRoot := env("MUSE2API_PROFILE_ROOT", filepath.Join(c.DataDir(), "chromium"))
	c.ProfileDir = env("MUSE2API_PROFILE_DIR", filepath.Join(profileRoot, "generate"))
	c.DownloadDir = env("MUSE2API_DOWNLOAD_DIR", filepath.Join(c.DataDir(), "downloads"))
	if c.Port, err = intEnv("MUSE2API_PORT", 18610, 1, 65535); err != nil {
		return Config{}, err
	}
	if c.Concurrency, err = intEnv("MUSE2API_CONCURRENCY", 2, 1, 128); err != nil {
		return Config{}, err
	}
	if c.QueueSize, err = intEnv("MUSE2API_QUEUE_SIZE", 64, 1, 10000); err != nil {
		return Config{}, err
	}
	if c.ImageTimeout, err = secondsEnv("MUSE2API_IMAGE_TIMEOUT", 240); err != nil {
		return Config{}, err
	}
	if c.VideoTimeout, err = secondsEnv("MUSE2API_VIDEO_TIMEOUT", 600); err != nil {
		return Config{}, err
	}
	if c.ChatTimeout, err = secondsEnv("MUSE2API_CHAT_TIMEOUT", 300); err != nil {
		return Config{}, err
	}
	if c.HTTPTimeout, err = secondsEnv("MUSE2API_HTTP_TIMEOUT", 20); err != nil {
		return Config{}, err
	}
	if c.CDPPort, err = intEnv("MUSE2API_CDP_PORT", 19210, 1, 65535); err != nil {
		return Config{}, err
	}
	if c.LoginWait, err = secondsEnv("MUSE2API_LOGIN_WAIT", 60); err != nil {
		return Config{}, err
	}
	ttlSeconds, err := intEnv("MUSE2API_SESSION_TTL", 12*3600, 300, 7*24*3600)
	if err != nil {
		return Config{}, err
	}
	c.SessionTTL = time.Duration(ttlSeconds) * time.Second
	if c.MaxUploadBytes, err = int64Env("MUSE2API_MAX_UPLOAD_BYTES", 40<<20, 1024, 1<<30); err != nil {
		return Config{}, err
	}
	if c.MaxMediaBytes, err = int64Env("MUSE2API_MAX_MEDIA_BYTES", 512<<20, 1024, 20<<30); err != nil {
		return Config{}, err
	}
	if c.PublicBase != "" {
		if _, err = safeHTTPURL(c.PublicBase); err != nil {
			return Config{}, fmt.Errorf("MUSE2API_PUBLIC_BASE: %w", err)
		}
	}
	if _, err = safeHTTPURL(c.SiteURL); err != nil {
		return Config{}, fmt.Errorf("MUSE2API_SITE: %w", err)
	}
	if c.APIKey == "" {
		return Config{}, errors.New("MUSE2API_KEY 必须设置；不再支持默认匿名管理")
	}
	if c.APIKey == "m2a_replace_with_a_long_random_secret" {
		return Config{}, errors.New("MUSE2API_KEY 仍是 .env.example 占位符，请替换为随机值")
	}
	if len(c.CredentialKey) == 0 && !c.AllowInsecureCredentials {
		return Config{}, errors.New("MUSE2API_CREDENTIAL_KEY 必须设置；开发环境如确需明文存储，请显式设置 MUSE2API_ALLOW_INSECURE_CREDENTIALS=1")
	}
	if c.AdminPassword == "" {
		return Config{}, errors.New("MUSE2API_ADMIN_PASSWORD 必须设置；后台使用独立的单管理员登录")
	}
	if c.AdminPassword == "replace_with_a_strong_admin_password" {
		return Config{}, errors.New("MUSE2API_ADMIN_PASSWORD 仍是 .env.example 占位符，请替换为强密码")
	}
	if len(c.AdminPassword) < 8 {
		return Config{}, errors.New("MUSE2API_ADMIN_PASSWORD 至少需要 8 个字符")
	}
	if c.ProxyURL != "" {
		if _, err = url.ParseRequestURI(c.ProxyURL); err != nil {
			return Config{}, fmt.Errorf("MUSE2API_PROXY 无效")
		}
	}
	return c, nil
}

func (c Config) DataDir() string  { return filepath.Join(c.Home, "data") }
func (c Config) MediaDir() string { return filepath.Join(c.DataDir(), "media") }
func (c Config) Address() string  { return net.JoinHostPort(c.Host, strconv.Itoa(c.Port)) }
func (c Config) EnsureDirs() error {
	for _, dir := range []string{c.DataDir(), c.MediaDir(), filepath.Join(c.DataDir(), "backups")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		// Existing named volumes may have been created with 0755. Tighten
		// them on startup instead of trusting the volume's original mode.
		if err := os.Chmod(dir, 0o700); err != nil {
			return err
		}
	}
	return nil
}

// secretEnv reads a secret from <NAME>_FILE when configured, otherwise from
// the environment. Docker Compose/ Swarm secrets are mounted as files; the
// file form avoids exposing credentials in `docker inspect` and process env.
func secretEnv(name string) (string, error) {
	path := strings.TrimSpace(os.Getenv(name + "_FILE"))
	if path == "" {
		return os.Getenv(name), nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("%s_FILE: 无法读取密钥文件: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s_FILE: 密钥路径不是普通文件", name)
	}
	if info.Size() > 64<<10 {
		return "", fmt.Errorf("%s_FILE: 密钥文件超过 64 KiB", name)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("%s_FILE: 无法读取密钥文件: %w", name, err)
	}
	return strings.TrimRight(string(data), "\r\n"), nil
}

func loadDotEnv(path string) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		if _, exists := os.LookupEnv(k); !exists && k != "" {
			if err := os.Setenv(k, strings.Trim(strings.TrimSpace(v), "\"'")); err != nil {
				return err
			}
		}
	}
	return s.Err()
}
func decodeCredentialKey(value string) ([]byte, error) {
	if value == "" {
		return nil, nil
	}
	b, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(b) != 32 {
		return nil, errors.New("MUSE2API_CREDENTIAL_KEY 必须是 32 字节的 base64")
	}
	return b, nil
}
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func split(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
func boolEnv(k string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(k)))
	return v == "1" || v == "true" || v == "yes"
}
func boolEnvDefault(k string, fallback bool) bool {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return fallback
	}
	return boolEnv(k)
}
func detectChromium() string {
	if configured := strings.TrimSpace(os.Getenv("MUSE2API_CHROMIUM")); configured != "" {
		return configured
	}
	for _, candidate := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable", `C:\Program Files\Google\Chrome\Application\chrome.exe`, `C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
	}
	return "chromium"
}
func intEnv(k string, d, min, max int) (int, error) {
	v := os.Getenv(k)
	if v == "" {
		return d, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < min || n > max {
		return 0, fmt.Errorf("%s 必须是 %d..%d 的整数", k, min, max)
	}
	return n, nil
}
func int64Env(k string, d, min, max int64) (int64, error) {
	v := os.Getenv(k)
	if v == "" {
		return d, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < min || n > max {
		return 0, fmt.Errorf("%s 超出有效范围", k)
	}
	return n, nil
}
func secondsEnv(k string, d int) (time.Duration, error) {
	n, err := intEnv(k, d, 1, 86400)
	return time.Duration(n) * time.Second, err
}
func safeHTTPURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return "", errors.New("仅允许无凭据的 http/https URL")
	}
	return raw, nil
}
