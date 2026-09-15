package adapter

import (
	"context"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
)

// TestStaticProvisionerObserveReadsWorkerRow is the local tier's equivalent of
// reading a pod label: the reconciler diff must behave the same in dev and prod.
func TestStaticProvisionerObserveReadsWorkerRow(t *testing.T) {
	store := newMemWorkerStore()
	logs := newMemLogStore()
	clock := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	p := NewStaticProvisioner(store, logs, clock, nil)

	ctx := context.Background()

	// Absent worker -> (0, false), not an error (converges to absent).
	if gen, exists, err := p.Observe(ctx, "missing"); err != nil || exists || gen != 0 {
		t.Fatalf("observe missing = (%d,%v,%v), want (0,false,nil)", gen, exists, err)
	}

	// Create records the audit intent without touching a cluster.
	w := domain.Worker{ID: "w1", Generation: 3, Name: "local-worker"}
	if err := p.CreateWorker(ctx, w); err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(logs.logs) != 1 || logs.logs[0].Op != domain.OpCreate || logs.logs[0].Generation != 3 {
		t.Fatalf("provision log wrong: %+v", logs.logs)
	}

	// Store the row (the caller does this in the real flow), then observe.
	if _, err := store.Create(ctx, w); err != nil {
		t.Fatalf("seed worker: %v", err)
	}
	if gen, exists, err := p.Observe(ctx, "w1"); err != nil || !exists || gen != 3 {
		t.Fatalf("observe = (%d,%v,%v), want (3,true,nil)", gen, exists, err)
	}

	// An observed generation is authoritative once a heartbeat has landed.
	observed := 5
	row, _ := store.GetByID(ctx, "w1")
	row.ObservedGen = &observed
	_, _ = store.Update(ctx, row)
	if gen, _, err := p.Observe(ctx, "w1"); err != nil || gen != 5 {
		t.Fatalf("observe after heartbeat = (%d,%v), want (5,nil)", gen, err)
	}

	// Delete records the symmetric audit row.
	if err := p.DeleteWorker(ctx, "w1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(logs.logs) != 2 || logs.logs[1].Op != domain.OpDelete {
		t.Fatalf("delete not audited: %+v", logs.logs)
	}
}

// TestStaticProvisionerWithoutStores keeps the API bootable before the DB is
// wired: no store means Observe reports absent instead of panicking.
func TestStaticProvisionerWithoutStores(t *testing.T) {
	p := NewStaticProvisioner(nil, nil, nil, nil)
	if gen, exists, err := p.Observe(context.Background(), "any"); err != nil || exists || gen != 0 {
		t.Fatalf("nil-store observe = (%d,%v,%v), want (0,false,nil)", gen, exists, err)
	}
	if err := p.CreateWorker(context.Background(), domain.Worker{ID: "x", Generation: 1}); err != nil {
		t.Fatalf("create with nil stores: %v", err)
	}
}
