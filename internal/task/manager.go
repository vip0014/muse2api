package task

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"muse2api/internal/apierr"
	"muse2api/internal/model"
	"muse2api/internal/storage"
)

var ErrClosed = errors.New("任务管理器已关闭")

type Result struct {
	MediaURL string
	Data     map[string]any
}
type Processor interface {
	Process(context.Context, model.Task) (Result, error)
}
type ProcessorFunc func(context.Context, model.Task) (Result, error)

func (f ProcessorFunc) Process(ctx context.Context, t model.Task) (Result, error) { return f(ctx, t) }

type Manager struct {
	store     *storage.Store
	processor Processor
	queue     chan string
	root      context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	mu        sync.Mutex
	cancels   map[string]context.CancelFunc
	closed    atomic.Bool
	closeOnce sync.Once
	done      chan struct{}
}

func NewManager(store *storage.Store, processor Processor, workers, queueSize int) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{store: store, processor: processor, queue: make(chan string, queueSize), root: ctx, cancel: cancel, cancels: map[string]context.CancelFunc{}, done: make(chan struct{})}
	for i := 0; i < workers; i++ {
		m.wg.Add(1)
		go m.worker()
	}
	return m
}
func (m *Manager) Enqueue(id string) error {
	if m.closed.Load() {
		return ErrClosed
	}
	select {
	case m.queue <- id:
		return nil
	default:
		return apierr.QueueFull()
	}
}
func (m *Manager) worker() {
	defer m.wg.Done()
	for {
		select {
		case <-m.root.Done():
			return
		default:
		}
		select {
		case <-m.root.Done():
			return
		case id := <-m.queue:
			m.run(id)
		}
	}
}
func (m *Manager) run(id string) {
	started := time.Now()
	t, err := m.store.ClaimTask(id)
	if err != nil {
		return
	}
	ctx, cancel := context.WithCancel(m.root)
	m.mu.Lock()
	m.cancels[id] = cancel
	m.mu.Unlock()
	defer func() { cancel(); m.mu.Lock(); delete(m.cancels, id); m.mu.Unlock() }()
	result, runErr := m.processSafe(ctx, t)
	_, updateErr := m.store.UpdateTask(id, func(t *model.Task) error {
		t.CompletedAt = time.Now().Unix()
		t.Elapsed = time.Since(started).Seconds()
		if runErr != nil {
			switch {
			case errors.Is(runErr, context.Canceled) && t.CancelRequested:
				t.Status = model.TaskCancelled
				t.Error = "本地执行已取消；无法保证上游任务同步取消"
			case errors.Is(runErr, context.Canceled) && m.root.Err() != nil:
				// Shutdown is not a task failure: upstream may still have
				// produced a result, so keep the task undecided instead of
				// falsely recording failure and never retrying it.
				t.Status = model.TaskUnknown
				t.Error = "服务关闭导致本地执行中断，上游结果未知；未自动重试"
			default:
				t.Status = model.TaskFailed
				t.Error = safeError(runErr)
			}
			t.ReferenceImage = ""
			t.ReferenceImages = nil
			return nil
		}
		t.Status = model.TaskSucceeded
		t.Progress = 100
		t.URL = result.MediaURL
		t.Result = result.Data
		t.ReferenceImage = ""
		t.ReferenceImages = nil
		return nil
	})
	if updateErr != nil {
		m.store.AddEvent(model.Event{Level: "error", Type: "task.persist", Message: "任务结果持久化失败", TaskID: id})
	}
}
func (m *Manager) processSafe(ctx context.Context, t model.Task) (result Result, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("任务执行 panic: %v", recovered)
		}
	}()
	return m.processor.Process(ctx, t)
}
func (m *Manager) Cancel(id string) error {
	t, err := m.store.GetTask(id)
	if err != nil {
		return storage.ErrNotFound
	}
	if t.Status != model.TaskQueued && t.Status != model.TaskRunning {
		return fmt.Errorf("%w: 终态任务不可取消", storage.ErrConflict)
	}
	if t.Kind == "chat" {
		return fmt.Errorf("%w: 对话任务随客户端断开取消，不支持后台取消", storage.ErrConflict)
	}
	_, err = m.store.UpdateTask(id, func(t *model.Task) error {
		t.CancelRequested = true
		if t.Status == model.TaskQueued {
			t.Status = model.TaskCancelled
			t.Error = "排队阶段已取消"
			t.CompletedAt = time.Now().Unix()
		}
		return nil
	})
	if err != nil {
		return err
	}
	m.mu.Lock()
	cancel := m.cancels[id]
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// Close waits for every worker to finish. Callers that must bound shutdown
// should use Shutdown with a context instead.
func (m *Manager) Close() {
	m.startClose()
	<-m.done
}

func (m *Manager) Shutdown(ctx context.Context) error {
	m.startClose()
	select {
	case <-m.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) startClose() {
	m.closeOnce.Do(func() {
		m.closed.Store(true)
		m.cancel()
		go func() {
			m.wg.Wait()
			close(m.done)
		}()
	})
}

func safeError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "任务超时"
	}
	msg := err.Error()
	if len(msg) > 500 {
		msg = msg[:500]
	}
	return msg
}
