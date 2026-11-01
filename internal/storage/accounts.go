package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"muse2api/internal/model"
)

func (s *Store) ListAccounts() []model.Account {
	rows, err := s.db.Query(`SELECT id, label, status, enabled, essential_ok, cookie_count, created_at,
last_used, last_checked, last_keepalive, use_count, expires_at, expiry_estimated,
last_error, note, max_concurrency, failure_count, cooldown_until, credentials_version, quota_json, ok, synced_at FROM accounts`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := make([]model.Account, 0)
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			continue
		}
		a.Cookies = nil
		a.CookieExpiry = nil
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt == out[j].CreatedAt {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt > out[j].CreatedAt
	})
	return out
}

func (s *Store) GetAccount(id string) (model.Account, error) {
	row := s.db.QueryRow(`SELECT id, label, status, enabled, essential_ok, cookie_count, created_at,
last_used, last_checked, last_keepalive, use_count, expires_at, expiry_estimated,
last_error, note, max_concurrency, failure_count, cooldown_until, credentials_version, quota_json, ok, synced_at FROM accounts WHERE id = ?`, id)
	a, err := scanAccount(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Account{}, ErrNotFound
		}
		return model.Account{}, err
	}
	if err := s.loadCredentials(&a); err != nil {
		return model.Account{}, err
	}
	normalizeAccount(&a)
	return a, nil
}

func (s *Store) AddAccount(label string, cookies map[string]string, expires map[string]int64) (model.Account, error) {
	if len(cookies) == 0 {
		return model.Account{}, errors.New("cookies required")
	}
	now := time.Now().Unix()
	a := model.Account{ID: randomID(6), Label: label, Cookies: cloneStrings(cookies), CookieExpiry: cloneInt64s(expires), Enabled: true, CreatedAt: now, MaxConcurrency: 1, Status: model.AccountUnknown}
	if a.Label == "" {
		a.Label = a.ID
	}
	normalizeAccount(&a)
	tx, err := s.db.Begin()
	if err != nil {
		return model.Account{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := s.insertAccountTx(tx, a); err != nil {
		return model.Account{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.Account{}, err
	}
	s.AddEvent(model.Event{Level: "info", Type: "account.import", Message: "账号凭据已导入", AccountID: a.ID})
	return sanitizeAccount(a), nil
}

func (s *Store) UpdateAccount(id string, mutate func(*model.Account) error) (model.Account, error) {
	a, err := s.GetAccount(id)
	if err != nil {
		return model.Account{}, err
	}
	if err := mutate(&a); err != nil {
		return model.Account{}, err
	}
	normalizeAccount(&a)
	tx, err := s.db.Begin()
	if err != nil {
		return model.Account{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := s.updateAccountTx(tx, a); err != nil {
		return model.Account{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.Account{}, err
	}
	return sanitizeAccount(a), nil
}

func (s *Store) DeleteAccount(id string) error {
	res, err := s.db.Exec(`DELETE FROM accounts WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	s.AddEvent(model.Event{Level: "info", Type: "account.delete", Message: "账号已删除", AccountID: id})
	return nil
}

func (s *Store) AccountStats() map[string]int {
	out := map[string]int{}
	accounts := s.ListAccounts()
	out["total"] = len(accounts)
	now := time.Now().Unix()
	for _, a := range accounts {
		if a.Enabled {
			out["enabled"]++
		} else {
			out["disabled"]++
		}
		if a.Status == model.AccountActive {
			out["healthy"]++
			out["active"]++
		}
		if a.Status == model.AccountExpired || a.Status == model.AccountError {
			out["error"]++
		}
		if a.ExpiresAt > 0 && a.ExpiresAt <= now {
			out["expired"]++
		} else if a.ExpiresAt > 0 && a.ExpiresAt-now < 3*86400 {
			out["expiring"]++
		}
	}
	return out
}

func (s *Store) insertAccountTx(tx *sql.Tx, a model.Account) error {
	quota, err := marshalQuota(a.Quota)
	if err != nil {
		return err
	}
	ok := nullableBool(a.OK)
	if _, err := tx.Exec(`INSERT INTO accounts(id, label, status, enabled, essential_ok, cookie_count,
created_at, last_used, last_checked, last_keepalive, use_count, expires_at, expiry_estimated,
last_error, note, max_concurrency, failure_count, cooldown_until, credentials_version, quota_json, ok, synced_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.Label, string(a.Status), boolInt(a.Enabled), boolInt(a.EssentialOK), a.CookieCount,
		a.CreatedAt, a.LastUsed, a.LastChecked, a.LastKeepalive, a.UseCount, a.ExpiresAt, boolInt(a.ExpiryEstimated),
		a.LastError, a.Note, a.MaxConcurrency, a.FailureCount, a.CooldownUntil, a.CredentialsVersion, quota, ok, a.SyncedAt); err != nil {
		return err
	}
	return s.saveCredentialsTx(tx, a)
}

func (s *Store) updateAccountTx(tx *sql.Tx, a model.Account) error {
	quota, err := marshalQuota(a.Quota)
	if err != nil {
		return err
	}
	res, err := tx.Exec(`UPDATE accounts SET label = ?, status = ?, enabled = ?, essential_ok = ?, cookie_count = ?,
last_used = ?, last_checked = ?, last_keepalive = ?, use_count = ?, expires_at = ?, expiry_estimated = ?,
last_error = ?, note = ?, max_concurrency = ?, failure_count = ?, cooldown_until = ?, credentials_version = ?, quota_json = ?, ok = ?, synced_at = ? WHERE id = ?`,
		a.Label, string(a.Status), boolInt(a.Enabled), boolInt(a.EssentialOK), a.CookieCount,
		a.LastUsed, a.LastChecked, a.LastKeepalive, a.UseCount, a.ExpiresAt, boolInt(a.ExpiryEstimated),
		a.LastError, a.Note, a.MaxConcurrency, a.FailureCount, a.CooldownUntil, a.CredentialsVersion, quota, nullableBool(a.OK), a.SyncedAt, a.ID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return s.saveCredentialsTx(tx, a)
}

func (s *Store) saveCredentialsTx(tx *sql.Tx, a model.Account) error {
	plain, err := json.Marshal(diskCredentials{Cookies: a.Cookies, CookieExpiry: a.CookieExpiry})
	if err != nil {
		return err
	}
	payload := string(plain)
	if s.box != nil {
		sealed, err := s.box.Seal(plain)
		if err != nil {
			return err
		}
		payload = sealed
	} else if !s.insecure {
		return errors.New("refusing to write plaintext credentials without explicit insecure mode")
	}
	_, err = tx.Exec(`INSERT INTO account_credentials(account_id, payload, updated_at) VALUES (?, ?, ?)
ON CONFLICT(account_id) DO UPDATE SET payload = excluded.payload, updated_at = excluded.updated_at`, a.ID, payload, time.Now().Unix())
	return err
}

func (s *Store) loadCredentials(a *model.Account) error {
	var payload string
	if err := s.db.QueryRow(`SELECT payload FROM account_credentials WHERE account_id = ?`, a.ID).Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	var creds diskCredentials
	if s.box != nil {
		plain, err := s.box.Open(payload)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(plain, &creds); err != nil {
			return err
		}
	} else {
		if !s.insecure {
			return errors.New("stored credentials are encrypted but credential key is missing")
		}
		if err := json.Unmarshal([]byte(payload), &creds); err != nil {
			return err
		}
	}
	a.Cookies, a.CookieExpiry = creds.Cookies, creds.CookieExpiry
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanAccount(row rowScanner) (model.Account, error) {
	var a model.Account
	var status, quota string
	var enabled, essential, estimated int
	var ok sql.NullInt64
	if err := row.Scan(&a.ID, &a.Label, &status, &enabled, &essential, &a.CookieCount,
		&a.CreatedAt, &a.LastUsed, &a.LastChecked, &a.LastKeepalive, &a.UseCount,
		&a.ExpiresAt, &estimated, &a.LastError, &a.Note, &a.MaxConcurrency, &a.FailureCount, &a.CooldownUntil, &a.CredentialsVersion, &quota, &ok, &a.SyncedAt); err != nil {
		return model.Account{}, err
	}
	a.Status = model.AccountStatus(status)
	a.Enabled = enabled != 0
	a.EssentialOK = essential != 0
	a.ExpiryEstimated = estimated != 0
	if quota != "" {
		var q model.Quota
		if json.Unmarshal([]byte(quota), &q) == nil {
			a.Quota = &q
		}
	}
	if ok.Valid {
		value := ok.Int64 != 0
		a.OK = &value
	}
	return a, nil
}

func marshalQuota(q *model.Quota) (string, error) {
	if q == nil {
		return "", nil
	}
	b, err := json.Marshal(q)
	return string(b), err
}

func nullableBool(v *bool) any {
	if v == nil {
		return nil
	}
	return boolInt(*v)
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// ---------------------------------------------------------------------------
// tasks
// ---------------------------------------------------------------------------

func normalizeAccount(a *model.Account) {
	a.CookieCount = len(a.Cookies)
	a.EssentialOK = a.Cookies["hatch_sess"] != "" && a.Cookies["hatch_vml"] != ""
	if a.MaxConcurrency <= 0 {
		a.MaxConcurrency = 1
	}
	if a.Status == "" {
		if !a.Enabled {
			a.Status = model.AccountDisabled
		} else if a.OK != nil && !*a.OK {
			a.Status = model.AccountError
		} else if a.OK != nil && *a.OK {
			a.Status = model.AccountActive
		} else {
			a.Status = model.AccountUnknown
		}
	}
	if !a.Enabled {
		a.Status = model.AccountDisabled
	}
	if exp := a.CookieExpiry["hatch_vml"]; exp > 0 {
		a.ExpiresAt = exp
		a.ExpiryEstimated = false
	} else if a.ExpiryEstimated {
		// Older builds invented CreatedAt+48h and then used it as a hard
		// scheduler cutoff. Drop that estimate when loading; only a real
		// Set-Cookie expiry may remove an account from the pool.
		a.ExpiresAt = 0
		a.ExpiryEstimated = false
	}
}

func sanitizeAccount(a model.Account) model.Account {
	a.Cookies = nil
	a.CookieExpiry = nil
	return a
}

func cloneAccount(a model.Account) model.Account {
	a.Cookies = cloneStrings(a.Cookies)
	a.CookieExpiry = cloneInt64s(a.CookieExpiry)
	if a.Quota != nil {
		q := *a.Quota
		if q.ExtraUsedPct != nil {
			value := *q.ExtraUsedPct
			q.ExtraUsedPct = &value
		}
		a.Quota = &q
	}
	if a.OK != nil {
		value := *a.OK
		a.OK = &value
	}
	return a
}
