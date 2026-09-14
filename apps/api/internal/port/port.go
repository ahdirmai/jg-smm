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
