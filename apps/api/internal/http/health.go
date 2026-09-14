package http

import (
	"context"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/service"
)

// HealthHandler exposes readiness/liveness endpoints.
type HealthHandler struct {
	health *service.HealthService
}

// NewHealthHandler builds the handler.
func NewHealthHandler(health *service.HealthService) *HealthHandler {
	return &HealthHandler{health: health}
}

// Register mounts the health routes on the router.
func (h *HealthHandler) Register(e *echo.Echo) {
	e.GET("/healthz", h.live)
	e.GET("/readyz", h.ready)
}

// live is liveness: the process is up. Never depends on downstreams.
func (h *HealthHandler) live(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// ready is readiness: dependencies reachable.
func (h *HealthHandler) ready(c echo.Context) error {
	ctx, cancel := context.WithTimeout(c.Request().Context(), readinessTimeout)
	defer cancel()

	status := h.health.Check(ctx)
	code := http.StatusOK
	if status.Status != "ok" {
		code = http.StatusServiceUnavailable
	}
	return c.JSON(code, status)
}
