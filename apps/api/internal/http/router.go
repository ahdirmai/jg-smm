package http

import (
	"time"

	"github.com/labstack/echo/v4"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
)

const (
	readinessTimeout = 3 * time.Second
)

// Dependencies holds the handlers mounted by the router. Health is mandatory;
// optional handlers (e.g. Auth) are skipped when nil so the API can boot before
// the database is wired.
type Dependencies struct {
	Health     *HealthHandler
	Auth       *AuthHandler
	Internal   *InternalHandler
	Containers *ContainerHandler
	Accounts   *AccountHandler
}

// NewRouter builds the Echo instance with middleware and routes. It does not
// start the server (see cmd/server), which keeps the router testable.
func NewRouter(deps Dependencies) *echo.Echo {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	e.HTTPErrorHandler = errorHandler
	e.Use(requestLogger(), recoverer())

	deps.Health.Register(e)

	// Root placeholder so the container has a stable liveness target too.
	e.GET("/", func(c echo.Context) error {
		return c.JSON(200, map[string]string{"service": "smm-api"})
	})

	if deps.Auth != nil {
		deps.Auth.Register(e)

		// Example of an RBAC-guarded route group. Real admin endpoints land
		// here as their features are built; P0-06 only needs one to prove the
		// permission gate works end to end.
		admin := e.Group("/api/admin", deps.Auth.requireAuth(), RequirePermission(domain.PermAdmin))
		admin.GET("/ping", func(c echo.Context) error {
			return c.JSON(200, map[string]string{"status": "ok"})
		})
	}

	if deps.Internal != nil {
		deps.Internal.Register(e)
	}

	if deps.Containers != nil && deps.Auth != nil {
		// Container ops need auth + the act permission (OPERATOR+). The group
		// carries both; the handler only adds routes.
		containers := e.Group("/api", deps.Auth.requireAuth(), RequirePermission(domain.PermAct))
		deps.Containers.Register(containers)

		// Accounts share the same permission tier as containers.
		if deps.Accounts != nil {
			deps.Accounts.Register(containers)
		}
	}

	return e
}
