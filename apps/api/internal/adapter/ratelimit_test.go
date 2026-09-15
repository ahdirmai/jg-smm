package adapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

// P3-10 rate limiter tests. Real Redis (SMM_TEST_REDIS=1) because the guarantee
// is the atomicity of INCR — a fake cannot prove two racers do not both take
// the last unit.

// keySuffix makes each test's counters unique so repeated runs against a shared
// dev Redis cannot collide, while staying stable within one test so a lookup
// re-finds the same counter.
func keySuffix(t *testing.T) string {
	h := sha256.Sum256([]byte(t.Name()))
	return hex.EncodeToString(h[:])[:10]
}

func newTestLimiter(t *testing.T) *RateLimiter {
	t.Helper()
	c := newTestRedis(t)
	prefix := "test-ratelimit-" + keySuffix(t)
	// A previous run against the same dev Redis can leave a counter behind, and
	// the deterministic suffix makes the collision certain. Clear the keyspace
	// so each test starts at a full budget.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if keys, err := c.Keys(ctx, prefix+":*").Result(); err == nil && len(keys) > 0 {
		if err := c.Del(ctx, keys...).Err(); err != nil {
			t.Fatalf("clear keyspace: %v", err)
		}
	}
	return NewRateLimiter(c, prefix)
}

func TestRateLimiterAllowsUpToLimit(t *testing.T) {
	lim := newTestLimiter(t)
	ctx := context.Background()

	// A budget of 3 admits exactly 3, then refuses.
	for i := 1; i <= 3; i++ {
		ok, _, err := lim.Allow(ctx, "instagram", 3, time.Hour)
		if err != nil {
			t.Fatalf("allow %d: %v", i, err)
		}
		if !ok {
			t.Fatalf("unit %d of a budget of 3 must be allowed", i)
		}
	}
	ok, retryAfter, err := lim.Allow(ctx, "instagram", 3, time.Hour)
	if err != nil {
		t.Fatalf("allow over: %v", err)
	}
	if ok {
		t.Fatal("the 4th unit must be refused — quota must not be exceeded")
	}
	if retryAfter <= 0 {
		t.Fatalf("a refused call must report how long until reset, got %v", retryAfter)
	}
}

func TestRateLimiterPlatformsIndependent(t *testing.T) {
	lim := newTestLimiter(t)
	ctx := context.Background()

	// Drain the whole Threads budget.
	for i := 0; i < 2; i++ {
		if ok, _, err := lim.Allow(ctx, "threads", 2, time.Hour); err != nil || !ok {
			t.Fatalf("threads unit %d: ok=%v err=%v", i, ok, err)
		}
	}
	// Threads is spent but Instagram is untouched: the limit is platform policy.
	ok, _, err := lim.Allow(ctx, "instagram", 2, time.Hour)
	if err != nil || !ok {
		t.Fatalf("instagram must be unaffected by the threads budget: ok=%v err=%v", ok, err)
	}
	ok, _, err = lim.Allow(ctx, "threads", 2, time.Hour)
	if err != nil || ok {
		t.Fatalf("threads must still be spent: ok=%v err=%v", ok, err)
	}
}

func TestRateLimiterWindowExpires(t *testing.T) {
	lim := newTestLimiter(t)
	ctx := context.Background()

	// A short window proves the budget actually resets. Redis floors TTLs at
	// 1s, so the test works in whole seconds.
	if ok, _, err := lim.Allow(ctx, "threads", 1, time.Second); err != nil || !ok {
		t.Fatalf("first allow: ok=%v err=%v", ok, err)
	}
	if ok, _, err := lim.Allow(ctx, "threads", 1, time.Second); err != nil || ok {
		t.Fatalf("second allow must be refused: ok=%v err=%v", ok, err)
	}
	time.Sleep(1100 * time.Millisecond)
	ok, _, err := lim.Allow(ctx, "threads", 1, time.Second)
	if err != nil || !ok {
		t.Fatalf("the budget must reset after the window: ok=%v err=%v", ok, err)
	}
}

func TestRateLimiterZeroLimitRefuses(t *testing.T) {
	lim := newTestLimiter(t)
	ctx := context.Background()
	// A zero limit means "no budget" (platform disabled), not "unlimited".
	ok, _, err := lim.Allow(ctx, "instagram", 0, time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("a zero limit must refuse rather than behave as unlimited")
	}
}

func TestRateLimiterInvalidInput(t *testing.T) {
	lim := newTestLimiter(t)
	ctx := context.Background()
	if _, _, err := lim.Allow(ctx, "", 5, time.Hour); err == nil {
		t.Error("expected error for empty platform")
	}
	if _, _, err := lim.Allow(ctx, "instagram", 5, 0); err == nil {
		t.Error("expected error for zero window")
	}
}
