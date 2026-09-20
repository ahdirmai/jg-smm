package port

import (
	"context"
	"time"
)

// ActionTextStore holds an operator-supplied comment body for a specific action
// job between enqueue and dispatch. It exists because the action_job table has
// no text column (comment text is normally composed from the template pool at
// dispatch); the region-select comment flow lets the operator write a distinct
// comment per account, so that text must survive from Enqueue to the scheduler's
// compose step. Keyed by job id, short-lived (a job dispatches within minutes),
// and a miss simply falls back to template composition — never an error.
type ActionTextStore interface {
	// Put stores the comment body for a job, expiring after ttl.
	Put(ctx context.Context, jobID, text string, ttl time.Duration) error
	// Get returns the stored body and whether one was present.
	Get(ctx context.Context, jobID string) (string, bool, error)
}
