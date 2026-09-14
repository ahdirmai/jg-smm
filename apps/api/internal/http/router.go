package http

import (
	"time"

	"github.com/labstack/echo/v4"
)

const (
	readinessTimeout = 3 * time.Second
)

// NewRouter builds the Echo instance with middleware and routes. It does not
// start the server (see cmd/server), which keeps the router testable.
func NewRouter(h *HealthHandler) *echo.Echo {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	e.HTTPErrorHandler = errorHandler
	e.Use(requestLogger(), recoverer())

	h.Register(e)

	// Root placeholder so the container has a stable liveness target too.
	e.GET("/", func(c echo.Context) error {
		return c.JSON(200, map[string]string{"service": "smm-api"})
	})

	return e
}
