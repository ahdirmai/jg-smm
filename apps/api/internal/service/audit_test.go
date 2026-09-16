package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// memAuditStore is the in-memory port.AuditStore for audit tests.
type memAuditStore struct {
	rows []domain.AuditEntry
}

func (s *memAuditStore) Record(_ context.Context, e port.AuditEntry) error {
	s.rows = append(s.rows, domain.AuditEntry{
		ActorID:  e.ActorID,
		Action:   e.Action,
		Entity:   e.Entity,
		EntityID: e.EntityID,
		Result:   e.Result,
		IP:       e.IP,
		TS:       e.TS,
	})
	return nil
}
func (s *memAuditStore) List(_ context.Context, f domain.AuditFilter) (domain.AuditResult, error) {
	out := []domain.AuditEntry{}
	for _, r := range s.rows {
		if f.Action != "" && r.Action != f.Action {
			continue
		}
		if f.Entity != "" && r.Entity != f.Entity {
			continue
		}
		out = append(out, r)
	}
	return domain.AuditResult{Rows: out, Total: int64(len(out)), Limit: f.Limit, Offset: f.Offset}, nil
}

// Record with a nil store is a no-op, so auditing can never stop the mutation
// it observes from completing.
func TestAuditServiceNilStoreIsNoOp(t *testing.T) {
	svc := NewAuditService(nil, AuditConfig{})
	svc.Record(context.Background(), "u1", "account.create", "account", "a1", "10.0.0.1", "ok", nil)

	res, err := svc.List(context.Background(), domain.AuditFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(res.Rows) != 0 {
		t.Fatalf("expected empty result, got %d rows", len(res.Rows))
	}
}

// Record stores the actor, the derived action, and the request IP; an empty
// result is normalised to the default "ok".
func TestAuditServiceRecordsEntry(t *testing.T) {
	store := &memAuditStore{}
	svc := NewAuditService(store, AuditConfig{Clock: fixedClock{t: time.Unix(1_750_000_000, 0).UTC()}})

	svc.Record(context.Background(), "u1", "template.create", "template", "t1", "10.0.0.4", "", nil)
	if len(store.rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(store.rows))
	}
	got := store.rows[0]
	if got.ActorID != "u1" || got.Action != "template.create" || got.Entity != "template" {
		t.Fatalf("unexpected row: %+v", got)
	}
	if got.IP != "10.0.0.4" {
		t.Errorf("IP = %q, want 10.0.0.4", got.IP)
	}
	if got.Result != AuditOK {
		t.Errorf("Result = %q, want %q", got.Result, AuditOK)
	}
	if got.TS.Unix() != 1_750_000_000 {
		t.Errorf("TS = %v, want fixed clock", got.TS)
	}
}

// List filters by action and entity, and rejects an inverted date range.
func TestAuditServiceListFiltersAndValidates(t *testing.T) {
	store := &memAuditStore{}
	svc := NewAuditService(store, AuditConfig{})
	svc.Record(context.Background(), "u1", "account.create", "account", "a1", "10.0.0.1", "ok", nil)
	svc.Record(context.Background(), "u1", "template.create", "template", "t1", "10.0.0.1", "ok", nil)

	res, err := svc.List(context.Background(), domain.AuditFilter{Action: "account.create", Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0].Action != "account.create" {
		t.Fatalf("expected only account.create, got %+v", res.Rows)
	}

	from := time.Unix(1_000, 0).UTC()
	_, err = svc.List(context.Background(), domain.AuditFilter{From: from, To: time.Unix(500, 0).UTC(), Limit: 10})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("expected ErrValidation for inverted range, got %v", err)
	}

	_, err = svc.List(context.Background(), domain.AuditFilter{Limit: 9999})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("expected ErrValidation for huge limit, got %v", err)
	}
}

// ActorLabel resolves to the user's email when known, else the system label.
func TestAuditActorLabel(t *testing.T) {
	if got := ActorLabel(domain.AuditEntry{ActorEmail: "ops@smm.local"}); got != "ops@smm.local" {
		t.Errorf("email actor = %q", got)
	}
	if got := ActorLabel(domain.AuditEntry{ActorName: "Ops"}); got != "Ops" {
		t.Errorf("name actor = %q", got)
	}
	if got := ActorLabel(domain.AuditEntry{}); got != domain.SystemActor {
		t.Errorf("empty actor = %q, want %q", got, domain.SystemActor)
	}
}
