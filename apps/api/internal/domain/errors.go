// Package domain holds entities, enums, constants and sentinel errors shared by
// the whole API. It MUST have zero internal imports (stdlib only) so every other
// layer can depend on it without creating cycles.
package domain

import "errors"

// Sentinel errors. Handlers map these to HTTP status codes; services return them
// instead of leaking driver/transport errors.
var (
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("conflict")
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
	ErrValidation   = errors.New("validation failed")
	ErrRateLimited  = errors.New("rate limited")
	ErrUnavailable  = errors.New("unavailable")
)
