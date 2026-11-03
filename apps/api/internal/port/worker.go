package port

import (
	"context"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
)

// ContainerState is what Observe reports about one worker's platform presence:
// nothing exists, something exists but is stopped (resumable), or it is
// running. The reconciler's diff needs all three — a PAUSED worker whose
// container is already stopped is in sync and must not be stopped again every
// tick.
type ContainerState int

const (
	// ContainerAbsent: no container/pod exists for the worker.
	ContainerAbsent ContainerState = iota
	// ContainerStopped: the container exists but is not running; its volumes
	// (and session) survive, so StartWorker resumes it.
	ContainerStopped
	// ContainerRunning: the container is up.
	ContainerRunning
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
	// Observe returns the generation on the platform for a worker and whether
	// it is running. Used by the reconciler's diff. A stopped container is
	// (gen, ContainerStopped): it still exists and can be resumed.
	Observe(ctx context.Context, workerID string) (generation int, state ContainerState, err error)
	// ListRunning returns the IDs of every worker the driver currently observes
	// as existing. Used by the orphan sweeper: any ID here without a Worker row
	// is a resource leaking on the platform.
	ListRunning(ctx context.Context) ([]string, error)
	// StopWorker halts a container WITHOUT removing it or its volumes, so a
	// later StartWorker resumes the same instance (and the session on it).
	// Idempotent: stopping an already-stopped or missing container is success,
	// because the desired end state is "not running".
	StopWorker(ctx context.Context, workerID string) error
	// StartWorker resumes a stopped container that still exists. The Worker row
	// is passed in because some drivers (k8s) must rebuild the pod from it, and
	// none of them hold a store. Idempotent like StopWorker. A container that
	// was never created is an error, not a silent success: the caller asked to
	// wake something that is not there.
	StartWorker(ctx context.Context, w domain.Worker) error
}

// Publisher pushes jobs and control messages onto the transport. Implementations
// must commit DB state BEFORE calling Publish/Enqueue (DEVELOPMENT_RULE §transport).
type Publisher interface {
	// Enqueue appends a durable action job to the worker's Redis list.
	Enqueue(ctx context.Context, workerID string, job []byte) error
	// PublishControl sends an ephemeral control message on control-<workerId>.
	PublishControl(ctx context.Context, workerID string, msg []byte) error
}

// Sealer encrypts and decrypts credential blobs at rest. Services depend on
// this port; the concrete AES-256-GCM implementation lives in adapter/crypto
// and satisfies it structurally (P1-07).
type Sealer interface {
	Seal(plaintext []byte) ([]byte, error)
	Open(ciphertext []byte) ([]byte, error)
}
