package service

import (
	"context"
	"testing"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
)

// TestContainerCreateManual exercises P1-19: a manual container is created
// RUNNING, MANUAL, and generation 1 — the state the reconciler provisions from.
func TestContainerCreateManual(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	svc := NewContainerService(store, accounts, nil, ContainerConfig{})

	w, err := svc.Create(context.Background(), "", "ID")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if w.Source != domain.SourceManual {
		t.Fatalf("source = %q, want MANUAL", w.Source)
	}
	if w.DesiredState != domain.DesiredRunning {
		t.Fatalf("desiredState = %q, want RUNNING", w.DesiredState)
	}
	if w.Generation != 1 {
		t.Fatalf("generation = %d, want 1", w.Generation)
	}
	if w.Region != "ID" {
		t.Fatalf("region = %q, want ID", w.Region)
	}
	if w.Name == "" {
		t.Fatal("name should be generated when omitted")
	}
	if store.get(w.ID).ID != w.ID {
		t.Fatalf("container not persisted: %s", w.ID)
	}
}

func TestContainerCreateValidatesRegion(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	svc := NewContainerService(store, accounts, nil, ContainerConfig{})

	if _, err := svc.Create(context.Background(), "c1", ""); err == nil {
		t.Fatal("want error for missing region")
	}
	if _, err := svc.Create(context.Background(), "c1", "IDN"); err == nil {
		t.Fatal("want error for a 3-letter region")
	}
	// The DB check constraint is region ~ '^[A-Z]{2}$'; lowercase must be
	// rejected here rather than surfacing as a driver error.
	if _, err := svc.Create(context.Background(), "c1", "id"); err == nil {
		t.Fatal("want error for a lowercase region")
	}
}

func TestContainerCreateRejectsLongName(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	svc := NewContainerService(store, accounts, nil, ContainerConfig{})

	long := make([]byte, 65)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := svc.Create(context.Background(), string(long), "ID"); err == nil {
		t.Fatal("want error for a name over 64 chars")
	}
}

func TestContainerCreateDuplicateName(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	svc := NewContainerService(store, accounts, nil, ContainerConfig{})

	if _, err := svc.Create(context.Background(), "dupe", "ID"); err != nil {
		t.Fatalf("create 1: %v", err)
	}
	// Simulate the store rejecting a second row with the same name.
	store.duplicateNames["dupe"] = true
	if _, err := svc.Create(context.Background(), "dupe", "ID"); err == nil {
		t.Fatal("want conflict for a duplicate name")
	}
}

func TestContainerListIncludesAccounts(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	packer := NewPacker(store, accounts, PackerConfig{MaxPerContainer: 2})
	svc := NewContainerService(store, accounts, packer, ContainerConfig{})

	w, err := svc.Create(context.Background(), "c1", "ID")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	accounts.seed(accountFixtureWithWorker("a1", domain.PlatformInstagram, w.ID))
	accounts.seed(accountFixtureWithWorker("a2", domain.PlatformThreads, w.ID))

	views, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("len(views) = %d, want 1", len(views))
	}
	if len(views[0].Accounts) != 2 {
		t.Fatalf("accounts = %d, want 2", len(views[0].Accounts))
	}
}

func TestContainerDeleteReleasesAccounts(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	packer := NewPacker(store, accounts, PackerConfig{MaxPerContainer: 2})
	svc := NewContainerService(store, accounts, packer, ContainerConfig{})

	w, err := svc.Create(context.Background(), "c1", "ID")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	accounts.seed(accountFixtureWithWorker("a1", domain.PlatformInstagram, w.ID))

	if err := svc.Delete(context.Background(), w.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := store.workers[w.ID]; ok {
		t.Fatal("container row should be removed")
	}
	if wid := accounts.get("a1").WorkerID; wid != nil {
		t.Fatalf("account still assigned to %s", *wid)
	}
}

func TestContainerDeleteMissing(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	svc := NewContainerService(store, accounts, nil, ContainerConfig{})

	if err := svc.Delete(context.Background(), "ghost"); err == nil {
		t.Fatal("want error for a missing container")
	}
}
