package http

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
)

// formQueryBinder restores query-parameter binding for the generated request
// types. oapi-codegen tags query params with `form:"name,omitempty"`, but Echo
// v4's DefaultBinder looks up query params by the `query` tag (and does not
// strip `,omitempty`), so every GET filter silently bound to its zero value.
// This binder keeps Echo's path/body binding and only replaces the query step:
// it reads each param by its `form` tag, then decodes through the struct's JSON
// tags so `*string`, named enum strings, and openapi_types.Date resolve the same
// way they do on the response side.
type formQueryBinder struct {
	echo.DefaultBinder
}

var _ echo.Binder = formQueryBinder{}

// Bind mirrors DefaultBinder.Bind (path, then query, then body) but routes the
// query step through the `form` tag.
func (b formQueryBinder) Bind(i any, c echo.Context) error {
	if err := b.BindPathParams(c, i); err != nil {
		return err
	}
	switch c.Request().Method {
	case http.MethodGet, http.MethodHead, http.MethodDelete:
		if err := bindQueryFormTag(i, c.QueryParams()); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error()).SetInternal(err)
		}
	}
	return b.BindBody(c, i)
}

// bindQueryFormTag maps query values onto dst by each field's `form` tag. Only
// single-valued params are supported; a repeated key binds its first value.
//
// Values arrive as strings, but the target field may not be (e.g. `limit` is a
// *int, `from` is a *time.Time). Rather than emitting {"limit":"10"} — which
// json.Unmarshal refuses to coerce into an int — each value is converted to
// the field's own JSON type first, so a numeric param binds as a number and a
// date-time param binds as a string the time format accepts.
func bindQueryFormTag(dst any, q map[string][]string) error {
	if len(q) == 0 {
		return nil
	}
	v := reflect.ValueOf(dst)
	if v.Kind() != reflect.Ptr || v.IsNil() {
		return nil
	}
	v = v.Elem()
	if v.Kind() != reflect.Struct {
		return nil
	}

	pairs := make(map[string]any, v.NumField())
	for i := 0; i < v.NumField(); i++ {
		field := v.Type().Field(i)
		name := strings.Split(field.Tag.Get("form"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		vals, ok := q[name]
		if !ok || len(vals) == 0 {
			continue
		}
		if len(vals) > 1 {
			pairs[name] = vals
			continue
		}
		pairs[name] = coerceQueryParam(vals[0], field.Type)
	}
	if len(pairs) == 0 {
		return nil
	}
	raw, err := json.Marshal(pairs)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return err
	}
	return nil
}

// coerceQueryParam converts a query-string scalar to the JSON shape the target
// Go type expects. Integers and booleans become real JSON numbers/bools so
// json.Unmarshal does not reject a string where a number is required; every
// other type (string, *time.Time, openapi_types.Date, enums) is passed through
// as a string, which those types decode from directly.
func coerceQueryParam(s string, t reflect.Type) any {
	// Drill through pointers: *int behaves like int for the value shape.
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if n, err := strconv.ParseUint(s, 10, 64); err == nil {
			return n
		}
	case reflect.Float32, reflect.Float64:
		if n, err := strconv.ParseFloat(s, 64); err == nil {
			return n
		}
	case reflect.Bool:
		if b, err := strconv.ParseBool(s); err == nil {
			return b
		}
	}
	return s
}
