package service

import (
	"context"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
)

func alertEngineFor(t *testing.T) (*AlertEngine, *fakeScrapeStore, *fakeAnalyticsStore) {
	t.Helper()
	scrapes := newFakeScrapeStore()
	analytics := newFakeAnalyticsStore()
	now := time.Now()
	e := NewAlertEngine(scrapes, analytics, AlertEngineConfig{
		ViewsDropFraction:    0.5,
		MentionSpikeMultiple: 3,
		BaselineDays:         7,
		Clock:                func() time.Time { return now },
	})
	return e, scrapes, analytics
}

func TestAlertEngineViewsDrop(t *testing.T) {
	e, scrapes, _ := alertEngineFor(t)
	ctx := context.Background()

	post, _ := scrapes.UpsertPost(ctx, domain.Post{
		Platform:     domain.PlatformInstagram,
		ExternalID:   "alert-post-1",
		AuthorHandle: "brand",
		AuthorID:     "1",
	})
	now := e.clock()
	// Prior 24h: 1000 views. Current 24h: 300 -> 30% of prior -> fires.
	scrapes.CreateMetricSnapshot(ctx, domain.MetricSnapshot{PostID: post.ID, TS: now.Add(-36 * time.Hour), Views: 1000})
	scrapes.CreateMetricSnapshot(ctx, domain.MetricSnapshot{PostID: post.ID, TS: now.Add(-12 * time.Hour), Views: 300})
	// A healthy post must NOT fire.
	healthy, _ := scrapes.UpsertPost(ctx, domain.Post{
		Platform:     domain.PlatformInstagram,
		ExternalID:   "alert-post-2",
		AuthorHandle: "brand",
		AuthorID:     "2",
	})
	scrapes.CreateMetricSnapshot(ctx, domain.MetricSnapshot{PostID: healthy.ID, TS: now.Add(-36 * time.Hour), Views: 100})
	scrapes.CreateMetricSnapshot(ctx, domain.MetricSnapshot{PostID: healthy.ID, TS: now.Add(-12 * time.Hour), Views: 90})
	// LatestMetricSnapshots needs an entry to know which posts to check.
	scrapes.CreateMetricSnapshot(ctx, domain.MetricSnapshot{PostID: post.ID, TS: now, Views: 300})
	scrapes.CreateMetricSnapshot(ctx, domain.MetricSnapshot{PostID: healthy.ID, TS: now, Views: 90})

	alerts, err := e.Evaluate(ctx)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("expected exactly one views_drop alert, got %d: %+v", len(alerts), alerts)
	}
	a := alerts[0]
	if a.Kind != "views_drop" || a.Subject != "post:"+post.ID {
		t.Fatalf("wrong alert: %+v", a)
	}
	if a.Severity != "warn" { // 30% is below the 50% threshold but not below 25%
		t.Fatalf("30%% drop should be warn, got %s", a.Severity)
	}
}

func TestAlertEngineViewsDropSeverity(t *testing.T) {
	e, scrapes, _ := alertEngineFor(t)
	ctx := context.Background()

	post, _ := scrapes.UpsertPost(ctx, domain.Post{
		Platform:     domain.PlatformInstagram,
		ExternalID:   "alert-post-crit",
		AuthorHandle: "brand",
		AuthorID:     "1",
	})
	now := e.clock()
	// 1000 -> 200 = 20% of prior: below the 25% critical line.
	scrapes.CreateMetricSnapshot(ctx, domain.MetricSnapshot{PostID: post.ID, TS: now.Add(-36 * time.Hour), Views: 1000})
	scrapes.CreateMetricSnapshot(ctx, domain.MetricSnapshot{PostID: post.ID, TS: now.Add(-12 * time.Hour), Views: 200})
	scrapes.CreateMetricSnapshot(ctx, domain.MetricSnapshot{PostID: post.ID, TS: now, Views: 200})

	alerts, _ := e.Evaluate(ctx)
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	if alerts[0].Severity != "critical" {
		t.Fatalf("20%% of prior should be critical, got %s", alerts[0].Severity)
	}
}

func TestAlertEngineNoBaselineNoAlert(t *testing.T) {
	e, scrapes, _ := alertEngineFor(t)
	ctx := context.Background()

	post, _ := scrapes.UpsertPost(ctx, domain.Post{
		Platform:     domain.PlatformInstagram,
		ExternalID:   "alert-post-new",
		AuthorHandle: "brand",
		AuthorID:     "1",
	})
	now := e.clock()
	// Only a current window exists — nothing to compare against.
	scrapes.CreateMetricSnapshot(ctx, domain.MetricSnapshot{PostID: post.ID, TS: now.Add(-12 * time.Hour), Views: 5})
	scrapes.CreateMetricSnapshot(ctx, domain.MetricSnapshot{PostID: post.ID, TS: now, Views: 5})

	alerts, _ := e.Evaluate(ctx)
	if len(alerts) != 0 {
		t.Fatalf("a post with no prior window must not alert, got %+v", alerts)
	}
}

func TestAlertEngineMentionSpike(t *testing.T) {
	e, _, analytics := alertEngineFor(t)
	ctx := context.Background()

	acc := seedOfficial(analytics, "brand-spike")
	now := e.clock()
	// Baseline: 7 daily samples of 10 mentions each.
	for i := 1; i <= 7; i++ {
		m := int64(10)
		analytics.UpsertAnalyticsSnapshot(ctx, domain.AnalyticsSnapshot{
			OfficialAccountID: acc.ID,
			Platform:          acc.Platform,
			TS:                now.Add(-time.Duration(i+1) * 24 * time.Hour),
			Mentions:          &m,
			Provider:          domain.AnalyticsProviderA,
		})
	}
	// Recent 24h: 60 mentions = 6x the 10/day baseline -> fires, and 6 >= 3*2 -> critical.
	recent := int64(60)
	analytics.UpsertAnalyticsSnapshot(ctx, domain.AnalyticsSnapshot{
		OfficialAccountID: acc.ID,
		Platform:          acc.Platform,
		TS:                now.Add(-6 * time.Hour),
		Mentions:          &recent,
		Provider:          domain.AnalyticsProviderA,
	})

	alerts, err := e.Evaluate(ctx)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("expected one mention_spike alert, got %d: %+v", len(alerts), alerts)
	}
	a := alerts[0]
	if a.Kind != "mention_spike" || a.Subject != "account:"+acc.ID {
		t.Fatalf("wrong alert: %+v", a)
	}
	if a.Severity != "critical" {
		t.Fatalf("6x spike (>= 2x threshold) should be critical, got %s", a.Severity)
	}
}

func TestAlertEngineArchivedAccountSkipped(t *testing.T) {
	e, _, analytics := alertEngineFor(t)
	ctx := context.Background()

	acc := seedOfficial(analytics, "brand-archived")
	analytics.ArchiveOfficialAccount(ctx, acc.ID)
	now := e.clock()
	recent := int64(600)
	analytics.UpsertAnalyticsSnapshot(ctx, domain.AnalyticsSnapshot{
		OfficialAccountID: acc.ID,
		Platform:          acc.Platform,
		TS:                now.Add(-time.Hour),
		Mentions:          &recent,
		Provider:          domain.AnalyticsProviderA,
	})

	alerts, _ := e.Evaluate(ctx)
	for _, a := range alerts {
		if a.Subject == "account:"+acc.ID {
			t.Fatalf("archived account must not alert: %+v", a)
		}
	}
}
