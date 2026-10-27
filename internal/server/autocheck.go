package server

import (
	"context"
	"runtime/debug"
	"sync"
	"time"

	"muse2api/internal/model"
	"muse2api/internal/muse/protocol"
)

// autoChecker serializes post-import verification. Browser work is already
// serialized by the engine, so a single worker avoids piling up contexts while
// still keeping the HTTP import response fast.
type autoChecker struct {
	ctx     context.Context
	cancel  context.CancelFunc
	queue   chan string
	mu      sync.Mutex
	pending map[string]bool
	wg      sync.WaitGroup
}

func newAutoChecker() *autoChecker {
	ctx, cancel := context.WithCancel(context.Background())
	return &autoChecker{
		ctx:     ctx,
		cancel:  cancel,
		queue:   make(chan string, 128),
		pending: map[string]bool{},
	}
}

func (s *Server) enqueueAutoCheck(id string) {
	if id == "" || s.autoCheck == nil {
		return
	}
	c := s.autoCheck
	c.mu.Lock()
	if c.pending[id] {
		c.mu.Unlock()
		return
	}
	c.pending[id] = true
	c.mu.Unlock()
	select {
	case c.queue <- id:
	default:
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		s.log.Warn("自动检测队列已满", "account", id)
	}
}

func (s *Server) autoCheckWorker() {
	defer s.autoCheck.wg.Done()
	for {
		select {
		case <-s.autoCheck.ctx.Done():
			return
		case id := <-s.autoCheck.queue:
			s.runAutoCheck(id)
			s.autoCheck.mu.Lock()
			delete(s.autoCheck.pending, id)
			s.autoCheck.mu.Unlock()
		}
	}
}

// runAutoCheck verifies the imported session first, then refreshes quota. It
// never blocks the import request and keeps going if one account fails.
func (s *Server) runAutoCheck(id string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			s.log.Error("自动检测 panic", "account", id, "panic", recovered, "stack", string(debug.Stack()))
		}
	}()
	if s.autoCheck.ctx.Err() != nil {
		return
	}
	account, err := s.store.GetAccount(id)
	if err != nil {
		return
	}
	checkCtx, cancel := context.WithTimeout(s.autoCheck.ctx, s.cfg.HTTPTimeout)
	result := s.checkAndStoreSession(checkCtx, account)
	cancel()
	if s.autoCheck.ctx.Err() != nil {
		return
	}
	if !result.OK {
		s.store.AddEvent(model.Event{Level: "warning", Type: "account.auto_check", Message: "导入后自动检测失败：" + result.Message, AccountID: id})
		return
	}
	s.store.AddEvent(model.Event{Level: "info", Type: "account.auto_check", Message: "导入后自动检测成功", AccountID: id})
	s.pool.Wake()

	driver, ok := s.driver.(quotaDriver)
	if !ok {
		return
	}
	fresh, err := s.store.GetAccount(id)
	if err != nil {
		return
	}
	quotaCtx, quotaCancel := context.WithTimeout(s.autoCheck.ctx, 90*time.Second)
	quota, err := s.queryQuota(quotaCtx, driver, protocol.AccountCredentials{ID: fresh.ID, Cookies: fresh.Cookies, CookieExpiry: fresh.CookieExpiry})
	quotaCancel()
	if err != nil {
		message := mapQuotaError(err).Error()
		s.log.Warn("自动刷新额度失败", "account", id, "error", err)
		if _, updateErr := s.store.UpdateAccount(id, func(a *model.Account) error {
			a.LastError = message
			a.Note = "会话有效；额度未同步"
			return nil
		}); updateErr != nil {
			s.log.Warn("自动额度状态写入失败", "account", id, "error", updateErr)
		}
		s.store.AddEvent(model.Event{Level: "warning", Type: "account.quota", Message: "导入后额度刷新失败：" + message, AccountID: id})
		return
	}
	if _, err := s.store.UpdateAccount(id, func(a *model.Account) error {
		a.Quota = quota
		a.LastError = ""
		a.Note = "会话有效，额度已同步"
		return nil
	}); err != nil {
		s.log.Warn("自动额度写入失败", "account", id, "error", err)
	}
	s.store.AddEvent(model.Event{Level: "info", Type: "account.quota", Message: "导入后额度已同步", AccountID: id})
	s.pool.Wake()
}
