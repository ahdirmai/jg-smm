package http

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/adapter"
	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
	"github.com/ahdirmai/jg-smm/apps/api/internal/service"
)

// newStreamTestServer boots the API with a hub wired to a stream handler at the
// given role. The hub is returned so the test can publish into it.
func newStreamTestServer(t *testing.T, role domain.Role) (*httptest.Server, *adapter.Hub) {
	t.Helper()
	hash, err := service.HashPassword("pw-123456789")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	users := &memUsers{m: map[string]port.User{
		"user@example.com": {ID: "u1", Email: "user@example.com", Name: "U", PasswordHash: hash, Role: role},
	}}
	sessions := &memSessions{active: map[string]port.User{}}
	issuer, _ := adapter.NewJWTIssuer("0123456789abcdef0123456789abcdef", "smm-test")
	authSvc := service.NewAuthService(users, sessions, issuer, adapter.SystemClock{})

	hub := adapter.NewHub(8)
	e := NewRouter(Dependencies{
		Health: NewHealthHandler(service.NewHealthService(nil)),
		Auth:   NewAuthHandler(authSvc, false),
		Stream: NewStreamHandler(hub),
	})
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)
	return srv, hub
}

// readSSE reads frames until n events arrive or the deadline passes.
func readSSE(t *testing.T, body *http.Response, n int) []string {
	t.Helper()
	sc := bufio.NewScanner(body.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	var events []string
	var kind string
	deadline := time.Now().Add(5 * time.Second)
	for len(events) < n && time.Now().Before(deadline) {
		if !sc.Scan() {
			if err := sc.Err(); err != nil {
				t.Fatalf("scan: %v", err)
			}
			time.Sleep(10 * time.Millisecond)
			continue
		}
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			kind = strings.TrimPrefix(line, "event: ")
		case line == "" && kind != "":
			events = append(events, kind)
			kind = ""
		}
	}
	return events
}

func TestStreamFanout(t *testing.T) {
	srv, hub := newStreamTestServer(t, domain.RoleOperator)
	cookies := loginCookieJar(t, srv.URL)

	req, err := http.NewRequest("GET", srv.URL+"/api/stream", nil)
	if err != nil {
		t.Fatalf("req: %v", err)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type = %q", ct)
	}

	// Publish after the connection is established. Give the server a moment to
	// register the subscriber.
	time.Sleep(50 * time.Millisecond)
	hub.Publish(context.Background(), "account-updated", []byte(`{"id":"a1"}`))
	hub.Publish(context.Background(), "worker-health", []byte(`{"workerId":"w1"}`))

	got := readSSE(t, resp, 2)
	want := map[string]bool{"account-updated": false, "worker-health": false}
	for _, k := range got {
		want[k] = true
	}
	for k, seen := range want {
		if !seen {
			t.Fatalf("missing SSE event %q (got %v)", k, got)
		}
	}
}

func TestStreamRequiresAuth(t *testing.T) {
	srv, _ := newStreamTestServer(t, domain.RoleOperator)
	resp, err := http.Get(srv.URL + "/api/stream")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d, want 401", resp.StatusCode)
	}
}

// P5-04: the /api group floor is `read`, and STRATEGIST/ANALYST hold it, so
// the SSE channel is a read the analyst role may open. This used to assert
// 403, which was the RBAC bug — a read-only role was locked out of the
// dashboard's own realtime channel.
func TestStreamAnalystAllowed(t *testing.T) {
	srv, _ := newStreamTestServer(t, domain.RoleAnalyst)
	cookies := loginCookieJar(t, srv.URL)
	resp, err := doWithCookies("GET", srv.URL, "/api/stream", cookies, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("analyst status = %d, want 200", resp.StatusCode)
	}
}

// TestStreamSlowConsumerDropped proves overflow disconnects the laggard instead
// of blocking the publisher (the hub's whole contract).
func TestStreamSlowConsumerDropped(t *testing.T) {
	hub := adapter.NewHub(2)
	events, unsub := hub.Subscribe()
	defer unsub()

	ctx := context.Background()
	for i := 0; i < 10; i++ {
		hub.Publish(ctx, "account-updated", []byte(`{}`))
	}
	// The queue holds 2; the rest overflow and the channel is closed.
	_, open := <-events
	if !open {
		t.Fatal("expected at least one buffered event before close")
	}
	if _, open := <-events; !open {
		t.Fatal("expected the channel to be closed after overflow")
	}
}
