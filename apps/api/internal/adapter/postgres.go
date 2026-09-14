package adapter

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Postgres wraps a pgx pool with the bits the API needs at P0-03 (health probe
// and lifecycle). Queries are added via sqlc in later phases.
type Postgres struct {
	pool *pgxpool.Pool
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
	return &Postgres{pool: pool}, nil
}

// Ping implements port.HealthChecker.
func (p *Postgres) Ping(ctx context.Context) error {
	return p.pool.Ping(ctx)
}

// Pool exposes the underlying pool for repositories (used from P0-04 onward).
func (p *Postgres) Pool() *pgxpool.Pool { return p.pool }

// Close releases all connections.
func (p *Postgres) Close() {
	p.pool.Close()
}
