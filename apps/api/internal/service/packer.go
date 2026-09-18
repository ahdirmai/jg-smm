package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// ErrNoSlot means no container can host the account and auto-create is off.
var ErrNoSlot = fmt.Errorf("%w: no container slot available", domain.ErrUnavailable)

// Packer bin-packs accounts into worker containers. One container hosts at most
// one account per platform (UNIQUE(worker_id, platform)); the cap on total
// accounts per container is MAX_ACCOUNTS_PER_CONTAINER.
//
// Packing prefers an existing container with a free slot. Only when none exists
// does it fall back to auto-creating an AUTO container (PROVISION_AUTO_CREATE);
// a MANUAL container is never auto-created and never auto-deleted — the
// operator owns its whole life (P1-05).
type Packer struct {
	workers  port.WorkerStore
	accounts port.AccountStore
	clock    port.Clock
	logger   *slog.Logger
	// maxPerContainer caps accounts per container (MAX_ACCOUNTS_PER_CONTAINER).
	maxPerContainer int
	// autoCreate allows the fallback that spawns an AUTO container.
	autoCreate bool
	// novnc allocates the live-view port for an AUTO container. Optional: nil
	// leaves novnc_service empty, so an auto-spawned card has a disabled Live
	// view button instead of a dead link.
	novnc *NovncAllocator
}

// PackerConfig tunes packing.
type PackerConfig struct {
	MaxPerContainer int
	AutoCreate      bool
	Clock           port.Clock
	Logger          *slog.Logger
	// Novnc allocates the live-view port for AUTO containers. Share one
	// allocator with ContainerService so the two paths cannot collide.
	Novnc *NovncAllocator
}

// NewPacker wires the packer. MaxPerContainer < 1 is clamped to 1.
func NewPacker(workers port.WorkerStore, accounts port.AccountStore, cfg PackerConfig) *Packer {
	if cfg.MaxPerContainer < 1 {
		cfg.MaxPerContainer = 1
	}
	if cfg.Clock == nil {
		cfg.Clock = systemClock{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Packer{
		workers:         workers,
		accounts:        accounts,
		clock:           cfg.Clock,
		logger:          cfg.Logger,
		maxPerContainer: cfg.MaxPerContainer,
		autoCreate:      cfg.AutoCreate,
		novnc:           cfg.Novnc,
	}
}

// Pack assigns an account to a container. It returns the updated account and
// the container it landed on. When no container can host the account:
//   - auto-create on  -> a fresh AUTO container is created and used,
//   - auto-create off -> ErrNoSlot.
//
// A race that wins a slot between our check and Assign surfaces as
// domain.ErrConflict from the store; the packer walks the next candidate
// rather than failing the whole call.
func (p *Packer) Pack(ctx context.Context, accountID string, platform domain.Platform) (domain.Account, domain.Worker, error) {
	account, err := p.accounts.GetByID(ctx, accountID)
	if err != nil {
		return domain.Account{}, domain.Worker{}, fmt.Errorf("packer: get account: %w", err)
	}
	if !account.IsPackable() {
		return domain.Account{}, domain.Worker{}, fmt.Errorf("%w: account status %q is not packable", domain.ErrValidation, account.Status)
	}
	if account.WorkerID != nil {
		return domain.Account{}, domain.Worker{}, fmt.Errorf("%w: account already on container %s", domain.ErrConflict, *account.WorkerID)
	}

	// Existing fleet first: the first container that can host the platform.
	candidates, err := p.workers.List(ctx, port.WorkerFilter{
		DesiredState: ptrDesired(domain.DesiredRunning),
		Limit:        200,
	})
	if err != nil {
		return domain.Account{}, domain.Worker{}, fmt.Errorf("packer: list workers: %w", err)
	}
	for i := range candidates {
		w := candidates[i]
		if !p.hasFreeSlot(ctx, w, platform) {
			continue
		}
		// A concurrent pack may have taken the slot since we checked; the
		// store's UNIQUE(worker_id, platform) is the authority, so a conflict
		// just means "try the next container".
		updated, err := p.accounts.Assign(ctx, account.ID, w.ID)
		if errors.Is(err, domain.ErrConflict) {
			continue
		}
		if err != nil {
			return domain.Account{}, domain.Worker{}, fmt.Errorf("packer: assign to %s: %w", w.ID, err)
		}
		p.logger.Info("packer: account packed", "accountId", account.ID, "workerId", w.ID, "platform", account.Platform)
		return updated, w, nil
	}

	if !p.autoCreate {
		return domain.Account{}, domain.Worker{}, ErrNoSlot
	}

	// Fallback: spawn an AUTO container. The row is the desired state; the
	// reconciler (P1-04) provisions the pod from it.
	w, err := p.createWorker(ctx, domain.SourceAuto)
	if err != nil {
		return domain.Account{}, domain.Worker{}, err
	}
	p.logger.Info("packer: auto-created container", "workerId", w.ID, "platform", platform)
	updated, err := p.assign(ctx, account, w)
	if err != nil {
		return domain.Account{}, domain.Worker{}, err
	}
	return updated, w, nil
}

// hasFreeSlot reports whether a container may host the platform: it is under
// the account cap and does not already host that platform.
func (p *Packer) hasFreeSlot(ctx context.Context, w domain.Worker, platform domain.Platform) bool {
	count, err := p.accounts.CountByWorker(ctx, w.ID)
	if err != nil {
		p.logger.Warn("packer: count failed", "workerId", w.ID, "err", err)
		return false
	}
	if count >= p.maxPerContainer {
		return false
	}
	hosted, err := p.accounts.ListByWorker(ctx, w.ID)
	if err != nil {
		p.logger.Warn("packer: list accounts failed", "workerId", w.ID, "err", err)
		return false
	}
	for _, a := range hosted {
		if a.Platform == platform {
			return false
		}
	}
	return true
}

// assign packs the account into the chosen container. Used by the auto-create
// path, where the container is brand new and cannot have a platform conflict.
func (p *Packer) assign(ctx context.Context, account domain.Account, w domain.Worker) (domain.Account, error) {
	updated, err := p.accounts.Assign(ctx, account.ID, w.ID)
	if err != nil {
		return domain.Account{}, fmt.Errorf("packer: assign to %s: %w", w.ID, err)
	}
	p.logger.Info("packer: account packed", "accountId", account.ID, "workerId", w.ID, "platform", account.Platform)
	return updated, nil
}

// createWorker inserts a container row with a unique name. A name collision is
// retried with a fresh suffix; the store surfaces it as domain.ErrConflict
// (UNIQUE(name) is the authority), so no driver error leaks here.
func (p *Packer) createWorker(ctx context.Context, source domain.WorkerSource) (domain.Worker, error) {
	var w domain.Worker
	var err error
	// The live-view port is allocated up front so the AUTO card gets the same
	// Live view button as a MANUAL one. Nil allocator = range unset, and the
	// button stays disabled rather than dead.
	var novncURL *string
	if p.novnc != nil {
		novncURL, err = p.novnc.Allocate(ctx, nil)
		if err != nil {
			return domain.Worker{}, fmt.Errorf("packer: novnc port: %w", err)
		}
	}
	for attempt := 0; attempt < 3; attempt++ {
		w, err = p.workers.Create(ctx, domain.Worker{
			Name:           newWorkerName(p.clock),
			ControlChannel: ptrString(domain.ControlChannel("pending")),
			ActionQueue:    ptrString(domain.ActionQueue("pending")),
			SessionPVC:     ptrString("pending"),
			DesiredState:   domain.DesiredRunning,
			Source:         source,
			Region:         "ID",
			Status:         domain.WorkerPending,
			Generation:     1,
			ImageVersion:   "v0.1",
			NoVNCService:   novncURL,
			CreatedAt:      p.clock.Now(),
		})
		if err == nil {
			return w, nil
		}
		if !errors.Is(err, domain.ErrConflict) {
			return domain.Worker{}, fmt.Errorf("packer: create worker: %w", err)
		}
	}
	return domain.Worker{}, fmt.Errorf("packer: create worker (name collision): %w", err)
}

// Release detaches an account from its container, then reaps the container when
// it is AUTO and now empty. MANUAL containers are always kept (operator-owned).
func (p *Packer) Release(ctx context.Context, accountID string) error {
	account, err := p.accounts.Unassign(ctx, accountID)
	if err != nil {
		return fmt.Errorf("packer: unassign: %w", err)
	}
	if account.WorkerID == nil {
		return nil
	}
	workerID := *account.WorkerID
	if err := p.ReapEmpty(ctx, workerID); err != nil {
		p.logger.Warn("packer: reap failed", "workerId", workerID, "err", err)
	}
	return nil
}

// ReapEmpty deletes an AUTO container that hosts no accounts. MANUAL containers
// and non-empty containers are left untouched.
func (p *Packer) ReapEmpty(ctx context.Context, workerID string) error {
	w, err := p.workers.GetByID(ctx, workerID)
	if err != nil {
		if err == domain.ErrNotFound {
			return nil
		}
		return fmt.Errorf("packer: get worker: %w", err)
	}
	if w.Source != domain.SourceAuto {
		return nil
	}
	count, err := p.accounts.CountByWorker(ctx, workerID)
	if err != nil {
		return fmt.Errorf("packer: count accounts: %w", err)
	}
	if count > 0 {
		return nil
	}
	if err := p.workers.Delete(ctx, workerID); err != nil {
		return fmt.Errorf("packer: delete worker: %w", err)
	}
	p.logger.Info("packer: reaped empty AUTO container", "workerId", workerID)
	return nil
}

// ptrString returns a pointer copy of s.
func ptrString(s string) *string { return &s }

// ptrDesired returns a pointer copy of the desired state for the list filter.
func ptrDesired(d domain.DesiredState) *domain.DesiredState { return &d }
