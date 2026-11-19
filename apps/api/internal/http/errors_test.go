package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
)

func TestExplicitConflictKeepsActionableMessage(t *testing.T) {
	e := echo.New()
	rec := httptest.NewRecorder()
	c := e.NewContext(httptest.NewRequest(http.MethodPost, "/login", nil), rec)
	errorHandler(echo.NewHTTPError(http.StatusConflict, "worker has no live view"), c)
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Message != "worker has no live view" {
		t.Fatalf("lost actionable reason: %s", rec.Body.String())
	}
}

// A client that vanishes mid-request (page reload, tab close, proxy drop) shows
// up as context.Canceled. That is not a server fault, and a 500 here makes the
// dashboards treat a flaky network as an outage. It must surface as 499 with a
// body that says so, and it must never leak driver internals.
func TestErrorHandlerClientClosedIs499(t *testing.T) {
	for name, err := range map[string]error{
		"context canceled": context.Canceled,
		"unexpected eof":   io.ErrUnexpectedEOF,
	} {
		t.Run(name, func(t *testing.T) {
			e := echo.New()
			req := httptest.NewRequest(http.MethodPost, "/api/containers", strings.NewReader("{}"))
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetPath("/api/containers")

			errorHandler(err, c)

			if rec.Code != 499 {
				t.Fatalf("status: got %d, want 499 (client closed, not a server fault)", rec.Code)
			}
			body := rec.Body.String()
			if !strings.Contains(body, "client_closed") {
				t.Fatalf("body: got %s, want code client_closed", body)
			}
		})
	}
}

// A wrapped cancel must resolve the same way: sql/pgx returns context.Canceled
// nested inside its own error type, and errors.Is is the whole point.
func TestErrorHandlerWrappedCancelIs499(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/api/containers", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	errorHandler(errors.Join(errors.New("driver write failed"), context.Canceled), c)

	if rec.Code != 499 {
		t.Fatalf("status: got %d, want 499 for a wrapped cancel", rec.Code)
	}
}

// A real server fault stays a 500 — the 499 branch must not swallow it.
func TestErrorHandlerUnknownStays500(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/api/containers", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	errorHandler(errors.New("boom: connection refused"), c)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "internal") {
		t.Fatalf("body: got %s, want code internal", rec.Body.String())
	}
}

// A committed response (already written) is left alone: there is nothing to
// send and overwriting it would corrupt a partial-but-valid response.
func TestErrorHandlerCommittedIsNoop(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/containers", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	_ = c.JSON(http.StatusOK, map[string]string{"ok": "true"})

	errorHandler(context.Canceled, c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want the committed 200 untouched", rec.Code)
	}
}

// The error envelope has a deliberately narrow public vocabulary. A 500 must
// never echo the wrapped internal error; 4xx Echo messages remain actionable.
func TestErrorHandlerKeeps500OpaqueAnd4xxExplicit(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		status  int
		code    string
		message string
	}{
		{"unknown", errors.New("pg: connection refused"), 500, "internal", "Internal Server Error"},
		{"domain not found", domain.ErrNotFound, 404, "not_found", "Not Found"},
		{"explicit 409", echo.NewHTTPError(409, "already exists"), 409, "already exists", "already exists"},
		{"empty 400", echo.NewHTTPError(400, ""), 400, "internal", "Bad Request"},
		{"explicit 500", echo.NewHTTPError(500, "SELECT secret FROM users"), 500, "internal", "Internal Server Error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			errorHandler(tc.err, c)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d", rec.Code, tc.status)
			}
			var body errorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.Error.Code != tc.code || body.Error.Message != tc.message {
				t.Fatalf("body = %+v, want code=%q message=%q", body.Error, tc.code, tc.message)
			}
		})
	}
}

func TestTranslateAuditErrorDoesNotExposeA500Message(t *testing.T) {
	err := translateAuditError(errors.New("pq: password authentication failed for user smm"))
	var he *echo.HTTPError
	if errors.As(err, &he) {
		t.Fatalf("audit 500 must fall through to the generic handler, got %+v", he)
	}
}
