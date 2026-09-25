package domain

import "time"

// KeywordBatchStatus is the lifecycle of a keyword search batch (background).
type KeywordBatchStatus string

const (
	KeywordBatchPending   KeywordBatchStatus = "PENDING"
	KeywordBatchRunning   KeywordBatchStatus = "RUNNING"
	KeywordBatchSucceeded KeywordBatchStatus = "SUCCEEDED"
	KeywordBatchFailed    KeywordBatchStatus = "FAILED"
)

func (s KeywordBatchStatus) Valid() bool {
	switch s {
	case KeywordBatchPending, KeywordBatchRunning, KeywordBatchSucceeded, KeywordBatchFailed:
		return true
	}
	return false
}

func (s KeywordBatchStatus) IsTerminal() bool {
	return s == KeywordBatchSucceeded || s == KeywordBatchFailed
}

// KeywordBatch is one async keyword search (1..5 keywords + window → many posts).
// It is created PENDING and driven to a terminal status by a background goroutine.
// Its posts are reachable via KeywordBatchPost join, so the batch dashboard is
// distinct from the single-post scrape view (POST /api/scrape/target).
type KeywordBatch struct {
	ID            string             `json:"id"`
	Platform      Platform           `json:"platform"`
	Keywords      []string           `json:"keywords"`
	WindowFrom    *time.Time         `json:"windowFrom"`
	WindowTo      *time.Time         `json:"windowTo"`
	MaxPosts      int                `json:"maxPosts"`
	ActorID       string             `json:"actorId"`
	Status        KeywordBatchStatus `json:"status"`
	ApifyRunID    *string            `json:"apifyRunId"`
	ItemsRead     int                `json:"itemsRead"`
	PostsCount    int                `json:"postsCount"`
	CommentsCount int                `json:"commentsCount"`
	Error         *string            `json:"error"`
	CreatedBy     *string            `json:"createdBy"`
	CreatedAt     time.Time          `json:"createdAt"`
	FinishedAt    *time.Time         `json:"finishedAt"`
}
