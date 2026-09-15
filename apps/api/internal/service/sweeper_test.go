package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/port"
)

// stepClock is a controllable clock for the sweeper's grace window.
type stepClock struct {
	mu  sync.Mutex
	now time.Time
}

func newStepClock() *stepClock {
	return &stepClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *stepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *stepClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

var _ port.Clock = (*stepClock)(nil)

// memLogStore is a tiny provision-log store for audit assertions.
type memLogStore struct {
	mu   sync.Mutex
	logs []domain.ProvisionLog
}

func newMemLogStore() *memLogStore { return &memLogStore{} }

func (s *memLogStore) Append(_ context.Context, l domain.ProvisionLog) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logs = append(s.logs, l)
	return nil
}

func (s *memLogStore) ListByWorker(_ context.Context, workerID string, limit int) ([]domain.ProvisionLog, error) {
	return nil, nil
}

func (s *memLogStore) snapshot() []domain.ProvisionLog {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]domain.ProvisionLog, len(s.logs))
	copy(out, s.logs)
	return out
}

// ---- LoggingDriver ----

func TestLoggingDriverRecordsCreateAndDelete(t *testing.T) {
	driver := newFakeDriver()
	logs := newMemLogStore()
	clock := newStepClock()
	ld := NewLoggingDriver(driver, logs, clock, nil)

	ctx := context.Background()
	w := workerFixture("w1", domain.DesiredRunning, 3, nil)
	if err := ld.CreateWorker(ctx, w); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := ld.DeleteWorker(ctx, "w1"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	entries := logs.snapshot()
	if len(entries) != 2 {
		t.Fatalf("log entries = %d, want 2", len(entries))
	}
	if entries[0].Op != domain.OpCreate || entries[0].Status != domain.ProvisionApplied || entries[0].Generation != 3 {
		t.Fatalf("create entry wrong: %+v", entries[0])
	}
	if entries[0].TS != clock.Now() {
		t.Fatalf("entry TS not from the injected clock: %v", entries[0].TS)
	}
	if entries[1].Op != domain.OpDelete || entries[1].Status != domain.ProvisionApplied {
		t.Fatalf("delete entry wrong: %+v", entries[1])
	}
}

func TestLoggingDriverRecordsFailures(t *testing.T) {
	driver := newFakeDriver()
	logs := newMemLogStore()
	ld := NewLoggingDriver(driver, logs, newStepClock(), nil)

	driver.failCreate = true
	w := workerFixture("w1", domain.DesiredRunning, 3, nil)
	// The failure must still surface to the caller...
	if err := ld.CreateWorker(context.Background(), w); err == nil {
		t.Fatal("want the driver error to propagate")
	}
	// ...and be recorded as FAILED.
	entries := logs.snapshot()
	if len(entries) != 1 || entries[0].Status != domain.ProvisionFailed || entries[0].Error == nil {
		t.Fatalf("failure not recorded: %+v", entries)
	}
}

func TestLoggingDriverPassesThroughReads(t *testing.T) {
	driver := newFakeDriver()
	logs := newMemLogStore()
	ld := NewLoggingDriver(driver, logs, newStepClock(), nil)

	ctx := context.Background()
	driver.running["w1"] = 4
	gen, exists, err := ld.Observe(ctx, "w1")
	if err != nil || !exists || gen != 4 {
		t.Fatalf("observe passthrough = (%d,%v,%v), want (4,true,nil)", gen, exists, err)
	}
	ids, err := ld.ListRunning(ctx)
	if err != nil || len(ids) != 1 || ids[0] != "w1" {
		t.Fatalf("listRunning passthrough = %v, want [w1]", ids)
	}
	if len(logs.snapshot()) != 0 {
		t.Fatalf("reads must not write audit rows, got %d", len(logs.snapshot()))
	}
}

func TestLoggingDriverWithoutLogStore(t *testing.T) {
	ld := NewLoggingDriver(newFakeDriver(), nil, nil, nil)
	w := workerFixture("w1", domain.DesiredRunning, 1, nil)
	if err := ld.CreateWorker(context.Background(), w); err != nil {
		t.Fatalf("create: %v", err)
	}
}

// ---- OrphanSweeper ----

func TestSweeperDeletesOrphanAfterGrace(t *testing.T) {
	store := newFakeWorkerStore()
	driver := newFakeDriver()
	clock := newStepClock()
	s := NewOrphanSweeper(store, driver, SweeperConfig{Grace: 60 * time.Second, Clock: clock})

	ctx := context.Background()
	driver.running["orphan"] = 1

	// First sweep: the orphan is observed but not deleted yet.
	deleted, err := s.Sweep(ctx)
	if err != nil {
		t.Fatalf("sweep 1: %v", err)
	}
	if len(deleted) != 0 {
		t.Fatalf("sweep 1 deleted %v, want nothing inside grace", deleted)
	}
	if _, ok := driver.running["orphan"]; !ok {
		t.Fatal("orphan deleted inside the grace window")
	}

	// Still inside grace.
	clock.advance(30 * time.Second)
	if deleted, _ := s.Sweep(ctx); len(deleted) != 0 {
		t.Fatalf("sweep 2 deleted %v, want nothing inside grace", deleted)
	}

	// Past grace: the orphan is reaped.
	clock.advance(31 * time.Second)
	deleted, err = s.Sweep(ctx)
	if err != nil {
		t.Fatalf("sweep 3: %v", err)
	}
	if len(deleted) != 1 || deleted[0] != "orphan" {
		t.Fatalf("sweep 3 deleted %v, want [orphan]", deleted)
	}
	if _, ok := driver.running["orphan"]; ok {
		t.Fatal("orphan still running after sweep")
	}
}

func TestSweeperKeepsWorkersWithRows(t *testing.T) {
	store := newFakeWorkerStore()
	driver := newFakeDriver()
	clock := newStepClock()
	s := NewOrphanSweeper(store, driver, SweeperConfig{Grace: time.Second, Clock: clock})

	ctx := context.Background()
	store.seed(workerFixture("w1", domain.DesiredRunning, 1, nil))
	driver.running["w1"] = 1

	clock.advance(2 * time.Second)
	deleted, err := s.Sweep(ctx)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(deleted) != 0 {
		t.Fatalf("deleted %v, want nothing: the row exists", deleted)
	}
	if _, ok := driver.running["w1"]; !ok {
		t.Fatal("a worker with a row must never be reaped")
	}
}

func TestSweeperDeletesOnlyOldOrphans(t *testing.T) {
	store := newFakeWorkerStore()
	driver := newFakeDriver()
	clock := newStepClock()
	s := NewOrphanSweeper(store, driver, SweeperConfig{Grace: 60 * time.Second, Clock: clock})

	ctx := context.Background()
	driver.running["old"] = 1

	s.Sweep(ctx) // old observed at t=0
	clock.advance(50 * time.Second)
	driver.running["new"] = 1       // a fresh orphan, still mid-grace at t=50
	clock.advance(15 * time.Second) // old is 65s old, new is 15s old

	deleted, err := s.Sweep(ctx)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(deleted) != 1 || deleted[0] != "old" {
		t.Fatalf("deleted %v, want only [old]", deleted)
	}
	if _, ok := driver.running["new"]; !ok {
		t.Fatal("the newer orphan must survive its grace window")
	}
}

func TestSweeperNoRunningIsEmptyAndClearsState(t *testing.T) {
	store := newFakeWorkerStore()
	driver := newFakeDriver()
	clock := newStepClock()
	s := NewOrphanSweeper(store, driver, SweeperConfig{Grace: time.Second, Clock: clock})

	ctx := context.Background()
	driver.running["ghost"] = 1
	s.Sweep(ctx) // observed
	if _, ok := s.firstSeen["ghost"]; !ok {
		t.Fatal("orphan should be tracked")
	}

	// Nothing on the platform: state resets so a recreate is not instantly reaped.
	driver.running = map[string]int{}
	deleted, err := s.Sweep(ctx)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(deleted) != 0 {
		t.Fatalf("deleted %v, want nothing", deleted)
	}
	if _, ok := s.firstSeen["ghost"]; ok {
		t.Fatal("firstSeen should reset when the platform is empty")
	}
}

func TestSweeperGraceDefaultsTo60Seconds(t *testing.T) {
	s := NewOrphanSweeper(newFakeWorkerStore(), newFakeDriver(), SweeperConfig{})
	if s.grace != 60*time.Second {
		t.Fatalf("default grace = %v, want 60s", s.grace)
	}
}

func TestSweeperListRunningFailure(t *testing.T) {
	store := newFakeWorkerStore()
	driver := newFakeDriver()
	clock := newStepClock()
	s := NewOrphanSweeper(store, driver, SweeperConfig{Grace: time.Second, Clock: clock})

	driver.failListRunning = true
	if _, err := s.Sweep(context.Background()); err == nil {
		t.Fatal("want error when ListRunning fails")
	}
}

func TestSweeperListWorkersFailure(t *testing.T) {
	store := newFakeWorkerStore()
	driver := newFakeDriver()
	clock := newStepClock()
	s := NewOrphanSweeper(store, driver, SweeperConfig{Grace: time.Second, Clock: clock})

	ctx := context.Background()
	driver.running["orphan"] = 1
	store.failList = true
	if _, err := s.Sweep(ctx); err == nil {
		t.Fatal("want error when the worker list fails")
	}
}
