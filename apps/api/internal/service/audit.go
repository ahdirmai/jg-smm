package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// AuditService records who did what, and reads it back for the dashboard audit
// page (P6-10). Recording is best-effort: a failed insert is logged and never
// propagates to the caller, so auditing can never take down the mutation it
// observes.
type AuditService struct {
	store  port.AuditStore
	clock  port.Clock
	logger *slog.Logger
}

// AuditConfig tunes the audit service.
type AuditConfig struct {
	Clock  port.Clock
	Logger *slog.Logger
}

// NewAuditService wires the audit store. A nil store yields a no-op recorder so
// the API still boots (and still serves the read path) when auditing is off.
func NewAuditService(store port.AuditStore, cfg AuditConfig) *AuditService {
	if cfg.Clock == nil {
		cfg.Clock = systemClock{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &AuditService{store: store, clock: cfg.Clock, logger: cfg.Logger}
}

// Record appends one audit row. actorID is empty for system-initiated work.
// A nil store makes this a no-op; a store error is logged, never returned.
func (s *AuditService) Record(ctx context.Context, actorID, action, entity, entityID, ip, result string, diff []byte) {
	if s.store == nil {
		return
	}
	if result == "" {
		result = AuditOK
	}
	err := s.store.Record(ctx, port.AuditEntry{
		ActorID:  actorID,
		Action:   action,
		Entity:   entity,
		EntityID: entityID,
		Diff:     diff,
		IP:       ip,
		Result:   result,
		TS:       s.clock.Now(),
	})
	if err != nil {
		s.logger.Warn("audit record failed",
			"action", action, "entity", entity, "entityId", entityID, "err", err)
	}
}

// List returns a filtered page of the audit trail, newest first.
func (s *AuditService) List(ctx context.Context, f domain.AuditFilter) (domain.AuditResult, error) {
	if s.store == nil {
		return domain.AuditResult{Rows: []domain.AuditEntry{}}, nil
	}
	if err := f.Validate(); err != nil {
		return domain.AuditResult{}, err
	}
	out, err := s.store.List(ctx, f)
	if err != nil {
		return domain.AuditResult{}, err
	}
	if out.Rows == nil {
		out.Rows = []domain.AuditEntry{}
	}
	return out, nil
}

// ActorLabel resolves "who" for display: the user's email when known, else the
// system label.
func ActorLabel(e domain.AuditEntry) string {
	if e.ActorEmail != "" {
		return e.ActorEmail
	}
	if e.ActorName != "" {
		return e.ActorName
	}
	return domain.SystemActor
}

// AuditOK is the recorded result when a mutation succeeded.
const AuditOK = "ok"

// AuditView is the API-facing shape of one entry; the handler maps it to the
// generated oapigen type so the service stays free of transport types.
type AuditView struct {
	ID       string
	ActorID  string
	Actor    string
	Action   string
	Entity   string
	EntityID string
	Result   string
	IP       string
	TS       time.Time
}

// ToAuditView maps a domain entry to the API view, resolving the actor label.
func ToAuditView(e domain.AuditEntry) AuditView {
	return AuditView{
		ID:       e.ID,
		ActorID:  e.ActorID,
		Actor:    ActorLabel(e),
		Action:   e.Action,
		Entity:   e.Entity,
		EntityID: e.EntityID,
		Result:   e.Result,
		IP:       e.IP,
		TS:       e.TS,
	}
}
