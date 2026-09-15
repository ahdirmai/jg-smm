package storage

import (
	"context"
	"fmt"
)

// Health wraps a MinIO client as a port.HealthChecker so the API's readiness
// probe reports object storage alongside Postgres/Redis.
type Health struct {
	client *MinIO
}

// NewHealth wraps a MinIO client for readiness probing.
func NewHealth(client *MinIO) *Health { return &Health{client: client} }

// Ping verifies the bucket exists (and that the client can reach the endpoint).
// A missing bucket is created on first Put, so it is not a failure here.
func (h *Health) Ping(ctx context.Context) error {
	exists, err := h.client.client.BucketExists(ctx, h.client.bucket)
	if err != nil {
		return fmt.Errorf("storage: ping: %w", err)
	}
	_ = exists
	return nil
}
