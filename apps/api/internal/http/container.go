package http

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/oapi-codegen/nullable"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/http/oapigen"
	"github.com/ahdirmai/jg-smm/apps/api/internal/service"
)

// ContainerHandler is the operator-facing container API (P1-19). It creates
// MANUAL containers, lists the fleet with its accounts, and deletes containers.
// RBAC (act permission) is enforced by the route group, not here.
type ContainerHandler struct {
	containers *service.ContainerService
}

// NewContainerHandler builds the handler. A nil service makes the routes
// return 503, which keeps the API bootable before the DB is wired.
func NewContainerHandler(containers *service.ContainerService) *ContainerHandler {
	return &ContainerHandler{containers: containers}
}

// Register mounts the container routes. The group must already carry auth +
// RequirePermission(PermAct); this only adds the handlers.
func (h *ContainerHandler) Register(g *echo.Group) {
	g.POST("/containers", h.create)
	g.GET("/containers", h.list)
	g.DELETE("/containers/:containerId", h.delete)
}

// create inserts a MANUAL container in the RUNNING desired state.
func (h *ContainerHandler) create(c echo.Context) error {
	var req oapigen.CreateContainerRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	name := ""
	if req.Name != nil {
		name = *req.Name
	}
	if h.containers == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "container service unavailable")
	}

	w, err := h.containers.Create(c.Request().Context(), name, req.Region)
	if err != nil {
		return translateContainerError(err)
	}
	view := service.ToContainerView(w, nil)
	return c.JSON(http.StatusCreated, toContainerResponse(view, nil))
}

// list returns every container with its hosted accounts.
func (h *ContainerHandler) list(c echo.Context) error {
	if h.containers == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "container service unavailable")
	}
	views, err := h.containers.List(c.Request().Context())
	if err != nil {
		return translateContainerError(err)
	}
	items := make([]oapigen.Container, 0, len(views))
	for _, v := range views {
		items = append(items, toContainerResponse(v, v.Accounts))
	}
	return c.JSON(http.StatusOK, oapigen.ContainerList{Containers: items})
}

// delete tears a container down: accounts released, desired state flipped to
// STOPPED, row removed.
func (h *ContainerHandler) delete(c echo.Context) error {
	if h.containers == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "container service unavailable")
	}
	id := c.Param("containerId")
	if id == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "containerId is required")
	}
	if err := h.containers.Delete(c.Request().Context(), id); err != nil {
		return translateContainerError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

// translateContainerError maps domain errors to HTTP statuses.
func translateContainerError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrValidation):
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	case errors.Is(err, domain.ErrNotFound):
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	case errors.Is(err, domain.ErrConflict):
		return echo.NewHTTPError(http.StatusConflict, err.Error())
	case errors.Is(err, domain.ErrUnauthorized):
		return echo.NewHTTPError(http.StatusUnauthorized, err.Error())
	case errors.Is(err, domain.ErrForbidden):
		return echo.NewHTTPError(http.StatusForbidden, err.Error())
	default:
		return err // 500 via the error handler
	}
}

// toContainerResponse builds the API shape from a container view. accounts may
// be empty; a container with zero accounts is a valid fleet state.
func toContainerResponse(cv service.ContainerView, accounts []service.AccountSummary) oapigen.Container {
	out := oapigen.Container{
		Id:           cv.ID,
		Name:         cv.Name,
		DesiredState: oapigen.ContainerDesiredState(cv.DesiredState),
		Source:       oapigen.ContainerSource(cv.Source),
		Region:       cv.Region,
		Status:       oapigen.ContainerStatus(cv.Status),
		Generation:   cv.Generation,
		CreatedAt:    cv.CreatedAt,
	}
	if cv.ObservedGen != nil {
		out.ObservedGeneration = nullable.NewNullableWithValue[int](*cv.ObservedGen)
	}
	accs := make([]oapigen.ContainerAccount, 0, len(accounts))
	for _, a := range accounts {
		accs = append(accs, oapigen.ContainerAccount{
			Id:         a.ID,
			Platform:   oapigen.ContainerAccountPlatform(a.Platform),
			Username:   a.Username,
			AuthStatus: oapigen.ContainerAccountAuthStatus(a.AuthStatus),
			Status:     oapigen.ContainerAccountStatus(a.Status),
		})
	}
	out.Accounts = &accs
	return out
}
