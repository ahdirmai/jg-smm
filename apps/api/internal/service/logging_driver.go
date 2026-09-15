package service

import (
	"context"
	"log/slog"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/port"
)

// LoggingDriver decorates a port.K8sClient so every provisioning op is written
// to the audit trail (provision_log). The DB write happens AFTER the driver op
// succeeds or fails; a failed audit write is only logged, never returned to the
// caller, because the platform state is the source of truth and the log is a
// record of it.
type LoggingDriver struct {
	next   port.K8sClient
	logs   port.ProvisionLogStore
	clock  port.Clock
	logger *slog.Logger
}

// NewLoggingDriver wraps driver. logs may be nil, in which case the decorator is
// a transparent passthrough (useful for tests that only exercise the platform).
func NewLoggingDriver(next port.K8sClient, logs port.ProvisionLogStore, clock port.Clock, logger *slog.Logger) *LoggingDriver {
	if logger == nil {
		logger = slog.Default()
	}
	if clock == nil {
		clock = systemClock{}
	}
	return &LoggingDriver{next: next, logs: logs, clock: clock, logger: logger}
}

var _ port.K8sClient = (*LoggingDriver)(nil)

// CreateWorker provisions through the wrapped driver and records the outcome.
func (d *LoggingDriver) CreateWorker(ctx context.Context, w domain.Worker) error {
	err := d.next.CreateWorker(ctx, w)
	d.record(ctx, w, domain.OpCreate, err)
	return err
}

// DeleteWorker deprovisions through the wrapped driver and records the outcome.
func (d *LoggingDriver) DeleteWorker(ctx context.Context, workerID string) error {
	err := d.next.DeleteWorker(ctx, workerID)
	d.record(ctx, domain.Worker{ID: workerID}, domain.OpDelete, err)
	return err
}

// Observe passes through untouched; it is a read, not a provisioning op.
func (d *LoggingDriver) Observe(ctx context.Context, workerID string) (int, bool, error) {
	return d.next.Observe(ctx, workerID)
}

// ListRunning passes through untouched; it is a read, not a provisioning op.
func (d *LoggingDriver) ListRunning(ctx context.Context) ([]string, error) {
	return d.next.ListRunning(ctx)
}

// record appends one audit row. Generation is the worker's own for a create
// (the desired generation) and 0 for a delete, matching the static driver.
func (d *LoggingDriver) record(ctx context.Context, w domain.Worker, op domain.ProvisionOp, err error) {
	if d.logs == nil {
		return
	}
	status := domain.ProvisionApplied
	var errMsg *string
	if err != nil {
		status = domain.ProvisionFailed
		s := err.Error()
		errMsg = &s
	}
	if appendErr := d.logs.Append(ctx, domain.ProvisionLog{
		WorkerID:   w.ID,
		Op:         op,
		Generation: w.Generation,
		Status:     status,
		Error:      errMsg,
		TS:         d.clock.Now(),
	}); appendErr != nil {
		d.logger.Warn("provision audit write failed",
			"workerId", w.ID, "op", op, "err", appendErr)
	}
}
