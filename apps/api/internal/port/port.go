package port

import (
	"context"
	"time"
)

// HealthChecker reports whether a dependency is reachable. Implemented by the
// pgx pool (and later Redis/MinIO) and consumed by the health service.
type HealthChecker interface {
	Ping(ctx context.Context) error
}

// Clock abstracts time so services and tests are deterministic.
type Clock interface {
	Now() time.Time
}

// TeamConfig is the single-team root configuration.
type TeamConfig struct {
	ID        string
	Name      string
	CreatedAt time.Time
}

// TeamConfigStore persists the singleton team configuration.
type TeamConfigStore interface {
	// Get returns the current team config, or domain.ErrNotFound when unset.
	Get(ctx context.Context) (TeamConfig, error)
	// Upsert creates the row on first call and renames it afterwards.
	Upsert(ctx context.Context, name string) (TeamConfig, error)
}
