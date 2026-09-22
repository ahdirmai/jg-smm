package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// ActionBatchStore implements port.ActionBatchStore on Redis: one JSON key per
// batch plus a capped index list (newest first) so recent batches list without
// a scan. Its own keyspace, separate from the queues / gates / text store.
type ActionBatchStore struct {
	client    *redis.Client
	keyPrefix string
	ttl       time.Duration
	maxIndex  int64
}

func NewActionBatchStore(client *redis.Client, keyPrefix string) *ActionBatchStore {
	if keyPrefix == "" {
		keyPrefix = "smm:actionbatch"
	}
	return &ActionBatchStore{client: client, keyPrefix: keyPrefix, ttl: 7 * 24 * time.Hour, maxIndex: 500}
}

var _ port.ActionBatchStore = (*ActionBatchStore)(nil)

func (s *ActionBatchStore) key(id string) string { return fmt.Sprintf("%s:%s", s.keyPrefix, id) }
func (s *ActionBatchStore) indexKey() string     { return s.keyPrefix + ":index" }

func (s *ActionBatchStore) Create(ctx context.Context, b port.ActionBatch) error {
	if b.ID == "" {
		return fmt.Errorf("actionbatch: id is required")
	}
	blob, err := json.Marshal(b)
	if err != nil {
		return fmt.Errorf("actionbatch.marshal: %w", err)
	}
	pipe := s.client.TxPipeline()
	pipe.Set(ctx, s.key(b.ID), blob, s.ttl)
	pipe.LPush(ctx, s.indexKey(), b.ID)
	pipe.LTrim(ctx, s.indexKey(), 0, s.maxIndex-1)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("actionbatch.create %s: %w", b.ID, err)
	}
	return nil
}

func (s *ActionBatchStore) Get(ctx context.Context, id string) (port.ActionBatch, bool, error) {
	if id == "" {
		return port.ActionBatch{}, false, nil
	}
	blob, err := s.client.Get(ctx, s.key(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return port.ActionBatch{}, false, nil
	}
	if err != nil {
		return port.ActionBatch{}, false, fmt.Errorf("actionbatch.get %s: %w", id, err)
	}
	var b port.ActionBatch
	if err := json.Unmarshal(blob, &b); err != nil {
		return port.ActionBatch{}, false, fmt.Errorf("actionbatch.unmarshal %s: %w", id, err)
	}
	return b, true, nil
}

func (s *ActionBatchStore) List(ctx context.Context, limit int) ([]port.ActionBatch, error) {
	if limit <= 0 || limit > int(s.maxIndex) {
		limit = int(s.maxIndex)
	}
	ids, err := s.client.LRange(ctx, s.indexKey(), 0, int64(limit-1)).Result()
	if err != nil {
		return nil, fmt.Errorf("actionbatch.index: %w", err)
	}
	out := make([]port.ActionBatch, 0, len(ids))
	for _, id := range ids {
		b, ok, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, b) // an expired batch (index entry outlived the TTL) is skipped
		}
	}
	return out, nil
}
