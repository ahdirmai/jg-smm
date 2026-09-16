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

// AccountHandler is the operator-facing account API (P1-15 / P1-16). RBAC is
// enforced by the route group (act permission), not here. The plaintext
// credential is accepted over TLS, sealed by the service, and never returned.
type AccountHandler struct {
	accounts *service.AccountService
}

// NewAccountHandler builds the handler. A nil service makes the routes return
// 503, keeping the API bootable before the DB is wired.
func NewAccountHandler(accounts *service.AccountService) *AccountHandler {
	return &AccountHandler{accounts: accounts}
}

// Register mounts the account routes into an authed group. The group carries
// the `read` floor; mutating routes add the stricter `act` gate (P5-04).
func (h *AccountHandler) Register(g *echo.Group) {
	g.POST("/accounts", h.create, RequirePermission(domain.PermAct))
	g.GET("/accounts", h.list)
	g.POST("/accounts/import", h.importRows, RequirePermission(domain.PermAct))
	g.POST("/accounts/:accountId", h.setStatus, RequirePermission(domain.PermAct))
	g.DELETE("/accounts/:accountId", h.remove, RequirePermission(domain.PermAct))
}

// create stores an account and packs it into a container.
func (h *AccountHandler) create(c echo.Context) error {
	var req oapigen.CreateAccountRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if h.accounts == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "account service unavailable")
	}

	tags := []string{}
	if req.Tags != nil {
		tags = *req.Tags
	}
	var proxyID *string
	if req.ProxyGroupId.IsSpecified() && !req.ProxyGroupId.IsNull() {
		p := req.ProxyGroupId.MustGet()
		proxyID = &p
	}

	account, _, err := h.accounts.Create(c.Request().Context(), service.AccountInput{
		Platform:     domain.Platform(req.Platform),
		Username:     req.Username,
		Password:     req.Password,
		ProxyGroupID: proxyID,
		Tags:         tags,
	})
	if err != nil {
		return translateAccountError(err)
	}
	return c.JSON(http.StatusCreated, toAccountResponse(account))
}

// importRows bulk-creates accounts (P4-07). Per-row failures stay in the body
// with a 200; only the call-level preconditions (empty, over-cap, rate limit)
// produce an error status.
func (h *AccountHandler) importRows(c echo.Context) error {
	var req oapigen.ImportAccountsRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if h.accounts == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "account service unavailable")
	}

	rows := make([]service.AccountInput, 0, len(req.Rows))
	for _, r := range req.Rows {
		tags := []string{}
		if r.Tags != nil {
			tags = *r.Tags
		}
		var proxyID *string
		if r.ProxyGroupId.IsSpecified() && !r.ProxyGroupId.IsNull() {
			p := r.ProxyGroupId.MustGet()
			proxyID = &p
		}
		rows = append(rows, service.AccountInput{
			Platform:     domain.Platform(r.Platform),
			Username:     r.Username,
			Password:     r.Password,
			ProxyGroupID: proxyID,
			Tags:         tags,
		})
	}

	// The caller key is the client IP: imports are an operator action, and a
	// shared-NAT office is the realistic worst case for a false refusal.
	caller := c.RealIP()

	res, err := h.accounts.Import(c.Request().Context(), caller, rows)
	if err != nil {
		if errors.Is(err, domain.ErrRateLimited) {
			return echo.NewHTTPError(http.StatusTooManyRequests, "import rate limit exceeded; try again in a minute")
		}
		return translateAccountError(err)
	}

	invalid := make([]struct {
		Reason string `json:"reason"`
		Row    int    `json:"row"`
	}, 0, len(res.Invalid))
	for _, e := range res.Invalid {
		invalid = append(invalid, struct {
			Reason string `json:"reason"`
			Row    int    `json:"row"`
		}{Row: e.Row, Reason: e.Reason})
	}
	return c.JSON(http.StatusOK, oapigen.ImportResult{
		Queued:      res.Queued,
		Invalid:     invalid,
		RateLimited: res.RateLimited,
	})
}

// list returns every account without credentials.
func (h *AccountHandler) list(c echo.Context) error {
	if h.accounts == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "account service unavailable")
	}
	views, err := h.accounts.List(c.Request().Context())
	if err != nil {
		return translateAccountError(err)
	}
	items := make([]oapigen.Account, 0, len(views))
	for _, v := range views {
		items = append(items, toAccountResponse(v))
	}
	return c.JSON(http.StatusOK, oapigen.AccountList{Accounts: items})
}

// setStatus drives pause/resume.
func (h *AccountHandler) setStatus(c echo.Context) error {
	if h.accounts == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "account service unavailable")
	}
	id := c.Param("accountId")
	if id == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "accountId is required")
	}
	var req oapigen.SetAccountStatusRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}

	var account service.AccountSummary
	var err error
	switch req.Status {
	case oapigen.SetAccountStatusRequestStatusACTIVE:
		account, err = h.accounts.Resume(c.Request().Context(), id)
	case oapigen.SetAccountStatusRequestStatusPAUSED:
		account, err = h.accounts.Pause(c.Request().Context(), id)
	default:
		return echo.NewHTTPError(http.StatusBadRequest, "status must be ACTIVE or PAUSED")
	}
	if err != nil {
		return translateAccountError(err)
	}
	return c.JSON(http.StatusOK, toAccountResponse(account))
}

// remove deletes an account and releases its container slot.
func (h *AccountHandler) remove(c echo.Context) error {
	if h.accounts == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "account service unavailable")
	}
	id := c.Param("accountId")
	if id == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "accountId is required")
	}
	if err := h.accounts.Remove(c.Request().Context(), id); err != nil {
		return translateAccountError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

// translateAccountError maps domain errors to HTTP statuses.
func translateAccountError(err error) error {
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

// toAccountResponse shapes the credential-free read model for the API.
func toAccountResponse(v service.AccountSummary) oapigen.Account {
	out := oapigen.Account{
		Id:         v.ID,
		Platform:   oapigen.AccountPlatform(v.Platform),
		Username:   v.Username,
		AuthStatus: oapigen.AccountAuthStatus(v.AuthStatus),
		Status:     oapigen.AccountStatus(v.Status),
	}
	if v.Handle != nil {
		out.Handle = nullable.NewNullableWithValue[string](*v.Handle)
	}
	if v.WorkerID != nil {
		out.WorkerId = nullable.NewNullableWithValue[string](*v.WorkerID)
	}
	if v.LastError != nil {
		out.LastError = nullable.NewNullableWithValue[string](*v.LastError)
	}
	tags := v.Tags
	out.Tags = &tags
	return out
}
