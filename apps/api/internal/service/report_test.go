package service

import (
	"bytes"
	"context"
	"encoding/csv"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
)

// fakeReportQuery backs the report service: rows are exactly what the repo
// would return, so the test covers folding + export, not SQL.
type fakeReportQuery struct {
	actions []ActionRow
	targets []TargetRow
}

func (f *fakeReportQuery) ActionRollup(_ context.Context, _ ReportFilter) ([]ActionRow, error) {
	return f.actions, nil
}

func (f *fakeReportQuery) TargetRollup(_ context.Context, _ ReportFilter) ([]TargetRow, error) {
	return f.targets, nil
}

func day(t string) time.Time {
	got, err := time.Parse(time.DateOnly, t)
	if err != nil {
		panic(err)
	}
	return got
}

func reportAnalyticsStore() *fakeAnalyticsStore {
	store := newFakeAnalyticsStore()
	store.CreateOfficialAccount(context.Background(), domain.OfficialAccount{
		Platform: "instagram", Handle: "brand", Status: domain.OfficialAccountActive,
	})
	// fakeID is deterministic, so the created account id is stable here.
	id := fakeID("oa", "instagram|brand")
	store.series = map[string][]domain.TrendPoint{
		id: {{Bucket: day("2026-09-01"), Value: 100}},
	}
	return store
}

func TestActionReportSeriesFoldsRowsPerDay(t *testing.T) {
	svc := NewReportService(&fakeReportQuery{
		actions: []ActionRow{
			{Day: day("2026-09-01"), AccountID: "a1", Username: "one", Platform: "instagram", ActionType: "action_like", Total: 3, Succeeded: 2, Failed: 1},
			{Day: day("2026-09-01"), AccountID: "a2", Username: "two", Platform: "threads", ActionType: "action_comment", Total: 4, Succeeded: 4},
			{Day: day("2026-09-02"), AccountID: "a1", Username: "one", Platform: "instagram", ActionType: "action_like", Total: 1},
		},
	}, newFakeAnalyticsStore(), nil)

	rows, series, err := svc.ActionReport(context.Background(), ReportFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	// Two distinct days, each the sum of that day's rows regardless of account.
	if len(series) != 2 {
		t.Fatalf("series points = %d, want 2", len(series))
	}
	if series[0].Value != 7 || series[1].Value != 1 {
		t.Fatalf("series = %v, want [7, 1]", series)
	}
	if series[0].Bucket != day("2026-09-01") {
		t.Fatalf("first bucket = %v, want 2026-09-01", series[0].Bucket)
	}
}

func TestAnalyticsReportRequiresAccount(t *testing.T) {
	svc := NewReportService(&fakeReportQuery{}, newFakeAnalyticsStore(), nil)
	if _, _, err := svc.AnalyticsReport(context.Background(), ReportFilter{}); err == nil {
		t.Fatal("expected validation error for missing accountId")
	}
}

func TestAnalyticsReportDefaultsMetricAndResolvesSeries(t *testing.T) {
	store := reportAnalyticsStore()
	id := fakeID("oa", "instagram|brand")
	svc := NewReportService(&fakeReportQuery{}, store, nil)

	metric, series, err := svc.AnalyticsReport(context.Background(), ReportFilter{AccountID: &id})
	if err != nil {
		t.Fatal(err)
	}
	if metric != string(domain.AnalyticsMetricFollowers) {
		t.Fatalf("metric = %q, want followers default", metric)
	}
	if len(series) != 1 || series[0].Value != 100 {
		t.Fatalf("series = %v, want one point of 100", series)
	}

	unknown := "does-not-exist"
	if _, _, err := svc.AnalyticsReport(context.Background(), ReportFilter{AccountID: &unknown}); err == nil {
		t.Fatal("expected not-found for unknown account")
	}
}

func TestExportCSVStreamsRows(t *testing.T) {
	svc := NewReportService(&fakeReportQuery{
		actions: []ActionRow{
			{Day: day("2026-09-01"), AccountID: "a1", Username: "one", Platform: "instagram", ActionType: "action_like", Total: 3, Succeeded: 2, Failed: 1},
		},
		targets: []TargetRow{
			{ID: "t1", URL: "https://www.instagram.com/p/abc", Platform: "instagram", Total: 2, Succeeded: 2},
		},
	}, newFakeAnalyticsStore(), nil)

	t.Run("actions", func(t *testing.T) {
		var buf bytes.Buffer
		if err := svc.Export(context.Background(), ExportActions, ExportCSV, ReportFilter{}, &buf); err != nil {
			t.Fatal(err)
		}
		rec, err := csv.NewReader(bytes.NewReader(buf.Bytes())).ReadAll()
		if err != nil {
			t.Fatal(err)
		}
		if len(rec) != 2 {
			t.Fatalf("csv rows = %d, want header + 1", len(rec))
		}
		if rec[0][0] != "day" || rec[1][5] != "3" {
			t.Fatalf("csv = %v", rec)
		}
	})

	t.Run("targets", func(t *testing.T) {
		var buf bytes.Buffer
		if err := svc.Export(context.Background(), ExportTargets, ExportCSV, ReportFilter{}, &buf); err != nil {
			t.Fatal(err)
		}
		rec, err := csv.NewReader(bytes.NewReader(buf.Bytes())).ReadAll()
		if err != nil {
			t.Fatal(err)
		}
		if len(rec) != 2 || rec[1][1] != "https://www.instagram.com/p/abc" {
			t.Fatalf("csv = %v", rec)
		}
	})

	t.Run("json", func(t *testing.T) {
		var buf bytes.Buffer
		if err := svc.Export(context.Background(), ExportTargets, ExportJSON, ReportFilter{}, &buf); err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(buf.Bytes(), []byte(`"url":"https://www.instagram.com/p/abc"`)) {
			t.Fatalf("json = %s", buf.String())
		}
	})

	t.Run("invalid", func(t *testing.T) {
		var buf bytes.Buffer
		if err := svc.Export(context.Background(), ExportKind("nope"), ExportCSV, ReportFilter{}, &buf); err == nil {
			t.Fatal("expected rejection of unknown kind")
		}
		if err := svc.Export(context.Background(), ExportActions, ExportFormat("xml"), ReportFilter{}, &buf); err == nil {
			t.Fatal("expected rejection of unknown format")
		}
	})
}
