// Package smtp talks to an SMTP server to deliver outbound email (P4-06).
//
// The adapter implements port.Mailer. It is the only place that knows the
// wire format of an email: MIME multipart, base64 attachment, CRLF folding.
// The weekly report scheduler works purely in domain terms.
package smtp

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// Mailer delivers email over SMTP. It is safe to call concurrently: every Send
// is a fresh connection.
type Mailer struct {
	addr     string
	from     string
	username string
	password string
	host     string // for PlainAuth (SMTPS STARTTLS server name)
	timeout  time.Duration
	log      func(msg string, args ...any)
}

// Config wires the mailer. From is the envelope sender; Auth is optional for
// an open relay / local mailpit.
type Config struct {
	// Addr is the "host:port" of the SMTP server.
	Addr string
	// Host is the server name used in EHLO and in PlainAuth (for STARTTLS).
	// Defaults to the host part of Addr when empty.
	Host string
	// From is the envelope sender.
	From string
	// Username/Password enable AUTH PLAIN. Empty = no auth (local relay).
	Username string
	Password string
	// Timeout per connection attempt. 0 = 10s.
	Timeout time.Duration
	// Log defaults to slog.Default.
	Log func(msg string, args ...any)
}

// NewMailer wires the mailer. Validation is at config-load time, not here, so a
// misconfiguration surfaces at boot where the message is actionable.
func NewMailer(cfg Config) *Mailer {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.Log == nil {
		cfg.Log = func(string, ...any) {}
	}
	host := cfg.Host
	if host == "" {
		h, _, err := net.SplitHostPort(cfg.Addr)
		if err == nil {
			host = h
		} else {
			host = cfg.Addr
		}
	}
	return &Mailer{
		addr:     cfg.Addr,
		from:     cfg.From,
		username: cfg.Username,
		password: cfg.Password,
		host:     host,
		timeout:  cfg.Timeout,
		log:      cfg.Log,
	}
}

// Send delivers one message. A message with an attachment becomes a
// multipart/mixed part; without one it is a single text/plain (or
// text/plain+text/html alternative) body.
func (m *Mailer) Send(ctx context.Context, msg port.Email) error {
	if len(msg.To) == 0 {
		return fmt.Errorf("%w: mailer: no recipients", errInvalid)
	}
	if m.addr == "" {
		return fmt.Errorf("%w: mailer: SMTP_ADDR not configured", errInvalid)
	}

	dialer := &net.Dialer{Timeout: m.timeout}
	conn, err := dialer.DialContext(ctx, "tcp", m.addr)
	if err != nil {
		return fmt.Errorf("mailer: dial %s: %w", m.addr, err)
	}
	defer conn.Close()

	c, err := smtp.NewClient(conn, m.host)
	if err != nil {
		return fmt.Errorf("mailer: client: %w", err)
	}
	defer c.Quit()

	if m.username != "" {
		if err := c.Auth(smtp.PlainAuth("", m.username, m.password, m.host)); err != nil {
			return fmt.Errorf("mailer: auth: %w", err)
		}
	}

	if err := c.Mail(m.from); err != nil {
		return fmt.Errorf("mailer: MAIL FROM: %w", err)
	}
	for _, to := range msg.To {
		if err := c.Rcpt(to); err != nil {
			return fmt.Errorf("mailer: RCPT TO %s: %w", to, err)
		}
	}

	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("mailer: DATA: %w", err)
	}
	if _, err := io.WriteString(w, m.build(msg)); err != nil {
		return fmt.Errorf("mailer: write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mailer: close body: %w", err)
	}
	return nil
}

// build renders the full RFC-822 message. Headers are CRLF-folded; the body is
// the MIME structure chosen by the message's fields.
func (m *Mailer) build(msg port.Email) string {
	var b strings.Builder
	b.WriteString("From: " + m.from + "\r\n")
	b.WriteString("To: " + strings.Join(msg.To, ", ") + "\r\n")
	b.WriteString("Subject: " + mimeFold(msg.Subject) + "\r\n")
	b.WriteString("Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")

	body, ctype := m.body(msg)
	if msg.Attachment != nil {
		boundary := "smm-report-" + randBoundary()
		b.WriteString("Content-Type: multipart/mixed; boundary=\"" + boundary + "\"\r\n\r\n")
		b.WriteString("--" + boundary + "\r\n")
		b.WriteString("Content-Type: " + ctype + "; charset=utf-8\r\n")
		b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
		b.WriteString(body)
		b.WriteString("\r\n")
		b.WriteString("--" + boundary + "\r\n")
		b.WriteString("Content-Type: " + msg.AttachMIME + "; name=\"" + msg.AttachName + "\"\r\n")
		b.WriteString("Content-Transfer-Encoding: base64\r\n")
		b.WriteString("Content-Disposition: attachment; filename=\"" + msg.AttachName + "\"\r\n\r\n")
		b.WriteString(base64.StdEncoding.EncodeToString(msg.Attachment))
		b.WriteString("\r\n--" + boundary + "--\r\n")
		return b.String()
	}

	b.WriteString("Content-Type: " + ctype + "; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	b.WriteString(body)
	return b.String()
}

// body returns the body text and its MIME type. An HTML alternative is only
// emitted when HTML is set (exactOptional: empty means text-only).
func (m *Mailer) body(msg port.Email) (string, string) {
	if msg.HTML != "" {
		boundary := "smm-alt-" + randBoundary()
		var b strings.Builder
		b.WriteString("--" + boundary + "\r\n")
		b.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
		b.WriteString(msg.Body)
		b.WriteString("\r\n--" + boundary + "\r\n")
		b.WriteString("Content-Type: text/html; charset=utf-8\r\n\r\n")
		b.WriteString(msg.HTML)
		b.WriteString("\r\n--" + boundary + "--\r\n")
		return b.String(), "multipart/alternative; boundary=\"" + boundary + "\""
	}
	return msg.Body, "text/plain"
}

// mimeFold breaks a long subject into continued lines so no header exceeds the
// 78-column soft limit. A short subject passes through untouched.
func mimeFold(subject string) string {
	const max = 70
	if len(subject) <= max {
		return subject
	}
	var b strings.Builder
	line := ""
	for _, word := range strings.Fields(subject) {
		if len(line)+len(word)+1 > max && line != "" {
			b.WriteString(line + "\r\n ")
			line = word
			continue
		}
		if line == "" {
			line = word
		} else {
			line += " " + word
		}
	}
	b.WriteString(line)
	return b.String()
}

// randBoundary is a cheap MIME boundary. It does not need cryptographic
// strength — it only has to be unique within this message.
func randBoundary() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// errInvalid is the local validation sentinel; the caller maps it to 400.
var errInvalid = errors.New("invalid")
