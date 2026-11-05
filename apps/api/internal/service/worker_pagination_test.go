package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// Match the repository's bounded pages; the shared fake otherwise ignores them.
type pagedWorkerStore struct {
	*fakeWorkerStore
	failOffset int
}

func newPagedWorkerStore(count int) *pagedWorkerStore {
	s := &pagedWorkerStore{fakeWorkerStore: newFakeWorkerStore(), failOffset: -1}
	for i := 0; i < count; i++ {
		w := workerFixture(fmt.Sprintf("w-%04d", i), domain.DesiredPaused, 1, nil)
		w.CreatedAt = time.Unix(int64(i), 0)
		s.seed(w)
	}
	return s
}

func (s *pagedWorkerStore) List(ctx context.Context, f port.WorkerFilter) ([]domain.Worker, error) {
	if s.failOffset >= 0 && f.Offset >= s.failOffset {
		return nil, errFake
	}
	rows, err := s.fakeWorkerStore.List(ctx, port.WorkerFilter{})
	if err != nil {
		return nil, err
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	start := max(f.Offset, 0)
	if start >= len(rows) {
		return nil, nil
	}
	return rows[start:min(start+limit, len(rows))], nil
}

func TestContainerListIncludesWorkersBeyondFirstPage(t *testing.T) {
	store := newPagedWorkerStore(511)
	w := store.get("w-0510")
	w.NoVNCService = ptrString("http://localhost:24101")
	store.seed(w)
	accounts := newFakeAccountStore()
	accounts.seed(accountFixtureWithWorker("a-old", domain.PlatformInstagram, w.ID))
	svc := NewContainerService(store, accounts, nil, ContainerConfig{})
	views, err := svc.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 511 {
		t.Fatalf("containers = %d, want all 511", len(views))
	}
	last := views[510]
	if last.ID != w.ID || last.NovncURL != "http://localhost:24101" || len(last.Accounts) != 1 {
		t.Fatalf("old account's live view missing: %+v", last)
	}
}

func TestReconcilerStartsWorkerBeyondFirstPage(t *testing.T) {
	store := newPagedWorkerStore(511)
	w := store.get("w-0510")
	w.DesiredState = domain.DesiredRunning
	store.seed(w)
	driver := newFakeDriver()
	r := NewReconciler(store, driver, nil).WithMaxActive(1)
	if _, err := r.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, state, _ := driver.Observe(context.Background(), w.ID); state != port.ContainerRunning {
		t.Fatal("old worker was never provisioned")
	}
}

func TestOrphanSweeperKeepsWorkerBeyondFirstPage(t *testing.T) {
	store := newPagedWorkerStore(511)
	driver := newFakeDriver()
	driver.running["w-0510"] = 1
	clock := newStepClock()
	s := NewOrphanSweeper(store, driver, SweeperConfig{Clock: clock, Grace: time.Minute})
	if _, err := s.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock.advance(2 * time.Minute)
	deleted, err := s.Sweep(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 0 || driver.deleteCalls != 0 {
		t.Fatalf("valid old worker deleted as orphan: %v", deleted)
	}
}

func TestNovncAllocatorReservesPortsBeyondFirstPage(t *testing.T) {
	store := newPagedWorkerStore(511)
	w := store.get("w-0510")
	w.NoVNCService = ptrString("http://localhost:24101")
	store.seed(w)
	a := NewNovncAllocator(store, "localhost", 24100, 24299)
	if _, err := a.Allocate(context.Background(), ptrInt(24101)); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("old worker's port was offered for reuse: %v", err)
	}
}

func TestElasticPoolPausesIdleWorkerBeyondFirstPage(t *testing.T) {
	store := newPagedWorkerStore(511)
	clock := newStepClock()
	w := store.get("w-0510")
	w.Source = domain.SourceAuto
	w.DesiredState = domain.DesiredRunning
	w.CreatedAt = clock.Now().Add(-time.Hour)
	// Preserve its last-page position in the fake's ascending order.
	for _, other := range store.all() {
		other.CreatedAt = clock.Now().Add(-2 * time.Hour)
		store.seed(other)
	}
	store.seed(w)
	p := NewElasticPool(store, newFakeDriver(), clock, 1, time.Minute, nil)
	if _, err := p.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.get(w.ID).DesiredState != domain.DesiredPaused {
		t.Fatal("idle old worker escaped pool management")
	}
}

func TestOrphanSweeperDoesNotDeleteOnLaterPageFailure(t *testing.T) {
	store := newPagedWorkerStore(511)
	driver := newFakeDriver()
	driver.running["w-0510"] = 1
	clock := newStepClock()
	s := NewOrphanSweeper(store, driver, SweeperConfig{Clock: clock, Grace: time.Minute})
	_, _ = s.Sweep(context.Background())
	clock.advance(2 * time.Minute)
	store.failOffset = 500
	if _, err := s.Sweep(context.Background()); err == nil {
		t.Fatal("incomplete worker inventory must fail the sweep")
	}
	if driver.deleteCalls != 0 {
		t.Fatal("deleted worker after incomplete inventory")
	}
}
