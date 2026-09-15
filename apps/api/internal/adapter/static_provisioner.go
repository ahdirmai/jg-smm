package adapter

import (
	"context"
	"log/slog"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/port"
)

// StaticProvisioner is the local-development driver. The local tier has no
// Kubernetes (INFRA_ANALYST.md §15.1): worker containers are scaled manually
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
	return p.appendLog(ctx, w, domain.OpCreate, domain.ProvisionApplied, nil)
}

// DeleteWorker records a delete intent. The Worker row is left to the caller;
// this only keeps the audit trail symmetric with the k8s driver.
func (p *StaticProvisioner) DeleteWorker(ctx context.Context, workerID string) error {
	p.logger.Info("static provisioner: delete recorded", "workerId", workerID)
	return p.appendLog(ctx, domain.Worker{ID: workerID}, domain.OpDelete, domain.ProvisionApplied, nil)
}

// Observe reports the generation stored on the Worker row. A live worker
// heartbeat updates that row, so this is the local equivalent of reading the
// pod label. Returns (0, false) when the row is missing or the store is unset.
func (p *StaticProvisioner) Observe(ctx context.Context, workerID string) (int, bool, error) {
	if p.workers == nil {
		return 0, false, nil
	}
	w, err := p.workers.GetByID(ctx, workerID)
	if err == domain.ErrNotFound {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if w.ObservedGen != nil {
		return *w.ObservedGen, true, nil
	}
	return w.Generation, true, nil
}

func (p *StaticProvisioner) appendLog(ctx context.Context, w domain.Worker, op domain.ProvisionOp, status domain.ProvisionStatus, errp *string) error {
	if p.logs == nil {
		return nil
	}
	return p.logs.Append(ctx, domain.ProvisionLog{
		WorkerID:   w.ID,
		Op:         op,
		Generation: w.Generation,
		Status:     status,
		Error:      errp,
		TS:         p.clock.Now(),
	})
}
