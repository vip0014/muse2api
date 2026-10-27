package storage

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"muse2api/internal/model"
)

func benchmarkTaskStore(b *testing.B, count int) *Store {
	b.Helper()
	dir := b.TempDir()
	tasks := make(map[string]model.Task, count)
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("task_%08d", i)
		tasks[id] = model.Task{
			ID: id, Object: "task", Kind: "image", Prompt: "prompt", APIPrompt: "prompt",
			Status: model.TaskSucceeded, Progress: 100, CreatedAt: int64(i), UpdatedAt: int64(i),
			URL: "/v1/media/x.png",
			Result: map[string]any{
				"filename": "x.png", "kind": "image", "bytes": 1234, "url": "/v1/media/x.png",
			},
		}
	}
	if err := writeJSON(filepath.Join(dir, "tasks.json"), tasks); err != nil {
		b.Fatal(err)
	}
	store, err := Open(dir, testKey(), false)
	if err != nil {
		b.Fatal(err)
	}
	return store
}

// BenchmarkDashboardProjection matches the current /admin/overview read path.
func BenchmarkDashboardProjection(b *testing.B) {
	store := benchmarkTaskStore(b, 10_000)
	since := time.Now().Unix() - 86400
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		counts := store.TaskCounts(since)
		recent := store.ListTasks(20)
		if counts.ImageTotal != 10_000 || len(recent) != 20 {
			b.Fatal("unexpected projection")
		}
	}
}

// BenchmarkFullTaskClone measures the previous dashboard read shape: loading
// and deep-copying the entire task history.
func BenchmarkFullTaskClone(b *testing.B) {
	store := benchmarkTaskStore(b, 10_000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		all := store.ListTasks(0)
		if len(all) != 10_000 {
			b.Fatal("unexpected task count")
		}
	}
}
