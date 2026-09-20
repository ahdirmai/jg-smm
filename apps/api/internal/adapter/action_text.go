package adapter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// ActionTextStore implements port.ActionTextStore on Redis: one string key per
// action job, holding the operator-supplied comment body until the scheduler
// composes the job (or the TTL lapses). A separate keyspace from the cooldown/
// rate gates and the transport queues.
type ActionTextStore struct {
	client    *redis.Client
	keyPrefix string
}

func NewActionTextStore(client *redis.Client, keyPrefix string) *ActionTextStore {
	if keyPrefix == "" {
		keyPrefix = "smm:actiontext"
	}
	return &ActionTextStore{client: client, keyPrefix: keyPrefix}
}

var _ port.ActionTextStore = (*ActionTextStore)(nil)

func (s *ActionTextStore) key(jobID string) string {
	return fmt.Sprintf("%s:%s", s.keyPrefix, jobID)
}

func (s *ActionTextStore) Put(ctx context.Context, jobID, text string, ttl time.Duration) error {
	if jobID == "" {
		return fmt.Errorf("actiontext: job id is required")
	}
	if ttl <= 0 {
		ttl = time.Hour
	}
	if err := s.client.Set(ctx, s.key(jobID), text, ttl).Err(); err != nil {
		return fmt.Errorf("actiontext.Set %s: %w", jobID, err)
	}
	return nil
}

func (s *ActionTextStore) Get(ctx context.Context, jobID string) (string, bool, error) {
	if jobID == "" {
		return "", false, nil
	}
	v, err := s.client.Get(ctx, s.key(jobID)).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("actiontext.Get %s: %w", jobID, err)
	}
	return v, true, nil
}
