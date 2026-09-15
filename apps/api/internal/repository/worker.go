package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/port"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/repository/sqlcgen"
)

// WorkerRepo persists worker (container) rows and their telemetry. It implements
// port.WorkerStore on top of the sqlc-generated query handle.
type WorkerRepo struct {
	q *sqlcgen.Queries
}

// NewWorkerRepo binds the repo to a sqlc query handle.
func NewWorkerRepo(q *sqlcgen.Queries) *WorkerRepo { return &WorkerRepo{q: q} }

var _ port.WorkerStore = (*WorkerRepo)(nil)

// GetByID returns the worker with the given id.
func (r *WorkerRepo) GetByID(ctx context.Context, id string) (domain.Worker, error) {
	row, err := r.q.GetWorkerByID(ctx, uuidValue(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Worker{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Worker{}, fmt.Errorf("repository.worker.GetByID: %w", err)
	}
	return toWorker(row), nil
}

// GetByName returns the worker with the given (unique) name.
func (r *WorkerRepo) GetByName(ctx context.Context, name string) (domain.Worker, error) {
	row, err := r.q.GetWorkerByName(ctx, name)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Worker{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Worker{}, fmt.Errorf("repository.worker.GetByName: %w", err)
	}
	return toWorker(row), nil
}

// List returns workers matching the filter. Optional dimensions (status,
// desired_state, source) are applied in Go: the MVP fleet is small and this
// keeps the query layer free of optional-filter plumbing. Pagination stays in
// SQL so the page size is bounded either way.
func (r *WorkerRepo) List(ctx context.Context, f port.WorkerFilter) ([]domain.Worker, error) {
	limit, offset := pageBounds(f.Limit, f.Offset)
	rows, err := r.q.ListWorkers(ctx, sqlcgen.ListWorkersParams{Limit: limit, Offset: offset})
	if err != nil {
		return nil, fmt.Errorf("repository.worker.List: %w", err)
	}
	out := make([]domain.Worker, 0, len(rows))
	for _, row := range rows {
		w := toWorker(row)
		if f.Status != nil && w.Status != *f.Status {
			continue
		}
		if f.DesiredState != nil && w.DesiredState != *f.DesiredState {
			continue
		}
		if f.Source != nil && w.Source != *f.Source {
			continue
		}
		out = append(out, w)
	}
	return out, nil
}

// Create inserts a worker. A duplicate name maps to domain.ErrConflict.
func (r *WorkerRepo) Create(ctx context.Context, w domain.Worker) (domain.Worker, error) {
	row, err := r.q.CreateWorker(ctx, sqlcgen.CreateWorkerParams{
		ID:             uuidValue(w.ID),
		Name:           w.Name,
		ContainerID:    w.ContainerID,
		ControlChannel: w.ControlChannel,
		ActionQueue:    w.ActionQueue,
		SessionPvc:     w.SessionPVC,
		NovncService:   w.NoVNCService,
		DesiredState:   sqlcgen.DesiredState(w.DesiredState),
		Source:         sqlcgen.WorkerSource(w.Source),
		Region:         w.Region,
		Status:         sqlcgen.WorkerStatus(w.Status),
		Generation:     int32(w.Generation),
		ImageVersion:   w.ImageVersion,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return domain.Worker{}, domain.ErrConflict
		}
		return domain.Worker{}, fmt.Errorf("repository.worker.Create: %w", err)
	}
	return toWorker(row), nil
}

// Update persists mutable worker fields. Missing row -> domain.ErrNotFound.
func (r *WorkerRepo) Update(ctx context.Context, w domain.Worker) (domain.Worker, error) {
	row, err := r.q.UpdateWorker(ctx, sqlcgen.UpdateWorkerParams{
		ID:            uuidValue(w.ID),
		ContainerID:   w.ContainerID,
		DesiredState:  sqlcgen.DesiredState(w.DesiredState),
		Status:        sqlcgen.WorkerStatus(w.Status),
		Generation:    int32(w.Generation),
		ObservedGen:   int32Ptr(w.ObservedGen),
		ProvisionErr:  w.ProvisionErr,
		BrowserStatus: w.BrowserStatus,
		CurrentJobID:  uuidValuePtr(w.CurrentJobID),
		LastHeartbeat: tsPtr(w.LastHeartbeat),
		LastActionAt:  tsPtr(w.LastActionAt),
		LastError:     w.LastError,
		QueueDepth:    int32(w.QueueDepth),
		RestartCount:  int32(w.RestartCount),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Worker{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Worker{}, fmt.Errorf("repository.worker.Update: %w", err)
	}
	return toWorker(row), nil
}

// Delete removes a worker. A missing row is not an error (converges to absent).
func (r *WorkerRepo) Delete(ctx context.Context, id string) error {
	if err := r.q.DeleteWorker(ctx, uuidValue(id)); err != nil {
		return fmt.Errorf("repository.worker.Delete: %w", err)
	}
	return nil
}

// RecordHeartbeat appends a telemetry sample and refreshes the denormalised
// worker fields. The worker row is touched FIRST: a failure between the two
// statements then only drops a historical sample, never the live status the
// dashboard relies on. Both are idempotent and self-heal on the next beat.
func (r *WorkerRepo) RecordHeartbeat(ctx context.Context, hb domain.Heartbeat, snap port.WorkerSnapshot) error {
	ts := pgtype.Timestamptz{Time: hb.TS, Valid: !hb.TS.IsZero()}
	if _, err := r.q.TouchWorkerHeartbeat(ctx, sqlcgen.TouchWorkerHeartbeatParams{
		ID:            uuidValue(hb.WorkerID),
		LastHeartbeat: ts,
		Status:        sqlcgen.WorkerStatus(snap.Status),
		BrowserStatus: snap.BrowserStatus,
		QueueDepth:    int32(snap.QueueDepth),
		CurrentJobID:  uuidValuePtr(snap.CurrentJobID),
	}); err != nil {
		return fmt.Errorf("repository.worker.RecordHeartbeat touch: %w", err)
	}
	if err := r.q.InsertHeartbeat(ctx, sqlcgen.InsertHeartbeatParams{
		WorkerID: uuidValue(hb.WorkerID),
		Ts:       ts,
		Cpu:      hb.CPU,
		Mem:      hb.Mem,
		JobsDone: int32(hb.JobsDone),
	}); err != nil {
		return fmt.Errorf("repository.worker.RecordHeartbeat insert: %w", err)
	}
	return nil
}

// toWorker maps a generated row to the domain entity. Postgres enum values are
// stored UPPER; domain uses lower for platform only (the rest already match).
func toWorker(r sqlcgen.Worker) domain.Worker {
	return domain.Worker{
		ID:             uuidString(r.ID),
		Name:           r.Name,
		ContainerID:    r.ContainerID,
		ControlChannel: r.ControlChannel,
		ActionQueue:    r.ActionQueue,
		SessionPVC:     r.SessionPvc,
		NoVNCService:   r.NovncService,
		DesiredState:   domain.DesiredState(r.DesiredState),
		Source:         domain.WorkerSource(r.Source),
		Region:         r.Region,
		Status:         domain.WorkerStatus(r.Status),
		Generation:     int(r.Generation),
		ObservedGen:    intPtr(r.ObservedGen),
		ProvisionErr:   r.ProvisionErr,
		BrowserStatus:  r.BrowserStatus,
		CurrentJobID:   uuidStrPtr(r.CurrentJobID),
		LastHeartbeat:  tsTime(r.LastHeartbeat),
		LastActionAt:   tsTime(r.LastActionAt),
		LastError:      r.LastError,
		QueueDepth:     int(r.QueueDepth),
		RestartCount:   int(r.RestartCount),
		ImageVersion:   r.ImageVersion,
		CreatedAt:      tsTimeOrZero(r.CreatedAt),
	}
}
