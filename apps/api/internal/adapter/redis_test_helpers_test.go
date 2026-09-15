package adapter

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// Redis test helpers for the adapter package. Real Redis, skipped unless
// SMM_TEST_REDIS=1 (or TEST_REDIS_URL), mirroring the transport package: the
// guarantees being tested (SET NX atomicity) cannot be proven with a fake.

// testRedisURL returns a test Redis URL, or "" to skip. Defaults to the local
// compose port (24637); override with TEST_REDIS_URL.
func testRedisURL() string {
	if v := os.Getenv("TEST_REDIS_URL"); v != "" {
		return v
	}
	if os.Getenv("SMM_TEST_REDIS") == "1" {
		return "redis://localhost:24637/0"
	}
	return ""
}

// newTestRedis connects a client for one test, or skips when Redis is off. The
// caller's keys are namespaced by test (the gate prefixes its keys), and the
// client is closed on cleanup.
func newTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	url := testRedisURL()
	if url == "" {
		t.Skip("set SMM_TEST_REDIS=1 (or TEST_REDIS_URL) to run Redis integration tests")
	}
	opt, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	c := redis.NewClient(opt)
	t.Cleanup(func() { _ = c.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Ping(ctx).Err(); err != nil {
		t.Skipf("redis not reachable: %v", err)
	}
	return c
}
