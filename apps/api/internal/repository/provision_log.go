package repository

import (
	"context"
	"fmt"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/port"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/repository/sqlcgen"
)

// ProvisionLogRepo appends provisioner audit rows. It is append-only by design:
// no update or delete query exists for provision_log.
type ProvisionLogRepo struct {
	q *sqlcgen.Queries
}

// NewProvisionLogRepo binds the repo to a sqlc query handle.
func NewProvisionLogRepo(q *sqlcgen.Queries) *ProvisionLogRepo { return &ProvisionLogRepo{q: q} }

var _ port.ProvisionLogStore = (*ProvisionLogRepo)(nil)

// Append records one provisioner operation.
func (r *ProvisionLogRepo) Append(ctx context.Context, l domain.ProvisionLog) error {
	status := string(domain.ProvisionPending)
	if l.Status != "" {
		status = string(l.Status)
	}
	if err := r.q.AppendProvisionLog(ctx, sqlcgen.AppendProvisionLogParams{
		WorkerID:   uuidValue(l.WorkerID),
		Op:         sqlcgen.ProvisionOp(l.Op),
		Generation: int32(l.Generation),
		K8sRef:     l.K8sRef,
		Status:     status,
		Error:      l.Error,
	}); err != nil {
		return fmt.Errorf("repository.provision_log.Append: %w", err)
	}
	return nil
}

// ListByWorker returns the most recent ops for a worker (newest first).
func (r *ProvisionLogRepo) ListByWorker(ctx context.Context, workerID string, limit int) ([]domain.ProvisionLog, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.q.ListProvisionLogsByWorker(ctx, sqlcgen.ListProvisionLogsByWorkerParams{
		WorkerID: uuidValue(workerID),
		Limit:    int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("repository.provision_log.ListByWorker: %w", err)
	}
	out := make([]domain.ProvisionLog, 0, len(rows))
	for _, row := range rows {
		out = append(out, toProvisionLog(row))
	}
	return out, nil
}

func toProvisionLog(r sqlcgen.ProvisionLog) domain.ProvisionLog {
	return domain.ProvisionLog{
		ID:         uuidString(r.ID),
		WorkerID:   uuidString(r.WorkerID),
		Op:         domain.ProvisionOp(r.Op),
		Generation: int(r.Generation),
		K8sRef:     r.K8sRef,
		Status:     domain.ProvisionStatus(r.Status),
		Error:      r.Error,
		TS:         tsTimeOrZero(r.Ts),
	}
}
