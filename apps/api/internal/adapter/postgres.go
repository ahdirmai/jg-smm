package adapter

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/repository/sqlcgen"
)

// Postgres wraps a pgx pool with the bits the API needs at P0-03 (health probe
// and lifecycle) plus the sqlc-backed query handle (P0-05).
type Postgres struct {
	pool    *pgxpool.Pool
	queries *sqlcgen.Queries
}

// NewPostgres connects a pool. The caller owns Close.
func NewPostgres(ctx context.Context, dsn string) (*Postgres, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("adapter.postgres: parse dsn: %w", err)
	}
	cfg.MaxConns = 10
	cfg.MinConns = 1
	cfg.MaxConnLifetime = time.Hour
	cfg.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("adapter.postgres: connect: %w", err)
	}
	return &Postgres{pool: pool, queries: sqlcgen.New(pool)}, nil
}

// Ping implements port.HealthChecker.
func (p *Postgres) Ping(ctx context.Context) error {
	return p.pool.Ping(ctx)
}

// Queries returns the sqlc query handle bound to the pool (non-transactional).
func (p *Postgres) Queries() *sqlcgen.Queries { return p.queries }

// Pool exposes the underlying pool for repositories that need raw access.
func (p *Postgres) Pool() *pgxpool.Pool { return p.pool }

// WithTx runs fn inside a transaction, committing on nil error and rolling back
// otherwise. Nested usage is not supported; the callback receives tx-bound
// Queries. This is the single transaction helper for the codebase
// (DEVELOPMENT_RULE §5).
func (p *Postgres) WithTx(ctx context.Context, fn func(q *sqlcgen.Queries) error) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("adapter.postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after a successful commit

	if err := fn(p.queries.WithTx(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("adapter.postgres: commit tx: %w", err)
	}
	return nil
}

// Close releases all connections.
func (p *Postgres) Close() {
	p.pool.Close()
}
