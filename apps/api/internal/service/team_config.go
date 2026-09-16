package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// TeamConfigService owns the singleton team configuration lifecycle.
type TeamConfigService struct {
	store port.TeamConfigStore
}

// NewTeamConfigService wires the store.
func NewTeamConfigService(store port.TeamConfigStore) *TeamConfigService {
	return &TeamConfigService{store: store}
}

// Get returns the current config, creating the default on first boot.
func (s *TeamConfigService) Get(ctx context.Context) (port.TeamConfig, error) {
	cfg, err := s.store.Get(ctx)
	if !errors.Is(err, domain.ErrNotFound) {
		return cfg, err
	}
	return s.store.Upsert(ctx, "Team")
}

// Rename validates and persists a new team name.
func (s *TeamConfigService) Rename(ctx context.Context, name string) (port.TeamConfig, error) {
	if err := validateTeamName(name); err != nil {
		return port.TeamConfig{}, err
	}
	return s.store.Upsert(ctx, name)
}

func validateTeamName(name string) error {
	if len(name) == 0 || len(name) > 120 {
		return fmt.Errorf("%w: name must be 1-120 chars", domain.ErrValidation)
	}
	for _, r := range name {
		if r < 0x20 {
			return fmt.Errorf("%w: name contains control characters", domain.ErrValidation)
		}
	}
	return nil
}
