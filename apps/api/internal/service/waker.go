package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// WakeTimeout bounds how long a caller waits for a paused container to come
// back before giving up and reporting the work as unavailable. A resume is a
// Docker start (seconds) but the host is not always yours: 45s proved too
// short while another process starved the VM (Blender at 370% CPU pushed a
// 5s boot past a minute and login requests died as 503), so 120s keeps login
// working on a loaded host without pretending a dead daemon will recover.
const WakeTimeout = 120 * time.Second

// readyHeartbeatAge is how fresh a heartbeat must be to prove the worker is
// alive NOW rather than showing a READY status left over from before its pause.
// Two missed beats at the worker's interval is the same tolerance the
// reconciler uses to call a worker unhealthy.
const readyHeartbeatAge = 30 * time.Second

// readyPollInterval is how often awaitReady re-reads the row. The worker boots
// in ~5s, so this trades a little latency for not hammering the DB.
const readyPollInterval = 500 * time.Millisecond

// ErrWakeTimeout is returned when a resumed container does not become ready in
// time. Callers surface it as "unavailable" rather than retrying blindly.
var ErrWakeTimeout = errors.New("worker did not become ready")

// Waker resumes a PAUSED workspace before work that needs it.
//
// WHY THIS IS ONE TYPE
// Three independent triggers need the same behaviour — an operator login, an
// action job being enqueued, and a dashboard opening the live view. Putting the
// logic in each caller would be three chances to skip the "is it already
// running?" check and start a container per click, and three copies of the
// same subtle wait-for-ready loop.
//
// A waker with a nil driver (or nil driver on a build without a provisioner) is
// a no-op that reports success: local/dev setups must keep working without
// pretending they can wake something.
type Waker struct {
	workers port.WorkerStore
	driver  port.K8sClient
	logger  *slog.Logger
}

// NewWaker wires the waker. driver may be nil; see the type comment.
func NewWaker(workers port.WorkerStore, driver port.K8sClient, logger *slog.Logger) *Waker {
	if logger == nil {
		logger = slog.Default()
	}
	return &Waker{workers: workers, driver: driver, logger: logger}
}

// Wake makes sure the worker is running and reachable before the caller uses
// it. It returns nil when the worker is already running, when it was resumed,
// or when there is no provisioner to resume with.
//
// It deliberately does NOT wait for a worker heartbeat: the caller's next step
// (a control publish, or a Redis enqueue the worker drains when it starts) is
// already durable, so the container joining late is a delay, not a lost job.
func (w *Waker) Wake(ctx context.Context, workerID string) error {
	if w.driver == nil || workerID == "" {
		return nil
	}
	wk, err := w.workers.GetByID(ctx, workerID)
	if err != nil {
		return fmt.Errorf("waker: get worker: %w", err)
	}
	// Only PAUSED is woken. A RUNNING worker needs nothing, and a STOPPED one
	// was a deliberate teardown — resurrecting it would undo an operator's
	// intent to delete.
	if wk.DesiredState != domain.DesiredPaused {
		return nil
	}
	// Stamped BEFORE the start: a heartbeat must be newer than the moment the
	// container was asked to boot to prove the NEW process is up. Taking it
	// after the call would let a heartbeat written during the start be accepted.
	wokeAt := time.Now()
	if err := w.driver.StartWorker(ctx, wk); err != nil {
		return fmt.Errorf("waker: start worker %s: %w", workerID, err)
	}
	// Flip the intent BEFORE returning, so a concurrent trigger does not start
	// the same container twice and the reconciler does not immediately pause it
	// again on the next tick.
	wk.DesiredState = domain.DesiredRunning
	// Status follows: the old process's READY is dead. PENDING says "booting"
	// until the fresh heartbeat arrives; the dashboard shows it as starting.
	wk.Status = domain.WorkerPending
	if _, err := w.workers.Update(ctx, wk); err != nil {
		// The container IS running now; a failed row write is a bookkeeping
		// lag the reconciler corrects, not a reason to fail the work.
		w.logger.Warn("waker: resume ran but worker row not updated", "workerId", workerID, "err", err)
	}
	// Wait for the worker to actually be UP before returning.
	//
	// A control message goes over Redis Pub/Sub, which has no delivery
	// guarantee: a worker that has not subscribed yet simply never sees it.
	// Starting the container and publishing immediately — what this did first
	// — lost every login dispatched onto a just-resumed worker, because the
	// worker takes ~5s to boot and only subscribes at the end. The dashboard
	// then sat on AUTHENTICATING until the 180s poll timed out.
	//
	// The heartbeat is the readiness signal: the container flips to READY and
	// refreshes last_heartbeat once its control subscriber is up.
	// Only a heartbeat stamped AFTER this wake proves the new process is up.
	// A container that was paused seconds ago still carries a fresh-looking
	// heartbeat from before the pause, and accepting it published the control
	// message into a Redis channel nobody had subscribed to yet — the login was
	// silently dropped and the account sat AUTHENTICATING until the stale sweep.
	// Observed: a woken worker showed a black screen and no control message.
	if err := w.awaitReadySince(ctx, workerID, wokeAt); err != nil {
		return err
	}
	w.logger.Info("waker: worker resumed", "workerId", workerID, "name", wk.Name)
	return nil
}

// awaitReady blocks until the worker reports READY with a fresh heartbeat.
//
// Health, not status: `status` alone can be a stale READY from before the
// pause, so the heartbeat age is what proves the process that is up now is the
// one we started. Polling (not a wake-on-event) because there is no event to
// subscribe to for "this specific worker just became ready".
func (w *Waker) awaitReady(ctx context.Context, workerID string) error {
	return w.awaitReadySince(ctx, workerID, time.Time{})
}

// awaitReadySince is awaitReady with a floor: the heartbeat must be newer than
// since (zero means "no floor", the pre-wake behaviour).
func (w *Waker) awaitReadySince(ctx context.Context, workerID string, since time.Time) error {
	deadline := time.Now().Add(WakeTimeout)
	for {
		wk, err := w.workers.GetByID(ctx, workerID)
		if err != nil {
			return fmt.Errorf("waker: re-read worker: %w", err)
		}
		if wk.Status == domain.WorkerReady || wk.Status == domain.WorkerIdle {
			if wk.LastHeartbeat != nil && time.Since(*wk.LastHeartbeat) < readyHeartbeatAge &&
				(since.IsZero() || wk.LastHeartbeat.After(since)) {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%w: worker %s did not become ready in %s", ErrWakeTimeout, workerID, WakeTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(readyPollInterval):
		}
	}
}
