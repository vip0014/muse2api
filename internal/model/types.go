package model

import "time"

type AccountStatus string

const (
	AccountActive   AccountStatus = "active"
	AccountDisabled AccountStatus = "disabled"
	AccountExpired  AccountStatus = "expired"
	AccountCooldown AccountStatus = "cooldown"
	AccountError    AccountStatus = "error"
	AccountUnknown  AccountStatus = "unknown"
)

type Account struct {
	ID              string            `json:"id"`
	Label           string            `json:"label"`
	Cookies         map[string]string `json:"cookies,omitempty"`
	CookieExpiry    map[string]int64  `json:"cookies_exp,omitempty"`
	CookieCount     int               `json:"cookie_count"`
	EssentialOK     bool              `json:"essential_ok"`
	Status          AccountStatus     `json:"status"`
	Enabled         bool              `json:"enabled"`
	CreatedAt       int64             `json:"created_at"`
	LastUsed        int64             `json:"last_used,omitempty"`
	LastChecked     int64             `json:"checked_at,omitempty"`
	LastKeepalive   int64             `json:"last_keepalive,omitempty"`
	UseCount        int               `json:"use_count"`
	ExpiresAt       int64             `json:"expires_at,omitempty"`
	ExpiryEstimated bool              `json:"expires_estimated"`
	LastError       string            `json:"last_error,omitempty"`
	Note            string            `json:"note,omitempty"`
	MaxConcurrency  int               `json:"max_concurrency"`
	Quota           *Quota            `json:"quota,omitempty"`
	OK              *bool             `json:"ok"`
	SyncedAt        int64             `json:"synced_at,omitempty"`
}

type Quota struct {
	Found         bool     `json:"found"`
	Plan          string   `json:"plan,omitempty"`
	WeeklyReset   string   `json:"weekly_reset,omitempty"`
	WeeklyUsedPct float64  `json:"weekly_used_pct,omitempty"`
	ExtraUsedPct  *float64 `json:"extra_used_pct,omitempty"`
	ExtraLeft     string   `json:"extra_left,omitempty"`
	ExtraExpires  string   `json:"extra_expires,omitempty"`
	CheckedAt     int64    `json:"checked_at,omitempty"`
	Error         string   `json:"error,omitempty"`
}

type TaskStatus string

const (
	TaskQueued    TaskStatus = "queued"
	TaskRunning   TaskStatus = "running"
	TaskSucceeded TaskStatus = "succeeded"
	TaskFailed    TaskStatus = "failed"
	TaskCancelled TaskStatus = "cancelled"
	TaskUnknown   TaskStatus = "unknown"
)

func ValidTaskTransition(from, to TaskStatus) bool {
	if from == to {
		return true
	}
	switch from {
	case TaskQueued:
		return to == TaskRunning || to == TaskCancelled || to == TaskFailed || to == TaskUnknown
	case TaskRunning:
		return to == TaskSucceeded || to == TaskFailed || to == TaskCancelled || to == TaskUnknown
	default:
		return false
	}
}

type Task struct {
	ID              string         `json:"id"`
	Object          string         `json:"object"`
	Kind            string         `json:"kind"`
	Prompt          string         `json:"prompt"`
	APIPrompt       string         `json:"api_prompt,omitempty"`
	Status          TaskStatus     `json:"status"`
	Progress        int            `json:"progress"`
	AccountID       string         `json:"account,omitempty"`
	CreatedAt       int64          `json:"created_at"`
	UpdatedAt       int64          `json:"updated_at"`
	StartedAt       int64          `json:"started_at,omitempty"`
	CompletedAt     int64          `json:"completed_at,omitempty"`
	Elapsed         float64        `json:"elapsed,omitempty"`
	Error           string         `json:"error,omitempty"`
	Result          map[string]any `json:"result,omitempty"`
	URL             string         `json:"url,omitempty"`
	Size            string         `json:"size,omitempty"`
	Model           string         `json:"model,omitempty"`
	AspectRatio     string         `json:"aspect_ratio,omitempty"`
	Resolution      string         `json:"resolution,omitempty"`
	Duration        int            `json:"duration,omitempty"`
	ReferenceImage  string         `json:"-"`
	ResponseFormat  string         `json:"response_format,omitempty"`
	IdempotencyKey  string         `json:"image_request_key,omitempty"`
	RequestHash     string         `json:"image_request_hash,omitempty"`
	CancelRequested bool           `json:"cancel_requested,omitempty"`
}

type Media struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	Bytes   int64  `json:"bytes"`
	ModTime int64  `json:"mtime"`
	Kind    string `json:"kind"`
	TaskID  string `json:"task_id,omitempty"`
	MIME    string `json:"mime"`
}

type Event struct {
	ID         string    `json:"id"`
	Time       time.Time `json:"time"`
	Level      string    `json:"level"`
	Type       string    `json:"type"`
	Message    string    `json:"message"`
	RequestID  string    `json:"request_id,omitempty"`
	TaskID     string    `json:"task_id,omitempty"`
	AccountID  string    `json:"account_id,omitempty"`
	Status     int       `json:"upstream_status,omitempty"`
	DurationMS int64     `json:"duration_ms,omitempty"`
}
