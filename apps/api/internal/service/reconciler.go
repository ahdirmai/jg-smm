package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/port"
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
}

// NewReconciler wires the loop. workers and driver are required; the reconciler
// owns no goroutine — Run ticks it (main.go decides whether to start one).
func NewReconciler(workers port.WorkerStore, driver port.K8sClient, logger *slog.Logger) *Reconciler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Reconciler{workers: workers, driver: driver, logger: logger}
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
	ActionRefresh ActionKind = "refresh" // observed_gen is stale on the row
	ActionNone    ActionKind = "none"    // already in sync
)

// diff decides what to do for one worker given the observed generation. It has
// no side effects, so it is safe to unit-test exhaustively.
//
// observedGen is the generation the driver reports running; 0 with exists=false
// means nothing exists.
func diff(w domain.Worker, observedGen int, exists bool) Action {
	switch w.DesiredState {
	case domain.DesiredRunning:
		switch {
		case !exists:
			return Action{WorkerID: w.ID, Kind: ActionCreate, Reason: "absent"}
		case observedGen < w.Generation:
			return Action{WorkerID: w.ID, Kind: ActionCreate, Reason: "stale-generation"}
		case staleObservedGen(w):
			return Action{WorkerID: w.ID, Kind: ActionRefresh, Reason: "observed-gen-not-persisted"}
		default:
			return Action{WorkerID: w.ID, Kind: ActionNone, Reason: "in-sync"}
		}
	case domain.DesiredStopped:
		if exists {
			return Action{WorkerID: w.ID, Kind: ActionDelete, Reason: "stopped-but-running"}
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
	workers, err := r.workers.List(ctx, port.WorkerFilter{Limit: 500})
	if err != nil {
		return 0, err
	}
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
// when a driver op (create/delete) was executed successfully.
func (r *Reconciler) reconcileOne(ctx context.Context, w domain.Worker) bool {
	observedGen, exists, err := r.driver.Observe(ctx, w.ID)
	if err != nil {
		r.logger.Error("reconcile: observe failed", "workerId", w.ID, "err", err)
		return false
	}

	act := diff(w, observedGen, exists)
	switch act.Kind {
	case ActionNone:
		return false
	case ActionCreate:
		if err := r.driver.CreateWorker(ctx, w); err != nil {
			r.logger.Error("reconcile: create failed", "workerId", w.ID, "generation", w.Generation, "err", err)
			return false
		}
		r.logger.Info("reconcile: created", "workerId", w.ID, "generation", w.Generation)
		r.markObserved(ctx, w, w.Generation)
		return true
	case ActionDelete:
		if err := r.driver.DeleteWorker(ctx, w.ID); err != nil {
			r.logger.Error("reconcile: delete failed", "workerId", w.ID, "err", err)
			return false
		}
		r.logger.Info("reconcile: deleted", "workerId", w.ID)
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
func (r *Reconciler) markObserved(ctx context.Context, w domain.Worker, gen int) {
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
