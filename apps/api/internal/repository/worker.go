package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
	"github.com/ahdirmai/jg-smm/apps/api/internal/repository/sqlcgen"
)

// WorkerRepo persists worker (container) rows and their telemetry. It implements
// port.WorkerStore on top of the sqlc-generated query handle. A beginner is
// held alongside it for the claim path, which needs a SELECT ... FOR UPDATE
// SKIP LOCKED that sqlc's generated one-shots cannot express.
type WorkerRepo struct {
	q     *sqlcgen.Queries
	begin beginner
}

// beginner is the slice of pgx the claim needs: start a transaction, and run
// queries inside it. *pgxpool.Pool and pgx.Tx both satisfy it, so the repo works
// on the pool and (for tests) on a hand-rolled tx.
type beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// NewWorkerRepo binds the repo to a sqlc query handle. The second argument is
// the pool the claim transaction runs on; it defaults to the generated handle's
// connection when nil (tests pass a stub instead of a live pool).
func NewWorkerRepo(q *sqlcgen.Queries, raw beginner) *WorkerRepo {
	r := &WorkerRepo{q: q, begin: raw}
	return r
}

var _ port.WorkerStore = (*WorkerRepo)(nil)

// The worker columns scanWorkerRow reads, in the order both claim queries return
// them. Kept identical to the generated SELECTs so toWorker can be reused.
const workerCols = `id, name, container_id, control_channel, action_queue, session_pvc,
	novnc_service, desired_state, source, region, status, generation, observed_gen,
	provision_err, browser_status, current_job_id, last_heartbeat, last_action_at,
	last_error, queue_depth, restart_count, image_version, created_at, location,
	latitude, longitude`

// claimSelectOwned finds the row this container already owns (idempotent
// re-claim across restarts).
const claimSelectOwned = `SELECT ` + workerCols + ` FROM worker WHERE container_id = $1`

// claimSelectByID binds the dashboard-created row whose id the driver injected
// as WORKER_ID. A recreated container changes hostname, so a row that was
// claimed under a compose-derived boot id ("worker-<hostname>") must be
// re-pointable to the same row by its real id — otherwise the worker falls
// back to its boot id and its queue goes unheard.
const claimSelectByID = `SELECT ` + workerCols + ` FROM worker WHERE id = $1 FOR UPDATE SKIP LOCKED`

// claimSelectFree takes the oldest PENDING row nobody has claimed yet, and
// locks it so concurrent `--scale`d workers each take a distinct one.
const claimSelectFree = `SELECT ` + workerCols + `
FROM worker
WHERE container_id IS NULL AND status = 'PENDING' AND desired_state = 'RUNNING'
ORDER BY created_at
FOR UPDATE SKIP LOCKED
LIMIT 1`

// claimBind stamps the container's live identity + runtime redis keys onto the
// row it just won.
const claimBind = `UPDATE worker SET
	container_id   = $1,
	control_channel = $2,
	action_queue    = $3,
	session_pvc     = $4,
	novnc_service   = $5
WHERE id = $6`

// beginTx starts a transaction on the pool, or reuses the one already bound
// (a test may hand the repo a tx directly). The claim is the only
// read-modify-write on the worker path that needs atomicity; the rest of the
// repo stays on the generated one-shots.
func beginTx(ctx context.Context, b beginner) (pgx.Tx, error) {
	if t, ok := b.(pgx.Tx); ok {
		return t, nil
	}
	return b.Begin(ctx)
}

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

// GetByContainerID resolves a row by the boot identity a live worker reported
// when it claimed the row ("worker-<hostname>"). A heartbeat or geolocation
// probe carries that id, not the row's UUID.
func (r *WorkerRepo) GetByContainerID(ctx context.Context, containerID string) (domain.Worker, error) {
	w, err := scanWorkerRow(ctx, r.begin.QueryRow(ctx, claimSelectOwned, containerID))
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Worker{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Worker{}, fmt.Errorf("repository.worker.GetByContainerID: %w", err)
	}
	return w, nil
}

// Claim atomically binds the oldest unclaimed PENDING row to this container's
// boot identity and re-points its redis channels at the worker's runtime keys.
//
// FOR UPDATE SKIP LOCKED is what makes `docker compose up --scale worker=N`
// safe: N containers race for N rows, each takes a distinct one, and a slow
// starter never blocks the rest. A UNIQUE constraint on container_id plus the
// idempotent re-claim branch means a container that restarts reclaims the row
// it already owns instead of consuming a second one.
func (r *WorkerRepo) Claim(ctx context.Context, claim port.WorkerClaim) (domain.Worker, error) {
	tx, err := beginTx(ctx, r.begin)
	if err != nil {
		return domain.Worker{}, fmt.Errorf("repository.worker.Claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed

	// 1. Idempotent re-claim: this container already owns a row.
	owned, err := scanWorkerRow(ctx, tx.QueryRow(ctx, claimSelectOwned, claim.ContainerID))
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return domain.Worker{}, fmt.Errorf("repository.worker.Claim: commit: %w", err)
		}
		return owned, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return domain.Worker{}, fmt.Errorf("repository.worker.Claim: owned lookup: %w", err)
	}

	// 2. A docker-provisioned container is launched with WORKER_ID=<row id>, so
	// the claim may name its own row directly even when the row was bound
	// earlier under a compose-derived boot id (a recreated container changes
	// hostname). Re-point that row instead of falling back to the boot id,
	// which would leave its queue unheard.
	//
	// Only attempted when the container id is a row uuid: a compose-derived
	// "worker-<hostname>" is not, and querying it would 22P02 instead of
	// falling through to the free-row path.
	//
	// And only when the row is unowned or already this container's: a row that
	// a different live container owns is not ours to take. Without the guard, a
	// stale WORKER_ID (an env baked into a reused image, or a row recycled
	// while its container was being replaced) would silently steal a row the
	// owner is still serving — two workers on one queue.
	if isUUID(claim.ContainerID) {
		row, err := scanWorkerRow(ctx, tx.QueryRow(ctx, claimSelectByID, claim.ContainerID))
		if err == nil {
			if row.ContainerID != nil && *row.ContainerID != "" && *row.ContainerID != claim.ContainerID {
				return domain.Worker{}, fmt.Errorf("%w: worker row %s is owned by another container", domain.ErrConflict, row.ID)
			}
			if _, err := tx.Exec(ctx, claimBind,
				claim.ContainerID, claim.ControlChannel, claim.ActionQueue, claim.SessionPVC,
				nilIfEmpty(claim.NovncURL), row.ID); err != nil {
				return domain.Worker{}, fmt.Errorf("repository.worker.Claim: rebind: %w", err)
			}
			row.ContainerID = &claim.ContainerID
			row.ControlChannel = &claim.ControlChannel
			row.ActionQueue = &claim.ActionQueue
			row.SessionPVC = &claim.SessionPVC
			if claim.NovncURL != nil && *claim.NovncURL != "" {
				row.NoVNCService = claim.NovncURL
			}
			if err := tx.Commit(ctx); err != nil {
				return domain.Worker{}, fmt.Errorf("repository.worker.Claim: commit: %w", err)
			}
			return row, nil
		}
		if !errors.Is(err, domain.ErrNotFound) {
			return domain.Worker{}, fmt.Errorf("repository.worker.Claim: id lookup: %w", err)
		}
	}

	// 3. Take the oldest free PENDING row that wants to run.
	row, err := scanWorkerRow(ctx, tx.QueryRow(ctx, claimSelectFree))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Worker{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Worker{}, fmt.Errorf("repository.worker.Claim: free lookup: %w", err)
	}

	// 4. Bind it. The "pending" placeholder channels the row was created with
	// are replaced by the live keys this worker actually subscribes to, so a job
	// queued to the row reaches the container that claimed it.
	if _, err := tx.Exec(ctx, claimBind,
		claim.ContainerID, claim.ControlChannel, claim.ActionQueue, claim.SessionPVC,
		nilIfEmpty(claim.NovncURL), row.ID); err != nil {
		return domain.Worker{}, fmt.Errorf("repository.worker.Claim: bind: %w", err)
	}
	row.ContainerID = &claim.ContainerID
	row.ControlChannel = &claim.ControlChannel
	row.ActionQueue = &claim.ActionQueue
	row.SessionPVC = &claim.SessionPVC
	if claim.NovncURL != nil && *claim.NovncURL != "" {
		row.NoVNCService = claim.NovncURL
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.Worker{}, fmt.Errorf("repository.worker.Claim: commit: %w", err)
	}
	return row, nil
}
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
		Location:       w.Location,
		Latitude:       w.Latitude,
		Longitude:      w.Longitude,
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
		NovncService:  snap.NovncURL,
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
// scanWorkerRow reads the worker column list in workerCols order into the
// domain type. Both claim queries return it, and it mirrors toWorker so a claimed
// row is shaped exactly like a fetched one.
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
		Location:       r.Location,
		Latitude:       float64Ptr(r.Latitude),
		Longitude:      float64Ptr(r.Longitude),
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

// scanWorkerRow reads the worker column list in workerCols order into the
// domain type. Both claim queries return it, and it mirrors toWorker so a
// claimed row is shaped exactly like a fetched one.
func scanWorkerRow(ctx context.Context, row pgx.Row) (domain.Worker, error) {
	var (
		id             pgtype.UUID
		name           string
		containerID    *string
		controlChannel *string
		actionQueue    *string
		sessionPVC     *string
		novncService   *string
		desiredState   sqlcgen.DesiredState
		source         sqlcgen.WorkerSource
		region         string
		status         sqlcgen.WorkerStatus
		generation     int32
		observedGen    *int32
		provisionErr   *string
		browserStatus  string
		currentJobID   pgtype.UUID
		lastHeartbeat  pgtype.Timestamptz
		lastActionAt   pgtype.Timestamptz
		lastError      *string
		queueDepth     int32
		restartCount   int32
		imageVersion   string
		createdAt      pgtype.Timestamptz
		location       *string
		latitude       *float64
		longitude      *float64
	)
	err := row.Scan(
		&id, &name, &containerID, &controlChannel, &actionQueue, &sessionPVC,
		&novncService, &desiredState, &source, &region, &status, &generation, &observedGen,
		&provisionErr, &browserStatus, &currentJobID, &lastHeartbeat, &lastActionAt,
		&lastError, &queueDepth, &restartCount, &imageVersion, &createdAt, &location,
		&latitude, &longitude,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Worker{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Worker{}, err
	}
	return domain.Worker{
		ID:             uuidString(id),
		Name:           name,
		ContainerID:    containerID,
		ControlChannel: controlChannel,
		ActionQueue:    actionQueue,
		SessionPVC:     sessionPVC,
		NoVNCService:   novncService,
		DesiredState:   domain.DesiredState(desiredState),
		Source:         domain.WorkerSource(source),
		Region:         region,
		Location:       location,
		Latitude:       latitude,
		Longitude:      longitude,
		Status:         domain.WorkerStatus(status),
		Generation:     int(generation),
		ObservedGen:    intPtr(observedGen),
		ProvisionErr:   provisionErr,
		BrowserStatus:  browserStatus,
		CurrentJobID:   uuidStrPtr(currentJobID),
		LastHeartbeat:  tsTime(lastHeartbeat),
		LastActionAt:   tsTime(lastActionAt),
		LastError:      lastError,
		QueueDepth:     int(queueDepth),
		RestartCount:   int(restartCount),
		ImageVersion:   imageVersion,
		CreatedAt:      tsTimeOrZero(createdAt),
	}, nil
}

// nilIfEmpty keeps an empty string from clobbering a real noVNC url: the claim
// sends the URL only when the worker actually publishes one.
func nilIfEmpty(s *string) *string {
	if s == nil || *s == "" {
		return nil
	}
	return s
}

// isUUID reports whether s parses as a row id. The docker driver injects one
// as WORKER_ID; a compose-derived "worker-<hostname>" does not, and querying
// the by-id path with it is a 22P02, not a fall-through.
func isUUID(s string) bool {
	_, err := uuid.Parse(s)
	return err == nil
}
