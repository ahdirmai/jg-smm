package smtp

import (
	"context"
	"strings"
	"testing"

	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// TestBuildPlainText covers the single-part path: no HTML, no attachment.
func TestBuildPlainText(t *testing.T) {
	m := NewMailer(Config{Addr: "mailhog:1025", From: "r@example.com"})
	msg := port.Email{To: []string{"a@x.com", "b@x.com"}, Subject: "hi", Body: "hello world"}
	got := m.build(msg)

	for _, want := range []string{
		"From: r@example.com\r\n",
		"To: a@x.com, b@x.com\r\n",
		"Subject: hi\r\n",
		"Content-Type: text/plain; charset=utf-8\r\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if !strings.HasSuffix(got, "hello world") {
		t.Errorf("body must trail the message, got %q", got)
	}
	if strings.Contains(got, "multipart") {
		t.Errorf("plain text must not be multipart: %q", got)
	}
}

// TestBuildHTML covers the alternative path: HTML present means
// multipart/alternative so a text-only client still degrades cleanly.
func TestBuildHTML(t *testing.T) {
	m := NewMailer(Config{Addr: "mailhog:1025", From: "r@example.com"})
	msg := port.Email{To: []string{"a@x.com"}, Subject: "hi", Body: "plain", HTML: "<b>rich</b>"}
	got := m.build(msg)

	if !strings.Contains(got, "multipart/alternative") {
		t.Errorf("HTML present must build an alternative part: %q", got)
	}
	if !strings.Contains(got, "Content-Type: text/plain; charset=utf-8") {
		t.Errorf("alternative must still carry the plain part: %q", got)
	}
	if !strings.Contains(got, "Content-Type: text/html; charset=utf-8") {
		t.Errorf("alternative must carry the html part: %q", got)
	}
	if !strings.Contains(got, "<b>rich</b>") {
		t.Errorf("html body missing: %q", got)
	}
}

// TestBuildAttachment covers the mixed path: a CSV becomes a base64 part.
func TestBuildAttachment(t *testing.T) {
	m := NewMailer(Config{Addr: "mailhog:1025", From: "r@example.com"})
	msg := port.Email{
		To: []string{"a@x.com"}, Subject: "hi", Body: "plain",
		Attachment: []byte("day,total\n2026-09-14,3\n"),
		AttachName: "actions.csv", AttachMIME: "text/csv",
	}
	got := m.build(msg)

	if !strings.Contains(got, "multipart/mixed") {
		t.Errorf("attachment present must build a mixed part: %q", got)
	}
	if !strings.Contains(got, `filename="actions.csv"`) {
		t.Errorf("attachment filename missing: %q", got)
	}
	if !strings.Contains(got, "Content-Transfer-Encoding: base64") {
		t.Errorf("attachment must be base64: %q", got)
	}
}

// TestMimeFold covers long-subject continuation: no header line may exceed the
// soft limit, and the words must survive the round trip.
func TestMimeFold(t *testing.T) {
	short := "weekly report"
	if got := mimeFold(short); got != short {
		t.Errorf("short subject must pass through: got %q", got)
	}

	long := "weekly action report for the JG Social automation fleet across instagram and threads accounts"
	got := mimeFold(long)
	for _, line := range strings.Split(got, "\r\n") {
		// A folded line carries one leading space from the continuation.
		if len(line) > 72 {
			t.Errorf("folded line too long (%d): %q", len(line), line)
		}
	}
	joined := strings.ReplaceAll(got, "\r\n ", " ")
	if joined != long {
		t.Errorf("fold mangled the subject:\ngot  %q\nwant %q", joined, long)
	}
}

// TestSendValidation covers the two cheap rejections that must never cost a
// network dial: no recipients and no server.
func TestSendValidation(t *testing.T) {
	m := NewMailer(Config{Addr: "", From: "r@example.com"})
	if err := m.Send(context.Background(), port.Email{To: []string{"a@x.com"}}); err == nil {
		t.Error("empty SMTP_ADDR must reject")
	}
	m2 := NewMailer(Config{Addr: "mailhog:1025", From: "r@example.com"})
	if err := m2.Send(context.Background(), port.Email{To: nil}); err == nil {
		t.Error("no recipients must reject")
	}
}
