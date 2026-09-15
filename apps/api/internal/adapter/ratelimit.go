package adapter

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/port"
)

// RateLimiter implements port.RateLimiter on Redis with a fixed-window counter:
// INCR a per-(platform, window) key, and EXPIRE it when the counter is new. A
// fixed window can overspend by at most `limit` once, at a boundary rollover —
// acceptable for a self-hosted tool whose goal is keeping a session well under
// a platform's tolerance, not metering to the unit.
type RateLimiter struct {
	client    *redis.Client
	keyPrefix string
}

// NewRateLimiter binds the limiter to a redis client. keyPrefix scopes the
// counter keyspace so it never collides with cooldown or queue keys.
func NewRateLimiter(client *redis.Client, keyPrefix string) *RateLimiter {
	if keyPrefix == "" {
		keyPrefix = "ratelimit"
	}
	return &RateLimiter{client: client, keyPrefix: keyPrefix}
}

var _ port.RateLimiter = (*RateLimiter)(nil)

// Allow takes one unit from the platform's budget. The INCR is atomic, so two
// schedulers racing on the same platform cannot both take the last unit; the
// EXPIRE is only set on the first increment so the window is anchored to the
// first action, not refreshed by every later one.
func (r *RateLimiter) Allow(ctx context.Context, platform string, limit int, window time.Duration) (bool, time.Duration, error) {
	if platform == "" {
		return false, 0, fmt.Errorf("ratelimit: platform is required")
	}
	if limit <= 0 {
		// A zero/negative limit means "no budget" — the caller configured the
		// platform off, so refuse rather than treating it as unlimited.
		return false, window, nil
	}
	if window <= 0 {
		return false, 0, fmt.Errorf("ratelimit: window must be positive, got %s", window)
	}
	key := r.key(platform, window)
	n, err := r.client.Incr(ctx, key).Result()
	if err != nil {
		return false, 0, fmt.Errorf("ratelimit.Incr %s: %w", key, err)
	}
	// Anchor the window only on the first unit; a boundary reset then clears
	// the whole budget at once.
	if n == 1 {
		if err := r.client.Expire(ctx, key, window).Err(); err != nil {
			return false, 0, fmt.Errorf("ratelimit.Expire %s: %w", key, err)
		}
		return true, 0, nil
	}
	if n > int64(limit) {
		// Budget spent: tell the caller how long until the key expires so a
		// reschedule can target the reset instead of polling blindly.
		ttl, err := r.client.TTL(ctx, key).Result()
		if err != nil {
			return false, 0, fmt.Errorf("ratelimit.TTL %s: %w", key, err)
		}
		return false, ttl, nil
	}
	return true, 0, nil
}

// key is stable per (prefix, platform, window): the window is quantised to the
// hour so an hourly budget is one counter, and the platform keeps its own
// counter — the limits are platform policy, never global.
func (r *RateLimiter) key(platform string, window time.Duration) string {
	hours := int64(window / time.Hour)
	if hours < 1 {
		hours = 1
	}
	return r.keyPrefix + ":" + platform + ":" + strconv.FormatInt(hours, 10) + "h"
}
