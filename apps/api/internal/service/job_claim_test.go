package service

import (
	"context"
	"errors"
	"testing"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// claimStore wraps the shared fakeWorkerStore and overrides only the claim path.
// The fake hardcodes Claim -> ErrNotFound, which cannot express "the handshake
// succeeded"; everything else is inherited so the stub stays one line of logic.
type claimStore struct {
	*fakeWorkerStore
	claimed *port.WorkerClaim
	free    bool
	loc     *string
	lat     *float64
	long    *float64
}

// Claim mirrors the repo's contract: return the row when one is free for this
// container, ErrNotFound when the fleet is fully claimed.
func (s *claimStore) Claim(ctx context.Context, claim port.WorkerClaim) (domain.Worker, error) {
	if s.claimed != nil {
		*s.claimed = claim
	}
	if !s.free {
		return domain.Worker{}, domain.ErrNotFound
	}
	w := domain.Worker{
		ID:        "10000000-0000-0000-0000-000000000001",
		Name:      "jakarta-01",
		Region:    "ID",
		Location:  s.loc,
		Latitude:  s.lat,
		Longitude: s.long,
	}
	id := claim.ContainerID
	w.ContainerID = &id
	w.ControlChannel = &claim.ControlChannel
	w.ActionQueue = &claim.ActionQueue
	return w, nil
}

// floatPtr is the pointer helper the GPS fields need; the service-level
// helpers cover int/time, not float64.
func floatPtr(v float64) *float64 { return &v }

// TestClaimNoFreeRow: with nothing free the service surfaces ErrNotFound so the
// worker logs "unassigned" and keeps its boot id instead of crashing. This is
// the case the dev-database integration test cannot cover (it always has rows).
func TestClaimNoFreeRow(t *testing.T) {
	svc := NewJobService(&claimStore{fakeWorkerStore: newFakeWorkerStore()}, nil, nil, nil, fixedClock{}, nil, nil, nil)
	_, err := svc.Claim(context.Background(), WorkerClaim{ContainerID: "worker-none"})
	if err != domain.ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// TestClaimAdoptsRowAndGPS: the worker must receive the row's real id plus the
// frozen GPS point it will spoof before its first job.
func TestClaimAdoptsRowAndGPS(t *testing.T) {
	loc := "Jakarta, Indonesia"
	svc := NewJobService(&claimStore{
		fakeWorkerStore: newFakeWorkerStore(),
		free:            true,
		loc:             &loc,
		lat:             floatPtr(-6.2088),
		long:            floatPtr(106.8456),
	}, nil, nil, nil, fixedClock{}, nil, nil, nil)

	got, err := svc.Claim(context.Background(), WorkerClaim{ContainerID: "worker-jakarta-01"})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if got.WorkerID != "10000000-0000-0000-0000-000000000001" {
		t.Fatalf("workerId: %s", got.WorkerID)
	}
	if got.Name != "jakarta-01" || got.Region != "ID" {
		t.Fatalf("identity mismatch: %+v", got)
	}
	if got.Location != loc || got.Latitude != -6.2088 || got.Longitude != 106.8456 {
		t.Fatalf("gps not carried: %+v", got)
	}
}

// TestClaimRequiresContainerID: an empty boot id can never be bound (the UNIQUE
// constraint on container_id would otherwise let a container steal any row).
func TestClaimRequiresContainerID(t *testing.T) {
	svc := NewJobService(&claimStore{fakeWorkerStore: newFakeWorkerStore(), free: true}, nil, nil, nil, fixedClock{}, nil, nil, nil)
	_, err := svc.Claim(context.Background(), WorkerClaim{})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("want ErrValidation, got %v", err)
	}
}

// TestClaimDerivesRedisKeys: when the worker sends only its hostname the service
// derives the channel/queue/pvc from it, so the row's keys match the keys the
// worker actually subscribes to.
func TestClaimDerivesRedisKeys(t *testing.T) {
	var captured port.WorkerClaim
	svc := NewJobService(&claimStore{
		fakeWorkerStore: newFakeWorkerStore(),
		free:            true,
		claimed:         &captured,
	}, nil, nil, nil, fixedClock{}, nil, nil, nil)

	if _, err := svc.Claim(context.Background(), WorkerClaim{ContainerID: "worker-derive"}); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if captured.ContainerID != "worker-derive" {
		t.Fatalf("containerId: %s", captured.ContainerID)
	}
	if captured.ControlChannel != domain.ControlChannel("worker-derive") {
		t.Fatalf("control channel not derived: %s", captured.ControlChannel)
	}
	if captured.ActionQueue != domain.ActionQueue("worker-derive") {
		t.Fatalf("action queue not derived: %s", captured.ActionQueue)
	}
	if captured.SessionPVC != domain.SessionPVCName("worker-derive") {
		t.Fatalf("session pvc not derived: %s", captured.SessionPVC)
	}
}
