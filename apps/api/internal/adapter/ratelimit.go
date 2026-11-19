package adapter

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
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

// allowScript takes a unit only when the budget admits it.
//
// A refused call MUST NOT consume a unit. The scheduler polls every pending
// job, so an INCR-on-refusal turns a queue larger than the budget into a
// self-starvation loop: each poll raises the counter, the window never admits
// another action, and the operator sees a batch stuck at PENDING for the whole
// hour. The DECR keeps the stored count equal to the actions actually admitted.
//
// KEYS[1] = counter, ARGV[1] = window seconds, ARGV[2] = limit.
// Returns {1, 0} when admitted, {0, ttlSeconds} when refused.
var allowScript = redis.NewScript(`
local n = redis.call('INCR', KEYS[1])
if n == 1 then redis.call('EXPIRE', KEYS[1], ARGV[1]) end
if n > tonumber(ARGV[2]) then
  redis.call('DECR', KEYS[1])
  return {0, redis.call('TTL', KEYS[1])}
end
return {1, 0}
`)

// Allow takes one unit from the platform's budget. The script is atomic, so two
// schedulers racing on the same platform cannot both take the last unit; the
// EXPIRE is only set on the first admitted unit so the window is anchored to
// the first action, not refreshed by every later one.
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
	res, err := allowScript.Run(ctx, r.client, []string{key}, int(window.Seconds()), limit).Slice()
	if err != nil {
		return false, 0, fmt.Errorf("ratelimit.Allow %s: %w", key, err)
	}
	if len(res) < 2 {
		return false, 0, fmt.Errorf("ratelimit.Allow %s: unexpected reply %v", key, res)
	}
	admitted, _ := res[0].(int64)
	seconds, _ := res[1].(int64)
	if admitted == 1 {
		return true, 0, nil
	}
	// Budget spent: report how long until the key expires so a reschedule can
	// target the reset instead of polling blindly.
	return false, time.Duration(seconds) * time.Second, nil
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
