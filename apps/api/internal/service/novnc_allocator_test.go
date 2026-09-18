package service

import (
	"context"
	"errors"
	"testing"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
)

// TestNovncAllocatorSharesPorts covers the accounts-audit BLOCKER: the AUTO
// packer path and the manual container API used to compute ports separately,
// so an auto-spawn could take the port a manual create was about to bind. Both
// now go through one allocator, and a taken port is refused.
func TestNovncAllocatorSharesPorts(t *testing.T) {
	store := newFakeWorkerStore()
	alloc := NewNovncAllocator(store, "localhost", 24100, 24101)

	// Manual path takes the first free port.
	manual, err := alloc.Allocate(context.Background(), nil)
	if err != nil {
		t.Fatalf("allocate manual: %v", err)
	}
	if manual == nil || *manual != "http://localhost:24100" {
		t.Fatalf("manual = %v, want http://localhost:24100", manual)
	}
	store.seed(domain.Worker{ID: "w1", NoVNCService: manual})

	// AUTO path must see the manual port as taken, not re-pick it.
	auto, err := alloc.Allocate(context.Background(), nil)
	if err != nil {
		t.Fatalf("allocate auto: %v", err)
	}
	if *auto != "http://localhost:24101" {
		t.Fatalf("auto = %v, want the next free port 24101", *auto)
	}

	// An explicit request for a held port is a conflict, not a silent reuse.
	if _, err := alloc.Allocate(context.Background(), ptrInt(24100)); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("want conflict on taken port 24100, got %v", err)
	}
	// Out of range is a validation error so the caller gets a 400, not a 409.
	if _, err := alloc.Allocate(context.Background(), ptrInt(9999)); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("want validation error for port 9999, got %v", err)
	}
}

// TestNovncAllocatorAutoCreate covers the packer BLOCKER: createWorker never
// set NoVNCService, so an AUTO card had no live-view link. With the allocator
// wired, an auto-spawn gets a real URL; an unallocated port does not collide
// with the manual path.
func TestNovncAllocatorAutoCreate(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	alloc := NewNovncAllocator(store, "localhost", 24100, 24199)
	packer := NewPacker(store, accounts, PackerConfig{
		MaxPerContainer: 2,
		AutoCreate:      true,
		Novnc:           alloc,
	})

	// A packed account with no free container triggers the AUTO spawn.
	accounts.seed(domain.Account{ID: "a1", Platform: domain.PlatformInstagram, Status: domain.AccountActive})
	_, w, err := packer.Pack(context.Background(), "a1", domain.PlatformInstagram)
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	if w.NoVNCService == nil || *w.NoVNCService != "http://localhost:24100" {
		t.Fatalf("auto worker novnc = %v, want http://localhost:24100", w.NoVNCService)
	}
}

// A disabled range yields nil, not an error: the dashboard renders a disabled
// button instead of failing the create.
func TestNovncAllocatorDisabled(t *testing.T) {
	alloc := NewNovncAllocator(newFakeWorkerStore(), "localhost", 0, 0)
	url, err := alloc.Allocate(context.Background(), nil)
	if err != nil {
		t.Fatalf("allocate with range unset: %v", err)
	}
	if url != nil {
		t.Fatalf("want nil URL when the range is unset, got %q", *url)
	}
}

// Create with no allocator wired must still insert the row (the static tier has
// no live view), not nil-panic.
func TestContainerCreateWithoutAllocator(t *testing.T) {
	store := newFakeWorkerStore()
	svc := NewContainerService(store, newFakeAccountStore(), nil, ContainerConfig{})
	w, err := svc.Create(context.Background(), "c1", "ID", "Jakarta", nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if w.NoVNCService != nil {
		t.Fatalf("want no live-view URL, got %q", *w.NoVNCService)
	}
}

func ptrInt(i int) *int { return &i }
