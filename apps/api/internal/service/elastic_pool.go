package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// ElasticPool keeps the number of RUNNING worker containers at or below a cap,
// and pauses a worker once it has been idle long enough.
//
// WHY THIS EXISTS
// A worker container holds a headful Chromium (plus Xvfb, x11vnc and noVNC) for
// as long as it runs: ~85 MiB idle and up to ~460 MiB measured after a session,
// with a 4 GiB limit for the spike. On a small host that is the binding
// resource, so a fleet sized for "how many accounts exist" cannot all run at
// once. But most of that fleet is idle most of the time: the hot path is about
// one action per minute per container (../../../../docs/INFRA_ANALYST.md §2.3).
//
// A PAUSED container costs 0 bytes of RAM (verified: `docker stats` reports
// `0B / 0B` for an Exited container) and resumes in ~3-5 seconds (measured:
// `docker start` 0.39s + entrypoint + node boot to "control subscriber ready"
// 0.37s). So the pool trades a few seconds of latency on the first action after
// an idle spell for a hard ceiling on resident memory.
//
// HOW IT STEERS
// It does NOT call the driver. It writes desired_state, the same lever the
// dashboard and the waker use, and the reconciler does the actual
// start/stop. That keeps one control path for provisioning: the pool decides
// WHAT should run, the reconciler decides HOW to make it so.
//
//	desired_state = RUNNING  -> reconciler ensures the container exists
//	desired_state = PAUSED   -> reconciler stops it, keeping volume + session
//
// It never writes STOPPED: that deletes the container *and its session PVC*,
// which would force an interactive re-login. Pausing deliberately keeps the
// volume so a resume is a `docker start`, not a `runLogin`.
type ElasticPool struct {
	workers port.WorkerStore
	driver  port.K8sClient
	logger  *slog.Logger
	clock   port.Clock
	// stream fans desired-state flips out to dashboards. Optional (nil OK):
	// without it the UI only learns of a pause on its next refresh.
	stream port.StreamPublisher

	// maxActive caps concurrently RUNNING pool-managed workers. 0 disables the
	// pool: Reconcile returns immediately and every worker keeps the
	// desired_state it was given by the dashboard or the waker.
	maxActive int
	// idleAfter is how long a worker must have an empty queue and no recent
	// action before it is paused. Long enough that a burst of actions on one
	// account does not flap the container stop/start between jobs.
	idleAfter time.Duration
}

// NewElasticPool wires the pool. A maxActive of 0 or less disables it, and a
// nil driver makes Apply a no-op: local dev and tests must keep working without
// a provisioner to start or stop anything.
func NewElasticPool(workers port.WorkerStore, driver port.K8sClient, clock port.Clock, maxActive int, idleAfter time.Duration, logger *slog.Logger) *ElasticPool {
	return NewElasticPoolWithStream(workers, driver, clock, nil, maxActive, idleAfter, logger)
}

// NewElasticPoolWithStream is NewElasticPool plus an SSE publisher so the
// dashboard sees a pause/resume the tick it happens instead of on refresh.
func NewElasticPoolWithStream(workers port.WorkerStore, driver port.K8sClient, clock port.Clock, stream port.StreamPublisher, maxActive int, idleAfter time.Duration, logger *slog.Logger) *ElasticPool {
	if logger == nil {
		logger = slog.Default()
	}
	if clock == nil {
		clock = systemClock{}
	}
	return &ElasticPool{
		workers:   workers,
		driver:    driver,
		clock:     clock,
		stream:    stream,
		maxActive: maxActive,
		idleAfter: idleAfter,
		logger:    logger,
	}
}

// Enabled reports whether the pool will do anything. Callers use it to skip
// wiring the loop entirely.
func (p *ElasticPool) Enabled() bool { return p != nil && p.maxActive > 0 && p.driver != nil }

// poolDecision is what the pool wants done with one worker this tick.
type poolDecision struct {
	workerID string
	run      bool // true = should be RUNNING, false = should be PAUSED
	reason   string
}

// Decide returns the desired run-state for every worker, given the fleet as it
// stands. Pure and side-effect free, so the policy is exhaustively unit-testable
// without a driver or a database.
//
// The rules, in order:
//
//  1. A worker the pool does not own is never touched. Ownership is
//     "source=AUTO and desired_state in {RUNNING,PAUSED}" — a MANUAL container
//     is the operator's, and a STOPPED one is already being torn down.
//  2. A worker with an empty queue and no action inside the idle window is
//     paused. This is the reclaim path, and it runs first so a quiet worker
//     frees its slot for one that is about to need it.
//  3. Among workers that want to run (fresh queue or recent action), the newest
//     activity wins, up to maxActive. The rest are paused even though they have
//     work: their job stays in Redis and is served when a slot frees.
//
// Recency uses last_action_at when present, else last_heartbeat, so a worker
// that has never acted is ranked by when it last reported in rather than
// sinking to the bottom forever.
func (p *ElasticPool) Decide(w []domain.Worker) []poolDecision {
	if p.maxActive <= 0 {
		return nil
	}
	now := p.clock.Now()

	// Split the fleet: workers the pool manages, ranked by how recently they
	// were busy.
	var managed []poolCandidate
	for _, wk := range w {
		if !p.owns(wk) {
			continue
		}
		lastSeen := workerLastSeen(wk)
		idle := wk.QueueDepth == 0 && now.Sub(lastSeen) >= p.idleAfter
		managed = append(managed, poolCandidate{w: wk, lastSeen: lastSeen, idle: idle})
	}

	// Rank busiest-first. Ties break on worker id so the choice is stable
	// across ticks (a non-deterministic order would make two workers trade the
	// last slot every tick, flapping both).
	sortManaged(managed)

	decisions := make([]poolDecision, 0, len(managed))
	active := 0
	for _, c := range managed {
		switch {
		case c.idle:
			decisions = append(decisions, poolDecision{c.w.ID, false, "idle"})
		case active < p.maxActive:
			decisions = append(decisions, poolDecision{c.w.ID, true, "has-work"})
			active++
		default:
			// Over the cap: pause even though the queue is not empty. The job
			// is durable in Redis and will be picked up when a slot frees.
			decisions = append(decisions, poolDecision{c.w.ID, false, "over-cap"})
		}
	}
	return decisions
}

// Reconcile is the per-tick body: read the fleet, decide, and persist only the
// changes. It returns how many workers it flipped, for the caller's log line.
//
// Writing desired_state is all it does — the reconciler performs the actual
// start/stop on its own tick. That split is deliberate: the pool is a policy
// loop over rows, the reconciler is the only thing that talks to the driver, so
// a driver outage cannot leave the pool half-applied.
func (p *ElasticPool) Reconcile(ctx context.Context) (int, error) {
	if !p.Enabled() {
		return 0, nil
	}
	workers, err := p.workers.List(ctx, port.WorkerFilter{Limit: poolListLimit})
	if err != nil {
		return 0, err
	}
	decisions := p.Decide(workers)

	byID := make(map[string]domain.Worker, len(workers))
	for _, w := range workers {
		byID[w.ID] = w
	}

	changed := 0
	for _, d := range decisions {
		w, ok := byID[d.workerID]
		if !ok {
			continue
		}
		want := domain.DesiredPaused
		if d.run {
			want = domain.DesiredRunning
		}
		if w.DesiredState == want {
			continue
		}
		w.DesiredState = want
		// Keep status truthful in the same write: a paused worker is not READY
		// READY, it is IDLE — the READY badge was a stale heartbeat and made
		// every paused worker look live on the dashboard.
		if want == domain.DesiredPaused {
			w.Status = domain.WorkerIdle
		} else if w.Status == domain.WorkerIdle {
			// Resuming: mark booting so the UI shows it coming up until the
			// first heartbeat of the new process reports READY.
			w.Status = domain.WorkerPending
		}
		if _, err := p.workers.Update(ctx, w); err != nil {
			// One row failing must not abandon the rest of the pass: the next
			// tick re-decides, and the policy is idempotent.
			p.logger.Warn("elastic pool: persist desired_state failed",
				"workerId", w.ID, "want", want, "reason", d.reason, "err", err)
			continue
		}
		changed++
		// A partial frame is enough: the browser upserts it onto the card it
		// already has (ADR 0010), and the badge reads desiredState first.
		if p.stream != nil {
			frame, err := json.Marshal(map[string]string{"id": w.ID, "desiredState": string(want)})
			if err == nil {
				p.stream.Publish(ctx, port.EventProvisionUpdated, frame)
			}
		}
		p.logger.Info("elastic pool: worker reseated",
			"workerId", w.ID, "name", w.Name, "desired", want, "reason", d.reason)
	}
	return changed, nil
}

// Run ticks Reconcile until ctx is cancelled. interval <= 0 is a programming
// error (main.go must leave the loop off instead).
func (p *ElasticPool) Run(ctx context.Context, interval time.Duration) {
	if !p.Enabled() {
		p.logger.Info("elastic pool disabled")
		return
	}
	if interval <= 0 {
		p.logger.Warn("elastic pool disabled: interval not set")
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	p.logger.Info("elastic pool started",
		"maxActive", p.maxActive, "idleAfter", p.idleAfter.String(), "interval", interval.String())
	for {
		select {
		case <-ctx.Done():
			p.logger.Info("elastic pool stopped")
			return
		case <-ticker.C:
			changed, err := p.Reconcile(ctx)
			if err != nil {
				p.logger.Error("elastic pool tick failed", "err", err)
				continue
			}
			if changed > 0 {
				p.logger.Info("elastic pool tick complete", "reseated", changed)
			}
		}
	}
}

// poolListLimit bounds one pass. The pool must see EVERY worker to rank them
// correctly, so unlike the dashboard's page it must not be silently truncated:
// pageBounds clamps anything above 500 down to 100, which would hide the oldest
// workers from the ranking entirely. 500 is that ceiling.
const poolListLimit = 500

// owns reports whether the pool may steer this worker. AUTO containers are
// pool-owned by construction (the packer spawns them PAUSED precisely so the
// pool can decide). MANUAL containers belong to the operator, and STOPPED ones
// are mid-teardown: steering either would fight the dashboard.
func (p *ElasticPool) owns(w domain.Worker) bool {
	if w.Source != domain.SourceAuto {
		return false
	}
	return w.DesiredState == domain.DesiredRunning || w.DesiredState == domain.DesiredPaused
}

// workerLastSeen is the most recent evidence of activity: the last action when
// there is one, else the last heartbeat. Either is proof the worker was doing
// something recently enough that stopping it would be premature.
func workerLastSeen(w domain.Worker) time.Time {
	if w.LastActionAt != nil {
		return *w.LastActionAt
	}
	if w.LastHeartbeat != nil {
		return *w.LastHeartbeat
	}
	// No evidence at all: treat the creation time as the last activity, so a
	// brand-new worker gets a full idle window before it is paused rather than
	// being stopped the instant it appears.
	return w.CreatedAt
}

// sortManaged orders busiest-first: more recent lastSeen first, ties by worker
// id for determinism. Insertion sort — this runs on a small fleet slice once
// per tick, and a stable order matters more than asymptotics: an unstable order
// would make two workers trade the last slot every tick, flapping both
// containers stop/start.
func sortManaged(c []poolCandidate) {
	for i := 1; i < len(c); i++ {
		for j := i; j > 0 && lessBusy(c[j], c[j-1]); j-- {
			c[j], c[j-1] = c[j-1], c[j]
		}
	}
}

// lessBusy reports whether a should be preferred over b for a running slot:
// more recent activity wins, and an exact tie breaks on the lower worker id.
func lessBusy(a, b poolCandidate) bool {
	if a.lastSeen.Equal(b.lastSeen) {
		return a.w.ID < b.w.ID
	}
	return a.lastSeen.After(b.lastSeen)
}

// poolCandidate is one pool-managed worker with the evidence the ranking needs.
type poolCandidate struct {
	w        domain.Worker
	lastSeen time.Time
	idle     bool
}
