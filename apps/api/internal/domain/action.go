package domain

import (
	"time"
)

// ActionJob (P3-01) is one like/comment to execute on one Target by one
// Account, run by a Worker via Playwright. It mirrors ScrapeJob's shape on
// purpose: both ride the same queue-side JobType/JobStatus enums and the same
// per-account FIFO shape, so a worker callback and the dashboard treat them
// uniformly.
//
// status is a projection of the latest ActionLog attempt; ActionLog is the
// source of truth for a verdict. Nothing is ever inferred from the absence of
// a log row — there is no SUCCESS without ground-truth verification (P3-08).
type ActionJob struct {
	ID          string
	Type        JobType
	TargetID    string
	AccountID   string
	WorkerID    string
	TemplateID  string
	Status      JobStatus
	ScheduledAt time.Time
	StartedAt   *time.Time
	FinishedAt  *time.Time
	Attempts    int
	Error       string
	CreatedAt   time.Time
}

// IsAction reports whether a JobType belongs to the action engine (P3).
// It is the inverse of IsScrape: the shared enum spans both pipelines.
func (t JobType) IsAction() bool {
	switch t {
	case JobTypeActionLike, JobTypeActionComment, JobTypeActionReport, JobTypeActionReplyComment:
		return true
	}
	return false
}

// ActionLog (P3-01) is one attempt of one ActionJob. The audit + debug trail:
// the input (rendered text), the output (response excerpt), the error class,
// and the screenshot key. UNIQUE (ActionJobID, Attempt) is the upsert heart —
// a worker's RUNNING callback and its terminal callback write the same row.
type ActionLog struct {
	ID              string
	ActionJobID     string
	Attempt         int
	Status          AttemptStatus
	Verified        bool
	WorkerID        string
	TemplateID      string
	RenderedText    string
	ResponseExcerpt string
	ErrorClass      string
	ScreenshotURL   string
	DurationMs      int64
	Ts              time.Time
}

// ErrorClass is the coarse classification of a failed attempt, surfaced to the
// dashboard and used by the retry policy (P3-11/P3-12). Plain string, not an
// enum type, so the classifier can grow a class without a migration.
type ErrorClass string

const (
	ErrorClassTransient ErrorClass = "TRANSIENT"
	ErrorClassAuth      ErrorClass = "AUTH"
	ErrorClassRateLimit ErrorClass = "RATE_LIMIT"
	ErrorClassBanned    ErrorClass = "BANNED"
	ErrorClassUnknown   ErrorClass = "UNKNOWN"
)

// AllErrorClasses lists every class the DB CHECK constraint allows.
var AllErrorClasses = []ErrorClass{
	ErrorClassTransient,
	ErrorClassAuth,
	ErrorClassRateLimit,
	ErrorClassBanned,
	ErrorClassUnknown,
}

// Valid reports whether c is a class the schema accepts.
func (c ErrorClass) Valid() bool {
	for _, known := range AllErrorClasses {
		if c == known {
			return true
		}
	}
	return false
}

// Retryable reports whether a failed attempt of this class should be retried
// (P3-11). AUTH and BANNED never retry: the session is gone or the account is
// locked, and another attempt cannot fix either.
func (c ErrorClass) Retryable() bool {
	switch c {
	case ErrorClassTransient, ErrorClassRateLimit:
		return true
	}
	return false
}
