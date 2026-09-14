package http

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
)

// errorBody is the single error shape returned to clients.
type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// errorHandler maps domain sentinel errors to HTTP status codes and never leaks
// raw error strings (driver/transport details) to the client.
func errorHandler(err error, c echo.Context) {
	if c.Response().Committed {
		return
	}

	httpStatus := http.StatusInternalServerError
	code := "internal"

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

	// Echo's own HTTPError (e.g. 404 route, 405) carries an explicit status.
	var he *echo.HTTPError
	if errors.As(err, &he) {
		httpStatus = he.Code
		if msg, ok := he.Message.(string); ok && msg != "" {
			code = msg
		}
	}

	message := http.StatusText(httpStatus)
	if err := c.JSON(httpStatus, errorBody{Error: errorDetail{Code: code, Message: message}}); err != nil {
		c.Logger().Error(err)
	}
}
