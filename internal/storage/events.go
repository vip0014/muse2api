package storage

import (
	"time"

	"muse2api/internal/model"
)

func (s *Store) AddEvent(e model.Event) {
	if e.ID == "" {
		e.ID = randomID(8)
	}
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	if e.Level == "" {
		e.Level = "info"
	}
	tx, err := s.db.Begin()
	if err != nil {
		return
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`INSERT INTO events(id, time_ns, level, type, message, request_id, task_id, account_id, upstream_status, duration_ms)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, e.ID, e.Time.UnixNano(), e.Level, e.Type, e.Message, e.RequestID, e.TaskID, e.AccountID, e.Status, e.DurationMS); err != nil {
		return
	}
	_, _ = tx.Exec(`DELETE FROM events WHERE id NOT IN (SELECT id FROM events ORDER BY time_ns DESC LIMIT 1000)`)
	_ = tx.Commit()
}

func (s *Store) Events(limit int) []model.Event {
	query := `SELECT id, time_ns, level, type, message, request_id, task_id, account_id, upstream_status, duration_ms FROM events ORDER BY time_ns DESC`
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
	out := make([]model.Event, 0)
	for rows.Next() {
		var e model.Event
		var ns int64
		if err := rows.Scan(&e.ID, &ns, &e.Level, &e.Type, &e.Message, &e.RequestID, &e.TaskID, &e.AccountID, &e.Status, &e.DurationMS); err != nil {
			continue
		}
		e.Time = time.Unix(0, ns)
		out = append(out, e)
	}
	return out
}

// RecordMedia links a delivered file to the task that produced it.
