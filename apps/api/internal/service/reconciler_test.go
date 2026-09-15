package service

import (
	"context"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/port"
)

// fakeDriver is an in-memory port.K8sClient recording every call.
type fakeDriver struct {
	mu              sync.Mutex
	running         map[string]int // workerID -> running generation
	createCalls     int
	deleteCalls     int
	failCreate      bool
	failDelete      bool
	failObserve     bool
	failListRunning bool
	observeCalls    int
}

func newFakeDriver() *fakeDriver {
	return &fakeDriver{running: map[string]int{}}
}

func (f *fakeDriver) CreateWorker(ctx context.Context, w domain.Worker) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls++
	if f.failCreate {
		return errFake
	}
	f.running[w.ID] = w.Generation
	return nil
}

func (f *fakeDriver) DeleteWorker(ctx context.Context, workerID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCalls++
	if f.failDelete {
		return errFake
	}
	delete(f.running, workerID)
	return nil
}

func (f *fakeDriver) Observe(ctx context.Context, workerID string) (int, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.observeCalls++
	if f.failObserve {
		return 0, false, errFake
	}
	gen, ok := f.running[workerID]
	return gen, ok, nil
}

func (f *fakeDriver) ListRunning(ctx context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failListRunning {
		return nil, errFake
	}
	ids := make([]string, 0, len(f.running))
	for id := range f.running {
		ids = append(ids, id)
	}
	return ids, nil
}

// fakeWorkerStore is a minimal port.WorkerStore for the reconciler.
type fakeWorkerStore struct {
	mu             sync.Mutex
	workers        map[string]domain.Worker
	failList       bool
	failUpd        bool
	duplicateNames map[string]bool
}

func newFakeWorkerStore() *fakeWorkerStore {
	return &fakeWorkerStore{workers: map[string]domain.Worker{}, duplicateNames: map[string]bool{}}
}

func (s *fakeWorkerStore) seed(w domain.Worker) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workers[w.ID] = w
}

func (s *fakeWorkerStore) get(id string) domain.Worker {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.workers[id]
}

func (s *fakeWorkerStore) GetByID(ctx context.Context, id string) (domain.Worker, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.workers[id]
	if !ok {
		return domain.Worker{}, domain.ErrNotFound
	}
	return w, nil
}

func (s *fakeWorkerStore) GetByName(ctx context.Context, name string) (domain.Worker, error) {
	return domain.Worker{}, domain.ErrNotFound
}

func (s *fakeWorkerStore) List(ctx context.Context, f port.WorkerFilter) ([]domain.Worker, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failList {
		return nil, errFake
	}
	// Deterministic created-at order so packing picks the oldest container
	// first (stable across map iteration).
	var out []domain.Worker
	for _, w := range s.workers {
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (s *fakeWorkerStore) Create(ctx context.Context, w domain.Worker) (domain.Worker, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if w.ID == "" {
		w.ID = "auto-" + w.Name
	}
	if s.duplicateNames[w.Name] {
		return domain.Worker{}, domain.ErrConflict
	}
	s.workers[w.ID] = w
	return w, nil
}

func (s *fakeWorkerStore) Update(ctx context.Context, w domain.Worker) (domain.Worker, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failUpd {
		return domain.Worker{}, errFake
	}
	if _, ok := s.workers[w.ID]; !ok {
		return domain.Worker{}, domain.ErrNotFound
	}
	s.workers[w.ID] = w
	return w, nil
}

func (s *fakeWorkerStore) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.workers, id)
	return nil
}

func (s *fakeWorkerStore) RecordHeartbeat(ctx context.Context, hb domain.Heartbeat, snap port.WorkerSnapshot) error {
	return nil
}

var errFake = context.Canceled

func workerFixture(id string, desired domain.DesiredState, gen int, observed *int) domain.Worker {
	n, _ := strconv.Atoi(id[1:])
	return domain.Worker{
		ID:           id,
		Name:         "worker-" + id,
		DesiredState: desired,
		Source:       domain.SourceManual,
		Region:       "ID",
		Status:       domain.WorkerPending,
		Generation:   gen,
		ObservedGen:  observed,
		CreatedAt:    time.Date(2026, 1, 1, 0, n, 0, 0, time.UTC),
	}
}

func intPtr(n int) *int { return &n }

func accountFixture(id string, platform domain.Platform) domain.Account {
	return domain.Account{
		ID:         id,
		Platform:   platform,
		Username:   "user-" + id,
		AuthStatus: domain.AuthAuthenticating,
		Status:     domain.AccountActive,
	}
}

func accountFixtureWithWorker(id string, platform domain.Platform, workerID string) domain.Account {
	a := accountFixture(id, platform)
	a.WorkerID = &workerID
	return a
}

func workerFixtureWithSource(id string, source domain.WorkerSource, desired domain.DesiredState, gen int, observed *int) domain.Worker {
	w := workerFixture(id, desired, gen, observed)
	w.Source = source
	return w
}

// ---- diff unit tests (pure, no side effects) ----

func TestDiffRunningAbsent(t *testing.T) {
	w := workerFixture("w1", domain.DesiredRunning, 3, nil)
	act := diff(w, 0, false)
	if act.Kind != ActionCreate {
		t.Fatalf("absent+running = %v, want create", act.Kind)
	}
}

func TestDiffRunningStaleGeneration(t *testing.T) {
	w := workerFixture("w1", domain.DesiredRunning, 5, intPtr(3))
	act := diff(w, 3, true)
	if act.Kind != ActionCreate {
		t.Fatalf("stale gen = %v, want create", act.Kind)
	}
	if act.Reason != "stale-generation" {
		t.Fatalf("reason = %q, want stale-generation", act.Reason)
	}
}

func TestDiffRunningInSync(t *testing.T) {
	w := workerFixture("w1", domain.DesiredRunning, 5, intPtr(5))
	act := diff(w, 5, true)
	if act.Kind != ActionNone {
		t.Fatalf("in-sync = %v, want none", act.Kind)
	}
}

func TestDiffRunningObservedGenNotPersisted(t *testing.T) {
	// Pod runs gen 5 but the row never recorded it: refresh the row.
	w := workerFixture("w1", domain.DesiredRunning, 5, nil)
	act := diff(w, 5, true)
	if act.Kind != ActionRefresh {
		t.Fatalf("unpersisted observed_gen = %v, want refresh", act.Kind)
	}
}

func TestDiffStoppedRunning(t *testing.T) {
	w := workerFixture("w1", domain.DesiredStopped, 5, intPtr(5))
	act := diff(w, 5, true)
	if act.Kind != ActionDelete {
		t.Fatalf("stopped+running = %v, want delete", act.Kind)
	}
}

func TestDiffStoppedAbsent(t *testing.T) {
	w := workerFixture("w1", domain.DesiredStopped, 5, nil)
	act := diff(w, 0, false)
	if act.Kind != ActionNone {
		t.Fatalf("stopped+absent = %v, want none", act.Kind)
	}
}

func TestDiffUnknownDesiredState(t *testing.T) {
	w := workerFixture("w1", domain.DesiredState("ZZZ"), 1, nil)
	act := diff(w, 0, false)
	if act.Kind != ActionNone {
		t.Fatalf("unknown desired = %v, want none", act.Kind)
	}
}

// ---- reconcileOne integration-style tests ----

func TestReconcileCreatesAbsentWorker(t *testing.T) {
	store := newFakeWorkerStore()
	driver := newFakeDriver()
	store.seed(workerFixture("w1", domain.DesiredRunning, 2, nil))

	r := NewReconciler(store, driver, nil)
	ctx := context.Background()
	applied, err := r.ReconcileAll(ctx)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if applied != 1 {
		t.Fatalf("applied = %d, want 1", applied)
	}
	if driver.createCalls != 1 {
		t.Fatalf("createCalls = %d, want 1", driver.createCalls)
	}
	if gen := store.get("w1").ObservedGen; gen == nil || *gen != 2 {
		t.Fatalf("observed_gen = %v, want 2", gen)
	}
}

func TestReconcileIsIdempotent(t *testing.T) {
	store := newFakeWorkerStore()
	driver := newFakeDriver()
	store.seed(workerFixture("w1", domain.DesiredRunning, 2, nil))

	r := NewReconciler(store, driver, nil)
	ctx := context.Background()

	if _, err := r.ReconcileAll(ctx); err != nil {
		t.Fatalf("reconcile 1: %v", err)
	}
	if _, err := r.ReconcileAll(ctx); err != nil {
		t.Fatalf("reconcile 2: %v", err)
	}
	if _, err := r.ReconcileAll(ctx); err != nil {
		t.Fatalf("reconcile 3: %v", err)
	}

	if driver.createCalls != 1 {
		t.Fatalf("createCalls = %d, want 1 (idempotent)", driver.createCalls)
	}
	if driver.observeCalls != 3 {
		t.Fatalf("observeCalls = %d, want 3", driver.observeCalls)
	}
}

func TestReconcileReplacesStaleGeneration(t *testing.T) {
	store := newFakeWorkerStore()
	driver := newFakeDriver()
	// Row is at gen 5, but a stale gen-2 pod is what the driver sees.
	store.seed(workerFixture("w1", domain.DesiredRunning, 5, intPtr(2)))
	driver.running["w1"] = 2

	r := NewReconciler(store, driver, nil)
	applied, err := r.ReconcileAll(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if applied != 1 {
		t.Fatalf("applied = %d, want 1", applied)
	}
	if driver.running["w1"] != 5 {
		t.Fatalf("running gen = %d, want 5 (stale replaced)", driver.running["w1"])
	}
	if gen := store.get("w1").ObservedGen; gen == nil || *gen != 5 {
		t.Fatalf("observed_gen = %v, want 5", gen)
	}
}

func TestReconcileDeletesStoppedWorker(t *testing.T) {
	store := newFakeWorkerStore()
	driver := newFakeDriver()
	store.seed(workerFixture("w1", domain.DesiredStopped, 2, intPtr(2)))
	driver.running["w1"] = 2

	r := NewReconciler(store, driver, nil)
	applied, err := r.ReconcileAll(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if applied != 1 {
		t.Fatalf("applied = %d, want 1", applied)
	}
	if _, ok := driver.running["w1"]; ok {
		t.Fatalf("worker still running after delete")
	}
	if gen := store.get("w1").ObservedGen; gen == nil || *gen != 0 {
		t.Fatalf("observed_gen = %v, want 0 after delete", gen)
	}
}

// TestReconcileDeleteIsIdempotent proves a delete followed by a re-tick does
// not keep hammering the driver.
func TestReconcileDeleteIsIdempotent(t *testing.T) {
	store := newFakeWorkerStore()
	driver := newFakeDriver()
	store.seed(workerFixture("w1", domain.DesiredStopped, 2, intPtr(2)))
	driver.running["w1"] = 2

	r := NewReconciler(store, driver, nil)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := r.ReconcileAll(ctx); err != nil {
			t.Fatalf("reconcile %d: %v", i, err)
		}
	}
	if driver.deleteCalls != 1 {
		t.Fatalf("deleteCalls = %d, want 1", driver.deleteCalls)
	}
}

// TestReconcileRetriesAfterFailure: a failed create is retried on the next
// tick and nothing is marked applied until it succeeds.
func TestReconcileRetriesAfterFailure(t *testing.T) {
	store := newFakeWorkerStore()
	driver := newFakeDriver()
	store.seed(workerFixture("w1", domain.DesiredRunning, 2, nil))

	r := NewReconciler(store, driver, nil)
	ctx := context.Background()

	driver.failCreate = true
	applied, err := r.ReconcileAll(ctx)
	if err != nil {
		t.Fatalf("reconcile should not return the driver error: %v", err)
	}
	if applied != 0 {
		t.Fatalf("applied = %d, want 0 on failure", applied)
	}
	if gen := store.get("w1").ObservedGen; gen != nil {
		t.Fatalf("observed_gen set despite failed create: %v", gen)
	}

	driver.failCreate = false
	applied, err = r.ReconcileAll(ctx)
	if err != nil {
		t.Fatalf("reconcile retry: %v", err)
	}
	if applied != 1 {
		t.Fatalf("applied = %d, want 1 after retry", applied)
	}
	if gen := store.get("w1").ObservedGen; gen == nil || *gen != 2 {
		t.Fatalf("observed_gen = %v, want 2 after retry", gen)
	}
}

// TestReconcileObserveFailureIsSkipped proves one bad worker never aborts the
// rest of the fleet.
func TestReconcileObserveFailureIsSkipped(t *testing.T) {
	store := newFakeWorkerStore()
	driver := newFakeDriver()
	store.seed(workerFixture("w1", domain.DesiredRunning, 2, nil))
	store.seed(workerFixture("w2", domain.DesiredRunning, 3, nil))

	r := NewReconciler(store, driver, nil)
	driver.failObserve = true
	applied, err := r.ReconcileAll(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if applied != 0 {
		t.Fatalf("applied = %d, want 0 when every observe fails", applied)
	}

	driver.failObserve = false
	applied, err = r.ReconcileAll(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if applied != 2 {
		t.Fatalf("applied = %d, want 2", applied)
	}
}

func TestReconcileCancelledContext(t *testing.T) {
	store := newFakeWorkerStore()
	driver := newFakeDriver()
	store.seed(workerFixture("w1", domain.DesiredRunning, 2, nil))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	r := NewReconciler(store, driver, nil)
	if _, err := r.ReconcileAll(ctx); err != context.Canceled {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestReconcileListFailure(t *testing.T) {
	store := newFakeWorkerStore()
	driver := newFakeDriver()
	store.failList = true
	r := NewReconciler(store, driver, nil)
	if _, err := r.ReconcileAll(context.Background()); err == nil {
		t.Fatalf("expected list error to surface")
	}
}

// TestReconcileMarkObservedFailure: a failed observed_gen write must not undo
// the driver op or mark it applied.
func TestReconcileMarkObservedFailure(t *testing.T) {
	store := newFakeWorkerStore()
	driver := newFakeDriver()
	store.seed(workerFixture("w1", domain.DesiredRunning, 2, nil))
	store.failUpd = true

	r := NewReconciler(store, driver, nil)
	applied, err := r.ReconcileAll(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if applied != 1 {
		t.Fatalf("applied = %d, want 1 (driver op succeeded)", applied)
	}
	if driver.running["w1"] != 2 {
		t.Fatalf("driver state lost: %d", driver.running["w1"])
	}
	// Next tick observes gen 2 running: refresh-only, no duplicate create.
	store.failUpd = false
	if _, err := r.ReconcileAll(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if driver.createCalls != 1 {
		t.Fatalf("createCalls = %d, want 1", driver.createCalls)
	}
}
