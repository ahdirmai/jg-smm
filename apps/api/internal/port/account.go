package port

import (
	"context"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
)

// WorkerFilter narrows a worker list query.
type WorkerFilter struct {
	Status       *domain.WorkerStatus
	DesiredState *domain.DesiredState
	Source       *domain.WorkerSource
	Limit        int
	Offset       int
}

// WorkerStore persists workers (containers) and their telemetry.
type WorkerStore interface {
	GetByID(ctx context.Context, id string) (domain.Worker, error)
	GetByName(ctx context.Context, name string) (domain.Worker, error)
	List(ctx context.Context, f WorkerFilter) ([]domain.Worker, error)
	Create(ctx context.Context, w domain.Worker) (domain.Worker, error)
	// Update persists mutable fields (desired_state, status, generation,
	// observed_gen, provision_err, heartbeat fields) and returns the new row.
	Update(ctx context.Context, w domain.Worker) (domain.Worker, error)
	Delete(ctx context.Context, id string) error
	// RecordHeartbeat appends a telemetry sample and refreshes the worker's
	// denormalised last_heartbeat / browser_status / queue_depth / current_job_id.
	RecordHeartbeat(ctx context.Context, hb domain.Heartbeat, snapshot WorkerSnapshot) error
}

// WorkerSnapshot carries the denormalised worker fields updated on each heartbeat.
type WorkerSnapshot struct {
	Status        domain.WorkerStatus
	BrowserStatus string
	QueueDepth    int
	CurrentJobID  *string
	// NovncURL is the browser-reachable live-view URL (P4-08). nil = unset.
	NovncURL *string
}

// AccountFilter narrows an account list query.
type AccountFilter struct {
	Platform *domain.Platform
	Status   *domain.AccountStatus
	WorkerID *string
	// UnassignedOnly returns accounts not yet packed into any container.
	UnassignedOnly bool
	Limit          int
	Offset         int
}

// AccountStore persists worker accounts. PasswordEnc is write-only: no method
// returns it except the dedicated credential accessor used by the login flow.
type AccountStore interface {
	GetByID(ctx context.Context, id string) (domain.Account, error)
	List(ctx context.Context, f AccountFilter) ([]domain.Account, error)
	Create(ctx context.Context, a domain.Account) (domain.Account, error)
	// Update persists mutable lifecycle/auth fields (never password_enc).
	Update(ctx context.Context, a domain.Account) (domain.Account, error)
	// Assign packs an account into a worker, honouring UNIQUE(worker_id, platform).
	Assign(ctx context.Context, accountID, workerID string) (domain.Account, error)
	// Unassign detaches an account from its worker.
	Unassign(ctx context.Context, accountID string) (domain.Account, error)
	Delete(ctx context.Context, id string) error
	// ListByWorker returns the accounts hosted by a container (max one per platform).
	ListByWorker(ctx context.Context, workerID string) ([]domain.Account, error)
	// CountByWorker returns how many accounts a container currently hosts.
	CountByWorker(ctx context.Context, workerID string) (int, error)
}

// ProxyGroupStore persists proxy pools.
type ProxyGroupStore interface {
	GetByID(ctx context.Context, id string) (domain.ProxyGroup, error)
	List(ctx context.Context) ([]domain.ProxyGroup, error)
	Create(ctx context.Context, g domain.ProxyGroup) (domain.ProxyGroup, error)
	Update(ctx context.Context, g domain.ProxyGroup) (domain.ProxyGroup, error)
	Delete(ctx context.Context, id string) error
}

// ProvisionLogStore appends provisioner audit rows.
type ProvisionLogStore interface {
	Append(ctx context.Context, l domain.ProvisionLog) error
	ListByWorker(ctx context.Context, workerID string, limit int) ([]domain.ProvisionLog, error)
}
