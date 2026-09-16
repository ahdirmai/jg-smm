package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"log/slog"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// ScrapeIngestor (P2-04) is the second half of the scrape pipeline: it reads
// the raw Apify dataset items back out of object storage and normalizes them
// into Postgres rows. It runs after a scrape job succeeds.
//
// Idempotence is the contract (TICKETS P2-04 AC): re-running the ingest for a
// run must not change row counts. Every upsert is keyed on
// (platform, external_id), and metric snapshots are pure appends whose value
// comes from the payload, so a replay refreshes the same rows.
type ScrapeIngestor struct {
	scrapes port.ScrapeStore
	storage port.RawStorage
	log     *slog.Logger
}

// NewScrapeIngestor wires the ingestor.
func NewScrapeIngestor(store port.ScrapeStore, storage port.RawStorage, logger *slog.Logger) *ScrapeIngestor {
	if logger == nil {
		logger = slog.Default()
	}
	return &ScrapeIngestor{scrapes: store, storage: storage, log: logger}
}

var _ port.ScrapeIngestor = (*ScrapeIngestor)(nil)

// IngestRun reads every raw payload key of one run and normalizes it. A run
// with zero items is a success with zero counts (an empty scrape is data, not
// an error). A payload that cannot be parsed is skipped with a warning — one
// malformed item must never block the well-formed ones.
func (s *ScrapeIngestor) IngestRun(ctx context.Context, runID string) (port.ScrapeIngestResult, error) {
	if s.storage == nil {
		return port.ScrapeIngestResult{}, errors.New("ingestor: storage is required")
	}
	keys, err := s.scrapes.ListRawPayloadsByRunID(ctx, runID)
	if err != nil {
		return port.ScrapeIngestResult{}, fmt.Errorf("ingestor: list payloads: %w", err)
	}

	var res port.ScrapeIngestResult
	for _, key := range keys {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		raw, err := s.storage.Get(ctx, key)
		if err != nil {
			s.log.Warn("ingestor: payload read failed", "key", key, "err", err)
			continue
		}
		res.BytesIngested += int64(len(raw))

		var item scrapeItem
		if err := json.Unmarshal(raw, &item); err != nil {
			s.log.Warn("ingestor: payload parse failed", "key", key, "err", err)
			continue
		}
		if err := s.ingestItem(ctx, item, &res); err != nil {
			s.log.Warn("ingestor: item skipped", "key", key, "err", err)
		}
	}
	s.log.Info("ingestor: run complete", "run", runID, "posts", res.PostsUpserted,
		"comments", res.CommentsUpserted, "snapshots", res.SnapshotsWritten,
		"bytes", res.BytesIngested)
	return res, nil
}

// ingestItem normalizes one actor dataset item into domain entities and writes
// them. A comment with a postId resolves to that post; an item without one is
// treated as a post (the actor's top-level shape).
func (s *ScrapeIngestor) ingestItem(ctx context.Context, item scrapeItem, res *port.ScrapeIngestResult) error {
	p := domain.Platform(item.Platform)
	if !p.Valid() {
		return fmt.Errorf("unknown platform %q", item.Platform)
	}
	if item.ExternalID == "" {
		return errors.New("item has no external id")
	}

	if item.PostID != "" || item.CommentOf != "" {
		return s.ingestComment(ctx, p, item, res)
	}
	return s.ingestPost(ctx, p, item, res)
}

func (s *ScrapeIngestor) ingestPost(ctx context.Context, p domain.Platform, item scrapeItem, res *port.ScrapeIngestResult) error {
	post, err := s.scrapes.UpsertPost(ctx, domain.Post{
		Platform:     p,
		ExternalID:   item.ExternalID,
		AuthorHandle: orDefault(item.AuthorHandle, "unknown"),
		AuthorID:     orDefault(item.AuthorID, item.ExternalID),
		Text:         nilIfEmpty(item.Text),
		MediaURLs:    item.MediaURLs,
		Metrics:      item.metricsJSON(),
	})
	if err != nil {
		return fmt.Errorf("upsert post: %w", err)
	}
	res.PostsUpserted++

	// A metric sample per ingest keeps the history hypertable fed.
	if err := s.scrapes.CreateMetricSnapshot(ctx, domain.MetricSnapshot{
		PostID:   post.ID,
		TS:       time.Now().UTC(),
		Views:    item.metricsInt("views", "viewCount", "playCount"),
		Likes:    item.metricsInt("likes", "likeCount"),
		Comments: item.metricsInt("comments", "commentCount"),
		Shares:   item.metricsInt("shares", "shareCount", "retweetCount"),
		Reach:    item.metricsIntPtr("reach", "impressions"),
	}); err != nil {
		return fmt.Errorf("metric snapshot: %w", err)
	}
	res.SnapshotsWritten++
	return nil
}

func (s *ScrapeIngestor) ingestComment(ctx context.Context, p domain.Platform, item scrapeItem, res *port.ScrapeIngestResult) error {
	// A comment needs a parent post. The actor may send the post id directly
	// (item.PostID) or the external id of the parent (item.CommentOf).
	postID := item.PostID
	if postID == "" {
		parent, err := s.scrapes.GetPostByExternalID(ctx, p, item.CommentOf)
		if err != nil {
			return fmt.Errorf("parent post %q: %w", item.CommentOf, err)
		}
		postID = parent.ID
	}
	_, err := s.scrapes.UpsertComment(ctx, domain.Comment{
		PostID:       postID,
		Platform:     p,
		ExternalID:   item.ExternalID,
		AuthorHandle: orDefault(item.AuthorHandle, "unknown"),
		Text:         orDefault(item.Text, ""),
		Metrics:      item.metricsJSON(),
	})
	if err != nil {
		return fmt.Errorf("upsert comment: %w", err)
	}
	res.CommentsUpserted++
	return nil
}

// --- helpers ----------------------------------------------------------------

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// scrapeItem is the loose shape of an Apify dataset row. The fields cover the
// keys the configured actors emit; unknown keys are ignored rather than
// erroring, so an actor schema change degrades to zero-valued metrics instead
// of breaking ingest.
type scrapeItem struct {
	Platform     string         `json:"platform"`
	ExternalID   string         `json:"externalId"`
	PostID       string         `json:"postId"`
	CommentOf    string         `json:"commentOf"`
	AuthorHandle string         `json:"authorHandle"`
	AuthorID     string         `json:"authorId"`
	Text         string         `json:"text"`
	MediaURLs    []string       `json:"mediaUrls"`
	Metrics      map[string]any `json:"metrics"`
}

// metricsJSON returns the item's metrics as a non-empty jsonb payload.
func (i scrapeItem) metricsJSON() []byte {
	if len(i.Metrics) == 0 {
		return []byte("{}")
	}
	b, err := json.Marshal(i.Metrics)
	if err != nil {
		return []byte("{}")
	}
	return b
}

// metricsInt reads one of several candidate keys as a non-negative int64.
func (i scrapeItem) metricsInt(keys ...string) int64 {
	for _, k := range keys {
		if v, ok := numeric(i.Metrics[k]); ok {
			return v
		}
	}
	return 0
}

func (i scrapeItem) metricsIntPtr(keys ...string) *int64 {
	for _, k := range keys {
		if v, ok := numeric(i.Metrics[k]); ok {
			return &v
		}
	}
	return nil
}

// numeric coerces a JSON number-ish value to int64. Apify datasets send numbers
// as floats and sometimes as strings; both are accepted, anything else is not.
func numeric(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	case string:
		// best-effort; a non-numeric string yields false
		var f float64
		if _, err := fmt.Sscanf(n, "%f", &f); err == nil {
			return int64(f), true
		}
	}
	return 0, false
}
