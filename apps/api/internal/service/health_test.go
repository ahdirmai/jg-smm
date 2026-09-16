package service

import (
	"context"
	"errors"
	"testing"

	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

type fakeChecker struct{ err error }

func (f fakeChecker) Ping(context.Context) error { return f.err }

func TestHealthService_Check(t *testing.T) {
	tests := []struct {
		name       string
		checkers   map[string]port.HealthChecker
		wantStatus string
		wantChecks map[string]string
	}{
		{
			name:       "no checkers is ok",
			checkers:   map[string]port.HealthChecker{},
			wantStatus: "ok",
			wantChecks: map[string]string{},
		},
		{
			name:       "all up",
			checkers:   map[string]port.HealthChecker{"postgres": fakeChecker{}, "redis": fakeChecker{}},
			wantStatus: "ok",
			wantChecks: map[string]string{"postgres": "up", "redis": "up"},
		},
		{
			name:       "one down makes it degraded",
			checkers:   map[string]port.HealthChecker{"postgres": fakeChecker{}, "redis": fakeChecker{err: errors.New("boom")}},
			wantStatus: "degraded",
			wantChecks: map[string]string{"postgres": "up", "redis": "down"},
		},
		{
			name:       "nil checker is skipped",
			checkers:   map[string]port.HealthChecker{"postgres": nil},
			wantStatus: "ok",
			wantChecks: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewHealthService(tt.checkers).Check(context.Background())
			if got.Status != tt.wantStatus {
				t.Fatalf("status: got %q want %q", got.Status, tt.wantStatus)
			}
			if len(got.Checks) != len(tt.wantChecks) {
				t.Fatalf("checks: got %v want %v", got.Checks, tt.wantChecks)
			}
			for k, v := range tt.wantChecks {
				if got.Checks[k] != v {
					t.Errorf("check %q: got %q want %q", k, got.Checks[k], v)
				}
			}
		})
	}
}
