package service

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

const ticketSecret = "test-secret-32-bytes-minimum-value"

// A ticket opens exactly the worker it was minted for, until it expires.
func TestLiveViewTicketLifecycle(t *testing.T) {
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	exp := TicketExpiry(now, 5*time.Minute)

	ticket, err := SignLiveViewTicket(ticketSecret, "smm-worker-03", exp)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := VerifyLiveViewTicket(ticketSecret, ticket, "smm-worker-03", now); err != nil {
		t.Fatalf("a fresh ticket must verify: %v", err)
	}

	cases := []struct {
		name   string
		secret string
		ticket string
		worker string
		at     time.Time
	}{
		{"wrong secret", "another-secret", ticket, "smm-worker-03", now},
		{"wrong worker", ticketSecret, ticket, "smm-worker-07", now},
		{"expired", ticketSecret, ticket, "smm-worker-03", exp},
		{"past expiry", ticketSecret, ticket, "smm-worker-03", exp.Add(time.Second)},
		{"tampered mac", ticketSecret, ticket[:len(ticket)-1] + "0", "smm-worker-03", now},
		{"malformed", ticketSecret, "only.two", "smm-worker-03", now},
		{"empty", ticketSecret, "", "smm-worker-03", now},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := VerifyLiveViewTicket(tc.secret, tc.ticket, tc.worker, tc.at); err == nil {
				t.Fatalf("expected rejection")
			}
		})
	}

	// An empty configured secret disables minting rather than minting with "".
	if _, err := SignLiveViewTicket("", "smm-worker-03", exp); err == nil {
		t.Fatalf("signing with no secret must fail: every boxd worker would accept it")
	}
}

// The ticket has to reach the WebSocket, not just the HTML page: noVNC forwards
// the `path` query as the socket path, so the ticket must be inside it, escaped.
func TestProtectedNoVNCURLCarriesTicketIntoTheSocketPath(t *testing.T) {
	now := time.Now()
	ticket, err := SignLiveViewTicket(ticketSecret, "smm-worker-03", TicketExpiry(now, time.Minute))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	for _, base := range []string{
		"https://smm-worker-03.boxd.sh",
		"https://smm-worker-03.boxd.sh/",
		"https://smm-worker-03.boxd.sh/vnc.html",
	} {
		got := BuildProtectedNoVNCURL(base, ticket)
		u, err := url.Parse(got)
		if err != nil {
			t.Fatalf("parse %q: %v", got, err)
		}
		if !strings.HasSuffix(u.Path, "/vnc.html") {
			t.Fatalf("%q: not a vnc.html URL", got)
		}
		if u.Query().Get("autoconnect") != "true" {
			t.Fatalf("%q: autoconnect missing", got)
		}
		inner := u.Query().Get("path")
		// Root-relative: the worker serves the client at "/" and the socket at
		// "/websockify" (websockify --web), so a "novnc/" prefix 404s both.
		if !strings.HasPrefix(inner, "/websockify?ticket=") {
			t.Fatalf("%q: socket path does not carry the ticket: %q", got, inner)
		}
		innerTicket := strings.TrimPrefix(inner, "/websockify?ticket=")
		if innerTicket != ticket {
			t.Fatalf("%q: ticket mangled by escaping: %q", got, innerTicket)
		}
		if err := VerifyLiveViewTicket(ticketSecret, innerTicket, "smm-worker-03", now); err != nil {
			t.Fatalf("%q: the ticket in the socket path must verify: %v", got, err)
		}
	}
}
