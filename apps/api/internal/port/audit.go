package port

import (
	"context"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
)

// AuditEntry is the write model for the audit trail. ActorID is empty for
// system-initiated rows; IP is empty when unknown.
type AuditEntry struct {
	ActorID  string
	Action   string
	Entity   string
	EntityID string
	Diff     []byte
	IP       string
	Result   string
	TS       time.Time
}

// AuditStore records and reads the immutable audit trail (P6-10).
//
// Writes are best-effort: the recorder logs a failure instead of failing the
// user's request, so a full audit_log must never take down a mutation.
type AuditStore interface {
	// Record appends one entry. A missing actor is stored as NULL.
	Record(ctx context.Context, entry AuditEntry) error
	// List returns a filtered page, newest first, with the actor's email/name
	// resolved via app_user, plus the total matching the same filter.
	List(ctx context.Context, filter domain.AuditFilter) (domain.AuditResult, error)
}
