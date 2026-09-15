package service

import (
	"context"
	"time"

	"log/slog"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/port"
)

// MetricAggregator (P2-05) keeps the top-post monitoring series fresh between
// scrape runs. Every tick it takes the current top-N posts by a metric and
// writes one new metric_snapshot row per post from the latest known values.
//
// Why this exists at all: scrapes are pull-based and infrequent, but the
// "top 100 posts" monitoring view is what an operator watches. Re-sampling the
// latest values on a short cadence means the trend hypertable has a point per
// interval per post even when no scrape landed in it, so charts and the
// views-drop alert rule see a continuous series rather than gaps.
//
// It never invents numbers: every value written is the last observed one, only
// the timestamp advances. A post with no history contributes nothing.
type MetricAggregator struct {
	scrapes port.ScrapeStore
	clock   func() time.Time
	log     *slog.Logger
	cfg     MetricAggregatorConfig
}

// MetricAggregatorConfig bounds one aggregation tick.
type MetricAggregatorConfig struct {
	// TopN is how many leading posts are re-sampled each tick (the ticket's
	// "top-100").
	TopN int
	// Metric ranks the top posts. "views" is the PRD's headline metric.
	Metric string
	// Clock is injectable, mainly for tests.
	Clock func() time.Time
	// Logger defaults to slog.Default.
	Logger *slog.Logger
}

// NewMetricAggregator wires the aggregator with the ticket's defaults.
func NewMetricAggregator(scrapes port.ScrapeStore, cfg MetricAggregatorConfig) *MetricAggregator {
	if cfg.TopN <= 0 || cfg.TopN > 500 {
		cfg.TopN = 100
	}
	if cfg.Metric == "" {
		cfg.Metric = "views"
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &MetricAggregator{scrapes: scrapes, clock: cfg.Clock, log: cfg.Logger, cfg: cfg}
}

// Run ticks until ctx is cancelled. A tick that errors is logged and skipped —
// one failed aggregation must never stop the loop, and the data is a stale
// snapshot, not a lost write.
func (a *MetricAggregator) Run(ctx context.Context, interval time.Duration) {
	a.log.Info("metric aggregator started", "interval", interval, "topN", a.cfg.TopN, "metric", a.cfg.Metric)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			a.log.Info("metric aggregator stopped")
			return
		case now := <-t.C:
			written, err := a.Aggregate(ctx)
			if err != nil {
				a.log.Warn("metric aggregation failed", "err", err)
				continue
			}
			a.log.Debug("metric aggregation ok", "written", written, "at", now.UTC())
		}
	}
}

// Aggregate performs one pass: rank the top posts, then re-sample each from its
// latest known values. Returns the number of fresh snapshot rows written.
func (a *MetricAggregator) Aggregate(ctx context.Context) (int, error) {
	top, err := a.scrapes.TopPostsByMetric(ctx, a.cfg.Metric, a.cfg.TopN)
	if err != nil {
		return 0, err
	}
	if len(top) == 0 {
		return 0, nil
	}

	ts := a.clock().UTC()
	written := 0
	for _, m := range top {
		// Carry the last observed values forward; only the timestamp is new.
		// Reach/reels_views are nullable pointers already, so they pass through
		// untouched when the source never reported them.
		if err := a.scrapes.CreateMetricSnapshot(ctx, domain.MetricSnapshot{
			PostID:     m.PostID,
			TS:         ts,
			Views:      m.Views,
			Likes:      m.Likes,
			Comments:   m.Comments,
			Shares:     m.Shares,
			Reach:      m.Reach,
			ReelsViews: m.ReelsViews,
		}); err != nil {
			// One post failing must not abort the rest of the batch.
			a.log.Warn("metric snapshot re-sample failed", "postId", m.PostID, "err", err)
			continue
		}
		written++
	}
	return written, nil
}
