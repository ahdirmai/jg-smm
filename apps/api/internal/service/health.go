package service

import (
	"context"

	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// HealthStatus is the result of a readiness probe.
type HealthStatus struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks,omitempty"`
}

// HealthService aggregates dependency checks. A single dependency being down
// makes the overall status "degraded" (not "ok").
type HealthService struct {
	checkers map[string]port.HealthChecker
}

// NewHealthService wires named dependency checkers. Pass nil-safe map; missing
// checkers are simply not probed.
func NewHealthService(checkers map[string]port.HealthChecker) *HealthService {
	return &HealthService{checkers: checkers}
}

// Check runs all dependency probes and returns the aggregate status. It does not
// short-circuit: every dependency is reported for observability.
func (s *HealthService) Check(ctx context.Context) HealthStatus {
	checks := make(map[string]string, len(s.checkers))
	ok := true
	for name, c := range s.checkers {
		if c == nil {
			continue
		}
		if err := c.Ping(ctx); err != nil {
			checks[name] = "down"
			ok = false
			continue
		}
		checks[name] = "up"
	}

	status := "ok"
	if !ok {
		status = "degraded"
	}
	return HealthStatus{Status: status, Checks: checks}
}
