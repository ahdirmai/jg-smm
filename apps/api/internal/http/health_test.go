package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/port"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/service"
)

type fakeChecker struct{ err error }

func (f fakeChecker) Ping(context.Context) error { return f.err }

func TestHealthEndpoints(t *testing.T) {
	tests := []struct {
		name       string
		checkers   map[string]port.HealthChecker
		path       string
		wantStatus int
	}{
		{"live is always ok", nil, "/healthz", http.StatusOK},
		{"ready ok when deps up", map[string]port.HealthChecker{"db": fakeChecker{}}, "/readyz", http.StatusOK},
		{"ready 503 when dep down", map[string]port.HealthChecker{"db": fakeChecker{err: errors.New("down")}}, "/readyz", http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := NewRouter(Dependencies{Health: NewHealthHandler(service.NewHealthService(tt.checkers))})
			srv := httptest.NewServer(e)
			defer srv.Close()

			resp, err := http.Get(srv.URL + tt.path)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status: got %d want %d", resp.StatusCode, tt.wantStatus)
			}
		})
	}
}

func TestErrorHandler_MapsSentinels(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"not found", domain.ErrNotFound, http.StatusNotFound, "not_found"},
		{"conflict", domain.ErrConflict, http.StatusConflict, "conflict"},
		{"validation", domain.ErrValidation, http.StatusBadRequest, "validation"},
		{"unauthorized", domain.ErrUnauthorized, http.StatusUnauthorized, "unauthorized"},
		{"rate limited", domain.ErrRateLimited, http.StatusTooManyRequests, "rate_limited"},
		{"unknown is internal", errors.New("some driver error"), http.StatusInternalServerError, "internal"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/boom", nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			errorHandler(tt.err, c)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status: got %d want %d", rec.Code, tt.wantStatus)
			}
			if body := rec.Body.String(); !strings.Contains(body, `"code":"`+tt.wantCode+`"`) {
				t.Fatalf("body %q missing code %q", body, tt.wantCode)
			}
		})
	}
}
