package scheduler

import (
	"context"
	"errors"
	"sync"
	"time"

	"muse2api/internal/model"
	"muse2api/internal/storage"
)

var ErrNoAccount = errors.New("没有可用账号")
var ErrConcurrency = errors.New("全局并发槽位已满")

type Pool struct {
	mu          sync.Mutex
	wake        chan struct{}
	store       *storage.Store
	globalLimit int
	global      int
	perAccount  map[string]int
	cursor      int
}
type Lease struct {
	Account model.Account
	pool    *Pool
	once    sync.Once
}

func NewPool(store *storage.Store, globalLimit int) *Pool {
	return &Pool{store: store, globalLimit: globalLimit, perAccount: map[string]int{}, wake: make(chan struct{})}
}

func eligibleAccount(a model.Account, now int64) bool {
	if !a.Enabled || !a.EssentialOK {
		return false
	}
	switch a.Status {
	case model.AccountDisabled, model.AccountExpired, model.AccountError, model.AccountCooldown:
		return false
	}
	if a.ExpiresAt > 0 && a.ExpiresAt <= now {
		return false
	}
	return true
}

func (p *Pool) Acquire(ctx context.Context) (*Lease, error) {
	for {
		p.mu.Lock()
		if p.global < p.globalLimit {
			accounts := p.store.ListAccounts()
			if len(accounts) == 0 {
				p.mu.Unlock()
				return nil, ErrNoAccount
			}
			now := time.Now().Unix()
			eligible := false
			for i := 0; i < len(accounts); i++ {
				idx := (p.cursor + i) % len(accounts)
				a := accounts[idx]
				if !eligibleAccount(a, now) {
					continue
				}
				eligible = true
				if p.perAccount[a.ID] >= a.MaxConcurrency {
					continue
				}
				p.global++
				p.perAccount[a.ID]++
				p.cursor = (idx + 1) % len(accounts)
				p.mu.Unlock()
				return &Lease{Account: a, pool: p}, nil
			}
			if !eligible {
				p.mu.Unlock()
				return nil, ErrNoAccount
			}
		}
		ch := p.wake
		p.mu.Unlock()
		if err := waitForWake(ctx, ch); err != nil {
			return nil, err
		}
	}
}

func waitForWake(ctx context.Context, ch <-chan struct{}) error {
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-ch:
		return nil
	case <-timer.C:
		return nil
	}
}

func (l *Lease) Release() {
	l.once.Do(func() {
		p := l.pool
		p.mu.Lock()
		p.global--
		p.perAccount[l.Account.ID]--
		p.broadcastLocked()
		p.mu.Unlock()
	})
}

// Wake broadcasts a state change (account add/enable/delete) to all waiters.
func (p *Pool) Wake() {
	p.mu.Lock()
	p.broadcastLocked()
	p.mu.Unlock()
}

func (p *Pool) broadcastLocked() {
	close(p.wake)
	p.wake = make(chan struct{})
}
func (p *Pool) Stats() (active int, accounts map[string]int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	accounts = map[string]int{}
	for k, v := range p.perAccount {
		accounts[k] = v
	}
	return p.global, accounts
}
