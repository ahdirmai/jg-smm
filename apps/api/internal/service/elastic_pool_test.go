package service

import (
	"context"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
)

// now is the reference instant every pool test reasons from.
var poolNow = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

// poolWorker builds an AUTO worker for the pool tests. secondsAgo is when it
// was last busy (action, else heartbeat); a queue depth overrides idleness.
func poolWorker(id string, desired domain.DesiredState, queueDepth int, secondsAgo time.Duration) domain.Worker {
	seen := poolNow.Add(-secondsAgo)
	return domain.Worker{
		ID:            id,
		Name:          "smm-" + id,
		Source:        domain.SourceAuto,
		DesiredState:  desired,
		QueueDepth:    queueDepth,
		LastActionAt:  &seen,
		LastHeartbeat: &seen,
		Status:        domain.WorkerReady,
		Generation:    1,
		CreatedAt:     poolNow.Add(-24 * time.Hour),
	}
}

// decideMap flattens a decision slice for order-independent assertions.
func decideMap(ds []poolDecision) map[string]poolDecision {
	out := make(map[string]poolDecision, len(ds))
	for _, d := range ds {
		out[d.workerID] = d
	}
	return out
}

func TestElasticPoolRunsAtMostMaxActive(t *testing.T) {
	store := newFakeWorkerStore()
	// Six workers all with a non-empty queue: all want to run, only 3 may.
	for i, id := range []string{"a", "b", "c", "d", "e", "f"} {
		// Spread recency so the ranking is unambiguous: a is freshest.
		store.seed(poolWorker(id, domain.DesiredPaused, 1, time.Duration(i)*time.Minute))
	}
	p := NewElasticPool(store, newFakeDriver(), fixedClock{t: poolNow}, 3, 15*time.Minute, nil)

	got := decideMap(p.Decide(store.all()))
	running := 0
	for _, d := range got {
		if d.run {
			running++
		}
	}
	if running != 3 {
		t.Fatalf("running = %d, want exactly 3", running)
	}
	// The three freshest (a, b, c) win the slots; d, e, f are over-cap.
	for _, want := range []string{"a", "b", "c"} {
		if !got[want].run {
			t.Errorf("%s should run (freshest), got paused: %s", want, got[want].reason)
		}
	}
	for _, want := range []string{"d", "e", "f"} {
		if got[want].run {
			t.Errorf("%s should be over-cap, got run", want)
		}
		if got[want].reason != "over-cap" {
			t.Errorf("%s reason = %q, want over-cap", want, got[want].reason)
		}
	}
}

func TestElasticPoolPausesIdleWorker(t *testing.T) {
	store := newFakeWorkerStore()
	store.seed(poolWorker("idle", domain.DesiredRunning, 0, 30*time.Minute))
	store.seed(poolWorker("busy", domain.DesiredRunning, 0, 1*time.Minute))
	p := NewElasticPool(store, newFakeDriver(), fixedClock{t: poolNow}, 5, 15*time.Minute, nil)

	got := decideMap(p.Decide(store.all()))
	if got["idle"].run {
		t.Errorf("idle worker (30m quiet) should be paused")
	}
	if got["idle"].reason != "idle" {
		t.Errorf("idle reason = %q, want idle", got["idle"].reason)
	}
	if !got["busy"].run {
		t.Errorf("busy worker (acted 1m ago) should keep running")
	}
}

// An empty queue alone must NOT pause a worker: the idle clock starts from the
// last activity, not from the queue draining. Otherwise a worker is killed the
// instant it finishes a job, before the next one in the same burst arrives.
func TestElasticPoolQueueEmptyAloneIsNotIdle(t *testing.T) {
	store := newFakeWorkerStore()
	store.seed(poolWorker("just-finished", domain.DesiredRunning, 0, 10*time.Second))
	p := NewElasticPool(store, newFakeDriver(), fixedClock{t: poolNow}, 5, 15*time.Minute, nil)

	got := decideMap(p.Decide(store.all()))
	if !got["just-finished"].run {
		t.Errorf("worker that acted 10s ago must not be paused yet")
	}
}

// A queued job must keep a worker inside its idle window, even past idleAfter
// as measured from the last action: the queue is fresher evidence than the
// action timestamp.
func TestElasticPoolQueueDepthBeatsIdleWindow(t *testing.T) {
	store := newFakeWorkerStore()
	w := poolWorker("queued", domain.DesiredRunning, 2, 40*time.Minute)
	store.seed(w)
	p := NewElasticPool(store, newFakeDriver(), fixedClock{t: poolNow}, 5, 15*time.Minute, nil)

	got := decideMap(p.Decide(store.all()))
	if !got["queued"].run {
		t.Errorf("worker with queue_depth=2 must keep running (got %s)", got["queued"].reason)
	}
}

// The pool steers only AUTO containers. A MANUAL container is the operator's,
// and a STOPPED one is mid-teardown; touching either would fight the dashboard.
func TestElasticPoolIgnoresManualAndStopped(t *testing.T) {
	store := newFakeWorkerStore()
	manual := poolWorker("manual", domain.DesiredRunning, 0, 99*time.Hour)
	manual.Source = domain.SourceManual
	store.seed(manual)
	stopped := poolWorker("stopped", domain.DesiredStopped, 0, 99*time.Hour)
	store.seed(stopped)
	store.seed(poolWorker("auto", domain.DesiredRunning, 0, 1*time.Minute))

	p := NewElasticPool(store, newFakeDriver(), fixedClock{t: poolNow}, 1, 15*time.Minute, nil)
	got := decideMap(p.Decide(store.all()))

	if _, ok := got["manual"]; ok {
		t.Errorf("MANUAL worker must not be steered by the pool")
	}
	if _, ok := got["stopped"]; ok {
		t.Errorf("STOPPED worker must not be steered by the pool")
	}
	if !got["auto"].run {
		t.Errorf("AUTO worker should run")
	}
}

// Ties must break deterministically on id, or two workers trade the last slot
// every tick and both flap stop/start forever.
func TestElasticPoolTieBreakIsStable(t *testing.T) {
	store := newFakeWorkerStore()
	same := 5 * time.Minute
	store.seed(poolWorker("z", domain.DesiredPaused, 1, same))
	store.seed(poolWorker("a", domain.DesiredPaused, 1, same))
	store.seed(poolWorker("m", domain.DesiredPaused, 1, same))
	p := NewElasticPool(store, newFakeDriver(), fixedClock{t: poolNow}, 1, 15*time.Minute, nil)

	// Repeat: the winner must be identical every time.
	for range 5 {
		got := decideMap(p.Decide(store.all()))
		if !got["a"].run {
			t.Fatalf("tie must break on lowest id (a), run=%v reason=%s", got["a"].run, got["a"].reason)
		}
	}
}

// A worker the pool never hears from ranks by creation time, so a brand-new
// container gets a full idle window instead of being stopped on sight.
func TestElasticPoolNewWorkerGetsIdleWindow(t *testing.T) {
	store := newFakeWorkerStore()
	fresh := domain.Worker{
		ID:           "new",
		Source:       domain.SourceAuto,
		DesiredState: domain.DesiredPaused,
		CreatedAt:    poolNow.Add(-1 * time.Minute), // born 1m ago, never beat
	}
	store.seed(fresh)
	p := NewElasticPool(store, newFakeDriver(), fixedClock{t: poolNow}, 5, 15*time.Minute, nil)

	got := decideMap(p.Decide(store.all()))
	if !got["new"].run {
		t.Errorf("worker created 1m ago must not be paused yet (got %s)", got["new"].reason)
	}
}

func TestElasticPoolDisabledIsNoop(t *testing.T) {
	store := newFakeWorkerStore()
	store.seed(poolWorker("a", domain.DesiredRunning, 0, 99*time.Hour))
	p := NewElasticPool(store, newFakeDriver(), fixedClock{t: poolNow}, 0, 15*time.Minute, nil)

	if p.Enabled() {
		t.Fatal("maxActive=0 must disable the pool")
	}
	if ds := p.Decide(store.all()); ds != nil {
		t.Errorf("disabled pool must decide nothing, got %v", ds)
	}
	n, err := p.Reconcile(context.Background())
	if err != nil || n != 0 {
		t.Errorf("disabled pool Reconcile = (%d, %v), want (0, nil)", n, err)
	}
}

// A nil driver must disable the pool: local dev has no provisioner, and a pool
// that paused workers it could never resume would strand every account.
func TestElasticPoolNilDriverIsDisabled(t *testing.T) {
	store := newFakeWorkerStore()
	p := NewElasticPool(store, nil, fixedClock{t: poolNow}, 5, 15*time.Minute, nil)
	if p.Enabled() {
		t.Fatal("nil driver must disable the pool")
	}
}

// Reconcile persists desired_state for exactly the rows that changed, and is
// idempotent: a second pass over the same fleet writes nothing.
func TestElasticPoolReconcilePersistsAndIsIdempotent(t *testing.T) {
	store := newFakeWorkerStore()
	store.seed(poolWorker("idle", domain.DesiredRunning, 0, 30*time.Minute))
	store.seed(poolWorker("busy", domain.DesiredRunning, 0, 1*time.Minute))
	p := NewElasticPool(store, newFakeDriver(), fixedClock{t: poolNow}, 5, 15*time.Minute, nil)

	n, err := p.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if n != 1 {
		t.Fatalf("first pass reseated %d, want 1 (only the idle one)", n)
	}
	if got := store.get("idle").DesiredState; got != domain.DesiredPaused {
		t.Errorf("idle worker desired = %q, want PAUSED", got)
	}
	if got := store.get("busy").DesiredState; got != domain.DesiredRunning {
		t.Errorf("busy worker desired = %q, want RUNNING (unchanged)", got)
	}

	// Second pass: nothing left to change.
	n, err = p.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile 2: %v", err)
	}
	if n != 0 {
		t.Errorf("second pass reseated %d, want 0 (idempotent)", n)
	}
}

// The pool must never write STOPPED: that deletes the container and its
// session PVC, forcing an interactive re-login. PAUSED keeps the volume.
func TestElasticPoolNeverStops(t *testing.T) {
	store := newFakeWorkerStore()
	store.seed(poolWorker("idle", domain.DesiredRunning, 0, 99*time.Hour))
	p := NewElasticPool(store, newFakeDriver(), fixedClock{t: poolNow}, 1, 15*time.Minute, nil)

	if _, err := p.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got := store.get("idle").DesiredState; got != domain.DesiredPaused {
		t.Fatalf("desired = %q, want PAUSED — STOPPED would delete the session", got)
	}
	if got := store.get("idle").DesiredState; got == domain.DesiredStopped {
		t.Fatal("pool wrote STOPPED, which destroys the session volume")
	}
}

// A worker that frees a slot must be re-seated on the next tick: the pool is a
// queue of intent, not a one-shot assignment.
func TestElasticPoolFreesSlotOnNextTick(t *testing.T) {
	store := newFakeWorkerStore()
	store.seed(poolWorker("a", domain.DesiredPaused, 1, 1*time.Minute))
	store.seed(poolWorker("b", domain.DesiredPaused, 1, 2*time.Minute))
	p := NewElasticPool(store, newFakeDriver(), fixedClock{t: poolNow}, 1, 15*time.Minute, nil)

	if _, err := p.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if store.get("a").DesiredState != domain.DesiredRunning {
		t.Fatalf("a should hold the single slot")
	}
	if store.get("b").DesiredState != domain.DesiredPaused {
		t.Fatalf("b should be paused while over cap")
	}

	// a goes idle; the next tick must hand the slot to b.
	w := store.get("a")
	old := poolNow.Add(-30 * time.Minute)
	w.LastActionAt = &old
	w.LastHeartbeat = &old
	store.seed(w)

	if _, err := p.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile 2: %v", err)
	}
	if store.get("a").DesiredState != domain.DesiredPaused {
		t.Errorf("a went idle, should be paused")
	}
	if store.get("b").DesiredState != domain.DesiredRunning {
		t.Errorf("b should have taken the freed slot")
	}
}
