package server

import (
	"testing"

	"muse2api/internal/model"
)

func TestKeepaliveEligible(t *testing.T) {
	active := model.Account{Enabled: true, Status: model.AccountActive, CookieCount: 2}
	if !keepaliveEligible(active, true) {
		t.Fatal("active account should be kept alive")
	}
	disabled := active
	disabled.Enabled = false
	disabled.Status = model.AccountDisabled
	if keepaliveEligible(disabled, true) {
		t.Fatal("disabled account skipped when configured")
	}
	if !keepaliveEligible(disabled, false) {
		t.Fatal("disabled account checked when skipping is off")
	}
	expired := active
	expired.Status = model.AccountExpired
	if keepaliveEligible(expired, true) {
		t.Fatal("expired account needs operator action, not keepalive")
	}
	noCookies := model.Account{Enabled: true, Status: model.AccountActive}
	if keepaliveEligible(noCookies, true) {
		t.Fatal("account without credentials cannot be checked")
	}
}
