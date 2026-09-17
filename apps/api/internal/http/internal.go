package http

import (
	"errors"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/http/oapigen"
	"github.com/ahdirmai/jg-smm/apps/api/internal/service"
)

// MaxCallbackBytes caps worker callback bodies. Workers post small JSON verdicts
// plus an optional short error string; anything bigger is a bug or an abuse
// attempt. See P1-13.
const MaxCallbackBytes = 64 << 10 // 64 KiB

// maxErrorRunes is the truncation budget for free-text fields so a misbehaving
// worker cannot store unbounded text in the DB.
const maxErrorRunes = 4096

// maxExcerptRunes is the budget for the platform-response excerpt: it is a
// debug trail, not a log dump, and the schema caps it at 1024.
const maxExcerptRunes = 1024

// screenshotExt is the only accepted extension for evidence file names.
const screenshotExt = ".png"

// InternalHandler receives worker callbacks (action verdicts, auth outcomes,
// heartbeats). Workers never touch the DB (ADR 0011): they SUBSCRIBE control and
// POST here. The routes are internal-only and body-size limited.
type InternalHandler struct {
	jobs *service.JobService
}

// NewInternalHandler builds the handler. A nil JobService keeps the API bootable
// before the worker path is wired (routes then return 503).
func NewInternalHandler(jobs *service.JobService) *InternalHandler {
	return &InternalHandler{jobs: jobs}
}

// Register mounts the internal routes behind a body-size limit. Mounted on the
// root echo (not the /api group): these are private-network calls from the
// API's own worker containers, so they carry no session and no RBAC gate.
func (h *InternalHandler) Register(e *echo.Echo) {
	g := e.Group("/internal", limitBody(MaxCallbackBytes))
	g.POST("/action-callback", h.actionCallback)
	g.POST("/account-callback", h.accountCallback)
	g.POST("/heartbeat", h.heartbeat)
	// A locally scaled worker boots knowing only its hostname; this is how it
	// learns which dashboard-created row is its and what GPS to spoof. Mounted
	// next to the heartbeat because it is the handshake that makes the heartbeat
	// resolvable at all (see WorkerClaim).
	g.POST("/claim", h.claim)
	g.GET("/worker/:workerId/geolocation", h.geolocation)
}

// actionCallback records the outcome of one action attempt.
func (h *InternalHandler) actionCallback(c echo.Context) error {
	var req oapigen.ActionCallback
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid callback body")
	}
	if req.AttemptId == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "attempt_id is required")
	}
	status, err := parseAttemptStatus(req.Status)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	screenshot, err := sanitiseScreenshot(req.ScreenshotPath)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if h.jobs == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "job service unavailable")
	}

	err = h.jobs.RecordAttempt(c.Request().Context(), service.AttemptRecord{
		AttemptID:       req.AttemptId,
		Status:          status,
		Screenshot:      screenshot,
		Error:           truncatePtr(req.Error, maxErrorRunes),
		ActionType:      req.ActionType,
		TargetURL:       req.TargetUrl,
		WorkerID:        req.WorkerId,
		RenderedText:    req.RenderedText,
		ResponseExcerpt: truncatePtr(req.ResponseExcerpt, maxExcerptRunes),
		DurationMs:      derefInt64(req.DurationMs),
	})
	return h.translate(c, err)
}

// accountCallback reports an account auth outcome (login result, 2FA need).
func (h *InternalHandler) accountCallback(c echo.Context) error {
	var req oapigen.AccountCallback
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid callback body")
	}
	if req.AccountId == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "account_id is required")
	}
	auth, err := parseAuthStatus(req.AuthStatus)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if h.jobs == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "job service unavailable")
	}

	err = h.jobs.RecordAuthOutcome(c.Request().Context(), service.AuthOutcome{
		AccountID:  req.AccountId,
		AuthStatus: auth,
		Handle:     req.Handle,
		Error:      truncatePtr(req.Error, maxErrorRunes),
	})
	return h.translate(c, err)
}

// heartbeat stores one worker telemetry sample.
func (h *InternalHandler) heartbeat(c echo.Context) error {
	var req oapigen.Heartbeat
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid heartbeat body")
	}
	if req.WorkerId == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "worker_id is required")
	}
	if req.QueueDepth != nil && *req.QueueDepth < 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "queue_depth must be >= 0")
	}
	if h.jobs == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "job service unavailable")
	}

	var lastAction *time.Time
	if req.LastActionAt != nil {
		lastAction = req.LastActionAt
	}
	var novncURL *string
	if req.NovncUrl.IsSpecified() && !req.NovncUrl.IsNull() {
		v := req.NovncUrl.MustGet()
		novncURL = &v
	}
	err := h.jobs.RecordHeartbeat(c.Request().Context(), service.HeartbeatRecord{
		WorkerID:      req.WorkerId,
		BrowserStatus: string(ptrBrowser(req.BrowserStatus)),
		QueueDepth:    ptrInt(req.QueueDepth),
		CurrentJobID:  req.CurrentJobId,
		CPU:           ptrFloat64(req.Cpu),
		Mem:           ptrFloat64(req.Mem),
		JobsDone:      ptrInt(req.JobsDone),
		LastActionAt:  lastAction,
		NovncURL:      novncURL,
	})
	return h.translate(c, err)
}

// claim binds a booted worker container to a dashboard-created row. The
// worker sends its hostname-derived identity and the redis keys it is already
// subscribed to; the response carries the row's real id (so every later
// heartbeat, queue pop and control subscription addresses the row) and the
// frozen GPS point the browser must spoof before its first job.
//
// Not in oapigen: the internal group is a private-network contract between the
// API and its own workers, not part of the public dashboard API, so it defines
// its request/response shapes inline rather than generating them.
func (h *InternalHandler) claim(c echo.Context) error {
	var req struct {
		ContainerID    string  `json:"containerId"`
		ControlChannel *string `json:"controlChannel"`
		ActionQueue    *string `json:"actionQueue"`
		SessionPVC     *string `json:"sessionPvc"`
		NovncURL       *string `json:"novncUrl"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid claim body")
	}
	if req.ContainerID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "containerId is required")
	}
	if h.jobs == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "job service unavailable")
	}

	res, err := h.jobs.Claim(c.Request().Context(), service.WorkerClaim{
		ContainerID:    req.ContainerID,
		ControlChannel: derefStr(req.ControlChannel),
		ActionQueue:    derefStr(req.ActionQueue),
		SessionPVC:     derefStr(req.SessionPVC),
		NovncURL:       req.NovncURL,
	})
	if err != nil {
		return h.translate(c, err)
	}
	return c.JSON(http.StatusOK, res)
}

// geolocation returns the worker's frozen GPS point so Playwright can spoof a
// fixed position before its first job. The internal group is private-network
// only, so this needs no session.
func (h *InternalHandler) geolocation(c echo.Context) error {
	workerID := c.Param("workerId")
	if workerID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "workerId is required")
	}
	if h.jobs == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "job service unavailable")
	}
	g, err := h.jobs.Geolocation(c.Request().Context(), workerID)
	if err != nil {
		return h.translate(c, err)
	}
	return c.JSON(http.StatusOK, oapigen.WorkerGeolocation{
		Latitude:  g.Latitude,
		Longitude: g.Longitude,
		Location:  &g.Location,
	})
}

// translate maps a service error onto an HTTP response.
func (h *InternalHandler) translate(c echo.Context, err error) error {
	if err == nil {
		return c.JSON(http.StatusOK, oapigen.CallbackAck{Accepted: true})
	}
	switch {
	case errors.Is(err, domain.ErrValidation):
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	case errors.Is(err, domain.ErrNotFound):
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	case errors.Is(err, domain.ErrUnavailable):
		// P3-deferred persistence: the payload was validated and accepted, so
		// answer 202 instead of making the worker retry pointlessly.
		return c.JSON(http.StatusAccepted, oapigen.CallbackAck{Accepted: false})
	default:
		return err // 500 via the error handler
	}
}

// sanitiseScreenshot applies path.Base (stopping traversal) and enforces the
// extension. Empty input is allowed (no evidence attached).
func sanitiseScreenshot(p *string) (string, error) {
	if p == nil || *p == "" {
		return "", nil
	}
	base := path.Base(*p)
	if base == "." || base == "/" || len(base) < len(screenshotExt) ||
		!strings.EqualFold(base[len(base)-len(screenshotExt):], screenshotExt) {
		return "", errors.New("screenshot_path must be a .png file name")
	}
	return base, nil
}

func parseAttemptStatus(s oapigen.AttemptStatus) (domain.AttemptStatus, error) {
	switch s {
	case oapigen.AttemptStatusSUCCESS:
		return domain.AttemptSuccess, nil
	case oapigen.AttemptStatusFAILED:
		return domain.AttemptFailed, nil
	case oapigen.AttemptStatusRETRY:
		return domain.AttemptRetry, nil
	case oapigen.AttemptStatusCANCELLED:
		return domain.AttemptCancelled, nil
	default:
		return "", errors.New("invalid status")
	}
}

func parseAuthStatus(s oapigen.AuthStatus) (domain.AuthStatus, error) {
	switch s {
	case oapigen.AuthStatusAUTHENTICATING:
		return domain.AuthAuthenticating, nil
	case oapigen.AuthStatusNEEDSINPUT:
		return domain.AuthNeedsInput, nil
	case oapigen.AuthStatusAUTHENTICATED:
		return domain.AuthAuthenticated, nil
	case oapigen.AuthStatusFAILED:
		return domain.AuthFailed, nil
	default:
		return "", errors.New("invalid authStatus")
	}
}

// truncatePtr caps a free-text pointer, returning nil for empty input.
func truncatePtr(p *string, max int) *string {
	if p == nil {
		return nil
	}
	v := strings.TrimSpace(*p)
	if v == "" {
		return nil
	}
	if len([]rune(v)) > max {
		v = string([]rune(v)[:max])
	}
	return &v
}

func ptrInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// derefInt64 unboxes an optional int64 the schema models as a pointer.
func derefInt64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func ptrBrowser(p *oapigen.HeartbeatBrowserStatus) oapigen.HeartbeatBrowserStatus {
	if p == nil {
		return ""
	}
	return *p
}

func ptrFloat64(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}
