package hatch

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"muse2api/internal/storage"
)

func credentialKeyFromEnvFile(home string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(home, ".env"))
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "MUSE2API_CREDENTIAL_KEY=") {
			value := strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "MUSE2API_CREDENTIAL_KEY=")), "\"'")
			return base64.StdEncoding.DecodeString(value)
		}
	}
	return nil, errors.New("MUSE2API_CREDENTIAL_KEY not found in .env")
}

// TestLiveGatewayHandshake validates the real protocol end to end against a
// stored account: HTTP bootstrap, Noise XX handshake, then one unary RPC.
func TestLiveGatewayHandshake(t *testing.T) {
	if os.Getenv("HATCH_LIVE") == "" {
		t.Skip("set HATCH_LIVE=1 to run the live gateway test")
	}
	home := os.Getenv("MUSE2API_HOME")
	if home == "" {
		home = `E:\me-project\muse2api`
	}
	key, err := credentialKeyFromEnvFile(home)
	if err != nil {
		t.Fatalf("credential key: %v", err)
	}
	store, err := storage.OpenWithOptions(filepath.Join(home, "data"), storage.Options{CredentialKey: key})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	accounts := store.ListAccounts()
	if len(accounts) == 0 {
		t.Fatalf("no account in store")
	}
	account, err := store.GetAccount(accounts[0].ID)
	if err != nil {
		t.Fatalf("get account: %v", err)
	}
	names := make([]string, 0, len(account.Cookies))
	for name := range account.Cookies {
		names = append(names, name)
	}
	sort.Strings(names)
	t.Logf("account=%s cookies=%d names=%v", account.ID, len(account.Cookies), names)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	base, _ := url.Parse("https://muse.ai")
	session, err := Bootstrap(ctx, SessionOptions{
		BaseURL: base,
		Cookies: account.Cookies,
		Client:  &http.Client{Timeout: 25 * time.Second},
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	t.Logf("vm=%s", session.VMID)

	conn, err := Dial(ctx, session.WSURL)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	result, err := conn.Request(ctx, http.MethodGet, "/model", nil, 20*time.Second)
	if err != nil {
		t.Fatalf("rpc /model: %v", err)
	}
	t.Logf("model: status=%d body=%s", result.Status, truncate(result.Body, 300))
	if result.Status != 200 {
		t.Fatalf("unexpected status %d", result.Status)
	}
}
