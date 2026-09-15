package port

import (
	"context"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
)

// K8sClient is the provisioner's view of the container platform. The concrete
// implementation (k8s.io/client-go, or a static/local driver for dev) lives in
// adapter/. Keeping it behind a port lets the reconciler be unit-tested with a
// fake and lets local dev run without a cluster.
type K8sClient interface {
	// CreateWorker provisions the pod + PVC + noVNC service for a worker at the
	// given generation. It must be idempotent per (workerID, generation).
	CreateWorker(ctx context.Context, w domain.Worker) error
	// DeleteWorker removes all resources for a worker. Missing resources are not
	// an error (converges toward "absent").
	DeleteWorker(ctx context.Context, workerID string) error
	// Observe returns the currently-existing generation for a worker, or
	// (0, false) when nothing exists. Used by the reconciler's diff.
	Observe(ctx context.Context, workerID string) (generation int, exists bool, err error)
}

// Publisher pushes jobs and control messages onto the transport. Implementations
// must commit DB state BEFORE calling Publish/Enqueue (DEVELOPMENT_RULE §transport).
type Publisher interface {
	// Enqueue appends a durable action job to the worker's Redis list.
	Enqueue(ctx context.Context, workerID string, job []byte) error
	// PublishControl sends an ephemeral control message on control-<workerId>.
	PublishControl(ctx context.Context, workerID string, msg []byte) error
}
