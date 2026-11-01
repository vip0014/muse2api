package storage

import "time"

func (s *Store) RecordMedia(name, taskID, kind, mime string, bytes int64) error {
	_, err := s.db.Exec(`INSERT INTO media(name, task_id, kind, mime, bytes, created_at) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(name) DO UPDATE SET task_id = excluded.task_id, kind = excluded.kind, mime = excluded.mime, bytes = excluded.bytes`,
		name, taskID, kind, mime, bytes, time.Now().Unix())
	return err
}

func (s *Store) MediaTaskIDs() map[string]string {
	rows, err := s.db.Query(`SELECT name, task_id FROM media WHERE task_id <> ''`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var name, taskID string
		if rows.Scan(&name, &taskID) == nil {
			out[name] = taskID
		}
	}
	return out
}

func (s *Store) DeleteMediaRecord(name string) {
	_, _ = s.db.Exec(`DELETE FROM media WHERE name = ?`, name)
}

// ---------------------------------------------------------------------------
// backup / API key
// ---------------------------------------------------------------------------

// StageRestore validates a SQLite backup and stages it for the next process
// start. The live process keeps its current database, so a later write cannot
// clobber the restored snapshot.
