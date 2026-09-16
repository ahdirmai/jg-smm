package service

import (
	"context"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
)

// aggregatorFor wires the aggregator against the in-memory fake with a fixed
// clock, so the re-sampled timestamp is deterministic.
func aggregatorFor(t *testing.T) (*MetricAggregator, *fakeScrapeStore) {
	t.Helper()
	scrapes := newFakeScrapeStore()
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	a := NewMetricAggregator(scrapes, MetricAggregatorConfig{Clock: func() time.Time { return now }})
	return a, scrapes
}

// TestMetricAggregatorResamples: one tick must write exactly one fresh row per
// top post, carrying the last observed values and only advancing the timestamp.
func TestMetricAggregatorResamples(t *testing.T) {
	a, scrapes := aggregatorFor(t)
	ctx := context.Background()

	post, _ := scrapes.UpsertPost(ctx, domain.Post{
		Platform:     domain.PlatformInstagram,
		ExternalID:   "agg-post-1",
		AuthorHandle: "brand",
		AuthorID:     "1",
	})
	reach := int64(40_000)
	reels := int64(12_000)
	scrapes.CreateMetricSnapshot(ctx, domain.MetricSnapshot{
		PostID:     post.ID,
		TS:         a.clock().Add(-2 * time.Hour),
		Views:      1_000,
		Likes:      50,
		Comments:   7,
		Shares:     3,
		Reach:      &reach,
		ReelsViews: &reels,
	})

	written, err := a.Aggregate(ctx)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if written != 1 {
		t.Fatalf("written = %d, want 1", written)
	}

	got := scrapes.snapshots[len(scrapes.snapshots)-1]
	if got.PostID != post.ID {
		t.Errorf("PostID = %s, want %s", got.PostID, post.ID)
	}
	if !got.TS.Equal(a.clock()) {
		t.Errorf("TS = %v, want the tick time %v", got.TS, a.clock())
	}
	if got.Views != 1_000 || got.Likes != 50 || got.Comments != 7 || got.Shares != 3 {
		t.Errorf("carried values = %+v, want the last observed values", got)
	}
	if got.Reach == nil || *got.Reach != reach {
		t.Errorf("Reach = %v, want %d", got.Reach, reach)
	}
	if got.ReelsViews == nil || *got.ReelsViews != reels {
		t.Errorf("ReelsViews = %v, want %d", got.ReelsViews, reels)
	}
}

// TestMetricAggregatorEmpty: no history means no rows written and no error —
// an empty fleet is a no-op, never a failure.
func TestMetricAggregatorEmpty(t *testing.T) {
	a, scrapes := aggregatorFor(t)
	written, err := a.Aggregate(context.Background())
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if written != 0 {
		t.Fatalf("written = %d, want 0 on an empty store", written)
	}
	if len(scrapes.snapshots) != 0 {
		t.Fatalf("snapshots = %d, want 0", len(scrapes.snapshots))
	}
}

// TestMetricAggregatorSkipsFailingWrite: one post whose snapshot write fails
// must not abort the rest of the batch.
func TestMetricAggregatorSkipsFailingWrite(t *testing.T) {
	a, scrapes := aggregatorFor(t)
	ctx := context.Background()

	p1, _ := scrapes.UpsertPost(ctx, domain.Post{Platform: domain.PlatformInstagram, ExternalID: "ok-1", AuthorHandle: "b", AuthorID: "1"})
	p2, _ := scrapes.UpsertPost(ctx, domain.Post{Platform: domain.PlatformInstagram, ExternalID: "bad-1", AuthorHandle: "b", AuthorID: "2"})
	scrapes.CreateMetricSnapshot(ctx, domain.MetricSnapshot{PostID: p1.ID, TS: a.clock().Add(-time.Hour), Views: 10})
	scrapes.CreateMetricSnapshot(ctx, domain.MetricSnapshot{PostID: p2.ID, TS: a.clock().Add(-time.Hour), Views: 20})

	scrapes.failSnapshotFor = p2.ID
	written, err := a.Aggregate(ctx)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if written != 1 {
		t.Fatalf("written = %d, want 1 (the post whose write succeeded)", written)
	}
}

// TestMetricAggregatorDefaults: the ticket's cadence/rank defaults apply when the
// config is left zero.
func TestMetricAggregatorDefaults(t *testing.T) {
	a, _ := aggregatorFor(t)
	if a.cfg.TopN != 100 {
		t.Errorf("TopN default = %d, want 100", a.cfg.TopN)
	}
	if a.cfg.Metric != "views" {
		t.Errorf("Metric default = %q, want %q", a.cfg.Metric, "views")
	}
}
