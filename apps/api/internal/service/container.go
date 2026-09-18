package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"strings"
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
	rnd      *rand.Rand
	// logs is the provisioner audit trail. Optional: nil keeps create/delete
	// working, only the per-card history stays unread.
	logs port.ProvisionLogStore
	// novncHost is the host a browser uses to reach a live view, baked into the
	// URL written to worker.novnc_service at create time.
	novncHost string
	// novncPortMin/Max bound the live-view host ports the service allocates.
	novncPortMin int
	novncPortMax int
	// stream fans container lifecycle frames to dashboards over SSE (ADR 0010).
	// Optional: a nil publisher means the write still lands, the dashboard just
	// has to poll. Create/Delete publish so every open tab sees the fleet change
	// in real time instead of on its next refresh.
	stream port.StreamPublisher
	logger *slog.Logger
}

// ContainerConfig tunes the container service.
type ContainerConfig struct {
	Clock  port.Clock
	Logger *slog.Logger
	Stream port.StreamPublisher
	// Logs stores the provisioner audit rows the dashboard reads back per card.
	Logs port.ProvisionLogStore
	// NovncHost is the host the browser resolves for a live view. Defaults to
	// localhost, which is correct for the local tier's 127.0.0.1 bindings.
	NovncHost string
	// NovncPortMin/Max bound the allocatable live-view host ports. Zero
	// disables allocation; novnc_service then stays empty until a caller sets
	// one, and the dashboard shows a disabled Live view button instead of none.
	NovncPortMin int
	NovncPortMax int
}

// NewContainerService wires the service. packer may be nil; deletion then only
// removes the row (local/static tier has no pod to reap). stream may be nil;
// the fleet then reconciles on poll instead of push.
func NewContainerService(workers port.WorkerStore, accounts port.AccountStore, packer *Packer, cfg ContainerConfig) *ContainerService {
	if cfg.Clock == nil {
		cfg.Clock = systemClock{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &ContainerService{
		workers:      workers,
		accounts:     accounts,
		packer:       packer,
		clock:        cfg.Clock,
		logs:         cfg.Logs,
		stream:       cfg.Stream,
		novncHost:    cfg.NovncHost,
		novncPortMin: cfg.NovncPortMin,
		novncPortMax: cfg.NovncPortMax,
		// Per-worker coordinates are chosen once; a fresh source per service is
		// fine because stability is per-worker, not per-process.
		rnd:    rand.New(rand.NewSource(time.Now().UnixNano())),
		logger: cfg.Logger,
	}
}

// Create inserts a MANUAL container in the RUNNING desired state. The operator
// owns its whole life: it is never auto-deleted, even at zero accounts.
// An empty name gets a generated unique one. novncPort optionally pins the host
// port the live view publishes on; nil lets the service allocate one.
func (s *ContainerService) Create(ctx context.Context, name, region, location string, novncPort *int) (domain.Worker, error) {
	if region == "" {
		return domain.Worker{}, fmt.Errorf("%w: region is required", domain.ErrValidation)
	}
	// The DB enforces region ~ '^[A-Z]{2}$' (worker_region_iso). Validate here so
	// a lowercase or wrong-length region is a 400, not a leaked driver error.
	if len(region) != 2 || region != strings.ToUpper(region) {
		return domain.Worker{}, fmt.Errorf("%w: region must be ISO 3166-1 alpha-2 (two uppercase letters)", domain.ErrValidation)
	}
	if location == "" {
		return domain.Worker{}, fmt.Errorf("%w: location is required", domain.ErrValidation)
	}
	city, ok := domain.FindCity(location)
	if !ok {
		return domain.Worker{}, fmt.Errorf("%w: unknown location %q", domain.ErrValidation, location)
	}
	// A worker is anchored to one randomized point inside its city and keeps
	// it: a stable GPS fingerprint reads as a real account, a jumping one does
	// not. See domain.City.RandomPoint.
	lat, lng := city.RandomPoint(s.rnd)
	if name == "" {
		name = newWorkerName(s.clock)
	}
	if len(name) > 64 {
		return domain.Worker{}, fmt.Errorf("%w: name too long (max 64)", domain.ErrValidation)
	}

	novncURL, err := s.resolveNovncURL(ctx, novncPort)
	if err != nil {
		return domain.Worker{}, err
	}

	w, err := s.workers.Create(ctx, domain.Worker{
		Name:           name,
		ControlChannel: ptrString(domain.ControlChannel("pending")),
		ActionQueue:    ptrString(domain.ActionQueue("pending")),
		SessionPVC:     ptrString("pending"),
		NoVNCService:   novncURL,
		DesiredState:   domain.DesiredRunning,
		Source:         domain.SourceManual,
		Region:         region,
		Location:       &location,
		Latitude:       &lat,
		Longitude:      &lng,
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
	// Push the new container to every open dashboard so the card appears
	// immediately. The caller's own POST response carries the same row, but
	// other tabs and the optimistic insert path reconcile from this frame.
	s.publishContainer(ctx, w, nil)
	return w, nil
}

// Logs returns the provisioner audit trail for one worker, newest first. The
// dashboard reads this on a PENDING card so a slow or failed provision says
// why instead of looking stuck (F-07).
func (s *ContainerService) Logs(ctx context.Context, workerID string, limit int) ([]domain.ProvisionLog, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if s.logs == nil {
		return nil, nil
	}
	logs, err := s.logs.ListByWorker(ctx, workerID, limit)
	if err != nil {
		return nil, fmt.Errorf("container service: logs: %w", err)
	}
	return logs, nil
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
	// Tell every dashboard to drop the card. The frame carries the id; the FE
	// reconciles by removal, so a concurrent refetch cannot resurrect it.
	if s.stream != nil {
		payload, err := json.Marshal(map[string]any{
			"id":      workerID,
			"removed": true,
		})
		if err == nil {
			s.stream.Publish(ctx, port.EventProvisionUpdated, payload)
		}
	}
	return nil
}

// ContainerView is a container with its hosted accounts (read model).
//
// JSON tags matter: this struct is marshaled directly onto the SSE stream
// (provision-updated, worker-health), and the browser reconciles those frames
// against the same Container type the REST API returns. The API layer maps
// through toContainerResponse, so the field names here must stay in lockstep
// with that mapping or the dashboard frames and the dashboard fetch would
// disagree about a card's shape.
type ContainerView struct {
	ID           string              `json:"id"`
	Name         string              `json:"name"`
	DesiredState domain.DesiredState `json:"desiredState"`
	Source       domain.WorkerSource `json:"source"`
	Region       string              `json:"region"`
	// Location + Latitude/Longitude are the worker's frozen GPS point: the city
	// it operates from and one randomized coordinate inside it. Empty on rows
	// created before worker geolocation existed.
	Location    *string             `json:"location"`
	Latitude    *float64            `json:"latitude"`
	Longitude   *float64            `json:"longitude"`
	Status      domain.WorkerStatus `json:"status"`
	Generation  int                 `json:"generation"`
	ObservedGen *int                `json:"observedGeneration"`
	Accounts    []AccountSummary    `json:"accounts"`
	CreatedAt   time.Time           `json:"createdAt"`
	// NovncURL is the browser-reachable live-view URL (P4-08), or empty when
	// the worker has not reported one (unpublished or pre-heartbeat).
	NovncURL string `json:"novncUrl"`
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
		Location:     w.Location,
		Latitude:     w.Latitude,
		Longitude:    w.Longitude,
		Status:       w.Status,
		Generation:   w.Generation,
		ObservedGen:  w.ObservedGen,
		Accounts:     views,
		CreatedAt:    w.CreatedAt,
		NovncURL:     derefStrPtr(w.NoVNCService),
	}
}

// isDomainConflict reports whether err is the domain-level conflict sentinel.
// The repository maps a unique-constraint violation to it, so this stays free
// of driver types.
// resolveNovncURL returns the browser-reachable live-view URL for a new worker,
// or nil when the local tier does not publish one (range unset). The port is
// persisted on the row at insert time, so the dashboard can render the button
// before the first heartbeat and the docker driver can bind it at provision
// time (REMEDIATION_PLAN R-03).
func (s *ContainerService) resolveNovncURL(ctx context.Context, requested *int) (*string, error) {
	if s.novncPortMax <= 0 {
		return nil, nil
	}
	port, err := s.allocateNovncPort(ctx, requested)
	if err != nil {
		return nil, err
	}
	host := s.novncHost
	if host == "" {
		host = "localhost"
	}
	return ptrString(fmt.Sprintf("http://%s:%d", host, port)), nil
}

// allocateNovncPort picks the live-view host port. An explicit request is
// validated against the range and against the ports every other worker already
// holds; nil takes the first free one. Ports are allocated as URLs on the row,
// so an unavailable port is reported as a 409, not a daemon error.
func (s *ContainerService) allocateNovncPort(ctx context.Context, requested *int) (int, error) {
	taken, err := s.usedNovncPorts(ctx)
	if err != nil {
		return 0, err
	}
	if requested != nil {
		// The range is the service's contract with the compose tier: the
		// operator sets NOVNC_PORT_MIN/MAX to match the published binding.
		if *requested < s.novncPortMin || *requested > s.novncPortMax {
			return 0, fmt.Errorf("%w: novncPort must be in %d-%d", domain.ErrValidation, s.novncPortMin, s.novncPortMax)
		}
		if taken[*requested] {
			return 0, fmt.Errorf("%w: novncPort %d is already used by another container", domain.ErrConflict, *requested)
		}
		return *requested, nil
	}
	for p := s.novncPortMin; p <= s.novncPortMax; p++ {
		if !taken[p] {
			return p, nil
		}
	}
	return 0, fmt.Errorf("%w: no free novnc port in %d-%d", domain.ErrConflict, s.novncPortMin, s.novncPortMax)
}

// usedNovncPorts is the set of ports already claimed by existing workers. The
// URL tail is the port; a malformed value is skipped, not fatal.
func (s *ContainerService) usedNovncPorts(ctx context.Context) (map[int]bool, error) {
	workers, err := s.workers.List(ctx, port.WorkerFilter{Limit: 500})
	if err != nil {
		return nil, fmt.Errorf("container service: list for novnc ports: %w", err)
	}
	taken := make(map[int]bool, len(workers))
	for _, w := range workers {
		if w.NoVNCService == nil {
			continue
		}
		if p := novncPortOf(*w.NoVNCService); p > 0 {
			taken[p] = true
		}
	}
	return taken, nil
}

// novncPortOf reads the trailing :port of a stored live-view URL.
func novncPortOf(url string) int {
	i := strings.LastIndex(url, ":")
	if i < 0 {
		return 0
	}
	var p int
	if _, err := fmt.Sscanf(url[i+1:], "%d", &p); err != nil {
		return 0
	}
	return p
}

func isDomainConflict(err error) bool {
	return err == domain.ErrConflict
}

// publishContainer fans the container out to dashboards as a provision-updated
// frame. The frame carries the full entity (ADR 0010) so the browser reconciles
// without a second fetch; accounts are optional and empty for a fresh create.
// A marshal failure is logged and swallowed: the write already succeeded, and
// the dashboard's next refresh still converges.
func (s *ContainerService) publishContainer(ctx context.Context, w domain.Worker, accounts []domain.Account) {
	if s.stream == nil {
		return
	}
	payload, err := json.Marshal(toContainerView(w, accounts))
	if err != nil {
		s.logger.Warn("container service: marshal stream frame failed", "workerId", w.ID, "err", err)
		return
	}
	s.stream.Publish(ctx, port.EventProvisionUpdated, payload)
}
