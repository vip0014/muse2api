package server

import (
	"context"
	"sync"
	"time"

	"muse2api/internal/model"
)

// keepaliveAccountSpacing avoids hammering the upstream with a burst of
// session checks when many accounts are configured.
const keepaliveAccountSpacing = 2 * time.Second

// keepaliver refreshes session cookies on a schedule. /api/session is a plain
// HTTP call: it never wakes the VM and never starts a generation.
type keepaliver struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newKeepaliver() *keepaliver {
	ctx, cancel := context.WithCancel(context.Background())
	return &keepaliver{ctx: ctx, cancel: cancel}
}

func (s *Server) keepaliveWorker() {
	defer s.keepalive.wg.Done()
	interval := s.cfg.KeepaliveInterval
	if interval <= 0 {
		return
	}
	// Give a freshly started instance a minute to settle before the first
	// pass, then repeat on the configured cadence.
	timer := time.NewTimer(time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-s.keepalive.ctx.Done():
			return
		case <-timer.C:
		}
		s.runKeepalivePass()
		timer.Reset(interval)
	}
}

func (s *Server) runKeepalivePass() {
	accounts := s.store.ListAccounts()
	checked := 0
	for _, account := range accounts {
		if s.keepalive.ctx.Err() != nil {
			return
		}
		if !keepaliveEligible(account, s.cfg.KeepaliveSkipDisabled) {
			continue
		}
		fresh, err := s.store.GetAccount(account.ID)
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(s.keepalive.ctx, s.cfg.HTTPTimeout)
		result := s.checkAndStoreSession(ctx, fresh)
		cancel()
		checked++
		if !result.OK {
			s.log.Warn("账号保活检测未通过", "account", account.ID, "message", result.Message)
		}
		select {
		case <-s.keepalive.ctx.Done():
			return
		case <-time.After(keepaliveAccountSpacing):
		}
	}
	if checked > 0 {
		s.log.Info("账号保活完成", "checked", checked)
	}
}

// keepaliveEligible skips sessions that can only be fixed by the operator:
// expired credentials are not retried, and disabled accounts are skipped when
// MUSE2API_KEEPALIVE_DISABLED_ACCOUNTS is set.
func keepaliveEligible(account model.Account, skipDisabled bool) bool {
	if skipDisabled && !account.Enabled {
		return false
	}
	if account.Status == model.AccountExpired {
		return false
	}
	return account.CookieCount > 0 || len(account.Cookies) > 0
}
