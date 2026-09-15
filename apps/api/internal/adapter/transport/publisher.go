// Package transport implements the outbound worker transport: a durable Redis
// List per worker for action jobs, and a Redis Pub/Sub channel for ephemeral
// control messages (login, OTP, clear-session).
//
// Ordering contract (DEVELOPMENT_RULE §transport, P1-08): the caller MUST commit
// DB state BEFORE calling Enqueue/PublishControl. This package only performs the
// transport operations; it never touches the database.
package transport

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
)

// Publisher implements port.Publisher over a Redis client.
type Publisher struct {
	client *redis.Client
}

// NewPublisher wires a Publisher onto an existing Redis client.
func NewPublisher(client *redis.Client) *Publisher {
	return &Publisher{client: client}
}

// Enqueue appends a durable action job to the worker's list. LPUSH is used so
// the worker's BLPOP pops the newest job first (LIFO); if FIFO is required the
// caller uses RPUSH instead — see ActionQueueMode.
func (p *Publisher) Enqueue(ctx context.Context, workerID string, job []byte) error {
	key := domain.ActionQueue(workerID)
	if err := p.client.LPush(ctx, key, job).Err(); err != nil {
		return fmt.Errorf("transport.enqueue %s: %w", key, err)
	}
	return nil
}

// EnqueueFIFO appends a durable action job so it is consumed in arrival order
// (RPUSH + the worker's BLPOP). Use for ordered action batches.
func (p *Publisher) EnqueueFIFO(ctx context.Context, workerID string, job []byte) error {
	key := domain.ActionQueue(workerID)
	if err := p.client.RPush(ctx, key, job).Err(); err != nil {
		return fmt.Errorf("transport.enqueue %s: %w", key, err)
	}
	return nil
}

// PublishControl sends an ephemeral control message on control-<workerId>.
// Pub/Sub has no delivery guarantee: a worker that is not subscribed drops it,
// which is acceptable for control (the caller re-issues on the next heartbeat).
func (p *Publisher) PublishControl(ctx context.Context, workerID string, msg []byte) error {
	ch := domain.ControlChannel(workerID)
	if err := p.client.Publish(ctx, ch, msg).Err(); err != nil {
		return fmt.Errorf("transport.control %s: %w", ch, err)
	}
	return nil
}

// QueueDepth returns the current number of pending action jobs for a worker.
func (p *Publisher) QueueDepth(ctx context.Context, workerID string) (int64, error) {
	n, err := p.client.LLen(ctx, domain.ActionQueue(workerID)).Result()
	if err != nil {
		return 0, fmt.Errorf("transport.depth: %w", err)
	}
	return n, nil
}
