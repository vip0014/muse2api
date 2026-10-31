package hatch

import (
	"errors"
	"net/url"
	"testing"

	"muse2api/internal/model"
)

var errAccountNotFound = errors.New("account not found")

func mustSiteURL(t *testing.T) *url.URL {
	t.Helper()
	site, err := url.Parse("https://muse.ai")
	if err != nil {
		t.Fatalf("parse site: %v", err)
	}
	return site
}

func TestCredentialFingerprint(t *testing.T) {
	cookies := map[string]string{
		"hatch_sess":               "session-a",
		"hatch_vml":                "vml-1",
		"hatch_native_auth_device": "device-a",
	}
	base := credentialFingerprint(cookies)
	if base == "" {
		t.Fatal("empty fingerprint")
	}
	// hatch_vml rotates during bootstrap; it must not force a reconnect.
	cookies["hatch_vml"] = "vml-2"
	if credentialFingerprint(cookies) != base {
		t.Fatal("vml rotation changed identity")
	}
	// A different session belongs to a different account and must reconnect.
	cookies["hatch_sess"] = "session-b"
	if credentialFingerprint(cookies) == base {
		t.Fatal("session change kept the same identity")
	}
}

type stubStore struct {
	accounts map[string]model.Account
}

func (s *stubStore) GetAccount(id string) (model.Account, error) {
	account, ok := s.accounts[id]
	if !ok {
		return model.Account{}, errAccountNotFound
	}
	return account, nil
}

func (s *stubStore) UpdateAccount(id string, mutate func(*model.Account) error) (model.Account, error) {
	account, ok := s.accounts[id]
	if !ok {
		return model.Account{}, errAccountNotFound
	}
	if err := mutate(&account); err != nil {
		return model.Account{}, err
	}
	s.accounts[id] = account
	return account, nil
}

func TestDriverClientIsolation(t *testing.T) {
	driver := NewDriver(DriverConfig{SiteURL: mustSiteURL(t)}, &stubStore{accounts: map[string]model.Account{}})
	first := driver.clientFor("account-a")
	if first == nil {
		t.Fatal("nil client")
	}
	if driver.clientFor("account-a") != first {
		t.Fatal("same account got a different client")
	}
	if driver.clientFor("account-b") == first {
		t.Fatal("different accounts shared one client")
	}
	driver.Close()
	if len(driver.clients) != 0 {
		t.Fatalf("clients not cleared: %d", len(driver.clients))
	}
}
