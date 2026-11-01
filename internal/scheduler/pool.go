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

type Class string

const (
	ClassDefault Class = "default"
	ClassChat    Class = "chat"
	ClassImage   Class = "image"
	ClassVideo   Class = "video"
	ClassAdmin   Class = "admin"
)

type slot struct {
	account  model.Account
	leases   int
	lastUsed int64
}

type Pool struct {
	mu          sync.Mutex
	wake        chan struct{}
	store       *storage.Store
	globalLimit int
	global      int
	classLimits map[Class]int
	classInUse  map[Class]int
	accounts    map[string]*slot
	cursor      int
}

type Lease struct {
	Account model.Account
	Class   Class
	pool    *Pool
	once    sync.Once
}

func NewPool(store *storage.Store, globalLimit int) *Pool {
	return NewPoolWithLimits(store, globalLimit, nil)
}

func NewPoolWithLimits(store *storage.Store, globalLimit int, limits map[Class]int) *Pool {
	if globalLimit < 1 {
		globalLimit = 1
	}
	p := &Pool{
		store:       store,
		globalLimit: globalLimit,
		wake:        make(chan struct{}),
		classLimits: map[Class]int{},
		classInUse:  map[Class]int{},
		accounts:    map[string]*slot{},
	}
	defaults := map[Class]int{
		ClassDefault: globalLimit,
		ClassChat:    globalLimit,
		ClassImage:   globalLimit,
		ClassVideo:   1,
		ClassAdmin:   1,
	}
	for class, limit := range defaults {
		p.classLimits[class] = limit
	}
	for class, limit := range limits {
		if limit > 0 {
			p.classLimits[class] = limit
		}
	}
	p.reload()
	return p
}

func eligibleAccount(a model.Account, now int64) bool {
	if !a.Enabled || !a.EssentialOK {
		return false
	}
	if a.CooldownUntil > now {
		return false
	}
	switch a.Status {
	case model.AccountDisabled, model.AccountExpired, model.AccountError:
		return false
	case model.AccountCooldown:
		if a.CooldownUntil > now {
			return false
		}
	}
	if a.ExpiresAt > 0 && a.ExpiresAt <= now {
		return false
	}
	return true
}

func (p *Pool) reload() {
	accounts := p.store.ListAccounts()
	next := make(map[string]*slot, len(accounts))
	for _, account := range accounts {
		if !account.Enabled || !account.EssentialOK {
			continue
		}
		switch account.Status {
		case model.AccountDisabled, model.AccountExpired, model.AccountError:
			continue
		}
		// Keep cooling-down accounts in the index so they become eligible
		// again when cooldown_until passes without requiring another Wake.
		next[account.ID] = &slot{account: account}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, old := range p.accounts {
		if current, ok := next[id]; ok {
			current.leases = old.leases
			current.lastUsed = old.lastUsed
		}
	}
	p.accounts = next
	if len(p.accounts) == 0 {
		p.cursor = 0
	}
	p.broadcastLocked()
}

// Wake refreshes the in-memory eligible-account index and wakes waiters.
// Account mutations are infrequent, so the O(accounts) reload is cheaper than
// querying and deep-copying every account on every Acquire.
func (p *Pool) Wake() {
	p.reload()
}

func (p *Pool) Acquire(ctx context.Context) (*Lease, error) {
	return p.AcquireFor(ctx, ClassDefault)
}

func (p *Pool) AcquireFor(ctx context.Context, class Class) (*Lease, error) {
	if class == "" {
		class = ClassDefault
	}
	for {
		p.mu.Lock()
		if len(p.accounts) == 0 {
			p.mu.Unlock()
			return nil, ErrNoAccount
		}
		now := time.Now().Unix()
		eligible := false
		classLimit := p.classLimits[class]
		if classLimit <= 0 {
			classLimit = p.globalLimit
		}
		if p.global < p.globalLimit && p.classInUse[class] < classLimit && len(p.accounts) > 0 {
			ids := make([]string, 0, len(p.accounts))
			for id := range p.accounts {
				ids = append(ids, id)
			}
			// Deterministic round-robin without relying on map order.
			potential := false
			for i := 0; i < len(ids); i++ {
				id := ids[(p.cursor+i)%len(ids)]
				slot := p.accounts[id]
				if slot == nil {
					continue
				}
				account := slot.account
				if account.Enabled && account.EssentialOK && account.Status != model.AccountDisabled &&
					account.Status != model.AccountExpired && account.Status != model.AccountError &&
					(account.ExpiresAt == 0 || account.ExpiresAt > now) {
					potential = true
				}
				if !eligibleAccount(account, now) {
					continue
				}
				eligible = true
				// Upstream turns are serialized per account. MaxConcurrency>1
				// only wastes global slots while the client mutex waits.
				if slot.leases >= 1 {
					continue
				}
				p.global++
				p.classInUse[class]++
				slot.leases++
				slot.lastUsed = now
				p.cursor = (p.cursor + i + 1) % len(ids)
				p.mu.Unlock()
				return &Lease{Account: account, Class: class, pool: p}, nil
			}
			if !eligible && !potential {
				p.mu.Unlock()
				return nil, ErrNoAccount
			}
		}
		ch := p.wake
		p.mu.Unlock()
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-ch:
		case <-timer.C:
		}
		timer.Stop()
	}
}

func (l *Lease) Release() {
	if l == nil || l.pool == nil {
		return
	}
	l.once.Do(func() {
		p := l.pool
		p.mu.Lock()
		if p.global > 0 {
			p.global--
		}
		if p.classInUse[l.Class] > 0 {
			p.classInUse[l.Class]--
		}
		if slot := p.accounts[l.Account.ID]; slot != nil && slot.leases > 0 {
			slot.leases--
		}
		p.broadcastLocked()
		p.mu.Unlock()
	})
}

func (p *Pool) broadcastLocked() {
	close(p.wake)
	p.wake = make(chan struct{})
}

func (p *Pool) Stats() (active int, accounts map[string]int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	accounts = map[string]int{}
	for id, slot := range p.accounts {
		accounts[id] = slot.leases
	}
	return p.global, accounts
}

func (p *Pool) EligibleCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now().Unix()
	count := 0
	for _, slot := range p.accounts {
		if eligibleAccount(slot.account, now) {
			count++
		}
	}
	return count
}

func (p *Pool) ClassStats() map[Class]int {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[Class]int, len(p.classInUse))
	for class, count := range p.classInUse {
		out[class] = count
	}
	return out
}
