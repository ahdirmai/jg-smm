package service

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"log/slog"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// ReportEmailService (P4-06) turns the report builder into a weekly digest.
// It is the "job mingguan" the ticket names: an opt-in loop that renders the
// last N days of action results, attaches the CSV, and mails a recipient list.
//
// Deliberately NOT a persisted schedule table: a weekly digest with one fixed
// recipient list and one fixed window is a cron, not a CRUD entity. The window,
// recipients, and subject are config; the content is the same report the
// dashboard renders, so the email can never disagree with the UI.
type ReportEmailService struct {
	reports *ReportService
	mailer  port.Mailer
	clock   func() time.Time
	log     *slog.Logger
	cfg     ReportEmailConfig
}

// ReportEmailConfig carries the digest shape.
type ReportEmailConfig struct {
	// Recipients is the To: list. Required at construction when enabled.
	Recipients []string
	// SenderName appears in the subject line, e.g. "JG Social".
	SenderName string
	// WindowDays is the look-back (default 7).
	WindowDays int
	// Kind selects which report is rendered (default actions).
	Kind ExportKind
	// Clock is injectable.
	Clock func() time.Time
	// Logger defaults to slog.Default.
	Logger *slog.Logger
}

// NewReportEmailService wires the digest loop. Defaults are applied here, not at
// call sites, so a misconfiguration surfaces at boot.
func NewReportEmailService(reports *ReportService, mailer port.Mailer, cfg ReportEmailConfig) *ReportEmailService {
	if cfg.WindowDays <= 0 {
		cfg.WindowDays = 7
	}
	if cfg.Kind == "" {
		cfg.Kind = ExportActions
	}
	if !cfg.Kind.Valid() {
		cfg.Kind = ExportActions
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &ReportEmailService{
		reports: reports,
		mailer:  mailer,
		clock:   cfg.Clock,
		log:     cfg.Logger,
		cfg:     cfg,
	}
}

// Run is the process-long loop. Interval is the tick; the ticket's weekly
// cadence is a 7-day interval, but the loop is agnostic so a daily digest is
// the same code path. A failed send is logged and retried next tick — the
// digest is a convenience, never a critical path.
func (s *ReportEmailService) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 7 * 24 * time.Hour
	}
	s.log.Info("report email started", "interval", interval, "recipients", len(s.cfg.Recipients))
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.log.Info("report email stopped")
			return
		case <-t.C:
			if err := s.SendOnce(ctx); err != nil {
				s.log.Warn("report email failed", "err", err)
			}
		}
	}
}

// SendOnce renders and delivers one digest. It is exported so a manual "send
// now" from the API reuses exactly the code path the loop uses.
func (s *ReportEmailService) SendOnce(ctx context.Context) error {
	if len(s.cfg.Recipients) == 0 {
		return fmt.Errorf("%w: report email has no recipients", domain.ErrValidation)
	}
	to := s.clock()
	from := to.AddDate(0, 0, -s.cfg.WindowDays)
	filter := ReportFilter{
		From: &from,
		To:   &to,
	}

	rows, series, err := s.reports.ActionReport(ctx, filter)
	if err != nil {
		return fmt.Errorf("report email: %w", err)
	}

	var csvBuf bytes.Buffer
	if err := s.reports.Export(ctx, s.cfg.Kind, ExportCSV, filter, &csvBuf); err != nil {
		return fmt.Errorf("report email: export: %w", err)
	}

	subject := fmt.Sprintf("%s weekly action report — %s to %s",
		s.cfg.SenderName,
		from.Format(time.DateOnly),
		to.Format(time.DateOnly))

	email := port.Email{
		To:         s.cfg.Recipients,
		Subject:    subject,
		Body:       s.renderText(rows, series, from, to),
		HTML:       s.renderHTML(rows, series, from, to),
		Attachment: csvBuf.Bytes(),
		AttachName: fmt.Sprintf("actions-%s.csv", to.Format(time.DateOnly)),
		AttachMIME: "text/csv",
	}
	if err := s.mailer.Send(ctx, email); err != nil {
		return fmt.Errorf("report email: send: %w", err)
	}
	s.log.Info("report email sent",
		"recipients", len(s.cfg.Recipients),
		"rows", len(rows))
	return nil
}

// totals folds the rollup into the headline numbers that open the digest.
func totals(rows []ActionRow) (total, succeeded, failed int64) {
	for _, r := range rows {
		total += r.Total
		succeeded += r.Succeeded
		failed += r.Failed
	}
	return total, succeeded, failed
}

// successRate returns "—" when the window was empty rather than a misleading 0%.
func successRate(total, succeeded int64) string {
	if total == 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f%%", float64(succeeded)/float64(total)*100)
}

// renderText is the plain-text body. It mirrors the HTML so a text-only client
// shows the same numbers.
func (s *ReportEmailService) renderText(rows []ActionRow, series []domain.TrendPoint, from, to time.Time) string {
	total, succeeded, failed := totals(rows)
	var b strings.Builder
	fmt.Fprintf(&b, "%s — action report\r\n", s.cfg.SenderName)
	fmt.Fprintf(&b, "Window: %s to %s (%d days)\r\n\r\n", from.Format(time.DateOnly), to.Format(time.DateOnly), s.cfg.WindowDays)
	fmt.Fprintf(&b, "Actions:    %d\r\n", total)
	fmt.Fprintf(&b, "Succeeded:  %d\r\n", succeeded)
	fmt.Fprintf(&b, "Failed:     %d\r\n", failed)
	fmt.Fprintf(&b, "Success:    %s\r\n\r\n", successRate(total, succeeded))

	if len(series) > 0 {
		b.WriteString("Per-day totals\r\n")
		for _, p := range series {
			fmt.Fprintf(&b, "  %s  %d\r\n", p.Bucket.Format(time.DateOnly), p.Value)
		}
		b.WriteString("\r\n")
	}

	b.WriteString("By account\r\n")
	byAccount := make(map[string]int64)
	usernames := make(map[string]string)
	for _, r := range rows {
		byAccount[r.AccountID] += r.Total
		usernames[r.AccountID] = r.Username
	}
	for id, n := range byAccount {
		fmt.Fprintf(&b, "  @%s (%s)  %d\r\n", usernames[id], id, n)
	}
	b.WriteString("\r\nFull CSV is attached.\r\n")
	return b.String()
}

// renderHTML is the rich body. It is intentionally table-based and inline
// styled: email clients strip <style> and class hooks, so the only robust
// styling is per-element.
func (s *ReportEmailService) renderHTML(rows []ActionRow, series []domain.TrendPoint, from, to time.Time) string {
	total, succeeded, failed := totals(rows)
	var b strings.Builder
	b.WriteString("<div style=\"font-family: -apple-system, Segoe UI, Roboto, sans-serif; color: #111; max-width: 560px;\">")
	fmt.Fprintf(&b, "<h2 style=\"margin:0 0 4px;\">%s — action report</h2>", s.cfg.SenderName)
	fmt.Fprintf(&b, "<p style=\"color:#666;margin:0 0 16px;\">%s to %s · %d days</p>",
		from.Format(time.DateOnly), to.Format(time.DateOnly), s.cfg.WindowDays)

	b.WriteString("<table style=\"border-collapse:collapse;margin-bottom:16px;\">")
	for _, cell := range [][2]string{
		{"Actions", fmt.Sprint(total)},
		{"Succeeded", fmt.Sprint(succeeded)},
		{"Failed", fmt.Sprint(failed)},
		{"Success rate", successRate(total, succeeded)},
	} {
		fmt.Fprintf(&b, "<tr><td style=\"padding:4px 24px 4px 0;color:#666;\">%s</td><td style=\"padding:4px 0;font-weight:600;\">%s</td></tr>",
			cell[0], cell[1])
	}
	b.WriteString("</table>")

	if len(series) > 0 {
		b.WriteString("<h3 style=\"margin:0 0 8px;\">Per-day totals</h3><table style=\"border-collapse:collapse;margin-bottom:16px;\">")
		for _, p := range series {
			fmt.Fprintf(&b, "<tr><td style=\"padding:2px 24px 2px 0;color:#666;\">%s</td><td style=\"padding:2px 0;\">%d</td></tr>",
				p.Bucket.Format(time.DateOnly), p.Value)
		}
		b.WriteString("</table>")
	}

	b.WriteString("<p style=\"color:#666;\">Full CSV is attached.</p></div>")
	return b.String()
}
