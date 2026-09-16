package http

import (
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/http/oapigen"
	"github.com/ahdirmai/jg-smm/apps/api/internal/service"
)

// AuditHandler mounts the read side of the audit trail (P6-10). The write side
// is the auditMiddleware below, which records every successful state-changing
// call in one place rather than threading a recorder through every service.
type AuditHandler struct {
	audit *service.AuditService
}

// NewAuditHandler wires the audit service.
func NewAuditHandler(audit *service.AuditService) *AuditHandler {
	return &AuditHandler{audit: audit}
}

// Register mounts the audit route on the auth+RBAC /api group.
func (h *AuditHandler) Register(g *echo.Group) {
	g.GET("/audit", h.list)
}

// Audit returns the wrapped service, for the router's recording middleware.
func (h *AuditHandler) Audit() *service.AuditService { return h.audit }

func (h *AuditHandler) list(c echo.Context) error {
	var params oapigen.ListAuditParams
	if err := c.Bind(&params); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid query parameters")
	}

	f := domain.AuditFilter{
		ActorID: ptrStr(params.ActorId),
		Action:  ptrStr(params.Action),
		Entity:  ptrStr(params.Entity),
		Limit:   ptrIntOrDefault(params.Limit, 100),
		Offset:  ptrIntOrDefault(params.Offset, 0),
	}
	if params.From != nil {
		f.From = *params.From
	}
	if params.To != nil {
		f.To = *params.To
	}

	res, err := h.audit.List(c.Request().Context(), f)
	if err != nil {
		return translateAuditError(err)
	}

	rows := make([]oapigen.AuditLog, 0, len(res.Rows))
	for _, e := range res.Rows {
		view := service.ToAuditView(e)
		rows = append(rows, toAuditLogDTO(view))
	}
	return c.JSON(http.StatusOK, oapigen.AuditLogList{
		Rows:   rows,
		Total:  int(res.Total),
		Limit:  res.Limit,
		Offset: res.Offset,
	})
}

func toAuditLogDTO(v service.AuditView) oapigen.AuditLog {
	return oapigen.AuditLog{
		Id:       v.ID,
		ActorId:  strPtr(v.ActorID),
		Actor:    strPtr(v.Actor),
		Action:   v.Action,
		Entity:   v.Entity,
		EntityId: v.EntityID,
		Result:   v.Result,
		Ip:       strPtr(v.IP),
		Ts:       v.TS,
	}
}

func translateAuditError(err error) error {
	switch {
	case errors.Is(err, domain.ErrValidation):
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	default:
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
}

// --- mutation recording -----------------------------------------------------

// mutatingMethods are the HTTP verbs that change state; GET/HEAD/OPTIONS are
// reads and are never audited.
var mutatingMethods = map[string]bool{
	http.MethodPost:   true,
	http.MethodPut:    true,
	http.MethodPatch:  true,
	http.MethodDelete: true,
}

// auditMiddleware records every state-changing /api call to the audit trail:
// who (the verified claims), what (entity + verb derived from the route), where
// (the request IP), and the outcome. Bodies are deliberately NOT captured —
// they carry secrets (account passwords, proxy pool keys).
//
// Recording is best-effort: a store failure is logged by the service and never
// affects the request it observed.
func auditMiddleware(audit *service.AuditService) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			err := next(c)

			req := c.Request()
			if !mutatingMethods[req.Method] {
				return err
			}

			// Only /api mutations carry an authenticated actor; auth routes
			// (login/refresh) are mounted outside the group and never reach here.
			claims, ok := ClaimsFrom(c)
			if !ok {
				return err
			}

			action, entity := auditActionFromRoute(c.Path(), req.Method)
			if entity == "" {
				return err
			}

			result := service.AuditOK
			if err != nil {
				result = err.Error()
			}

			audit.Record(c.Request().Context(), claims.UserID, action, entity,
				routeEntityID(c), c.RealIP(), result, nil)
			return err
		}
	}
}

// auditActionFromRoute turns `/api/accounts/:accountId` + DELETE into the
// audit pair (action=`account.remove`, entity=`account`). The verb map is the
// project's CRUD vocabulary; unknown shapes are skipped (return "") rather
// than recorded with a made-up name.
func auditActionFromRoute(path, method string) (action, entity string) {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	// Expect ["api", <entity>, ...] for a mutating route.
	if len(segments) < 2 || segments[0] != "api" {
		return "", ""
	}
	entity = strings.TrimSuffix(segments[1], "s")
	// "proxies" -> "proxy" is the only irregular plural in the API surface.
	if segments[1] == "proxies" {
		entity = "proxy"
	}

	var verb string
	switch method {
	case http.MethodPost:
		verb = "create"
	case http.MethodPut, http.MethodPatch:
		verb = "update"
	case http.MethodDelete:
		verb = "remove"
	default:
		return "", ""
	}
	// A POST to a resource collection with an id is a state change on that
	// resource (e.g. set account status), not a create.
	if len(segments) > 2 && method == http.MethodPost {
		verb = "update"
	}
	return entity + "." + verb, entity
}

// routeEntityID pulls the id segment the route matched (e.g. :accountId),
// preferring the entity's own id param.
func routeEntityID(c echo.Context) string {
	for _, name := range []string{"accountId", "templateId", "containerId", "proxyGroupId", "userId", "officialAccountId"} {
		if v := c.Param(name); v != "" {
			return v
		}
	}
	return ""
}

func ptrStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func ptrIntOrDefault(p *int, def int) int {
	if p == nil {
		return def
	}
	return *p
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
