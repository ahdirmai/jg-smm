package http

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/service"
)

// ScrapeHandler serves the dashboard's on-demand scrape: paste a post URL, run
// the platform's Apify actor synchronously, read the stored post + comments
// back — and the AI comment generator that drafts replies from what it read.
// Both live outside the OpenAPI contract (hand-rolled shape) like the batch
// endpoints — these are tool surfaces, not generated clients.
type ScrapeHandler struct {
	scrapes  *service.ScrapeService
	keywords *service.KeywordScrapeService
	gen      *service.CommentGenerator
}

// NewScrapeHandler wires the handler. A nil service keeps the API bootable
// without a scrape runner (routes return 503). A nil generator disables AI
// generation only (503 on that route); scraping still works. A nil keyword
// service disables the keyword-search route only.
func NewScrapeHandler(scrapes *service.ScrapeService, keywords *service.KeywordScrapeService, gen *service.CommentGenerator) *ScrapeHandler {
	return &ScrapeHandler{scrapes: scrapes, keywords: keywords, gen: gen}
}

// Register mounts the routes on the /api group.
// scrape/target is single post (satuan) — sync. scrape/keywords is batch (keyword) — async 202 + batch endpoints.
func (h *ScrapeHandler) Register(g *echo.Group) {
	g.POST("/scrape/target", h.scrapeTarget, RequirePermission(domain.PermAct))
	g.POST("/scrape/keywords", h.scrapeKeywords, RequirePermission(domain.PermAct))
	g.GET("/scrape/batches", h.listBatches, RequirePermission(domain.PermAct))
	g.GET("/scrape/batches/:id", h.getBatch, RequirePermission(domain.PermAct))
	g.GET("/scrape/batches/:id/posts", h.listBatchPosts, RequirePermission(domain.PermAct))
	g.POST("/comments/generate", h.generateComment, RequirePermission(domain.PermAct))
	g.GET("/scrape/recent", h.listRecent, RequirePermission(domain.PermAct))
}

// scrapeTargetRequest is the on-demand scrape body.
type scrapeTargetRequest struct {
	Platform string `json:"platform"`
	URL      string `json:"url"`
}

// scrapeTarget runs the actor synchronously. A long request by design: the
// dashboard shows a loader while Apify finishes (run-sync endpoint).
func (h *ScrapeHandler) scrapeTarget(c echo.Context) error {
	if h.scrapes == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "scrape is not configured")
	}
	var req scrapeTargetRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	detail, err := h.scrapes.ScrapeTarget(c.Request().Context(), domain.Platform(req.Platform), req.URL)
	if err != nil {
		return scrapeError(err)
	}
	return c.JSON(http.StatusOK, detail)
}

// scrapeKeywordsRequest is the dashboard's keyword search body.
type scrapeKeywordsRequest struct {
	Platform string   `json:"platform"`
	Keywords []string `json:"keywords"`
	// Window: RFC3339 or YYYY-MM-DD; zero on parse failure is "no bound".
	From     string `json:"from"`
	To       string `json:"to"`
	MaxPosts int    `json:"maxPosts"`
}

// scrapeKeywords enqueues a keyword batch in background and returns 202.
// Batch scrape (keyword → many posts) vs post scrape (satuan → 1 URL) are distinct dashboards.
func (h *ScrapeHandler) scrapeKeywords(c echo.Context) error {
	if h.keywords == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "keyword scrape is not configured")
	}
	var req scrapeKeywordsRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	in := service.KeywordScrapeInput{
		Platform: domain.Platform(req.Platform),
		Keywords: req.Keywords,
		MaxPosts: req.MaxPosts,
	}
	for _, pair := range []struct {
		raw string
		dst *time.Time
	}{{req.From, &in.Window.From}, {req.To, &in.Window.To}} {
		v := strings.TrimSpace(pair.raw)
		if v == "" {
			continue
		}
		t, err := parseWindowDate(v)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid window date: "+v)
		}
		*pair.dst = t
	}
	if claims, ok := ClaimsFrom(c); ok && claims.UserID != "" {
		s := claims.UserID
		in.CreatedBy = &s
	}
	batch, err := h.keywords.CreateBatch(c.Request().Context(), in)
	if err != nil {
		return scrapeError(err)
	}
	return c.JSON(http.StatusAccepted, map[string]any{"batch": batch})
}

func (h *ScrapeHandler) listBatches(c echo.Context) error {
	if h.keywords == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "keyword scrape is not configured")
	}
	limit, offset := parseLimitOffset(c, 20, 0)
	batches, err := h.keywords.ListBatches(c.Request().Context(), &limit, &offset)
	if err != nil {
		return scrapeError(err)
	}
	if batches == nil {
		batches = []domain.KeywordBatch{}
	}
	return c.JSON(http.StatusOK, map[string]any{"batches": batches})
}

func (h *ScrapeHandler) getBatch(c echo.Context) error {
	if h.keywords == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "keyword scrape is not configured")
	}
	b, err := h.keywords.GetBatch(c.Request().Context(), c.Param("id"))
	if err != nil {
		return scrapeError(err)
	}
	return c.JSON(http.StatusOK, map[string]any{"batch": b})
}

func (h *ScrapeHandler) listBatchPosts(c echo.Context) error {
	if h.keywords == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "keyword scrape is not configured")
	}
	limit, offset := parseLimitOffset(c, 50, 0)
	posts, err := h.keywords.ListBatchPosts(c.Request().Context(), c.Param("id"), &limit, &offset)
	if err != nil {
		return scrapeError(err)
	}
	if posts == nil {
		posts = []domain.Post{}
	}
	return c.JSON(http.StatusOK, map[string]any{"posts": posts})
}

func parseLimitOffset(c echo.Context, defLimit, defOffset int) (int, int) {
	limit := defLimit
	offset := defOffset
	if raw := c.QueryParam("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	if raw := c.QueryParam("offset"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			offset = n
		}
	}
	return limit, offset
}

// listRecent returns stored posts newest-scrape-first — the "already scraped"
// history for the dashboard.
func (h *ScrapeHandler) listRecent(c echo.Context) error {
	if h.scrapes == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "scrape is not configured")
	}
	limit := 20
	if raw := c.QueryParam("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	posts, err := h.scrapes.ListRecentPosts(c.Request().Context(), limit)
	if err != nil {
		return scrapeError(err)
	}
	return c.JSON(http.StatusOK, map[string]any{"posts": posts})
}

// parseWindowDate accepts RFC3339 or a bare YYYY-MM-DD (dashboard date input).
func parseWindowDate(v string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02", v)
}

// generateCommentRequest is the AI-draft body: the scraped post's text, a
type generateCommentRequest struct {
	PostText         string   `json:"postText"`
	AuthorHandle     string   `json:"authorHandle"`
	ExistingComments []string `json:"existingComments"`
	Mode             string   `json:"mode"` // support | counter
	Count            int      `json:"count"`
	Language         string   `json:"language"`
}

// generateComment drafts comment candidates from the scraped context. The
// caller picks one per account on the dashboard; nothing here enqueues.
func (h *ScrapeHandler) generateComment(c echo.Context) error {
	if h.gen == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "AI generation is not configured")
	}
	var req generateCommentRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	texts, err := h.gen.Generate(c.Request().Context(), service.GenerateInput{
		PostText:         req.PostText,
		AuthorHandle:     req.AuthorHandle,
		ExistingComments: req.ExistingComments,
		Mode:             service.CommentMode(req.Mode),
		Count:            req.Count,
		Language:         req.Language,
	})
	if err != nil {
		return scrapeError(err)
	}
	return c.JSON(http.StatusOK, map[string]any{"comments": texts})
}

// scrapeError maps service errors onto HTTP. Validation → 400/404-shaped
// messages, unavailable runner → 503, everything else → 502 (the actor failed,
// which is upstream, not this API's fault).
func scrapeError(err error) error {
	switch {
	case errors.Is(err, domain.ErrValidation):
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	case errors.Is(err, domain.ErrUnavailable):
		return echo.NewHTTPError(http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, domain.ErrNotFound):
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	default:
		return echo.NewHTTPError(http.StatusBadGateway, err.Error())
	}
}
