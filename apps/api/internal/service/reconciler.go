package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// Reconciler drives Worker.DesiredState toward the container platform. It is a
// pure diff-and-apply loop over port.K8sClient + port.WorkerStore: the desired
// side is the Worker row, the actual side is what the driver observes.
//
// Invariants (P1-04):
//   - desired=RUNNING & absent           -> CreateWorker
//   - desired=RUNNING & gen stale        -> CreateWorker (driver replaces the
//     stale pod; k8s ensurePod deletes it)
//   - desired=STOPPED & exists           -> DeleteWorker
//   - observed_gen is refreshed only on  a successful apply.
//
// Every op is idempotent: retrying a half-applied pass converges to the same
// end state without duplicate resources.
type Reconciler struct {
	workers port.WorkerStore
	driver  port.K8sClient
	logger  *slog.Logger
	// maxActive is a hard ceiling on containers this pass may leave running. It
	// is the backstop the elastic pool cannot be: the pool is a policy over
	// ROWS (it writes desired_state), so any path that creates a container
	// without a RUNNING row — a bug, a manual flip, a stale generation — bypasses
	// it entirely. 0 disables the cap (dev, tests, single-worker setups).
	maxActive int
	// counted/running are the per-pass capacity ledger: running is the count
	// taken once when the pass first needs it, and each admitted create or
	// resume increments it. Reset by ReconcileAll.
	counted bool
	running int
}

// NewReconciler wires the loop. workers and driver are required; the reconciler
// owns no goroutine — Run ticks it (main.go decides whether to start one).
func NewReconciler(workers port.WorkerStore, driver port.K8sClient, logger *slog.Logger) *Reconciler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Reconciler{workers: workers, driver: driver, logger: logger}
}

// WithMaxActive sets the hard ceiling on concurrently running containers and
// returns the reconciler for chaining. n <= 0 leaves the cap off.
//
// Deferred work is never lost: a create skipped for the cap stays absent on the
// platform while its row keeps its RUNNING intent, so the next tick retries it
// as soon as a slot frees.
func (r *Reconciler) WithMaxActive(n int) *Reconciler {
	if n < 0 {
		n = 0
	}
	r.maxActive = n
	return r
}

// createPermitted reports whether one more container may be started now. It
// counts what is actually running on the platform rather than what the rows
// claim, because the platform is the resource being spent. A failing count is
// NOT permission: over-admitting is what took the host down.
func (r *Reconciler) createPermitted(ctx context.Context) bool {
	if r.maxActive <= 0 {
		return true
	}
	// One exact count per pass. Already-admitted containers from earlier in this
	// same pass are the difference between "on the platform" and "clicked
	// Apply": a driver whose list lags its own writes would otherwise admit the
	// whole fleet in one tick.
	if !r.counted {
		ids, err := r.driver.ListRunning(ctx)
		if err != nil {
			r.logger.Warn("reconcile: running count unavailable; skipping creates", "err", err)
			return false
		}
		r.running = len(ids)
		r.counted = true
	}
	return r.running < r.maxActive
}

// Action is one reconciler decision, returned by diff for testability.
type Action struct {
	WorkerID string
	Kind     ActionKind
	Reason   string
}

// ActionKind enumerates the reconciler's moves.
type ActionKind string

const (
	ActionCreate  ActionKind = "create"  // provision at the current generation
	ActionDelete  ActionKind = "delete"  // tear down all resources
	ActionStart   ActionKind = "start"   // resume a stopped-but-existing container
	ActionStop    ActionKind = "stop"    // halt a container, keeping it and its volumes
	ActionRefresh ActionKind = "refresh" // observed_gen is stale on the row
	ActionNone    ActionKind = "none"    // already in sync
)

// diff decides what to do for one worker given the observed container state.
// It has no side effects, so it is safe to unit-test exhaustively.
//
// observedGen is the generation the driver reports on the platform;
// state==ContainerAbsent means nothing exists.
func diff(w domain.Worker, observedGen int, state port.ContainerState) Action {
	switch w.DesiredState {
	case domain.DesiredRunning:
		switch {
		case state == port.ContainerAbsent:
			return Action{WorkerID: w.ID, Kind: ActionCreate, Reason: "absent"}
		case observedGen < w.Generation:
			return Action{WorkerID: w.ID, Kind: ActionCreate, Reason: "stale-generation"}
		case state == port.ContainerStopped:
			// The container exists but is not running (leftPaused by the pool or
			// a crash) while the operator now wants it RUNNING: resume it.
			return Action{WorkerID: w.ID, Kind: ActionStart, Reason: "stopped-but-wanted-running"}
		case staleObservedGen(w):
			return Action{WorkerID: w.ID, Kind: ActionRefresh, Reason: "observed-gen-not-persisted"}
		default:
			return Action{WorkerID: w.ID, Kind: ActionNone, Reason: "in-sync"}
		}
	case domain.DesiredPaused:
		// A paused worker must NOT be running. The stopped state is exactly what
		// PAUSED wants, so it is in sync — stopping an already stopped container
		// every tick was the fleet's steady-state churn.
		//
		// An ABSENT container is in sync too. Creating it so "a later resume has
		// something to start" booted a full Chromium per paused row: the packer
		// seeds the whole fleet as AUTO/PAUSED, so the first tick after a restart
		// materialised every account at once (~460 MiB each) and OOM-killed the
		// host. Promotion materialises the container when someone actually needs
		// it — Waker.Wake calls CreateWorker, which is idempotent per generation.
		if state == port.ContainerAbsent {
			return Action{WorkerID: w.ID, Kind: ActionNone, Reason: "paused-and-absent"}
		}
		if observedGen < w.Generation {
			// Mid-rollout: finish the rollout, then pause next tick.
			return Action{WorkerID: w.ID, Kind: ActionCreate, Reason: "stale-generation"}
		}
		if state == port.ContainerRunning {
			return Action{WorkerID: w.ID, Kind: ActionStop, Reason: "paused-but-running"}
		}
		return Action{WorkerID: w.ID, Kind: ActionNone, Reason: "paused-and-stopped"}
	case domain.DesiredStopped:
		if state != port.ContainerAbsent {
			return Action{WorkerID: w.ID, Kind: ActionDelete, Reason: "stopped-but-present"}
		}
		return Action{WorkerID: w.ID, Kind: ActionNone, Reason: "stopped-and-absent"}
	default:
		// Unknown desired state: never act, never crash the loop.
		return Action{WorkerID: w.ID, Kind: ActionNone, Reason: "unknown-desired-state"}
	}
}

// staleObservedGen reports whether the persisted observed_gen lags the
// generation actually running. The reconciler refreshes the row so a future
// diff is a no-op.
func staleObservedGen(w domain.Worker) bool {
	if w.ObservedGen == nil {
		return true
	}
	return *w.ObservedGen != w.Generation
}

// ReconcileAll scans every worker and applies the diff. It is the per-tick body
// of Run; calling it directly is safe too. A failure on one worker never aborts
// the rest — the next tick retries it.
func (r *Reconciler) ReconcileAll(ctx context.Context) (int, error) {
	workers, err := listAllWorkers(ctx, r.workers)
	if err != nil {
		return 0, err
	}
	// A new pass recounts the platform: the previous pass's ledger describes a
	// fleet that has since started and stopped containers of its own.
	r.counted, r.running = false, 0
	applied := 0
	for _, w := range workers {
		if ctx.Err() != nil {
			return applied, ctx.Err()
		}
		if r.reconcileOne(ctx, w) {
			applied++
		}
	}
	return applied, nil
}

// reconcileOne observes + diffs + applies for a single worker. It returns true
// when a driver op (create/delete/start/stop) was executed successfully.
func (r *Reconciler) reconcileOne(ctx context.Context, w domain.Worker) bool {
	observedGen, state, err := r.driver.Observe(ctx, w.ID)
	if err != nil {
		r.logger.Error("reconcile: observe failed", "workerId", w.ID, "err", err)
		return false
	}

	act := diff(w, observedGen, state)
	switch act.Kind {
	case ActionNone:
		return false
	case ActionCreate:
		if !r.createPermitted(ctx) {
			r.logger.Info("reconcile: at capacity, deferring", "workerId", w.ID, "maxActive", r.maxActive)
			return false
		}
		r.running++ // spent before the call: a failed create still cost a slot this pass
		if err := r.driver.CreateWorker(ctx, w); err != nil {
			r.running--
			r.logger.Error("reconcile: create failed", "workerId", w.ID, "generation", w.Generation, "err", err)
			return false
		}
		r.logger.Info("reconcile: created", "workerId", w.ID, "generation", w.Generation)
		r.markObserved(ctx, w, w.Generation)
		return true
	case ActionStart:
		// A resume spends the same slot a create does, so it is capped the same
		// way: the row keeps its RUNNING intent and the next tick starts it once
		// a slot frees.
		if !r.createPermitted(ctx) {
			r.logger.Info("reconcile: at capacity, deferring resume", "workerId", w.ID, "maxActive", r.maxActive)
			return false
		}
		r.running++
		if err := r.driver.StartWorker(ctx, w); err != nil {
			r.running--
			r.logger.Error("reconcile: start failed", "workerId", w.ID, "err", err)
			return false
		}
		r.logger.Info("reconcile: started", "workerId", w.ID)
		// A starting container is PENDING, not its dead process's READY.
		if w.Status == domain.WorkerIdle || w.Status == domain.WorkerReady {
			w.Status = domain.WorkerPending
		}
		r.markObserved(ctx, w, w.Generation)
		return true
	case ActionStop:
		if err := r.driver.StopWorker(ctx, w.ID); err != nil {
			r.logger.Error("reconcile: stop failed", "workerId", w.ID, "err", err)
			return false
		}
		r.logger.Info("reconcile: paused", "workerId", w.ID)
		// A stop frees its slot for a later worker in this pass. Only when the
		// count is already known: with no count there is nothing to adjust.
		if r.counted && r.running > 0 {
			r.running--
		}
		// The row must not keep saying READY: that was the last heartbeat of a
		// process that is now stopped. IDLE is the truthful runtime state of a
		// paused worker (resume is cheap; nothing is running).
		if w.Status == domain.WorkerReady || w.Status == domain.WorkerBusy {
			w.Status = domain.WorkerIdle
		}
		// observed_gen stays: the container still exists at this generation,
		// and clearing it would make a later resume look stale.
		r.markObserved(ctx, w, w.Generation)
		return true
	case ActionDelete:
		if err := r.driver.DeleteWorker(ctx, w.ID); err != nil {
			r.logger.Error("reconcile: delete failed", "workerId", w.ID, "err", err)
			return false
		}
		r.logger.Info("reconcile: deleted", "workerId", w.ID)
		if r.counted && r.running > 0 {
			r.running--
		}
		r.markObserved(ctx, w, 0)
		return true
	case ActionRefresh:
		r.markObserved(ctx, w, observedGen)
		return false
	default:
		return false
	}
}

// markObserved persists observed_gen so the next diff for this worker is a
// no-op. A delete records 0: the row stays (it is still the desired-state
// record) but the reconciler stops deleting on every tick. Failure to persist
// is not fatal — it only makes the next tick redo the (idempotent) op.
//
// A row already carrying the value is left alone: the reconciler ticks every
// RECONCILE_INTERVAL_SECONDS and a paused fleet would otherwise take one DB
// write per paused worker per tick to record an unchanged number.
func (r *Reconciler) markObserved(ctx context.Context, w domain.Worker, gen int) {
	if w.ObservedGen != nil && *w.ObservedGen == gen {
		return
	}
	next := w
	next.ObservedGen = &gen
	updated, err := r.workers.Update(ctx, next)
	if err != nil {
		r.logger.Warn("reconcile: persist observed_gen failed", "workerId", w.ID, "err", err)
		return
	}
	w.ObservedGen = updated.ObservedGen
}

// Run ticks ReconcileAll until ctx is cancelled. interval <= 0 is a programming
// error (main.go must leave the reconciler off instead).
func (r *Reconciler) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		r.logger.Warn("reconciler disabled: interval not set")
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	r.logger.Info("reconciler started", "interval", interval.String())
	for {
		select {
		case <-ctx.Done():
			r.logger.Info("reconciler stopped")
			return
		case <-ticker.C:
			applied, err := r.ReconcileAll(ctx)
			if err != nil {
				r.logger.Error("reconcile tick failed", "err", err)
				continue
			}
			if applied > 0 {
				r.logger.Info("reconcile tick complete", "applied", applied)
			}
		}
	}
}
