package repository

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
	"github.com/ahdirmai/jg-smm/apps/api/internal/repository/sqlcgen"
)

// AuditRepo persists the immutable audit trail. The read path joins app_user so
// the dashboard receives a human-readable actor without a second round trip.
type AuditRepo struct {
	q *sqlcgen.Queries
}

// NewAuditRepo binds the repo to a sqlc query handle.
func NewAuditRepo(q *sqlcgen.Queries) *AuditRepo { return &AuditRepo{q: q} }

var _ port.AuditStore = (*AuditRepo)(nil)

// Record appends one entry. A missing actor is stored as NULL; an unparseable
// IP is dropped rather than failing the write (audit is best-effort).
func (r *AuditRepo) Record(ctx context.Context, e port.AuditEntry) error {
	if _, err := r.q.InsertAuditLog(ctx, sqlcgen.InsertAuditLogParams{
		ActorID:  uuidValue(e.ActorID),
		Action:   e.Action,
		Entity:   e.Entity,
		EntityID: e.EntityID,
		Diff:     e.Diff,
		Ip:       inetOrNull(e.IP),
		Result:   auditResult(e.Result),
	}); err != nil {
		return fmt.Errorf("repository.audit.Record: %w", err)
	}
	return nil
}

// List returns a filtered page, newest first, with the actor's email/name
// resolved, plus the total matching the same filter for pagination.
func (r *AuditRepo) List(ctx context.Context, f domain.AuditFilter) (domain.AuditResult, error) {
	limit, offset := pageBounds(f.Limit, f.Offset)
	params := sqlcgen.ListAuditLogsFilteredParams{
		Column1: uuidValue(f.ActorID),
		Column2: f.Action,
		Column3: f.Entity,
		Column4: tsOrNull(f.From),
		Column5: tsOrNull(f.To),
		Limit:   limit,
		Offset:  offset,
	}

	rows, err := r.q.ListAuditLogsFiltered(ctx, params)
	if err != nil {
		return domain.AuditResult{}, fmt.Errorf("repository.audit.List: %w", err)
	}
	out := make([]domain.AuditEntry, 0, len(rows))
	for _, row := range rows {
		out = append(out, toAuditEntry(row))
	}

	total, err := r.q.CountAuditLogsFiltered(ctx, sqlcgen.CountAuditLogsFilteredParams{
		Column1: params.Column1,
		Column2: params.Column2,
		Column3: params.Column3,
		Column4: params.Column4,
		Column5: params.Column5,
	})
	if err != nil {
		return domain.AuditResult{}, fmt.Errorf("repository.audit.List: count: %w", err)
	}

	return domain.AuditResult{
		Rows:   out,
		Total:  total,
		Limit:  int(limit),
		Offset: int(offset),
	}, nil
}

func toAuditEntry(r sqlcgen.ListAuditLogsFilteredRow) domain.AuditEntry {
	return domain.AuditEntry{
		ID:         uuidString(r.ID),
		ActorID:    uuidString(r.ActorID),
		ActorEmail: derefStr(r.ActorEmail),
		ActorName:  derefStr(r.ActorName),
		Action:     r.Action,
		Entity:     r.Entity,
		EntityID:   r.EntityID,
		Result:     r.Result,
		IP:         ipString(r.Ip),
		TS:         tsTimeOrZero(r.Ts),
	}
}

// auditResult normalises the outcome; empty becomes the default "ok".
func auditResult(s string) string {
	if s == "" {
		return "ok"
	}
	return s
}

// ipString renders a nullable inet as text; missing stays empty.
func ipString(a *netip.Addr) string {
	if a == nil {
		return ""
	}
	return a.String()
}

// tsOrNull maps a zero time to a NULL timestamptz so an unset filter bound does
// not become an implicit year-1 filter.
func tsOrNull(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t, Valid: true}
}
