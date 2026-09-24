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

// Register mounts the container routes. The group carries auth + the `read`
// floor; mutating routes add the stricter `act` gate (P5-04).
func (h *ContainerHandler) Register(g *echo.Group) {
	g.POST("/containers", h.create, RequirePermission(domain.PermAct))
	g.GET("/containers", h.list)
	g.GET("/containers/:containerId", h.listLogs)
	g.DELETE("/containers/:containerId", h.delete, RequirePermission(domain.PermAct))
	g.POST("/containers/:containerId/browser", h.openBrowser, RequirePermission(domain.PermAct))
	// The city dropdown sits on the same page as the create form.
	g.GET("/locations", h.listLocations)
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

	var novncPort *int
	if req.NovncPort != nil && !req.NovncPort.IsNull() {
		p := int(req.NovncPort.MustGet())
		novncPort = &p
	}
	w, err := h.containers.Create(c.Request().Context(), name, req.Region, req.Location, novncPort)
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

// listLogs returns the provisioner audit trail for one container, so a PENDING
// card can explain itself instead of looking stuck (F-07).
func (h *ContainerHandler) listLogs(c echo.Context) error {
	if h.containers == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "container service unavailable")
	}
	var params oapigen.ListContainerLogsParams
	if err := c.Bind(&params); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid query parameters")
	}
	limit := 20
	if params.Limit != nil {
		limit = *params.Limit
	}
	logs, err := h.containers.Logs(c.Request().Context(), c.Param("containerId"), limit)
	if err != nil {
		return translateContainerError(err)
	}
	items := make([]oapigen.ProvisionLogEntry, 0, len(logs))
	for _, l := range logs {
		entry := oapigen.ProvisionLogEntry{
			Id:         l.ID,
			WorkerId:   l.WorkerID,
			Op:         oapigen.ProvisionLogEntryOp(l.Op),
			Generation: l.Generation,
			Status:     oapigen.ProvisionLogEntryStatus(l.Status),
			Ts:         l.TS,
		}
		if l.K8sRef != nil {
			entry.K8sRef = nullable.NewNullableWithValue(*l.K8sRef)
		}
		if l.Error != nil {
			entry.Error = nullable.NewNullableWithValue(*l.Error)
		}
		items = append(items, entry)
	}
	return c.JSON(http.StatusOK, oapigen.ProvisionLogList{Logs: items})
}

// openBrowser asks the worker to launch Chrome on its Xvfb display so the
// noVNC view shows a real browser immediately for manual testing.
func (h *ContainerHandler) openBrowser(c echo.Context) error {
	if h.containers == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "container service unavailable")
	}
	id := c.Param("containerId")
	if id == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "containerId is required")
	}
	var body struct {
		URL *string `json:"url"`
	}
	_ = c.Bind(&body)
	url := ""
	if body.URL != nil {
		url = *body.URL
	}
	if err := h.containers.OpenBrowser(c.Request().Context(), id, url); err != nil {
		return translateContainerError(err)
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
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

// listLocations returns the city list the create dropdown renders and the
// validator accepts. The list is a domain constant, so this reads straight off
// it — no store round-trip.
func (h *ContainerHandler) listLocations(c echo.Context) error {
	out := make([]oapigen.Location, 0, len(domain.Cities))
	for _, city := range domain.Cities {
		out = append(out, oapigen.Location{
			Name:      city.Name,
			Latitude:  city.Latitude,
			Longitude: city.Longitude,
			RadiusKm:  city.RadiusKm,
		})
	}
	return c.JSON(http.StatusOK, out)
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
	if cv.NovncURL != "" {
		out.NovncUrl = nullable.NewNullableWithValue[string](cv.NovncURL)
	}
	// A row predating worker geolocation has a nil location; leave the fields
	// null rather than emitting an empty string and (0,0).
	if cv.Location != nil {
		out.Location = nullable.NewNullableWithValue[string](*cv.Location)
	}
	if cv.Latitude != nil {
		out.Latitude = nullable.NewNullableWithValue[float64](*cv.Latitude)
	}
	if cv.Longitude != nil {
		out.Longitude = nullable.NewNullableWithValue[float64](*cv.Longitude)
	}
	return out
}
