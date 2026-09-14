package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/port"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/repository/sqlcgen"
)

// TeamConfigRepo stores the singleton team configuration.
type TeamConfigRepo struct {
	q *sqlcgen.Queries
}

// NewTeamConfigRepo binds the repo to a sqlc query handle (pool- or tx-bound).
func NewTeamConfigRepo(q *sqlcgen.Queries) *TeamConfigRepo {
	return &TeamConfigRepo{q: q}
}

var _ port.TeamConfigStore = (*TeamConfigRepo)(nil)

// Get returns the current team config, or domain.ErrNotFound when unset.
func (r *TeamConfigRepo) Get(ctx context.Context) (port.TeamConfig, error) {
	row, err := r.q.GetTeamConfig(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return port.TeamConfig{}, domain.ErrNotFound
	}
	if err != nil {
		return port.TeamConfig{}, fmt.Errorf("repository.team_config.Get: %w", err)
	}
	return toTeamConfig(row), nil
}

// Upsert creates the row on first call and renames it afterwards.
func (r *TeamConfigRepo) Upsert(ctx context.Context, name string) (port.TeamConfig, error) {
	row, err := r.q.UpsertTeamConfig(ctx, name)
	if err != nil {
		return port.TeamConfig{}, fmt.Errorf("repository.team_config.Upsert: %w", err)
	}
	return toTeamConfig(row), nil
}

func toTeamConfig(row sqlcgen.TeamConfig) port.TeamConfig {
	return port.TeamConfig{
		ID:        uuidString(row.ID),
		Name:      row.Name,
		CreatedAt: row.CreatedAt.Time,
	}
}
