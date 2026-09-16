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

// A numeric query param must bind into an int field: the binder marshals the
// query value to JSON before unmarshalling into the struct, so a raw "10"
// string would be rejected by json.Unmarshal into an *int (audit pagination
// returned 400 until this coercion existed).
func TestFormQueryBinderCoercesNumericParams(t *testing.T) {
	type params struct {
		Limit  *int   `form:"limit,omitempty" json:"limit,omitempty"`
		Offset *int   `form:"offset,omitempty" json:"offset,omitempty"`
		Name   string `form:"name,omitempty" json:"name,omitempty"`
	}

	e := echo.New()
	e.Binder = formQueryBinder{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/r?limit=25&offset=5&name=audit", nil)
	c := e.NewContext(req, rec)

	var p params
	if err := c.Bind(&p); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if p.Limit == nil || *p.Limit != 25 {
		t.Errorf("Limit = %v, want 25", p.Limit)
	}
	if p.Offset == nil || *p.Offset != 5 {
		t.Errorf("Offset = %v, want 5", p.Offset)
	}
	if p.Name != "audit" {
		t.Errorf("Name = %q, want audit", p.Name)
	}
}

// A non-numeric value for an int field must not silently bind as 0; the bind
// should fail loudly so a bad query is a 400, not a wrong default.
func TestFormQueryBinderRejectsBadNumericParam(t *testing.T) {
	type params struct {
		Limit *int `form:"limit,omitempty" json:"limit,omitempty"`
	}

	e := echo.New()
	e.Binder = formQueryBinder{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/r?limit=abc", nil)
	c := e.NewContext(req, rec)

	var p params
	if err := c.Bind(&p); err == nil {
		t.Fatalf("expected bind error for limit=abc, got nil (Limit=%v)", p.Limit)
	}
}
