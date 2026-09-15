package http

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

const traceIDKey = "traceId"

// requestLogger logs each request with traceId, method, route, status and
// duration using slog. It also echoes the traceId back in X-Request-Id.
func requestLogger() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			traceID := c.Request().Header.Get(echo.HeaderXRequestID)
			if traceID == "" {
				traceID = uuid.NewString()
			}
			c.Response().Header().Set(echo.HeaderXRequestID, traceID)
			c.Set(traceIDKey, traceID)

			start := time.Now()
			err := next(c)
			if err != nil {
				c.Error(err)
			}

			slog.InfoContext(c.Request().Context(), "http_request",
				"traceId", traceID,
				"method", c.Request().Method,
				"path", c.Request().URL.Path,
				"status", c.Response().Status,
				"durationMs", time.Since(start).Milliseconds(),
			)
			return nil
		}
	}
}

// limitBody rejects requests whose Content-Length (or streamed body) exceeds
// max bytes. Used by the internal callback routes (P1-13) so a worker cannot
// push unbounded payloads.
func limitBody(max int64) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			req := c.Request()
			if req.ContentLength > max {
				return echo.NewHTTPError(http.StatusRequestEntityTooLarge,
					fmt.Sprintf("body exceeds %d bytes", max))
			}
			req.Body = http.MaxBytesReader(c.Response().Writer, req.Body, max)
			return next(c)
		}
	}
}

// recoverer turns panics into 500s without killing the process.
func recoverer() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			defer func() {
				if r := recover(); r != nil {
					slog.ErrorContext(c.Request().Context(), "panic", "err", r, "traceId", c.Get(traceIDKey))
					_ = c.JSON(http.StatusInternalServerError, errorBody{
						Error: errorDetail{Code: "internal", Message: http.StatusText(http.StatusInternalServerError)},
					})
				}
			}()
			return next(c)
		}
	}
}
