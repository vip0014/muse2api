package scheduler

import (
	"context"
	"testing"
	"time"

	"muse2api/internal/model"
	"muse2api/internal/storage"
)

func TestPoolNoEligibleAccountFailsFast(t *testing.T) {
	s, _ := storage.Open(t.TempDir(), []byte("01234567890123456789012345678901"), false)
	a, _ := s.AddAccount("a", map[string]string{"hatch_sess": "s", "hatch_vml": "v", "hatch_native_auth_device": "d"}, nil)
	_, _ = s.UpdateAccount(a.ID, func(a *model.Account) error { a.Enabled = false; return nil })
	p := NewPool(s, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := p.Acquire(ctx); err == nil {
		t.Fatal("disabled account was scheduled")
	}
}

func TestPoolSkipsExpiredAccount(t *testing.T) {
	s, _ := storage.Open(t.TempDir(), []byte("01234567890123456789012345678901"), false)
	a, _ := s.AddAccount("a", map[string]string{"hatch_sess": "s", "hatch_vml": "v", "hatch_native_auth_device": "d"}, nil)
	_, _ = s.UpdateAccount(a.ID, func(a *model.Account) error {
		a.Status = model.AccountActive
		a.ExpiresAt = time.Now().Add(-time.Minute).Unix()
		return nil
	})
	p := NewPool(s, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := p.Acquire(ctx); err == nil {
		t.Fatal("expired account was scheduled")
	}
}

func TestPoolLimits(t *testing.T) {
	s, _ := storage.Open(t.TempDir(), []byte("01234567890123456789012345678901"), false)
	a, _ := s.AddAccount("a", map[string]string{"hatch_sess": "s", "hatch_vml": "v", "hatch_native_auth_device": "d"}, nil)
	_, _ = s.UpdateAccount(a.ID, func(a *model.Account) error { a.Status = model.AccountActive; return nil })
	p := NewPool(s, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	lease, err := p.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Acquire(ctx); err == nil {
		t.Fatal("global limit ignored")
	}
	lease.Release()
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	if _, err := p.Acquire(ctx2); err != nil {
		t.Fatal(err)
	}
}
