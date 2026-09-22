package port

import (
	"context"
	"time"
)

// ActionBatch records one enqueue call (one operator submit — e.g. a region-
// select comment run) as a unit, so a fan-out of N jobs can be monitored
// together instead of as N unrelated rows. action_job has no batch column and
// sqlc cannot be regenerated in this environment, so batches live in their own
// store keyed by a generated id, alongside the per-job action_job / action_log
// records (which remain the source of truth for each job's verdict).
type ActionBatch struct {
	ID          string    `json:"id"`
	CreatedAt   time.Time `json:"createdAt"`
	ActionTypes []string  `json:"actionTypes"` // distinct job types in the batch
	Count       int       `json:"count"`
	TargetURLs  []string  `json:"targetUrls"` // distinct targets (capped)
	JobIDs      []string  `json:"jobIds"`
}

// ActionBatchStore persists and lists action batches. Best-effort: a batch is a
// monitoring aid, never the queue itself, so a store failure must not fail an
// enqueue (the jobs still run and are visible per-job).
type ActionBatchStore interface {
	Create(ctx context.Context, b ActionBatch) error
	// List returns the most recent batches, newest first, up to limit.
	List(ctx context.Context, limit int) ([]ActionBatch, error)
	Get(ctx context.Context, id string) (ActionBatch, bool, error)
}
