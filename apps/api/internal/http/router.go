package http

import (
	"time"

	"github.com/labstack/echo/v4"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
)

const (
	readinessTimeout = 3 * time.Second
)

// Dependencies holds the handlers mounted by the router. Health is mandatory;
// optional handlers (e.g. Auth) are skipped when nil so the API can boot before
// the database is wired.
type Dependencies struct {
	Health      *HealthHandler
	Auth        *AuthHandler
	Internal    *InternalHandler
	Containers  *ContainerHandler
	Accounts    *AccountHandler
	ProxyGroups *ProxyGroupHandler
	Analytics   *AnalyticsHandler
	Actions     *ActionHandler
	Templates   *TemplateHandler
	Reports     *ReportHandler
	Users       *UserHandler
	Audit       *AuditHandler
	Stream      *StreamHandler
	Metrics     *MetricsHandler
}

// NewRouter builds the Echo instance with middleware and routes. It does not
// start the server (see cmd/server), which keeps the router testable.
func NewRouter(deps Dependencies) *echo.Echo {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	// Generated query-param structs carry `form` tags, so query binding needs
	// the shim above rather than Echo's `query`-tag DefaultBinder.
	e.Binder = formQueryBinder{}

	e.HTTPErrorHandler = errorHandler
	e.Use(requestLogger(), recoverer())

	deps.Health.Register(e)

	// /metrics is a root route, not an /api one: the RBAC layer never sees a
	// scrape, and the alerts keep working when a token expires.
	if deps.Metrics != nil {
		deps.Metrics.Register(e)
	}

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

		// Everything under /api needs an authenticated user with at least the
		// `read` permission (OPERATOR/ANALYST/STRATEGIST/OWNER). Mutating routes
		// add a stricter `act`/`export` gate per handler — a role that can read
		// the dashboard must never be able to queue actions or delete a worker.
		// P5-04: the group previously required `act`, which locked
		// STRATEGIST/ANALYST out of every read endpoint with a 403.
		api := e.Group("/api", deps.Auth.requireAuth(), RequirePermission(domain.PermRead))
		// Record who changed what. Mounted inside the group so every mutation
		// carries a verified actor; reads and auth routes are not audited.
		if deps.Audit != nil {
			api.Use(auditMiddleware(deps.Audit.Audit()))
		}
		if deps.Containers != nil {
			deps.Containers.Register(api)
		}
		if deps.Accounts != nil {
			deps.Accounts.Register(api)
		}
		if deps.Stream != nil {
			deps.Stream.Register(api)
		}
		if deps.ProxyGroups != nil {
			deps.ProxyGroups.Register(api)
		}
		if deps.Analytics != nil {
			deps.Analytics.Register(api)
		}
		if deps.Actions != nil {
			deps.Actions.Register(api)
		}
		if deps.Templates != nil {
			deps.Templates.Register(api)
		}
		if deps.Reports != nil {
			deps.Reports.Register(api)
		}
		if deps.Users != nil {
			deps.Users.Register(api)
		}
		if deps.Audit != nil {
			deps.Audit.Register(api)
		}
	}

	if deps.Internal != nil {
		deps.Internal.Register(e)
	}

	return e
}
