package http

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/oapi-codegen/nullable"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/http/oapigen"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
	"github.com/ahdirmai/jg-smm/apps/api/internal/service"
)

// ActionHandler (P3-13) is the operator-facing action API: enqueue a batch of
// like/comment intents, and read the queue back with live status. RBAC is
// enforced by the route group (act permission), not here. Comment text is
// never accepted: the template engine composes and screens it at dispatch.
type ActionHandler struct {
	actions *service.ActionService
}

// NewActionHandler builds the handler. A nil service makes the routes 503.
func NewActionHandler(actions *service.ActionService) *ActionHandler {
	return &ActionHandler{actions: actions}
}

// Register mounts the action routes into an authed group. The group carries
// the `read` floor; enqueueing is an `act` (P5-04).
func (h *ActionHandler) Register(g *echo.Group) {
	g.POST("/actions", h.enqueue, RequirePermission(domain.PermAct))
	g.GET("/actions", h.list)
	// Batch monitoring: each enqueue call is recorded as a unit.
	g.GET("/actions/batches", h.listBatches)
	g.GET("/actions/batches/:batchId", h.getBatch)
}

// enqueue turns a batch of intents into PENDING jobs.
func (h *ActionHandler) enqueue(c echo.Context) error {
	var req oapigen.EnqueueActionsRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if h.actions == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "action service unavailable")
	}

	items := make([]service.ActionItem, 0, len(req.Items))
	for _, it := range req.Items {
		item := service.ActionItem{
			AccountID: it.AccountId,
			TargetURL: it.TargetUrl,
			Type:      domain.JobType(it.ActionType),
		}
		if it.Text != nil {
			item.Text = *it.Text
		}
		items = append(items, item)
	}

	jobs, err := h.actions.Enqueue(c.Request().Context(), items)
	if err != nil {
		return actionError(err)
	}

	out := make([]oapigen.ActionJob, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, jobToDTO(j))
	}
	return c.JSON(http.StatusCreated, oapigen.ActionJobList{Actions: out})
}

// list returns the queue newest-first with each job's latest verdict.
func (h *ActionHandler) list(c echo.Context) error {
	if h.actions == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "action service unavailable")
	}

	limit := 50
	if raw := c.QueryParam("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	var status *domain.JobStatus
	if raw := c.QueryParam("status"); raw != "" {
		st := domain.JobStatus(raw)
		status = &st
	}

	views, err := h.actions.List(c.Request().Context(), status, limit)
	if err != nil {
		return actionError(err)
	}

	out := make([]oapigen.ActionJob, 0, len(views))
	for _, v := range views {
		row := jobToDTO(v.Job)
		if v.RenderedText != "" {
			row.RenderedText = nullable.NewNullableWithValue(v.RenderedText)
		}
		if v.ErrorClass != "" {
			row.ErrorClass = nullable.NewNullableWithValue(oapigen.ErrorClass(v.ErrorClass))
		}
		if v.TargetURL != "" {
			row.TargetUrl = nullable.NewNullableWithValue(v.TargetURL)
		}
		out = append(out, row)
	}
	return c.JSON(http.StatusOK, oapigen.ActionJobList{Actions: out})
}

// listBatches returns recent enqueue batches (monitoring), newest first. Shape
// is hand-rolled (not oapigen): batches live outside the OpenAPI contract.
func (h *ActionHandler) listBatches(c echo.Context) error {
	if h.actions == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "action service unavailable")
	}
	limit := 50
	if raw := c.QueryParam("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	batches, err := h.actions.ListBatches(c.Request().Context(), limit)
	if err != nil {
		return actionError(err)
	}
	if batches == nil {
		batches = []port.ActionBatch{}
	}
	return c.JSON(http.StatusOK, map[string]any{"batches": batches})
}

// getBatch returns one batch with a live rollup of its jobs' current statuses.
func (h *ActionHandler) getBatch(c echo.Context) error {
	if h.actions == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "action service unavailable")
	}
	st, err := h.actions.GetBatchStatus(c.Request().Context(), c.Param("batchId"))
	if err != nil {
		return actionError(err)
	}
	return c.JSON(http.StatusOK, st)
}

// jobToDTO maps the domain job to the wire shape. Nullable fields are omitted
// until the dispatch path fills them.
func jobToDTO(j domain.ActionJob) oapigen.ActionJob {
	row := oapigen.ActionJob{
		Id:          j.ID,
		ActionType:  oapigen.ActionJobActionType(j.Type),
		AccountId:   j.AccountID,
		TargetId:    j.TargetID,
		Status:      oapigen.JobStatus(j.Status),
		Attempts:    j.Attempts,
		ScheduledAt: j.ScheduledAt,
	}
	if j.Error != "" {
		row.Error = nullable.NewNullableWithValue(j.Error)
	}
	return row
}

// actionError maps a service error to its HTTP status. Validation failures are
// 400s (the operator can fix them); anything else is a 500 with the message.
func actionError(err error) error {
	if errors.Is(err, domain.ErrValidation) {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if errors.Is(err, domain.ErrNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
}
