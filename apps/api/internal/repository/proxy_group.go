package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
	"github.com/ahdirmai/jg-smm/apps/api/internal/repository/sqlcgen"
)

// ProxyGroupRepo persists residential proxy pools. pool_key is write-only: it
// is stored encrypted (P1-07) and only ever written, never decrypted here.
type ProxyGroupRepo struct {
	q *sqlcgen.Queries
}

// NewProxyGroupRepo binds the repo to a sqlc query handle.
func NewProxyGroupRepo(q *sqlcgen.Queries) *ProxyGroupRepo { return &ProxyGroupRepo{q: q} }

var _ port.ProxyGroupStore = (*ProxyGroupRepo)(nil)

// GetByID returns the proxy group with the given id.
func (r *ProxyGroupRepo) GetByID(ctx context.Context, id string) (domain.ProxyGroup, error) {
	row, err := r.q.GetProxyGroupByID(ctx, uuidValue(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProxyGroup{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.ProxyGroup{}, fmt.Errorf("repository.proxy_group.GetByID: %w", err)
	}
	return toProxyGroup(row), nil
}

// List returns all proxy groups.
func (r *ProxyGroupRepo) List(ctx context.Context) ([]domain.ProxyGroup, error) {
	rows, err := r.q.ListProxyGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("repository.proxy_group.List: %w", err)
	}
	out := make([]domain.ProxyGroup, 0, len(rows))
	for _, row := range rows {
		out = append(out, toProxyGroup(row))
	}
	return out, nil
}

// Create inserts a proxy group. Duplicate name -> domain.ErrConflict.
func (r *ProxyGroupRepo) Create(ctx context.Context, g domain.ProxyGroup) (domain.ProxyGroup, error) {
	row, err := r.q.CreateProxyGroup(ctx, sqlcgen.CreateProxyGroupParams{
		ID:             uuidValue(g.ID),
		Name:           g.Name,
		Region:         g.Region,
		Provider:       g.Provider,
		PoolKey:        g.PoolKeyEnc,
		MaxConcurrency: int32(g.MaxConcurrency),
		DailyBudgetMb:  int32(g.DailyBudgetMB),
	})
	if err != nil {
		if isUniqueViolation(err) {
			return domain.ProxyGroup{}, domain.ErrConflict
		}
		return domain.ProxyGroup{}, fmt.Errorf("repository.proxy_group.Create: %w", err)
	}
	return toProxyGroup(row), nil
}

// Update persists mutable proxy group fields. pool_key is never updated here.
func (r *ProxyGroupRepo) Update(ctx context.Context, g domain.ProxyGroup) (domain.ProxyGroup, error) {
	row, err := r.q.UpdateProxyGroup(ctx, sqlcgen.UpdateProxyGroupParams{
		ID:             uuidValue(g.ID),
		Name:           g.Name,
		Region:         g.Region,
		Provider:       g.Provider,
		MaxConcurrency: int32(g.MaxConcurrency),
		DailyBudgetMb:  int32(g.DailyBudgetMB),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProxyGroup{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.ProxyGroup{}, fmt.Errorf("repository.proxy_group.Update: %w", err)
	}
	return toProxyGroup(row), nil
}

// Delete removes a proxy group. Accounts referencing it are set NULL (ON DELETE
// SET NULL), which is the desired "unassigned proxy" outcome.
func (r *ProxyGroupRepo) Delete(ctx context.Context, id string) error {
	if err := r.q.DeleteProxyGroup(ctx, uuidValue(id)); err != nil {
		return fmt.Errorf("repository.proxy_group.Delete: %w", err)
	}
	return nil
}

func toProxyGroup(r sqlcgen.ProxyGroup) domain.ProxyGroup {
	return domain.ProxyGroup{
		ID:             uuidString(r.ID),
		Name:           r.Name,
		Region:         r.Region,
		Provider:       r.Provider,
		PoolKeyEnc:     r.PoolKey,
		MaxConcurrency: int(r.MaxConcurrency),
		DailyBudgetMB:  int(r.DailyBudgetMb),
		CreatedAt:      tsTimeOrZero(r.CreatedAt),
	}
}
