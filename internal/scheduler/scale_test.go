package scheduler

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"muse2api/internal/model"
)

func TestPoolTenAccountsConcurrentLeases(t *testing.T) {
	store := openTestStore(t)
	for i := 0; i < 10; i++ {
		account, err := store.AddAccount(fmt.Sprintf("a%d", i), map[string]string{"hatch_sess": "s", "hatch_vml": "v"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.UpdateAccount(account.ID, func(a *model.Account) error {
			a.Status = model.AccountActive
			a.ExpiresAt = time.Now().Add(time.Hour).Unix()
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	pool := NewPool(store, 10)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				lease, err := pool.AcquireFor(ctx, ClassChat)
				if err != nil {
					errs <- err
					return
				}
				time.Sleep(time.Millisecond)
				lease.Release()
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	active, _ := pool.Stats()
	if active != 0 {
		t.Fatalf("leases leaked: %d", active)
	}
}
