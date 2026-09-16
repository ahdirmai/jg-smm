package http

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/http/oapigen"
	"github.com/ahdirmai/jg-smm/apps/api/internal/service"
)

// ProxyGroupHandler is the CRUD API for residential proxy pools (P1-14). The
// pool key is write-only: it is accepted on create and never returned.
type ProxyGroupHandler struct {
	groups *service.ProxyGroupService
}

// NewProxyGroupHandler wires the service.
func NewProxyGroupHandler(groups *service.ProxyGroupService) *ProxyGroupHandler {
	return &ProxyGroupHandler{groups: groups}
}

// Register mounts the proxy group routes on the auth+RBAC /api group.
// Mutating routes add the `act` gate (P5-04).
func (h *ProxyGroupHandler) Register(g *echo.Group) {
	g.POST("/proxy-groups", h.create, RequirePermission(domain.PermAct))
	g.GET("/proxy-groups", h.list)
	g.DELETE("/proxy-groups/:proxyGroupId", h.remove, RequirePermission(domain.PermAct))
}

func (h *ProxyGroupHandler) create(c echo.Context) error {
	var req oapigen.CreateProxyGroupRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	summary, err := h.groups.Create(c.Request().Context(), service.ProxyGroupInput{
		Name:           string(req.Name),
		Region:         string(req.Region),
		Provider:       string(req.Provider),
		PoolKey:        string(req.PoolKey),
		MaxConcurrency: int(req.MaxConcurrency),
		DailyBudgetMB:  int(req.DailyBudgetMb),
	})
	if err != nil {
		return translateProxyGroupError(err)
	}
	body, err := toProxyGroupResponse(summary)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusCreated, body)
}

func (h *ProxyGroupHandler) list(c echo.Context) error {
	rows, err := h.groups.List(c.Request().Context())
	if err != nil {
		return translateProxyGroupError(err)
	}
	items := make([]oapigen.ProxyGroup, 0, len(rows))
	for _, row := range rows {
		item, err := toProxyGroupResponse(row)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
		items = append(items, item)
	}
	return c.JSON(http.StatusOK, oapigen.ProxyGroupList{ProxyGroups: items})
}

func (h *ProxyGroupHandler) remove(c echo.Context) error {
	if err := h.groups.Remove(c.Request().Context(), c.Param("proxyGroupId")); err != nil {
		return translateProxyGroupError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

func toProxyGroupResponse(s service.ProxyGroupSummary) (oapigen.ProxyGroup, error) {
	created, err := time.Parse(time.RFC3339, s.CreatedAt)
	if err != nil {
		return oapigen.ProxyGroup{}, fmt.Errorf("proxy group: parse created_at: %w", err)
	}
	return oapigen.ProxyGroup{
		Id:             s.ID,
		Name:           s.Name,
		Region:         s.Region,
		Provider:       s.Provider,
		MaxConcurrency: s.MaxConcurrency,
		DailyBudgetMb:  s.DailyBudgetMB,
		CreatedAt:      &created,
	}, nil
}

func translateProxyGroupError(err error) error {
	switch {
	case errors.Is(err, domain.ErrValidation):
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	case errors.Is(err, domain.ErrConflict):
		return echo.NewHTTPError(http.StatusConflict, err.Error())
	case errors.Is(err, domain.ErrNotFound):
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	case errors.Is(err, domain.ErrUnavailable):
		return echo.NewHTTPError(http.StatusServiceUnavailable, err.Error())
	default:
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
}
