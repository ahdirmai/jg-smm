package adapter

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Redis wraps a go-redis client with the pieces the API needs: a health probe
// and the command handle the transport Publisher / SSE hub use.
type Redis struct {
	client *redis.Client
}

// NewRedis connects a client from a redis:// URL. The caller owns Close.
func NewRedis(ctx context.Context, url string) (*Redis, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("adapter.redis: parse url: %w", err)
	}
	opt.MaxRetries = 3
	opt.DialTimeout = 5 * time.Second
	opt.ReadTimeout = 3 * time.Second
	opt.WriteTimeout = 3 * time.Second

	client := redis.NewClient(opt)
	// Fail fast on boot so a bad URL surfaces at startup, not on first use.
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("adapter.redis: ping: %w", err)
	}
	return &Redis{client: client}, nil
}

// Ping implements port.HealthChecker.
func (r *Redis) Ping(ctx context.Context) error { return r.client.Ping(ctx).Err() }

// Client exposes the underlying client for repositories/adapters.
func (r *Redis) Client() *redis.Client { return r.client }

// Close releases the connection pool.
func (r *Redis) Close() error { return r.client.Close() }
