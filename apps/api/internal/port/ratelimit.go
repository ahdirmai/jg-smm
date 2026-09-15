package port

import (
	"context"
	"time"
)

// RateLimiter (P3-10) caps actions per platform per hour, enforced by the BE
// before a job is published — the ticket is explicit that the quota is a
// platform policy, not a per-job retry flag. The point is account safety: a
// burst that exceeds a platform's tolerance is what gets a session flagged, so
// the scheduler refuses to publish rather than letting the worker discover the
// limit by being blocked.
type RateLimiter interface {
	// Allow consumes one unit from the platform's budget if the window allows
	// it. Returns allowed=true and a zero retryAfter when the action may
	// proceed; allowed=false with the time until the window resets when the
	// budget is spent.
	Allow(ctx context.Context, platform string, limit int, window time.Duration) (allowed bool, retryAfter time.Duration, err error)
}
