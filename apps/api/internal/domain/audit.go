package domain

import (
	"fmt"
	"time"
)

// AuditEntry is one row of the immutable audit trail (P6-10). Actor is NULL for
// system-initiated rows (schedulers, reconcilers), which is why every actor
// field is optional on the read side.
type AuditEntry struct {
	ID         string
	ActorID    string // empty when the actor is "system"
	ActorEmail string // empty when the actor is "system"
	ActorName  string // empty when the actor is "system"
	Action     string // e.g. account.create, worker.create
	Entity     string // e.g. account, worker, template
	EntityID   string // the affected row's id, or a human label
	Result     string // "ok", or the failure reason
	IP         string // empty when unknown / system
	TS         time.Time
}

// AuditFilter is the read-side query for the dashboard audit page. Every field
// is optional; an all-zero filter returns the newest entries.
type AuditFilter struct {
	ActorID string    // exact user id
	Action  string    // e.g. account.create
	Entity  string    // e.g. account
	From    time.Time // inclusive; zero = no lower bound
	To      time.Time // exclusive; zero = no upper bound
	Limit   int       // page size
	Offset  int       // page offset
}

// AuditResult is a page of entries plus the total that matches the same filter,
// so the dashboard can render "Showing N of M entries".
type AuditResult struct {
	Rows   []AuditEntry
	Total  int64
	Limit  int
	Offset int
}

// Validate clamps the page bounds so a hostile or careless query cannot ask
// for an unbounded result set.
func (f AuditFilter) Validate() error {
	if f.Limit < 0 || f.Limit > 500 {
		return fmt.Errorf("%w: limit must be between 0 and 500", ErrValidation)
	}
	if f.Offset < 0 {
		return fmt.Errorf("%w: offset must not be negative", ErrValidation)
	}
	if !f.From.IsZero() && !f.To.IsZero() && !f.To.After(f.From) {
		return fmt.Errorf("%w: 'to' must be after 'from'", ErrValidation)
	}
	return nil
}

// SystemActor is the recorded actor for rows written by a background process
// rather than a person (the action scheduler, the reconciler, the ingesters).
const SystemActor = "system"
