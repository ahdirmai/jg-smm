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

// WorkerClaim is what a booted worker container presents to adopt a row: the
// hostname-derived id it boots with, and the redis channels it subscribes to.
// The claim binds container_id so the dashboard can see which row is live, and
// re-points control_channel/action_queue at the worker's runtime keys so a job
// queued to the row actually reaches the container that claimed it.
type WorkerClaim struct {
	// ContainerID is the worker's boot identity: WORKER_ID=worker-<hostname>.
	// Unique on the worker row, so a restart of the same container reclaims the
	// same row instead of stealing a different one.
	ContainerID    string
	ControlChannel string
	ActionQueue    string
	SessionPVC     string
	NovncURL       *string
}

// WorkerStore persists workers (containers) and their telemetry.
type WorkerStore interface {
	GetByID(ctx context.Context, id string) (domain.Worker, error)
	GetByName(ctx context.Context, name string) (domain.Worker, error)
	// GetByContainerID resolves a row by the boot id the worker claimed it with
	// ("worker-<hostname>"). A heartbeat or geolocation probe arrives carrying
	// that id, not the row's UUID, so this is the lookup that makes a locally
	// scaled container reachable at all.
	GetByContainerID(ctx context.Context, containerID string) (domain.Worker, error)
	List(ctx context.Context, f WorkerFilter) ([]domain.Worker, error)
	Create(ctx context.Context, w domain.Worker) (domain.Worker, error)
	// Claim binds an unclaimed PENDING row to this container's boot identity and
	// re-points its channels at the worker's runtime keys. Idempotent for the
	// same ContainerID: a worker that reboots reclaims the row it already owns
	// rather than consuming a second one. Returns ErrNotFound when no row is free.
	Claim(ctx context.Context, claim WorkerClaim) (domain.Worker, error)
	// Update persists mutable fields (desired_state, status, generation,
	// observed_gen, provision_err, heartbeat fields) and returns the new row.
	Update(ctx context.Context, w domain.Worker) (domain.Worker, error)
	Delete(ctx context.Context, id string) error
	// RecordHeartbeat appends a telemetry sample and refreshes the worker's
	// denormalised last_heartbeat / browser_status / queue_depth / current_job_id.
	RecordHeartbeat(ctx context.Context, hb domain.Heartbeat, snapshot WorkerSnapshot) error
	// ListPoolWorkers returns the workers a slot may be handed to: RUNNING,
	// configured (source MANUAL) and reachable (endpoint_url set). Unpaginated
	// on purpose — a candidate silently dropped past a page would strand the
	// accounts that hash to it.
	ListPoolWorkers(ctx context.Context) ([]domain.Worker, error)
	// UpsertPoolWorker seeds one pool worker by NAME (the stable smm-worker-NN
	// identity), re-pointing endpoint_url/novnc_service and forcing RUNNING.
	// Idempotent, so every boot of the API reasserts the configured fleet.
	UpsertPoolWorker(ctx context.Context, w domain.Worker) (domain.Worker, error)
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
	// GetPasswordEnc is the single narrow read of the write-only credential
	// blob, used once per auth-login dispatch so the worker can fill the login
	// form. No list/read model ever selects the column.
	GetPasswordEnc(ctx context.Context, id string) ([]byte, error)

	// --- stateless pool (000024) ---

	// AcquireLease pins an account to a pool worker for an interactive session.
	// It is the DB-level truth behind the Redis slot cache: an account is pinned
	// at most once, and a worker hosts at most one account. domain.ErrConflict
	// when either is already taken.
	AcquireLease(ctx context.Context, accountID, workerID string) (domain.Account, error)
	// ReleaseLease returns the account to the schedulable pool. Guarded on
	// workerID so a late release from an old session cannot free a new lease.
	ReleaseLease(ctx context.Context, accountID, workerID string) error
	// ListUnlocated returns accounts still missing a city/GPS identity, for the
	// startup backfill. Lim = 0 means no explicit bound.
	ListUnlocated(ctx context.Context, limit int) ([]domain.Account, error)
	// ListAll returns every account unpaginated: region grouping must not
	// silently truncate a 486-account fleet at one page.
	ListAll(ctx context.Context) ([]domain.Account, error)
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
