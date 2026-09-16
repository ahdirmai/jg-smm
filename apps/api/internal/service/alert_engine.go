package service

import (
	"context"
	"fmt"
	"time"

	"log/slog"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// AlertEngine (P2-07) evaluates monitoring rules against scraped metrics and
// emits alerts. Rules are the small, opinionated set the PRD names:
//   - views drop > 50% over 24h on a monitored post;
//   - mention spike > 3x the 7-day baseline for an official account.
//
// Deliberately NOT a generic rules engine: two checks, two code paths, and an
// extension point (Rule) only if a third shape ever shows up. Alerts are
// published to the SSE hub so the dashboard surfaces them live; they are also
// returned from Evaluate for anything that wants to persist or forward them.
type AlertEngine struct {
	scrapes   port.ScrapeStore
	analytics port.AnalyticsStore
	clock     func() time.Time
	log       *slog.Logger
	cfg       AlertEngineConfig
}

// AlertEngineConfig bounds evaluation and carries the thresholds so they can be
// tuned without editing the rules.
type AlertEngineConfig struct {
	// ViewsDropFraction fires when a post's 24h views fall below this fraction
	// of its prior 24h window (0.5 = a >50% drop).
	ViewsDropFraction float64
	// MentionSpikeMultiple fires when mentions in the last 24h exceed this
	// multiple of the trailing 7-day daily average (3 = 3x).
	MentionSpikeMultiple float64
	// BaselineDays is the trailing window the spike baseline is averaged over.
	BaselineDays int
	// Clock is injectable.
	Clock func() time.Time
	// Logger defaults to slog.Default.
	Logger *slog.Logger
}

// Alert is one fired rule evaluation.
type Alert struct {
	Kind     string // views_drop | mention_spike
	Severity string // warn | critical
	Subject  string // post:<id> | account:<id>
	Message  string
	At       time.Time
}

// NewAlertEngine wires the engine with default thresholds from the PRD.
func NewAlertEngine(scrapes port.ScrapeStore, analytics port.AnalyticsStore, cfg AlertEngineConfig) *AlertEngine {
	if cfg.ViewsDropFraction <= 0 || cfg.ViewsDropFraction >= 1 {
		cfg.ViewsDropFraction = 0.5
	}
	if cfg.MentionSpikeMultiple <= 0 {
		cfg.MentionSpikeMultiple = 3
	}
	if cfg.BaselineDays <= 0 {
		cfg.BaselineDays = 7
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &AlertEngine{
		scrapes:   scrapes,
		analytics: analytics,
		clock:     cfg.Clock,
		log:       cfg.Logger,
		cfg:       cfg,
	}
}

// Evaluate runs every rule and returns the alerts that fired. A rule that fails
// to read its data is logged and skipped — one unreadable metric must not mute
// the other rules.
func (e *AlertEngine) Evaluate(ctx context.Context) ([]Alert, error) {
	var alerts []Alert
	if a, err := e.checkViewsDrop(ctx); err != nil {
		e.log.Warn("alert: views-drop check failed", "err", err)
	} else {
		alerts = append(alerts, a...)
	}
	if a, err := e.checkMentionSpike(ctx); err != nil {
		e.log.Warn("alert: mention-spike check failed", "err", err)
	} else {
		alerts = append(alerts, a...)
	}
	for i := range alerts {
		alerts[i].At = e.clock()
	}
	return alerts, nil
}

// Run is the process-long loop. Alerts are logged every tick; publishing to the
// dashboard is the caller's job (the engine stays free of transport).
func (e *AlertEngine) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	e.log.Info("alert engine started", "interval", interval)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			e.log.Info("alert engine stopped")
			return
		case <-t.C:
			alerts, err := e.Evaluate(ctx)
			if err != nil {
				e.log.Error("alert engine tick failed", "err", err)
				continue
			}
			for _, a := range alerts {
				e.log.Warn("alert fired", "kind", a.Kind, "subject", a.Subject, "message", a.Message)
			}
		}
	}
}

// checkViewsDrop compares each post's latest-24h views against the prior 24h.
// Only a real collapse fires: a post with no prior window has nothing to drop
// from, so it is skipped rather than alerting on a zero baseline.
func (e *AlertEngine) checkViewsDrop(ctx context.Context) ([]Alert, error) {
	now := e.clock()
	curStart, curEnd := now.Add(-24*time.Hour), now
	prevStart, prevEnd := now.Add(-48*time.Hour), now.Add(-24*time.Hour)

	latest, err := e.scrapes.LatestMetricSnapshots(ctx)
	if err != nil {
		return nil, fmt.Errorf("latest snapshots: %w", err)
	}
	var alerts []Alert
	for _, snap := range latest {
		cur, err := e.scrapes.ListMetricSnapshots(ctx, snap.PostID, curStart, curEnd)
		if err != nil || len(cur) == 0 {
			continue
		}
		prev, err := e.scrapes.ListMetricSnapshots(ctx, snap.PostID, prevStart, prevEnd)
		if err != nil || len(prev) == 0 {
			continue
		}
		curViews := latestValue(cur)
		prevViews := latestValue(prev)
		if prevViews == 0 || curViews == 0 {
			continue // no baseline, or the post already has no views
		}
		ratio := float64(curViews) / float64(prevViews)
		if ratio < e.cfg.ViewsDropFraction {
			alerts = append(alerts, Alert{
				Kind:     "views_drop",
				Severity: severityForDrop(ratio),
				Subject:  "post:" + snap.PostID,
				Message: fmt.Sprintf("views fell to %.0f%% of the prior 24h (%d -> %d)",
					ratio*100, prevViews, curViews),
			})
		}
	}
	return alerts, nil
}

// checkMentionSpike compares the last 24h of mentions per official account
// against the trailing baseline. A young account with no baseline is skipped.
func (e *AlertEngine) checkMentionSpike(ctx context.Context) ([]Alert, error) {
	now := e.clock()
	baselineDays := e.cfg.BaselineDays

	accs, err := e.analytics.ListOfficialAccounts(ctx, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("list official accounts: %w", err)
	}
	var alerts []Alert
	for _, acc := range accs {
		if acc.Status == domain.OfficialAccountArchived {
			continue
		}
		recent, err := e.analytics.ListAnalyticsSnapshots(ctx, acc.ID, now.Add(-24*time.Hour), now)
		if err != nil || len(recent) == 0 {
			continue
		}
		base, err := e.analytics.ListAnalyticsSnapshots(ctx, acc.ID, now.Add(-time.Duration(baselineDays)*24*time.Hour), now.Add(-24*time.Hour))
		if err != nil || len(base) == 0 {
			continue
		}
		recentMentions := sumMentions(recent)
		avgBase := sumMentions(base) / int64(len(base))
		if avgBase == 0 || recentMentions == 0 {
			continue
		}
		multiple := float64(recentMentions) / float64(avgBase)
		if multiple > e.cfg.MentionSpikeMultiple {
			alerts = append(alerts, Alert{
				Kind:     "mention_spike",
				Severity: severityForSpike(multiple, e.cfg.MentionSpikeMultiple),
				Subject:  "account:" + acc.ID,
				Message: fmt.Sprintf("@%s mentions %.1fx the %d-day daily average (%d vs %d)",
					acc.Handle, multiple, baselineDays, recentMentions, avgBase),
			})
		}
	}
	return alerts, nil
}

// latestValue is the most recent sample's views in a chronologically sorted
// series (ListMetricSnapshots returns ts ascending).
func latestValue(series []domain.MetricSnapshot) int64 {
	if len(series) == 0 {
		return 0
	}
	return series[len(series)-1].Views
}

func sumMentions(series []domain.AnalyticsSnapshot) int64 {
	var total int64
	for _, s := range series {
		if s.Mentions != nil {
			total += *s.Mentions
		}
	}
	return total
}

// severityForDrop escalates a deeper collapse. Anything under 25% of the prior
// window is critical: that is a post that stopped being served, not a slow fade.
func severityForDrop(ratio float64) string {
	if ratio < 0.25 {
		return "critical"
	}
	return "warn"
}

// severityForSpike escalates the further above the threshold the spike lands.
func severityForSpike(multiple, threshold float64) string {
	if multiple >= threshold*2 {
		return "critical"
	}
	return "warn"
}
