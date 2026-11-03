package adapter

import (
	"context"
	"log/slog"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// StaticProvisioner is the local-development driver. The local tier has no
// Kubernetes (../../../../docs/INFRA_ANALYST.md §15.1): worker containers are scaled manually
// with `docker compose up --scale worker=N`, so provisioning here is
// bookkeeping only — it records intent and observes the Worker row the
// containers heartbeat into. It implements the same port.K8sClient contract so
// the reconciler and business logic are identical across tiers.
type StaticProvisioner struct {
	workers port.WorkerStore
	logs    port.ProvisionLogStore
	clock   port.Clock
	logger  *slog.Logger
}

// NewStaticProvisioner wires the local driver. workers may be nil; Observe then
// always reports absent, which keeps the API bootable before the DB is wired.
func NewStaticProvisioner(workers port.WorkerStore, logs port.ProvisionLogStore, clock port.Clock, logger *slog.Logger) *StaticProvisioner {
	if logger == nil {
		logger = slog.Default()
	}
	if clock == nil {
		clock = SystemClock{}
	}
	return &StaticProvisioner{workers: workers, logs: logs, clock: clock, logger: logger}
}

var _ port.K8sClient = (*StaticProvisioner)(nil)

// CreateWorker records the provisioning intent in the audit log. No container is
// spawned: the operator scales workers with docker compose. The desired-state row
// itself is written by the caller (container service), not here.
func (p *StaticProvisioner) CreateWorker(ctx context.Context, w domain.Worker) error {
	p.logger.Info("static provisioner: no cluster, recording intent",
		"workerId", w.ID, "generation", w.Generation,
		"note", "scale locally with `docker compose up --scale worker=N`")
	return nil
}

// DeleteWorker is a no-op locally: containers are scaled manually, so there is
// nothing on the platform to remove. The audit row is written by the
// LoggingDriver decorator that wraps every driver.
func (p *StaticProvisioner) DeleteWorker(ctx context.Context, workerID string) error {
	p.logger.Info("static provisioner: delete is local no-op", "workerId", workerID)
	return nil
}

// Observe reports the generation stored on the Worker row. A live worker
// heartbeat updates that row, so this is the local equivalent of reading the
// pod label. Returns ContainerAbsent when the row is missing or the store is
// unset; the static tier cannot see run state, so an existing row reports
// ContainerRunning (the row's observed state is the platform view locally).
func (p *StaticProvisioner) Observe(ctx context.Context, workerID string) (int, port.ContainerState, error) {
	if p.workers == nil {
		return 0, port.ContainerAbsent, nil
	}
	w, err := p.workers.GetByID(ctx, workerID)
	if err == domain.ErrNotFound {
		return 0, port.ContainerAbsent, nil
	}
	if err != nil {
		return 0, port.ContainerAbsent, err
	}
	if w.ObservedGen != nil {
		return *w.ObservedGen, port.ContainerRunning, nil
	}
	return w.Generation, port.ContainerRunning, nil
}

// ListRunning returns every worker the local tier observes: since local
// containers are scaled manually and heartbeat into the Worker row, the row IS
// the platform state. Orphans cannot occur here, but implementing the method
// keeps the sweeper's contract identical across tiers.
func (p *StaticProvisioner) ListRunning(ctx context.Context) ([]string, error) {
	if p.workers == nil {
		return nil, nil
	}
	workers, err := p.workers.List(ctx, port.WorkerFilter{Limit: 500})
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(workers))
	for _, w := range workers {
		ids = append(ids, w.ID)
	}
	return ids, nil
}

// StopWorker is bookkeeping only. This driver has no container platform to
// act on (local tier scales workers by hand), so it records the intent by
// flipping the Worker row to PAUSED. The reconciler then treats it as
// in-sync, which is the truthful description of a driver that cannot act.
func (p *StaticProvisioner) StopWorker(_ context.Context, _ string) error {
	return nil
}

// StartWorker is the matching no-op for the static driver. It does NOT fetch
// or mutate the row: the caller already flipped desired_state to RUNNING, and
// rewriting it here would race that write.
func (p *StaticProvisioner) StartWorker(_ context.Context, _ domain.Worker) error {
	return nil
}
