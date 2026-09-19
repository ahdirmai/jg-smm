package http

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/http/oapigen"
	"github.com/ahdirmai/jg-smm/apps/api/internal/service"
)

// TemplateHandler (P3-13) is the comment-pool composer: the surface an operator
// uses to author variants. The denylist compile-check and the var/text
// agreement are enforced by the store on every write, so a malformed variant is
// a 400 here, never a broken comment later.
type TemplateHandler struct {
	templates *service.TemplateService
}

// NewTemplateHandler builds the handler. A nil service makes the routes 503.
func NewTemplateHandler(templates *service.TemplateService) *TemplateHandler {
	return &TemplateHandler{templates: templates}
}

// Register mounts the template routes into an authed group. Reads are open to
// any read role; editing the comment pool is an `act` (P5-04).
func (h *TemplateHandler) Register(g *echo.Group) {
	g.POST("/templates", h.create, RequirePermission(domain.PermAct))
	g.GET("/templates", h.list)
	g.PUT("/templates/:templateId", h.update, RequirePermission(domain.PermAct))
	g.DELETE("/templates/:templateId", h.remove, RequirePermission(domain.PermAct))
}

// create adds one variant to the pool.
func (h *TemplateHandler) create(c echo.Context) error {
	var req oapigen.CreateTemplateRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if h.templates == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "template service unavailable")
	}

	t, err := h.templates.Create(c.Request().Context(), templateInput(req))
	if err != nil {
		return templateError(err)
	}
	return c.JSON(http.StatusCreated, templateToDTO(t))
}

// update replaces a variant's mutable fields.
func (h *TemplateHandler) update(c echo.Context) error {
	var req oapigen.CreateTemplateRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if h.templates == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "template service unavailable")
	}
	id := c.Param("templateId")

	t, err := h.templates.Update(c.Request().Context(), mergeID(templateInput(req), id))
	if err != nil {
		return templateError(err)
	}
	return c.JSON(http.StatusOK, templateToDTO(t))
}

// list returns the pool. With no `platform` query it lists EVERY platform's
// variants (the dashboard fetches unfiltered and filters client-side, so a
// per-platform default here would hide every non-Instagram variant); a
// `platform` query scopes the listing. The composer's Pick stays
// platform-scoped regardless.
func (h *TemplateHandler) list(c echo.Context) error {
	if h.templates == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "template service unavailable")
	}
	// Empty platform = all platforms. The service/repo omit the platform filter
	// for the empty case rather than defaulting to one.
	platform := domain.Platform(c.QueryParam("platform"))
	includeInactive := c.QueryParam("includeInactive") == "true"

	pool, err := h.templates.List(c.Request().Context(), platform, includeInactive)
	if err != nil {
		return templateError(err)
	}

	out := make([]oapigen.CommentTemplate, 0, len(pool))
	for _, t := range pool {
		out = append(out, templateToDTO(t))
	}
	return c.JSON(http.StatusOK, oapigen.TemplateList{Templates: out})
}

// remove deletes a variant from the pool.
func (h *TemplateHandler) remove(c echo.Context) error {
	if h.templates == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "template service unavailable")
	}
	if err := h.templates.Delete(c.Request().Context(), c.Param("templateId")); err != nil {
		return templateError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

// templateInput maps the request to the domain variant.
func templateInput(req oapigen.CreateTemplateRequest) domain.CommentTemplate {
	vars := []string{}
	for _, v := range req.Vars {
		vars = append(vars, string(v))
	}
	var banned []string
	if req.BannedWords != nil {
		banned = *req.BannedWords
	}
	active := true
	if req.IsActive != nil {
		active = *req.IsActive
	}
	return domain.CommentTemplate{
		Platform:    domain.Platform(req.Platform),
		Text:        req.Text,
		Vars:        vars,
		Weight:      req.Weight,
		BannedWords: banned,
		IsActive:    active,
	}
}

func mergeID(t domain.CommentTemplate, id string) domain.CommentTemplate {
	t.ID = id
	return t
}

// templateToDTO maps the domain variant to the wire shape.
func templateToDTO(t domain.CommentTemplate) oapigen.CommentTemplate {
	return oapigen.CommentTemplate{
		Id:          t.ID,
		Platform:    oapigen.Platform(t.Platform),
		Text:        t.Text,
		Vars:        t.Vars,
		Weight:      t.Weight,
		BannedWords: &t.BannedWords,
		IsActive:    t.IsActive,
		CreatedAt:   t.CreatedAt,
	}
}

// templateError maps a store error to its HTTP status. A validation failure is
// the operator's fixable mistake (400); a missing variant is a 404.
func templateError(err error) error {
	if errors.Is(err, domain.ErrValidation) {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if errors.Is(err, domain.ErrNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
}
