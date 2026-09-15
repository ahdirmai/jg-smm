package transport

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
)

// redisURL returns a test Redis URL, or "" to skip. Defaults to the local
// compose port (24637); override with TEST_REDIS_URL.
func redisURL() string {
	if v := os.Getenv("TEST_REDIS_URL"); v != "" {
		return v
	}
	if os.Getenv("SMM_TEST_REDIS") == "1" {
		return "redis://localhost:24637/0"
	}
	return ""
}

func newTestClient(t *testing.T) *redis.Client {
	t.Helper()
	url := redisURL()
	if url == "" {
		t.Skip("set SMM_TEST_REDIS=1 (or TEST_REDIS_URL) to run Redis integration tests")
	}
	opt, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	c := redis.NewClient(opt)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Ping(ctx).Err(); err != nil {
		t.Skipf("redis not reachable: %v", err)
	}
	return c
}

func TestPublisher_EnqueueAndDepth(t *testing.T) {
	c := newTestClient(t)
	defer c.Close()
	ctx := context.Background()
	defer func() { _ = c.Del(ctx, domain.ActionQueue("t-worker")).Err() }()

	p := NewPublisher(c)
	for i := 0; i < 3; i++ {
		if err := p.Enqueue(ctx, "t-worker", []byte(`{"id":"x"}`)); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}
	depth, err := p.QueueDepth(ctx, "t-worker")
	if err != nil {
		t.Fatalf("depth: %v", err)
	}
	if depth != 3 {
		t.Fatalf("want depth 3, got %d", depth)
	}
}

func TestPublisher_FIFOOrder(t *testing.T) {
	c := newTestClient(t)
	defer c.Close()
	ctx := context.Background()
	key := domain.ActionQueue("t-fifo")
	defer func() { _ = c.Del(ctx, key).Err() }()

	p := NewPublisher(c)
	for _, j := range []string{"a", "b", "c"} {
		if err := p.EnqueueFIFO(ctx, "t-fifo", []byte(j)); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}
	// Worker pops with BLPOP (left = oldest when RPUSH'd).
	for _, want := range []string{"a", "b", "c"} {
		got, err := c.LPop(ctx, key).Result()
		if err != nil {
			t.Fatalf("lpop: %v", err)
		}
		if got != want {
			t.Fatalf("FIFO order violated: want %q got %q", want, got)
		}
	}
}

func TestPublisher_PublishControlReachesSubscriber(t *testing.T) {
	c := newTestClient(t)
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	pubsub := c.Subscribe(ctx, domain.ControlChannel("t-ctl"))
	defer pubsub.Close()
	if _, err := pubsub.Receive(ctx); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	ch := pubsub.Channel()

	if err := NewPublisher(c).PublishControl(ctx, "t-ctl", []byte(`{"type":"auth-login"}`)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	select {
	case msg := <-ch:
		if msg.Channel != domain.ControlChannel("t-ctl") {
			t.Fatalf("wrong channel %q", msg.Channel)
		}
	case <-ctx.Done():
		t.Fatal("subscriber did not receive the control message")
	}
}
