package http

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/http/oapigen"
)

// errorBody is the wire error shape. It aliases the generated type so
// openapi/openapi.yaml stays the single source of truth for the contract.
type errorBody = oapigen.ErrorBody

type errorDetail = oapigen.ErrorDetail

// errorHandler maps domain sentinel errors to HTTP status codes and never leaks
// raw error strings (driver/transport details) to the client.
func errorHandler(err error, c echo.Context) {
	if c.Response().Committed {
		return
	}

	httpStatus := http.StatusInternalServerError
	code := "internal"

	// The caller went away (page reload, tab close, a flaky proxy killing the
	// connection). The work did not fail — nobody is left to read the response,
	// so report it as client-closed rather than a 500 the dashboards will treat
	// as a server fault. Same for a broken pipe / unexpected EOF mid-write.
	if errors.Is(err, context.Canceled) || errors.Is(err, io.ErrUnexpectedEOF) {
		c.Logger().Warnf("client closed request: %v %s", c.Request().Method, c.Path())
		httpStatus, code = 499, "client_closed"
		c.JSON(httpStatus, errorBody{Error: errorDetail{Code: code, Message: http.StatusText(httpStatus)}})
		return
	}

	switch {
	case errors.Is(err, domain.ErrNotFound):
		httpStatus, code = http.StatusNotFound, "not_found"
	case errors.Is(err, domain.ErrConflict):
		httpStatus, code = http.StatusConflict, "conflict"
	case errors.Is(err, domain.ErrValidation):
		httpStatus, code = http.StatusBadRequest, "validation"
	case errors.Is(err, domain.ErrUnauthorized):
		httpStatus, code = http.StatusUnauthorized, "unauthorized"
	case errors.Is(err, domain.ErrForbidden):
		httpStatus, code = http.StatusForbidden, "forbidden"
	case errors.Is(err, domain.ErrRateLimited):
		httpStatus, code = http.StatusTooManyRequests, "rate_limited"
	case errors.Is(err, domain.ErrUnavailable):
		httpStatus, code = http.StatusServiceUnavailable, "unavailable"
	}

	// Echo's own HTTPError (e.g. 404 route, 405) carries an explicit status and
	// optionally an explicit message. Read the message once; use it as the public
	// code only for 4xx. A 500's raw text can name a query, host, or driver and
	// must not be copied into the response.
	var he *echo.HTTPError
	var explicitMessage string
	if errors.As(err, &he) {
		httpStatus = he.Code
		explicitMessage, _ = he.Message.(string)
		if explicitMessage != "" && httpStatus < 500 {
			code = explicitMessage
		}
	}

	message := http.StatusText(httpStatus)
	if explicitMessage != "" && httpStatus >= 400 && httpStatus < 500 {
		message = explicitMessage
	}
	if err := c.JSON(httpStatus, errorBody{Error: errorDetail{Code: code, Message: message}}); err != nil {
		c.Logger().Error(err)
	}
}
