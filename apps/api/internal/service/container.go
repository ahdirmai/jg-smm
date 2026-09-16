package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// ContainerService is the operator-facing API for worker containers. It creates
// MANUAL containers (P1-19), lists the fleet with its accounts, and deletes
// them. The reconciler (P1-04) does the actual provisioning from the rows this
// service writes — the service never talks to the container platform.
type ContainerService struct {
	workers  port.WorkerStore
	accounts port.AccountStore
	packer   *Packer
	clock    port.Clock
	logger   *slog.Logger
}

// ContainerConfig tunes the container service.
type ContainerConfig struct {
	Clock  port.Clock
	Logger *slog.Logger
}

// NewContainerService wires the service. packer may be nil; deletion then only
// removes the row (local/static tier has no pod to reap).
func NewContainerService(workers port.WorkerStore, accounts port.AccountStore, packer *Packer, cfg ContainerConfig) *ContainerService {
	if cfg.Clock == nil {
		cfg.Clock = systemClock{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &ContainerService{
		workers:  workers,
		accounts: accounts,
		packer:   packer,
		clock:    cfg.Clock,
		logger:   cfg.Logger,
	}
}

// Create inserts a MANUAL container in the RUNNING desired state. The operator
// owns its whole life: it is never auto-deleted, even at zero accounts.
// An empty name gets a generated unique one.
func (s *ContainerService) Create(ctx context.Context, name, region string) (domain.Worker, error) {
	if region == "" {
		return domain.Worker{}, fmt.Errorf("%w: region is required", domain.ErrValidation)
	}
	if len(region) != 2 {
		return domain.Worker{}, fmt.Errorf("%w: region must be ISO 3166-1 alpha-2", domain.ErrValidation)
	}
	if name == "" {
		name = newWorkerName(s.clock)
	}
	if len(name) > 64 {
		return domain.Worker{}, fmt.Errorf("%w: name too long (max 64)", domain.ErrValidation)
	}

	w, err := s.workers.Create(ctx, domain.Worker{
		Name:           name,
		ControlChannel: ptrString(domain.ControlChannel("pending")),
		ActionQueue:    ptrString(domain.ActionQueue("pending")),
		SessionPVC:     ptrString("pending"),
		DesiredState:   domain.DesiredRunning,
		Source:         domain.SourceManual,
		Region:         region,
		Status:         domain.WorkerPending,
		Generation:     1,
		ImageVersion:   "v0.1",
		CreatedAt:      s.clock.Now(),
	})
	if err != nil {
		if isDomainConflict(err) {
			return domain.Worker{}, fmt.Errorf("%w: container name %q already used", domain.ErrConflict, name)
		}
		return domain.Worker{}, fmt.Errorf("container service: create: %w", err)
	}
	s.logger.Info("container created (manual)", "workerId", w.ID, "name", w.Name, "region", region)
	return w, nil
}

// List returns every container with the accounts it hosts.
func (s *ContainerService) List(ctx context.Context) ([]ContainerView, error) {
	workers, err := s.workers.List(ctx, port.WorkerFilter{Limit: 200})
	if err != nil {
		return nil, fmt.Errorf("container service: list: %w", err)
	}
	views := make([]ContainerView, 0, len(workers))
	for i := range workers {
		w := workers[i]
		accounts, err := s.accounts.ListByWorker(ctx, w.ID)
		if err != nil {
			s.logger.Warn("container service: list accounts failed", "workerId", w.ID, "err", err)
		}
		views = append(views, toContainerView(w, accounts))
	}
	return views, nil
}

// Delete tears a container down: accounts are released first (so an AUTO
// container reaps cleanly), then the row is removed. The reconciler deletes the
// pod+PVC+service for any row still desired=RUNNING before this runs.
func (s *ContainerService) Delete(ctx context.Context, workerID string) error {
	w, err := s.workers.GetByID(ctx, workerID)
	if err != nil {
		return fmt.Errorf("container service: get: %w", err)
	}

	accounts, err := s.accounts.ListByWorker(ctx, workerID)
	if err != nil {
		return fmt.Errorf("container service: list accounts: %w", err)
	}
	for _, a := range accounts {
		if s.packer != nil {
			if err := s.packer.Release(ctx, a.ID); err != nil {
				// Keep going: a failed release must not strand the container.
				s.logger.Warn("container service: release account failed", "accountId", a.ID, "err", err)
			}
			continue
		}
		if _, err := s.accounts.Unassign(ctx, a.ID); err != nil {
			s.logger.Warn("container service: unassign failed", "accountId", a.ID, "err", err)
		}
	}

	if w.DesiredState == domain.DesiredRunning {
		// Flip to STOPPED so the reconciler deletes the pod first; the row is
		// removed below by the caller or the next sweep.
		w.DesiredState = domain.DesiredStopped
		if _, err := s.workers.Update(ctx, w); err != nil {
			return fmt.Errorf("container service: mark stopped: %w", err)
		}
	}

	if err := s.workers.Delete(ctx, workerID); err != nil {
		return fmt.Errorf("container service: delete: %w", err)
	}
	s.logger.Info("container deleted", "workerId", workerID, "source", w.Source)
	return nil
}

// ContainerView is a container with its hosted accounts (read model).
type ContainerView struct {
	ID           string
	Name         string
	DesiredState domain.DesiredState
	Source       domain.WorkerSource
	Region       string
	Status       domain.WorkerStatus
	Generation   int
	ObservedGen  *int
	Accounts     []AccountSummary
	CreatedAt    time.Time
}

// ToContainerView builds the read model for a single worker. Exported so the
// HTTP layer can shape a freshly created container the same way as a listed one.
func ToContainerView(w domain.Worker, accounts []domain.Account) ContainerView {
	return toContainerView(w, accounts)
}

func toContainerView(w domain.Worker, accounts []domain.Account) ContainerView {
	views := make([]AccountSummary, 0, len(accounts))
	for _, a := range accounts {
		views = append(views, AccountSummary{
			ID:         a.ID,
			Platform:   a.Platform,
			Username:   a.Username,
			AuthStatus: a.AuthStatus,
			Status:     a.Status,
		})
	}
	return ContainerView{
		ID:           w.ID,
		Name:         w.Name,
		DesiredState: w.DesiredState,
		Source:       w.Source,
		Region:       w.Region,
		Status:       w.Status,
		Generation:   w.Generation,
		ObservedGen:  w.ObservedGen,
		Accounts:     views,
		CreatedAt:    w.CreatedAt,
	}
}

// isDomainConflict reports whether err is the domain-level conflict sentinel.
// The repository maps a unique-constraint violation to it, so this stays free
// of driver types.
func isDomainConflict(err error) bool {
	return err == domain.ErrConflict
}
