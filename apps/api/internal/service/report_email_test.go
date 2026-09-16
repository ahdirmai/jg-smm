package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// fakeMailer captures the message instead of sending it.
type fakeMailer struct {
	sent []port.Email
	fail error
}

func (m *fakeMailer) Send(_ context.Context, msg port.Email) error {
	if m.fail != nil {
		return m.fail
	}
	m.sent = append(m.sent, msg)
	return nil
}

func newDigestService(t *testing.T, mailer port.Mailer, rows []ActionRow) *ReportEmailService {
	t.Helper()
	reports := NewReportService(&fakeReportQuery{actions: rows}, reportAnalyticsStore(), nil)
	return NewReportEmailService(reports, mailer, ReportEmailConfig{
		Recipients: []string{"team@example.com"},
		SenderName: "JG Social",
		WindowDays: 7,
		Clock:      func() time.Time { return day("2026-09-14") },
	})
}

func TestReportEmailSendsDigestWithAttachment(t *testing.T) {
	mailer := &fakeMailer{}
	svc := newDigestService(t, mailer, []ActionRow{
		{Day: day("2026-09-08"), AccountID: "acc1", Username: "one", Platform: domain.PlatformInstagram, ActionType: "comment", Total: 10, Succeeded: 8, Failed: 2},
		{Day: day("2026-09-09"), AccountID: "acc2", Username: "two", Platform: domain.PlatformThreads, ActionType: "like", Total: 4, Succeeded: 4},
	})

	if err := svc.SendOnce(context.Background()); err != nil {
		t.Fatalf("SendOnce: %v", err)
	}
	if len(mailer.sent) != 1 {
		t.Fatalf("got %d messages, want 1", len(mailer.sent))
	}
	msg := mailer.sent[0]

	if got, want := len(msg.To), 1; got != want {
		t.Errorf("To: got %d, want %d", got, want)
	}
	if got, want := msg.To[0], "team@example.com"; got != want {
		t.Errorf("To[0]: got %q, want %q", got, want)
	}
	if !strings.Contains(msg.Subject, "weekly action report") {
		t.Errorf("subject %q missing 'weekly action report'", msg.Subject)
	}
	if !strings.Contains(msg.Subject, "2026-09-07") || !strings.Contains(msg.Subject, "2026-09-14") {
		t.Errorf("subject %q must span the window", msg.Subject)
	}

	// Headline numbers appear in both bodies.
	for _, body := range []string{msg.Body, msg.HTML} {
		if !strings.Contains(body, "Actions:") && !strings.Contains(body, "Actions") {
			t.Errorf("body missing total: %q", body)
		}
	}
	if !strings.Contains(msg.Body, "14") {
		t.Errorf("plain body missing total 14: %q", msg.Body)
	}
	if !strings.Contains(msg.Body, "@one") || !strings.Contains(msg.Body, "@two") {
		t.Errorf("plain body missing per-account lines: %q", msg.Body)
	}

	// The CSV attachment is the same export the dashboard offers.
	if len(msg.Attachment) == 0 {
		t.Fatal("no attachment")
	}
	if got, want := msg.AttachName, "actions-2026-09-14.csv"; got != want {
		t.Errorf("attach name: got %q, want %q", got, want)
	}
	if !strings.Contains(string(msg.Attachment), "comment") {
		t.Errorf("attachment missing the comment row: %q", msg.Attachment)
	}
}

func TestReportEmailEmptyWindowSendsDashNotZeroPercent(t *testing.T) {
	mailer := &fakeMailer{}
	svc := newDigestService(t, mailer, nil)

	if err := svc.SendOnce(context.Background()); err != nil {
		t.Fatalf("SendOnce: %v", err)
	}
	if !strings.Contains(mailer.sent[0].Body, "—") {
		t.Errorf("empty window should show an em dash, not 0.0%%: %q", mailer.sent[0].Body)
	}
}

func TestReportEmailRejectsEmptyRecipients(t *testing.T) {
	reports := NewReportService(&fakeReportQuery{}, reportAnalyticsStore(), nil)
	svc := NewReportEmailService(reports, &fakeMailer{}, ReportEmailConfig{
		WindowDays: 7,
		Clock:      func() time.Time { return day("2026-09-14") },
	})
	if err := svc.SendOnce(context.Background()); err == nil {
		t.Fatal("SendOnce with no recipients must fail")
	}
}

func TestReportEmailSendFailureIsSurfaced(t *testing.T) {
	mailer := &fakeMailer{fail: context.Canceled}
	svc := newDigestService(t, mailer, []ActionRow{
		{Day: day("2026-09-08"), AccountID: "acc1", Username: "one", ActionType: "comment", Total: 1},
	})
	if err := svc.SendOnce(context.Background()); err == nil {
		t.Fatal("a mailer failure must propagate")
	}
}

func TestReportEmailDefaultsApply(t *testing.T) {
	reports := NewReportService(&fakeReportQuery{}, reportAnalyticsStore(), nil)
	svc := NewReportEmailService(reports, &fakeMailer{}, ReportEmailConfig{
		Recipients: []string{"x@example.com"},
		Clock:      func() time.Time { return day("2026-09-14") },
	})
	if svc.cfg.WindowDays != 7 {
		t.Errorf("WindowDays default: got %d, want 7", svc.cfg.WindowDays)
	}
	if svc.cfg.Kind != ExportActions {
		t.Errorf("Kind default: got %q, want %q", svc.cfg.Kind, ExportActions)
	}
	if svc.cfg.SenderName != "" {
		t.Errorf("SenderName should be optional, got %q", svc.cfg.SenderName)
	}
}

// The em-dash sentinel and the success-rate helper are the two values that a
// 0/0 division could turn into a NaN on the dashboard.
func TestSuccessRateHandlesZero(t *testing.T) {
	if got := successRate(0, 0); got != "—" {
		t.Errorf("successRate(0,0): got %q, want —", got)
	}
	if got := successRate(10, 8); got != "80.0%" {
		t.Errorf("successRate(10,8): got %q, want 80.0%%", got)
	}
}
