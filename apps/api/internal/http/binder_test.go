package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	openapiTypes "github.com/oapi-codegen/runtime/types"
)

// Echo v4 binds query params by the `query` tag, but the generated structs only
// carry `form` tags. formQueryBinder must fill them from the query string,
// including optional params (`,omitempty`) and non-string types (Date).
func TestFormQueryBinderBindsAllParamKinds(t *testing.T) {
	type params struct {
		Kind     string             `form:"kind" json:"kind"`
		Format   *string            `form:"format,omitempty" json:"format,omitempty"`
		Platform *string            `form:"platform,omitempty" json:"platform,omitempty"`
		From     *openapiTypes.Date `form:"from,omitempty" json:"from,omitempty"`
		Metric   *string            `form:"metric,omitempty" json:"metric,omitempty"`
		Ignored  string             `json:"ignored"`
		Tags     []string           `form:"tags,omitempty" json:"tags,omitempty"`
	}

	e := echo.New()
	e.Binder = formQueryBinder{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/r?kind=actions&format=csv&platform=instagram&from=2026-09-01&tags=a&tags=b&metric=followers", nil)
	c := e.NewContext(req, rec)

	var p params
	if err := c.Bind(&p); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if p.Kind != "actions" {
		t.Errorf("Kind = %q, want actions", p.Kind)
	}
	if p.Format == nil || *p.Format != "csv" {
		t.Errorf("Format = %v, want csv", p.Format)
	}
	if p.Platform == nil || *p.Platform != "instagram" {
		t.Errorf("Platform = %v, want instagram", p.Platform)
	}
	if p.From == nil || p.From.Time.IsZero() {
		t.Errorf("From = %v, want a parsed date", p.From)
	}
	if p.From != nil && p.From.Time.Format("2006-01-02") != "2026-09-01" {
		t.Errorf("From = %v, want 2026-09-01", p.From.Time)
	}
	if p.Metric == nil || *p.Metric != "followers" {
		t.Errorf("Metric = %v, want followers", p.Metric)
	}
	if len(p.Tags) != 2 || p.Tags[0] != "a" || p.Tags[1] != "b" {
		t.Errorf("Tags = %v, want [a b]", p.Tags)
	}
}

// An omitted optional param must stay nil; the required one must error via the
// caller when the service validates it (binder leaves it at zero).
func TestFormQueryBinderOmitsAbsentOptionals(t *testing.T) {
	type params struct {
		Kind   string  `form:"kind" json:"kind"`
		Format *string `form:"format,omitempty" json:"format,omitempty"`
	}
	e := echo.New()
	e.Binder = formQueryBinder{}
	c := e.NewContext(httptest.NewRequest(http.MethodGet, "/r?kind=targets", nil), httptest.NewRecorder())

	var p params
	if err := c.Bind(&p); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if p.Kind != "targets" {
		t.Errorf("Kind = %q, want targets", p.Kind)
	}
	if p.Format != nil {
		t.Errorf("Format = %v, want nil when omitted", *p.Format)
	}
}

// A value that cannot decode into the target type must surface as a 400, not a
// silent zero value.
func TestFormQueryBinderRejectsBadValue(t *testing.T) {
	type params struct {
		From *openapiTypes.Date `form:"from,omitempty" json:"from,omitempty"`
	}
	e := echo.New()
	e.Binder = formQueryBinder{}
	c := e.NewContext(httptest.NewRequest(http.MethodGet, "/r?from=not-a-date", nil), httptest.NewRecorder())

	var p params
	if err := c.Bind(&p); err == nil {
		t.Fatalf("expected an error for an invalid date, got none (From=%v)", p.From)
	}
}
