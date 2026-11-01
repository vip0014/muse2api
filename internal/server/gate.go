package server

import (
	"context"
	"sync"
)

// keyedGate serializes work per key. Waiters abandon the queue when their
// request context ends, so a manual admin action can never sit forever behind
// a slow background check (sync.Mutex locking is not cancellable).
type keyedGate struct {
	mu    sync.Mutex
	gates map[string]chan struct{}
}

func newKeyedGate() *keyedGate {
	return &keyedGate{gates: map[string]chan struct{}{}}
}

// acquire blocks until the key is free or ctx is done. On success the caller
// must run the returned release exactly once.
func (g *keyedGate) acquire(ctx context.Context, key string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g.mu.Lock()
	gate := g.gates[key]
	if gate == nil {
		gate = make(chan struct{}, 1)
		g.gates[key] = gate
	}
	g.mu.Unlock()
	select {
	case gate <- struct{}{}:
		return func() { <-gate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
