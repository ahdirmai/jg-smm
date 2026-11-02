package service

import (
	"context"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// fakeDriver is an in-memory port.K8sClient recording every call.
type fakeDriver struct {
	mu              sync.Mutex
	running         map[string]int  // workerID -> generation on the platform (named for the history: not all of these are RUNNING)
	stoppedIDs      map[string]bool // workerIDs Observe reports as not running
	createCalls     int
	deleteCalls     int
	stopCalls       int
	startCalls      int
	failCreate      bool
	failDelete      bool
	failStop        bool
	failStart       bool
	failObserve     bool
	failListRunning bool
	observeCalls    int
}

func newFakeDriver() *fakeDriver {
	return &fakeDriver{running: map[string]int{}, stoppedIDs: map[string]bool{}}
}

func (f *fakeDriver) CreateWorker(ctx context.Context, w domain.Worker) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls++
	if f.failCreate {
		return errFake
	}
	f.running[w.ID] = w.Generation
	delete(f.stoppedIDs, w.ID)
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
	delete(f.stoppedIDs, workerID)
	return nil
}

func (f *fakeDriver) Observe(ctx context.Context, workerID string) (int, port.ContainerState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.observeCalls++
	if f.failObserve {
		return 0, port.ContainerAbsent, errFake
	}
	gen, ok := f.running[workerID]
	if !ok {
		return 0, port.ContainerAbsent, nil
	}
	if f.stoppedIDs[workerID] {
		return gen, port.ContainerStopped, nil
	}
	return gen, port.ContainerRunning, nil
}

func (f *fakeDriver) StopWorker(ctx context.Context, workerID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopCalls++
	if f.failStop {
		return errFake
	}
	f.stoppedIDs[workerID] = true
	return nil
}

func (f *fakeDriver) StartWorker(ctx context.Context, w domain.Worker) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startCalls++
	if f.failStart {
		return errFake
	}
	delete(f.stoppedIDs, w.ID)
	f.running[w.ID] = w.Generation
	return nil
}

func (f *fakeDriver) ListRunning(ctx context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failListRunning {
		return nil, errFake
	}
	// A stopped container still exists but spends no resources, so the cap
	// must not count it — matching the real driver's State filter.
	ids := make([]string, 0, len(f.running))
	for id := range f.running {
		if f.stoppedIDs[id] {
			continue
		}
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
	// onReady simulates a worker process coming up: invoked on Update with
	// status PENDING (a waker/reconciler resume), it writes back READY with a
	// fresh heartbeat — what the real heartbeat POST does after boot.
	onReady func(s *fakeWorkerStore, id string)
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

// all returns every seeded worker, for tests that drive Decide directly
// without the ordering List applies.
func (s *fakeWorkerStore) all() []domain.Worker {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]domain.Worker, 0, len(s.workers))
	for _, w := range s.workers {
		out = append(out, w)
	}
	return out
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

func (s *fakeWorkerStore) GetByContainerID(ctx context.Context, containerID string) (domain.Worker, error) {
	return domain.Worker{}, domain.ErrNotFound
}

func (s *fakeWorkerStore) Claim(ctx context.Context, claim port.WorkerClaim) (domain.Worker, error) {
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
	hook := s.onReady
	s.mu.Unlock()
	// A resume (PENDING write) is followed by the booted process's first
	// heartbeat in the real system; emulate it so awaitReady converges.
	if hook != nil && w.Status == domain.WorkerPending {
		hook(s, w.ID)
	}
	s.mu.Lock()
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
	act := diff(w, 0, port.ContainerAbsent)
	if act.Kind != ActionCreate {
		t.Fatalf("absent+running = %v, want create", act.Kind)
	}
}

func TestDiffRunningStaleGeneration(t *testing.T) {
	w := workerFixture("w1", domain.DesiredRunning, 5, intPtr(3))
	act := diff(w, 3, port.ContainerRunning)
	if act.Kind != ActionCreate {
		t.Fatalf("stale gen = %v, want create", act.Kind)
	}
	if act.Reason != "stale-generation" {
		t.Fatalf("reason = %q, want stale-generation", act.Reason)
	}
}

func TestDiffRunningInSync(t *testing.T) {
	w := workerFixture("w1", domain.DesiredRunning, 5, intPtr(5))
	act := diff(w, 5, port.ContainerRunning)
	if act.Kind != ActionNone {
		t.Fatalf("in-sync = %v, want none", act.Kind)
	}
}

func TestDiffRunningObservedGenNotPersisted(t *testing.T) {
	// Pod runs gen 5 but the row never recorded it: refresh the row.
	w := workerFixture("w1", domain.DesiredRunning, 5, nil)
	act := diff(w, 5, port.ContainerRunning)
	if act.Kind != ActionRefresh {
		t.Fatalf("unpersisted observed_gen = %v, want refresh", act.Kind)
	}
}

func TestDiffStoppedRunning(t *testing.T) {
	w := workerFixture("w1", domain.DesiredStopped, 5, intPtr(5))
	act := diff(w, 5, port.ContainerRunning)
	if act.Kind != ActionDelete {
		t.Fatalf("stopped+running = %v, want delete", act.Kind)
	}
}

func TestDiffStoppedAbsent(t *testing.T) {
	w := workerFixture("w1", domain.DesiredStopped, 5, nil)
	act := diff(w, 0, port.ContainerAbsent)
	if act.Kind != ActionNone {
		t.Fatalf("stopped+absent = %v, want none", act.Kind)
	}
}

func TestDiffUnknownDesiredState(t *testing.T) {
	w := workerFixture("w1", domain.DesiredState("ZZZ"), 1, nil)
	act := diff(w, 0, port.ContainerAbsent)
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

// StopWorker / StartWorker live with the fake above: a stop records "exists
// but not running" (stoppedIDs) exactly like the real Docker driver, and a
// start clears it. Tests that exercise the PAUSED branch need that
// distinction, not a delete.

// PAUSED must STOP the container, never delete it. The distinction is the whole
// point of the state: a delete destroys the volume the session lives on, so a
// resume would cost a fresh login — exactly what the pause was meant to avoid.
func TestPausedStopsButDoesNotDelete(t *testing.T) {
	driver := newFakeDriver()
	driver.running["w1"] = 1 // exists and running
	store := newFakeWorkerStore()
	store.seed(workerFixture("w1", domain.DesiredPaused, 1, intPtr(1)))
	r := NewReconciler(store, driver, nil)

	applied, err := r.ReconcileAll(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if applied != 1 {
		t.Fatalf("applied = %d, want 1 (a stop)", applied)
	}
	if driver.deleteCalls != 0 {
		t.Fatalf("deleteCalls = %d, want 0 (PAUSED must never delete)", driver.deleteCalls)
	}
	if driver.stopCalls != 1 {
		t.Fatalf("stopCalls = %d, want 1", driver.stopCalls)
	}
	if _, exists := driver.running["w1"]; !exists {
		t.Fatal("container was removed; PAUSED must keep it so a resume is cheap")
	}
}

// A paused worker with no container gets one created, so a later resume has
// something to start. Staying absent would leave desired=PAUSED with nothing.
// The steady state that broke the fleet before ContainerState existed: a
// paused worker whose container is already stopped is IN SYNC. Reconciling it
// must do nothing — the old diff issued a stop (and its DB write) for every
// paused worker on every tick, forever.
func TestPausedAndStoppedIsInSync(t *testing.T) {
	driver := newFakeDriver()
	driver.running["w1"] = 1
	driver.stoppedIDs["w1"] = true
	store := newFakeWorkerStore()
	store.seed(workerFixture("w1", domain.DesiredPaused, 1, intPtr(1)))
	r := NewReconciler(store, driver, nil)

	applied, err := r.ReconcileAll(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if applied != 0 {
		t.Fatalf("applied = %d, want 0 (paused+stopped is in sync)", applied)
	}
	if driver.stopCalls != 0 || driver.observeCalls != 1 {
		t.Fatalf("stopCalls = %d, observeCalls = %d, want 0 stops after exactly one observe", driver.stopCalls, driver.observeCalls)
	}
}

// A paused worker with no container is IN SYNC, not a create. Materialising it
// "so a later resume has something to start" booted a Chromium per paused row:
// the packer seeds the fleet as AUTO/PAUSED, so the first tick after a restart
// started every account at once and OOM-killed the host.
func TestPausedAbsentDoesNotCreate(t *testing.T) {
	driver := newFakeDriver() // nothing running
	store := newFakeWorkerStore()
	store.seed(workerFixture("w1", domain.DesiredPaused, 1, intPtr(1)))
	r := NewReconciler(store, driver, nil)

	applied, err := r.ReconcileAll(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if applied != 0 {
		t.Fatalf("applied = %d, want 0", applied)
	}
	if driver.createCalls != 0 {
		t.Fatalf("createCalls = %d, want 0", driver.createCalls)
	}
}

// The cap counts containers on the platform, not RUNNING rows, so a create the
// pool cannot see is still refused once the ceiling is reached.
func TestReconcileCapRefusesCreateAtCeiling(t *testing.T) {
	driver := newFakeDriver()
	// Two containers already up on the platform: the ceiling of 2 is spent.
	driver.running["other-a"] = 1
	driver.running["other-b"] = 1
	store := newFakeWorkerStore()
	store.seed(workerFixture("w1", domain.DesiredRunning, 1, intPtr(1)))
	r := NewReconciler(store, driver, nil).WithMaxActive(2)

	applied, err := r.ReconcileAll(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if applied != 0 {
		t.Fatalf("applied = %d, want 0 (deferred)", applied)
	}
	if driver.createCalls != 0 {
		t.Fatalf("createCalls = %d, want 0", driver.createCalls)
	}
}

// A slot freeing lets the deferred create through on the next tick: the row
// keeps its RUNNING intent and is retried, never dropped.
func TestReconcileCapAdmitsAfterSlotFrees(t *testing.T) {
	driver := newFakeDriver()
	// Two running containers against a ceiling of two: the fleet is full, so the
	// create must wait. Deleting one below frees the only slot.
	driver.running["other-a"] = 1
	driver.running["other-b"] = 1
	store := newFakeWorkerStore()
	store.seed(workerFixture("w1", domain.DesiredRunning, 1, intPtr(1)))
	r := NewReconciler(store, driver, nil).WithMaxActive(2)

	if _, err := r.ReconcileAll(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if driver.createCalls != 0 {
		t.Fatalf("createCalls = %d, want 0 while at ceiling", driver.createCalls)
	}

	delete(driver.running, "other-a")
	if _, err := r.ReconcileAll(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if driver.createCalls != 1 {
		t.Fatalf("createCalls = %d, want 1 once a slot freed", driver.createCalls)
	}
}

// An unreadable running count is not permission: over-admitting is exactly what
// took the host down, so the create waits a tick instead.
func TestReconcileCapFailsClosedWhenCountFails(t *testing.T) {
	driver := newFakeDriver()
	driver.failListRunning = true
	store := newFakeWorkerStore()
	store.seed(workerFixture("w1", domain.DesiredRunning, 1, intPtr(1)))
	r := NewReconciler(store, driver, nil).WithMaxActive(5)

	if _, err := r.ReconcileAll(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if driver.createCalls != 0 {
		t.Fatalf("createCalls = %d, want 0", driver.createCalls)
	}
}

// The cap holds within ONE pass: five RUNNING rows against a ceiling of 3 start
// exactly 3 containers, not all 5. This is the regression that took the host
// down — every row was admitted because nothing counted the pass's own work.
func TestReconcileCapHoldsWithinOnePass(t *testing.T) {
	driver := newFakeDriver() // nothing running anywhere
	store := newFakeWorkerStore()
	for _, id := range []string{"w1", "w2", "w3", "w4", "w5"} {
		store.seed(workerFixture(id, domain.DesiredRunning, 1, intPtr(1)))
	}
	r := NewReconciler(store, driver, nil).WithMaxActive(3)

	applied, err := r.ReconcileAll(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if driver.createCalls != 3 {
		t.Fatalf("createCalls = %d, want 3 (the ceiling, not the row count)", driver.createCalls)
	}
	if applied != 3 {
		t.Fatalf("applied = %d, want 3", applied)
	}
}

// A resume spends a slot like a create does.
func TestReconcileCapRefusesResumeAtCeiling(t *testing.T) {
	driver := newFakeDriver()
	driver.running["other-a"] = 1
	driver.running["other-b"] = 1
	driver.stoppedIDs["w1"] = true
	store := newFakeWorkerStore()
	store.seed(workerFixture("w1", domain.DesiredRunning, 1, intPtr(1)))
	r := NewReconciler(store, driver, nil).WithMaxActive(2)

	if _, err := r.ReconcileAll(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if driver.startCalls != 0 {
		t.Fatalf("startCalls = %d, want 0", driver.startCalls)
	}
}

// A RUNNING intent on a paused container resumes it.
func TestWakerResumesPausedWorker(t *testing.T) {
	driver := newFakeDriver()
	driver.running["w1"] = 0 // present, stopped
	store := newFakeWorkerStore()
	// A fresh heartbeat + READY is what awaitReady waits for; without it the
	// waker would correctly block, and the test would sit out the full timeout.
	store.seed(readyFixture(workerFixture("w1", domain.DesiredPaused, 1, intPtr(1))))
	// The resume writes PENDING; the booted container's first heartbeat writes
	// READY back. The store replays that transition.
	store.onReady = func(s *fakeWorkerStore, id string) {
		row := s.get(id)
		fresh := readyFixture(row)
		s.mu.Lock()
		s.workers[id] = fresh
		s.mu.Unlock()
	}
	w := NewWaker(store, driver, nil)

	if err := w.Wake(context.Background(), "w1"); err != nil {
		t.Fatalf("wake: %v", err)
	}
	if driver.startCalls != 1 {
		t.Fatalf("startCalls = %d, want 1", driver.startCalls)
	}
	if got := store.get("w1").DesiredState; got != domain.DesiredRunning {
		t.Errorf("desired state = %q, want RUNNING (a resumed worker must not be paused again next tick)", got)
	}
}

// Waking a worker that is already RUNNING must not restart it.
func TestWakerLeavesRunningWorkerAlone(t *testing.T) {
	driver := newFakeDriver()
	store := newFakeWorkerStore()
	store.seed(workerFixture("w1", domain.DesiredRunning, 1, intPtr(1)))
	w := NewWaker(store, driver, nil)

	if err := w.Wake(context.Background(), "w1"); err != nil {
		t.Fatalf("wake: %v", err)
	}
	if driver.startCalls != 0 {
		t.Fatalf("startCalls = %d, want 0", driver.startCalls)
	}
}

// A STOPPED worker was deliberately torn down. Waking it would undo that.
func TestWakerDoesNotResurrectStoppedWorker(t *testing.T) {
	driver := newFakeDriver()
	store := newFakeWorkerStore()
	store.seed(workerFixture("w1", domain.DesiredStopped, 1, intPtr(1)))
	w := NewWaker(store, driver, nil)

	if err := w.Wake(context.Background(), "w1"); err != nil {
		t.Fatalf("wake: %v", err)
	}
	if driver.startCalls != 0 {
		t.Fatalf("startCalls = %d, want 0 (STOPPED means delete, not pause)", driver.startCalls)
	}
}

// readyFixture marks a worker as freshly heartbeating, the state awaitReady
// waits for after a resume.
func readyFixture(w domain.Worker) domain.Worker {
	now := time.Now()
	w.Status = domain.WorkerReady
	w.LastHeartbeat = &now
	return w
}
