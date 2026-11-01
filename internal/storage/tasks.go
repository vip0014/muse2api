package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"muse2api/internal/model"
)

func (s *Store) CreateTask(kind, prompt, apiPrompt, key, hash string) (model.Task, error) {
	t, _, err := s.FindOrCreateTask(model.Task{Kind: kind, Prompt: prompt, APIPrompt: apiPrompt, IdempotencyKey: key, RequestHash: hash})
	return t, err
}

func (s *Store) FindOrCreateTask(t model.Task) (model.Task, bool, error) {
	if t.Kind == "" {
		return model.Task{}, false, errors.New("task kind required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return model.Task{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if t.IdempotencyKey != "" {
		existing, err := getTaskByKeyTx(tx, t.Kind, t.IdempotencyKey)
		if err == nil {
			if existing.RequestHash != t.RequestHash {
				return model.Task{}, false, ErrConflict
			}
			return existing, false, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return model.Task{}, false, err
		}
	}
	now := time.Now().Unix()
	if t.ID == "" {
		t.ID = "task_" + randomID(10)
	}
	t.Object = "task"
	t.Status = model.TaskQueued
	t.CreatedAt = now
	t.UpdatedAt = now
	if err := insertTaskTx(tx, t); err != nil {
		if isUniqueViolation(err) {
			return model.Task{}, false, ErrConflict
		}
		return model.Task{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return model.Task{}, false, err
	}
	return cloneTask(t), true, nil
}

func insertTaskTx(tx *sql.Tx, t model.Task) error {
	normalizeTask(&t)
	result, err := marshalResult(t.Result)
	if err != nil {
		return err
	}
	references, err := marshalReferences(t.ReferenceImages)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO tasks(id, object, kind, prompt, api_prompt, status, progress, account_id,
created_at, updated_at, started_at, completed_at, elapsed, error, result_json, url, size, model,
aspect_ratio, resolution, duration, response_format, idempotency_key, request_hash, cancel_requested,
reference_image, reference_images_json)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Object, t.Kind, t.Prompt, t.APIPrompt, string(t.Status), t.Progress, t.AccountID,
		t.CreatedAt, t.UpdatedAt, t.StartedAt, t.CompletedAt, t.Elapsed, t.Error, result, t.URL, t.Size, t.Model,
		t.AspectRatio, t.Resolution, t.Duration, t.ResponseFormat, t.IdempotencyKey, t.RequestHash, boolInt(t.CancelRequested),
		t.ReferenceImage, references)
	return err
}

func (s *Store) UpdateTask(id string, mutate func(*model.Task) error) (model.Task, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return model.Task{}, err
	}
	defer func() { _ = tx.Rollback() }()
	old, err := getTaskTx(tx, id)
	if err != nil {
		return model.Task{}, err
	}
	t := cloneTask(old)
	if err := mutate(&t); err != nil {
		return model.Task{}, err
	}
	if !model.ValidTaskTransition(old.Status, t.Status) {
		return model.Task{}, fmt.Errorf("%w: invalid task transition %s -> %s", ErrConflict, old.Status, t.Status)
	}
	t.UpdatedAt = time.Now().Unix()
	if err := updateTaskTx(tx, t); err != nil {
		return model.Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.Task{}, err
	}
	return cloneTask(t), nil
}

func (s *Store) GetTask(id string) (model.Task, error) {
	t, err := getTaskTx(s.db, id)
	if err != nil {
		return model.Task{}, err
	}
	return cloneTask(t), nil
}

func (s *Store) ClaimTask(id string) (model.Task, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return model.Task{}, err
	}
	defer func() { _ = tx.Rollback() }()
	t, err := getTaskTx(tx, id)
	if err != nil {
		return model.Task{}, err
	}
	if t.Status != model.TaskQueued {
		return model.Task{}, fmt.Errorf("%w: task %s is %s, not queued", ErrConflict, id, t.Status)
	}
	now := time.Now()
	t.Status = model.TaskRunning
	t.StartedAt = now.Unix()
	t.Progress = 5
	t.Error = ""
	t.UpdatedAt = now.Unix()
	if err := updateTaskTx(tx, t); err != nil {
		return model.Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.Task{}, err
	}
	return cloneTask(t), nil
}

func (s *Store) ListTasks(limit int) []model.Task {
	query := `SELECT id, object, kind, prompt, api_prompt, status, progress, account_id,
created_at, updated_at, started_at, completed_at, elapsed, error, result_json, url, size, model,
aspect_ratio, resolution, duration, response_format, idempotency_key, request_hash, cancel_requested,
'' AS reference_image, '' AS reference_images_json
FROM tasks ORDER BY created_at DESC, id DESC`
	args := []any{}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := make([]model.Task, 0)
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			continue
		}
		// List responses must not carry reference payloads; workers load the
		// full task through GetTask/ClaimTask.
		t.ReferenceImage = ""
		t.ReferenceImages = nil
		out = append(out, cloneTask(t))
	}
	return out
}

// QueuedTaskIDs returns durable work left by a previous process or by a
// queue overflow. The task manager refills its in-memory notification channel
// from this list.
func (s *Store) QueuedTaskIDs(limit int, kinds ...string) []string {
	if limit <= 0 {
		limit = 1000
	}
	query := `SELECT id FROM tasks WHERE status = ?`
	args := []any{string(model.TaskQueued)}
	if len(kinds) > 0 {
		placeholders := make([]string, len(kinds))
		for i, kind := range kinds {
			placeholders[i] = "?"
			args = append(args, kind)
		}
		query += ` AND kind IN (` + strings.Join(placeholders, ",") + `)`
	}
	query += ` ORDER BY created_at ASC, id ASC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out
}

type TaskCounts struct {
	RequestsToday  int
	Running        int
	Queued         int
	Completed      int
	ImageTotal     int
	ImageSucceeded int
	VideoTotal     int
	VideoSucceeded int
}

func (s *Store) TaskCounts(since int64) TaskCounts {
	var c TaskCounts
	_ = s.db.QueryRow(`SELECT
COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0),
COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0),
COALESCE(SUM(CASE WHEN created_at >= ? THEN 1 ELSE 0 END), 0),
COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0),
COALESCE(SUM(CASE WHEN kind = 'image' THEN 1 ELSE 0 END), 0),
COALESCE(SUM(CASE WHEN kind = 'image' AND status = ? THEN 1 ELSE 0 END), 0),
COALESCE(SUM(CASE WHEN kind = 'video' THEN 1 ELSE 0 END), 0),
COALESCE(SUM(CASE WHEN kind = 'video' AND status = ? THEN 1 ELSE 0 END), 0)
FROM tasks`, string(model.TaskRunning), string(model.TaskQueued), since, string(model.TaskSucceeded), string(model.TaskSucceeded), string(model.TaskSucceeded)).
		Scan(&c.Running, &c.Queued, &c.RequestsToday, &c.Completed, &c.ImageTotal, &c.ImageSucceeded, &c.VideoTotal, &c.VideoSucceeded)
	return c
}

// RequeueTask is the explicit, operator-driven retry path. It never runs
// automatically: unknown tasks may already have consumed upstream quota.
func (s *Store) RequeueTask(id string, force bool) (model.Task, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return model.Task{}, err
	}
	defer func() { _ = tx.Rollback() }()
	t, err := getTaskTx(tx, id)
	if err != nil {
		return model.Task{}, err
	}
	if t.Kind == "chat" {
		return model.Task{}, fmt.Errorf("%w: 对话任务不进入后台队列", ErrConflict)
	}
	switch t.Status {
	case model.TaskFailed, model.TaskCancelled:
	case model.TaskUnknown:
		if !force {
			return model.Task{}, fmt.Errorf("%w: 结果未知的任务可能已扣费，需 force 才可重试", ErrConflict)
		}
	default:
		return model.Task{}, fmt.Errorf("%w: 仅失败/取消/未知任务可重试", ErrConflict)
	}
	t.Status = model.TaskQueued
	t.Progress = 0
	t.Error = ""
	t.StartedAt = 0
	t.CompletedAt = 0
	t.Elapsed = 0
	t.CancelRequested = false
	t.UpdatedAt = time.Now().Unix()
	if err := updateTaskTx(tx, t); err != nil {
		return model.Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.Task{}, err
	}
	return cloneTask(t), nil
}

func (s *Store) DeleteTask(id string) error {
	res, err := s.db.Exec(`DELETE FROM tasks WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ClearTasks(keep int) int {
	tx, err := s.db.Begin()
	if err != nil {
		return 0
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.Query(`SELECT id FROM tasks WHERE status <> ? ORDER BY created_at DESC, id DESC`, string(model.TaskRunning))
	if err != nil {
		return 0
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	_ = rows.Close()
	if keep > 0 {
		if len(ids) <= keep {
			ids = nil
		} else {
			ids = ids[keep:]
		}
	}
	for _, id := range ids {
		if _, err := tx.Exec(`DELETE FROM tasks WHERE id = ?`, id); err != nil {
			return 0
		}
	}
	if err := tx.Commit(); err != nil {
		return 0
	}
	return len(ids)
}

func getTaskTx(q interface {
	QueryRow(string, ...any) *sql.Row
	Exec(string, ...any) (sql.Result, error)
}, id string) (model.Task, error) {
	row := q.QueryRow(`SELECT id, object, kind, prompt, api_prompt, status, progress, account_id,
created_at, updated_at, started_at, completed_at, elapsed, error, result_json, url, size, model,
aspect_ratio, resolution, duration, response_format, idempotency_key, request_hash, cancel_requested, reference_image, reference_images_json
FROM tasks WHERE id = ?`, id)
	t, err := scanTask(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Task{}, ErrNotFound
		}
		return model.Task{}, err
	}
	return t, nil
}

func getTaskByKeyTx(tx *sql.Tx, kind, key string) (model.Task, error) {
	row := tx.QueryRow(`SELECT id, object, kind, prompt, api_prompt, status, progress, account_id,
created_at, updated_at, started_at, completed_at, elapsed, error, result_json, url, size, model,
aspect_ratio, resolution, duration, response_format, idempotency_key, request_hash, cancel_requested, reference_image, reference_images_json
FROM tasks WHERE kind = ? AND idempotency_key = ?`, kind, key)
	t, err := scanTask(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Task{}, ErrNotFound
		}
		return model.Task{}, err
	}
	return t, nil
}

func updateTaskTx(tx *sql.Tx, t model.Task) error {
	result, err := marshalResult(t.Result)
	if err != nil {
		return err
	}
	references, err := marshalReferences(t.ReferenceImages)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE tasks SET object = ?, status = ?, progress = ?, account_id = ?, created_at = ?,
updated_at = ?, started_at = ?, completed_at = ?, elapsed = ?, error = ?, result_json = ?, url = ?, size = ?,
model = ?, aspect_ratio = ?, resolution = ?, duration = ?, response_format = ?, idempotency_key = ?,
request_hash = ?, cancel_requested = ?, reference_image = ?, reference_images_json = ? WHERE id = ?`,
		t.Object, string(t.Status), t.Progress, t.AccountID, t.CreatedAt, t.UpdatedAt, t.StartedAt,
		t.CompletedAt, t.Elapsed, t.Error, result, t.URL, t.Size, t.Model, t.AspectRatio,
		t.Resolution, t.Duration, t.ResponseFormat, t.IdempotencyKey, t.RequestHash,
		boolInt(t.CancelRequested), t.ReferenceImage, references, t.ID)
	return err
}

func scanTask(row rowScanner) (model.Task, error) {
	var t model.Task
	var status, result, references string
	var cancel int
	if err := row.Scan(&t.ID, &t.Object, &t.Kind, &t.Prompt, &t.APIPrompt, &status, &t.Progress,
		&t.AccountID, &t.CreatedAt, &t.UpdatedAt, &t.StartedAt, &t.CompletedAt, &t.Elapsed, &t.Error,
		&result, &t.URL, &t.Size, &t.Model, &t.AspectRatio, &t.Resolution, &t.Duration,
		&t.ResponseFormat, &t.IdempotencyKey, &t.RequestHash, &cancel,
		&t.ReferenceImage, &references); err != nil {
		return model.Task{}, err
	}
	t.Status = model.TaskStatus(status)
	t.CancelRequested = cancel != 0
	if result != "" {
		_ = json.Unmarshal([]byte(result), &t.Result)
	}
	if references != "" {
		_ = json.Unmarshal([]byte(references), &t.ReferenceImages)
	}
	return t, nil
}

func marshalReferences(references []string) (string, error) {
	if len(references) == 0 {
		return "", nil
	}
	b, err := json.Marshal(references)
	return string(b), err
}

func marshalResult(result map[string]any) (string, error) {
	if result == nil {
		return "", nil
	}
	b, err := json.Marshal(result)
	return string(b), err
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique")
}

// ---------------------------------------------------------------------------
// events
// ---------------------------------------------------------------------------

func cloneTask(t model.Task) model.Task {
	if t.ReferenceImages != nil {
		t.ReferenceImages = append([]string(nil), t.ReferenceImages...)
	}
	if t.Result != nil {
		out := make(map[string]any, len(t.Result))
		for key, value := range t.Result {
			out[key] = cloneResultValue(value)
		}
		t.Result = out
	}
	return t
}

func cloneResultValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = cloneResultValue(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = cloneResultValue(item)
		}
		return out
	default:
		return value
	}
}

func normalizeTask(t *model.Task) {
	switch t.Status {
	case "processing":
		t.Status = model.TaskRunning
	case "completed":
		t.Status = model.TaskSucceeded
	case "success", "done":
		t.Status = model.TaskSucceeded
	case "":
		t.Status = model.TaskUnknown
	}
}

func cloneStrings(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneInt64s(in map[string]int64) map[string]int64 {
	if in == nil {
		return nil
	}
	out := make(map[string]int64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
