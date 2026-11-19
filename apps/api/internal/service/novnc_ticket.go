package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ErrTicketInvalid is returned by Verify for a malformed, mis-signed, wrongly
// bound, or expired ticket. Callers answer 403; the reason is deliberately not
// distinguished, because "expired" vs "bad signature" is an oracle.
var ErrTicketInvalid = errors.New("live-view ticket invalid or expired")

// A boxd worker publishes its noVNC view on a PUBLIC hostname
// (https://smm-worker-03.boxd.sh). On a laptop the same view is bound to
// 127.0.0.1 and the OS keeps the strangers out; on boxd anyone who guesses the
// hostname has a WebSocket to a browser that is logged into an operator account.
//
// So the ticket is the only lock: an HMAC over (expiry, worker name) with the
// fleet's INTERNAL_API_SECRET, minted per click, checked by nginx auth_request
// and again by the worker itself. It is bound to the worker name so a ticket
// for worker-03 cannot open worker-07.

// SignLiveViewTicket mints "<expUnix>.<workerName>.<hex HMAC-SHA256>".
func SignLiveViewTicket(secret, workerName string, expiresAt time.Time) (string, error) {
	if secret == "" {
		return "", errors.New("live-view ticket: secret is not configured")
	}
	if workerName == "" {
		return "", errors.New("live-view ticket: worker name is required")
	}
	exp := strconv.FormatInt(expiresAt.Unix(), 10)
	return exp + "." + workerName + "." + ticketMAC(secret, exp, workerName), nil
}

// VerifyLiveViewTicket checks the signature first, then the expiry, and reports
// whether the ticket was minted for wantWorker. Comparison is constant-time.
func VerifyLiveViewTicket(secret, ticket, wantWorker string, now time.Time) error {
	if secret == "" {
		return ErrTicketInvalid
	}
	exp, worker, mac, ok := splitTicket(ticket)
	if !ok {
		return ErrTicketInvalid
	}
	if worker != wantWorker {
		return ErrTicketInvalid
	}
	if !hmac.Equal([]byte(mac), []byte(ticketMAC(secret, exp, worker))) {
		return ErrTicketInvalid
	}
	expUnix, err := strconv.ParseInt(exp, 10, 64)
	if err != nil {
		return ErrTicketInvalid
	}
	if now.Unix() >= expUnix {
		return ErrTicketInvalid
	}
	return nil
}

func ticketMAC(secret, exp, workerName string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(exp + ":" + workerName))
	return hex.EncodeToString(m.Sum(nil))
}

func splitTicket(ticket string) (exp, worker, mac string, ok bool) {
	parts := strings.Split(ticket, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

// noVNCQuery is the fixed client configuration of the live view: connect on
// load, fit the viewport, and route the WebSocket through the worker's proxy
// path so the nginx auth_request applies to the socket, not just the page.
const noVNCQuery = "autoconnect=true&resize=scale"

// BuildProtectedNoVNCURL appends a ticket to a worker's noVNC base URL.
//
// baseURL is either the bare view (https://host/novnc) or a full vnc.html URL.
// The ticket rides INSIDE the `path` parameter, because that is what the noVNC
// client forwards as the WebSocket path — a ticket in the page query alone would
// authorise the HTML but leave the socket open. It is query-escaped as a whole
// so the inner "?" survives.
//
// CONSEQUENCE FOR THE PROXY (infra/boxd/nginx-worker.conf): the page URL carries
// no ticket, so nginx MUST NOT require one for /novnc/ — only for the exact
// WebSocket endpoint. An auth_request on the whole prefix 403s vnc.html and
// every asset, and the live view never renders.
func BuildProtectedNoVNCURL(baseURL, ticket string) string {
	base := strings.TrimRight(baseURL, "/")
	// Accept both shapes an operator may configure: a bare host/port, or one
	// that already names the client page. Only the latter is left alone.
	if !strings.HasSuffix(base, "vnc.html") {
		base += "/vnc.html"
	}
	// noVNC builds the socket URL as "<scheme>://<host>/" + path, so the path
	// MUST be relative. A leading slash produced "//websockify" (404 under the
	// boxd worker's nginx, which gates exactly /novnc/websockify). Keep the
	// client page's directory as the prefix: a worker mounted at /novnc needs
	// "novnc/websockify", while a direct websockify root needs "websockify".
	prefix := ""
	if u, err := url.Parse(base); err == nil {
		dir := strings.Trim(strings.TrimSuffix(u.Path, "/vnc.html"), "/")
		if dir != "" {
			prefix = dir + "/"
		}
	}
	wsPath := prefix + "websockify?ticket=" + url.QueryEscape(ticket)
	return base + "?" + noVNCQuery + "&path=" + url.QueryEscape(wsPath)
}

// TicketExpiry returns the mint instant plus ttl, never less than a second, so a
// zero ttl cannot silently mint an already-dead ticket.
func TicketExpiry(now time.Time, ttl time.Duration) time.Time {
	if ttl <= 0 {
		ttl = time.Second
	}
	return now.Add(ttl).UTC()
}
