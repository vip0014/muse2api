package task

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"muse2api/internal/model"
	"muse2api/internal/storage"
)

func TestManagerLifecycleAndQueue(t *testing.T) {
	s, _ := storage.Open(t.TempDir(), []byte("01234567890123456789012345678901"), false)
	release := make(chan struct{})
	m := NewManager(s, ProcessorFunc(func(ctx context.Context, t model.Task) (Result, error) {
		select {
		case <-release:
			return Result{MediaURL: "/v1/media/x"}, nil
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}), 1, 1)
	defer m.Close()
	a, _ := s.CreateTask("image", "a", "a", "", "")
	b, _ := s.CreateTask("image", "b", "b", "", "")
	c, _ := s.CreateTask("image", "c", "c", "", "")
	if err := m.Enqueue(a.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Enqueue(b.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Enqueue(c.ID); err == nil {
		t.Fatal("queue overflow accepted")
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		ta, _ := s.GetTask(a.ID)
		tb, _ := s.GetTask(b.ID)
		if ta.Status == model.TaskSucceeded && tb.Status == model.TaskSucceeded {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("tasks did not complete")
}
func TestCancelRunning(t *testing.T) {
	s, _ := storage.Open(t.TempDir(), []byte("01234567890123456789012345678901"), false)
	started := make(chan struct{})
	m := NewManager(s, ProcessorFunc(func(ctx context.Context, t model.Task) (Result, error) {
		close(started)
		<-ctx.Done()
		return Result{}, ctx.Err()
	}), 1, 1)
	task, _ := s.CreateTask("video", "v", "v", "", "")
	_ = m.Enqueue(task.ID)
	<-started
	if err := m.Cancel(task.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		got, _ := s.GetTask(task.ID)
		if got.Status == model.TaskCancelled {
			m.Close()
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	m.Close()
	t.Fatal("task not cancelled")
}

func TestManagerProcessesDuplicateEnqueueOnce(t *testing.T) {
	s, _ := storage.Open(t.TempDir(), []byte("01234567890123456789012345678901"), false)
	var calls atomic.Int32
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	m := NewManager(s, ProcessorFunc(func(context.Context, model.Task) (Result, error) {
		calls.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		return Result{}, nil
	}), 2, 4)
	defer m.Close()
	task, _ := s.CreateTask("image", "p", "p", "", "")
	if err := m.Enqueue(task.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Enqueue(task.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("task never started")
	}
	// Give the second worker time to race for the same queued ID.
	time.Sleep(100 * time.Millisecond)
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := s.GetTask(task.ID)
		if got.Status == model.TaskSucceeded {
			if calls.Load() != 1 {
				t.Fatalf("processor ran %d times for one task", calls.Load())
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("task did not complete")
}

func TestShutdownLeavesInterruptedTaskUnknown(t *testing.T) {
	s, _ := storage.Open(t.TempDir(), []byte("01234567890123456789012345678901"), false)
	started := make(chan struct{})
	m := NewManager(s, ProcessorFunc(func(ctx context.Context, _ model.Task) (Result, error) {
		close(started)
		<-ctx.Done()
		return Result{}, ctx.Err()
	}), 1, 1)
	defer m.Close()
	task, _ := s.CreateTask("video", "v", "v", "", "")
	if err := m.Enqueue(task.ID); err != nil {
		t.Fatal(err)
	}
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := m.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.TaskUnknown || !strings.Contains(got.Error, "服务关闭") {
		t.Fatalf("shutdown task state = %s %q", got.Status, got.Error)
	}
	if err := m.Enqueue("after-close"); !errors.Is(err, ErrClosed) {
		t.Fatalf("enqueue after close = %v", err)
	}
}
