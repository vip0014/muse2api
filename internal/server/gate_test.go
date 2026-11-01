package server

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestKeyedGateWaitIsCancellable(t *testing.T) {
	gate := newKeyedGate()
	release, err := gate.acquire(context.Background(), "acc_1")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := gate.acquire(ctx, "acc_1"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting acquire error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("cancellable wait took %s", elapsed)
	}

	release()
	again, err := gate.acquire(context.Background(), "acc_1")
	if err != nil {
		t.Fatalf("gate not reusable after release: %v", err)
	}
	again()
}

func TestKeyedGateIndependentKeys(t *testing.T) {
	gate := newKeyedGate()
	releaseA, err := gate.acquire(context.Background(), "acc_a")
	if err != nil {
		t.Fatalf("acquire a: %v", err)
	}
	defer releaseA()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	releaseB, err := gate.acquire(ctx, "acc_b")
	if err != nil {
		t.Fatalf("independent key blocked: %v", err)
	}
	releaseB()
}
