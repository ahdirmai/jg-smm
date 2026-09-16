package http

import (
	"github.com/labstack/echo/v4"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// MetricsHandler exposes the Prometheus scrape endpoint. It is deliberately
// unauthenticated: Prometheus has no user account, and the endpoint carries
// only aggregate counters and gauges — never credentials, target URLs, or
// rendered comment text (which is why the action label set stops at ids and
// classes). It is mounted outside the /api group so the RBAC layer never sees
// it, and a scrape cannot fail because a token expired.
type MetricsHandler struct {
	handler echo.HandlerFunc
}

// NewMetricsHandler builds the scrape handler off the process registry.
func NewMetricsHandler() *MetricsHandler {
	h := promhttp.Handler()
	return &MetricsHandler{handler: echo.WrapHandler(h)}
}

// Register mounts /metrics on the root of the router.
func (h *MetricsHandler) Register(e *echo.Echo) {
	e.GET("/metrics", func(c echo.Context) error {
		return h.handler(c)
	})
}

// MetricsContentType is asserted by the scrape test so a future refactor that
// silently returns JSON (and breaks every alert) fails CI rather than prod.
const MetricsContentType = "text/plain"
