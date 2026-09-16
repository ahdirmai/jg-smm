package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// ProxyGroupService manages residential proxy pools (P1-14). pool_key is a
// secret: it is sealed before the store and never returned to the client.
// Region matching binds an account to the group whose region equals the
// account's container region, so one account always egresses from one pool.
type ProxyGroupService struct {
	groups   port.ProxyGroupStore
	accounts port.AccountStore
	workers  port.WorkerStore
	sealer   port.Sealer
	clock    port.Clock
	logger   *slog.Logger
}

// ProxyGroupConfig tunes the proxy group service.
type ProxyGroupConfig struct {
	Sealer port.Sealer
	Clock  port.Clock
	Logger *slog.Logger
}

// NewProxyGroupService wires the service. sealer may be nil only when no group
// is ever created; Create rejects a missing sealer rather than storing a key
// in plaintext.
func NewProxyGroupService(
	groups port.ProxyGroupStore,
	accounts port.AccountStore,
	workers port.WorkerStore,
	cfg ProxyGroupConfig,
) *ProxyGroupService {
	if cfg.Clock == nil {
		cfg.Clock = systemClock{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &ProxyGroupService{
		groups:   groups,
		accounts: accounts,
		workers:  workers,
		sealer:   cfg.Sealer,
		clock:    cfg.Clock,
		logger:   cfg.Logger,
	}
}

// ProxyGroupInput is the create/update payload. PoolKey is plaintext in memory.
type ProxyGroupInput struct {
	Name           string
	Region         string
	Provider       string
	PoolKey        string
	MaxConcurrency int
	DailyBudgetMB  int
}

// Validate rejects input before any crypto or DB work.
func (in ProxyGroupInput) Validate() error {
	if in.Name == "" {
		return fmt.Errorf("%w: name is required", domain.ErrValidation)
	}
	if !isoRegion(in.Region) {
		return fmt.Errorf("%w: region must be ISO 3166-1 alpha-2", domain.ErrValidation)
	}
	if in.Provider == "" {
		return fmt.Errorf("%w: provider is required", domain.ErrValidation)
	}
	if in.PoolKey == "" {
		return fmt.Errorf("%w: pool key is required", domain.ErrValidation)
	}
	if in.MaxConcurrency < 1 {
		return fmt.Errorf("%w: max concurrency must be >= 1", domain.ErrValidation)
	}
	if in.DailyBudgetMB < 1 {
		return fmt.Errorf("%w: daily budget must be >= 1 MB", domain.ErrValidation)
	}
	return nil
}

// ProxyGroupSummary is the secret-free read model.
type ProxyGroupSummary struct {
	ID             string
	Name           string
	Region         string
	Provider       string
	MaxConcurrency int
	DailyBudgetMB  int
	CreatedAt      string
}

// Create stores a proxy group with the pool key sealed at rest.
func (s *ProxyGroupService) Create(ctx context.Context, in ProxyGroupInput) (ProxyGroupSummary, error) {
	if err := in.Validate(); err != nil {
		return ProxyGroupSummary{}, err
	}
	if s.sealer == nil {
		return ProxyGroupSummary{}, fmt.Errorf("%w: credential sealer not configured", domain.ErrUnavailable)
	}
	enc, err := s.sealer.Seal([]byte(in.PoolKey))
	if err != nil {
		return ProxyGroupSummary{}, fmt.Errorf("proxy group: seal pool key: %w", err)
	}
	saved, err := s.groups.Create(ctx, domain.ProxyGroup{
		Name:           in.Name,
		Region:         in.Region,
		Provider:       in.Provider,
		PoolKeyEnc:     enc,
		MaxConcurrency: in.MaxConcurrency,
		DailyBudgetMB:  in.DailyBudgetMB,
	})
	if err != nil {
		if isDomainConflict(err) {
			return ProxyGroupSummary{}, fmt.Errorf("%w: %s already exists", domain.ErrConflict, in.Name)
		}
		return ProxyGroupSummary{}, fmt.Errorf("proxy group: create: %w", err)
	}
	s.logger.Info("proxy group created", "groupId", saved.ID, "region", saved.Region)
	return toProxyGroupView(saved), nil
}

// List returns every group without keys.
func (s *ProxyGroupService) List(ctx context.Context) ([]ProxyGroupSummary, error) {
	rows, err := s.groups.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("proxy group: list: %w", err)
	}
	out := make([]ProxyGroupSummary, 0, len(rows))
	for _, g := range rows {
		out = append(out, toProxyGroupView(g))
	}
	return out, nil
}

// Get returns one group without its key.
func (s *ProxyGroupService) Get(ctx context.Context, id string) (ProxyGroupSummary, error) {
	g, err := s.groups.GetByID(ctx, id)
	if err != nil {
		return ProxyGroupSummary{}, fmt.Errorf("proxy group: get: %w", err)
	}
	return toProxyGroupView(g), nil
}

// Remove deletes a group. Accounts referencing it fall back to no proxy
// (the column is ON DELETE SET NULL), so the delete never strands an account.
func (s *ProxyGroupService) Remove(ctx context.Context, id string) error {
	if err := s.groups.Delete(ctx, id); err != nil {
		return fmt.Errorf("proxy group: delete: %w", err)
	}
	s.logger.Info("proxy group removed", "groupId", id)
	return nil
}

func toProxyGroupView(g domain.ProxyGroup) ProxyGroupSummary {
	return ProxyGroupSummary{
		ID:             g.ID,
		Name:           g.Name,
		Region:         g.Region,
		Provider:       g.Provider,
		MaxConcurrency: g.MaxConcurrency,
		DailyBudgetMB:  g.DailyBudgetMB,
		CreatedAt:      g.CreatedAt.Format(timeRFC3339),
	}
}

// isoRegion is the ISO 3166-1 alpha-2 guard the DB CHECK also enforces; the
// service rejects early so a bad region never reaches the store.
func isoRegion(region string) bool {
	if len(region) != 2 {
		return false
	}
	for _, r := range region {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}
