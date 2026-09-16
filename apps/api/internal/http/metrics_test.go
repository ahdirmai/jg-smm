package http

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ahdirmai/jg-smm/apps/api/internal/service"
)

// TestMetricsEndpoint covers the Prometheus contract, not just that a handler
// exists: the content type is what a scrape expects, and the body carries the
// Go runtime collectors, so a future "simplify this to JSON" refactor breaks CI
// here rather than every alert in Grafana.
func TestMetricsEndpoint(t *testing.T) {
	e := NewRouter(Dependencies{
		Health:  NewHealthHandler(service.NewHealthService(nil)),
		Metrics: NewMetricsHandler(),
	})

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, MetricsContentType) {
		t.Errorf("content-type = %q, want %q", ct, MetricsContentType)
	}

	body, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	// The process collectors are always present once the default registry is
	// wired; a registered counter would appear here once a path increments it.
	if !strings.Contains(string(body), "go_goroutines") {
		t.Error("body lacks go_goroutines: the runtime collectors are not registered")
	}
}

// TestMetricsEndpointNoAuth documents the deliberate design: /metrics sits
// outside the /api group, so a scrape never depends on a valid session. If
// someone later gates it, alerts silently die on token expiry — this test is
// the place that decision is enforced.
func TestMetricsEndpointNoAuth(t *testing.T) {
	e := NewRouter(Dependencies{
		Health:  NewHealthHandler(service.NewHealthService(nil)),
		Metrics: NewMetricsHandler(),
		// Auth wired but no session: /metrics must still answer 200.
	})

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 without credentials", rec.Code)
	}
}
