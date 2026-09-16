package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ahdirmai/jg-smm/apps/api/internal/adapter"
	"github.com/ahdirmai/jg-smm/apps/api/internal/service"
)

// TestCORSPreflightAnswersBeforeAuth guards the failure mode that took the whole
// dashboard down: the browser runs on a different origin than the API, so it
// sends a preflight OPTIONS before the real request. If that preflight reaches
// the /api auth gate it comes back 401 with no access-control headers, the
// browser refuses to send the real request, and every page dies with a generic
// "Failed to fetch". CORS must be answered at the root, ahead of auth.
func TestCORSPreflightAnswersBeforeAuth(t *testing.T) {
	const origin = "http://localhost:24081"

	e := NewRouter(Dependencies{
		Health:             NewHealthHandler(service.NewHealthService(nil)),
		Metrics:            NewMetricsHandler(),
		CORSAllowedOrigins: []string{origin},
		// Auth wired but no session store, so any request that reaches the gate
		// answers 401. That is the point: the preflight must never get there.
		Auth: NewAuthHandler(service.NewAuthService(nil, nil, nil, adapter.SystemClock{}), false),
	})

	req := httptest.NewRequest(http.MethodOptions, "/api/containers", nil)
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	req.Header.Set("Access-Control-Request-Headers", "content-type")

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204 (a 401 here means the auth gate ran first and the browser will block the request)", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, origin)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want \"true\" (auth is cookie-based; without it the browser drops the session)", got)
	}
}

// TestCORSSkippedWhenOriginsUnset keeps the test servers (which never serve a
// browser) free of CORS headers — an empty list means "not configured", not
// "allow everything".
func TestCORSSkippedWhenOriginsUnset(t *testing.T) {
	e := NewRouter(Dependencies{
		Health:  NewHealthHandler(service.NewHealthService(nil)),
		Metrics: NewMetricsHandler(),
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "http://localhost:24081")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want empty (CORS must not apply when no origins are configured)", got)
	}
}
