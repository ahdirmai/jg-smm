package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// claimCleanup restores any row the test bound to a synthetic container id back
// to its pre-claim state (container_id IS NULL is exactly what claim selects),
// and deletes the rows the test created. Keyed by container id so cleanup is
// safe even when the test bound pre-existing dev rows.
const claimCleanupSQL = `UPDATE worker
SET container_id = NULL, control_channel = NULL, action_queue = NULL,
    session_pvc = NULL, novnc_service = NULL
WHERE container_id = ANY($1)`

// TestWorkerClaimProvesAssignment is the handshake that makes a
// dashboard-created row come alive. A scaled container knows only its hostname,
// so it claims the oldest free PENDING row; the row's real id is then what the
// queue, the control channel and every heartbeat address.
//
// Assertions are deliberately row-agnostic: a dev database usually already holds
// unclaimed PENDING rows, and claim is defined to take the OLDEST free one, so
// the test must not assume which row it wins. What it must prove is the binding
// contract — one container to one row, idempotency across restarts, and that
// the container id resolves back to the row.
func TestWorkerClaimProvesAssignment(t *testing.T) {
	workers, _, _ := newTestRepos(t)
	pool := testPool(t) // a handle for the cleanup statements (repo has no Exec)
	ctx := context.Background()

	// Rows a dashboard would create: PENDING, desired RUNNING, no container yet.
	// Distinct names keep the UNIQUE constraints honest and scope the cleanup.
	newClaimRow(t, workers, "claim-test-free")
	newClaimRow(t, workers, "claim-test-second")
	t.Cleanup(func() {
		// Unbind whatever the synthetic containers claimed first: some of it may
		// be pre-existing dev data, and leaving it bound would orphan the row.
		if _, err := pool.Exec(ctx, claimCleanupSQL,
			[]string{"worker-claim-test-a", "worker-claim-test-b"}); err != nil {
			t.Errorf("cleanup unbind: %v", err)
		}
		for _, name := range []string{"claim-test-free", "claim-test-second"} {
			if _, err := pool.Exec(ctx, `DELETE FROM worker WHERE name = $1`, name); err != nil {
				t.Errorf("cleanup %s: %v", name, err)
			}
		}
	})

	// 1. A container with no row takes the oldest free one and is bound to it.
	first, err := workers.Claim(ctx, port.WorkerClaim{
		ContainerID:    "worker-claim-test-a",
		ControlChannel: "control-worker-claim-test-a",
		ActionQueue:    "queue:action:worker-claim-test-a",
		SessionPVC:     "smm-session-worker-claim-test-a",
	})
	if err != nil {
		t.Fatalf("claim free: %v", err)
	}
	if first.ID == "" {
		t.Fatal("claim returned no row id")
	}
	if first.ContainerID == nil || *first.ContainerID != "worker-claim-test-a" {
		t.Fatalf("container_id not bound: %v", first.ContainerID)
	}
	// The row's queue must now point at the worker's live key, which is how a
	// job queued to the row reaches the container that claimed it.
	if first.ActionQueue == nil || *first.ActionQueue != "queue:action:worker-claim-test-a" {
		t.Fatalf("action queue not rebound to the worker's live key: %v", first.ActionQueue)
	}
	if first.Status != domain.WorkerPending {
		t.Fatalf("claim must not change status (the heartbeat does): %s", first.Status)
	}

	// 2. Re-claim is idempotent: a restart must reclaim the row it already owns,
	// not consume a second one (container_id is UNIQUE).
	again, err := workers.Claim(ctx, port.WorkerClaim{
		ContainerID:    "worker-claim-test-a",
		ControlChannel: "control-worker-claim-test-a",
		ActionQueue:    "queue:action:worker-claim-test-a",
		SessionPVC:     "smm-session-worker-claim-test-a",
	})
	if err != nil {
		t.Fatalf("re-claim: %v", err)
	}
	if again.ID != first.ID {
		t.Fatalf("re-claim stole another row: %s != %s", again.ID, first.ID)
	}

	// 3. A second container takes a DIFFERENT row, never the taken one.
	second, err := workers.Claim(ctx, port.WorkerClaim{
		ContainerID:    "worker-claim-test-b",
		ControlChannel: "control-worker-claim-test-b",
		ActionQueue:    "queue:action:worker-claim-test-b",
		SessionPVC:     "smm-session-worker-claim-test-b",
	})
	if err != nil {
		t.Fatalf("claim second: %v", err)
	}
	if second.ID == "" {
		t.Fatal("second claim returned no row id")
	}
	if second.ID == first.ID {
		t.Fatal("second claim took the already-claimed row")
	}
	if second.ContainerID == nil || *second.ContainerID != "worker-claim-test-b" {
		t.Fatalf("second container_id not bound: %v", second.ContainerID)
	}

	// 4. A heartbeat-style lookup by container id resolves the same row.
	if got, err := workers.GetByContainerID(ctx, "worker-claim-test-b"); err != nil || got.ID != second.ID {
		t.Fatalf("container lookup mismatch: got %v err %v", got.ID, err)
	}
	if got, err := workers.GetByContainerID(ctx, "worker-claim-test-a"); err != nil || got.ID != first.ID {
		t.Fatalf("container lookup mismatch: got %v err %v", got.ID, err)
	}
	if _, err := workers.GetByContainerID(ctx, "worker-claim-test-unknown"); err != domain.ErrNotFound {
		t.Fatalf("unknown container: want ErrNotFound, got %v", err)
	}
}

// TestWorkerClaimRefusesStolenRow covers the claim-steal gap: the by-id
// rebind path exists so a recreated container can find its own row again, but
// it must not rebind a row another live container already owns. Without the
// guard, a stale WORKER_ID puts two workers on one queue — the second one
// silently wins the heartbeat and the first is orphaned.
func TestWorkerClaimRefusesStolenRow(t *testing.T) {
	workers, _, _ := newTestRepos(t)
	pool := testPool(t)
	ctx := context.Background()

	row := newClaimRow(t, workers, "claim-test-stolen")
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, claimCleanupSQL,
			[]string{"worker-claim-owner", "worker-claim-thief"}); err != nil {
			t.Errorf("cleanup unbind: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM worker WHERE name = $1`, "claim-test-stolen"); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})

	// The legitimate owner claims the row first.
	owner, err := workers.Claim(ctx, port.WorkerClaim{
		ContainerID:    "worker-claim-owner",
		ControlChannel: "control-owner",
		ActionQueue:    "queue:action:owner",
		SessionPVC:     "smm-session-owner",
	})
	if err != nil {
		t.Fatalf("owner claim: %v", err)
	}
	if owner.ID != row.ID {
		t.Fatalf("owner claimed %s, expected the free row %s", owner.ID, row.ID)
	}

	// A container that names the SAME row by its real id — the docker driver's
	// injected WORKER_ID, which an image reuse or a row recycled mid-replace
	// can carry past its owner — must be refused: the row is owned and the
	// claimant is not the owner. The container id here IS the row uuid, which
	// is what makes the by-id path run at all.
	_, err = workers.Claim(ctx, port.WorkerClaim{
		ContainerID:    row.ID,
		ControlChannel: "control-thief",
		ActionQueue:    "queue:action:thief",
		SessionPVC:     "smm-session-thief",
	})
	if err == nil {
		t.Fatal("claim of an owned row must fail")
	}
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("want conflict, got %v", err)
	}

	// The owner keeps the row: the refused claim changed nothing.
	if got, err := workers.GetByContainerID(ctx, "worker-claim-owner"); err != nil || got.ID != row.ID {
		t.Fatalf("owner lost its row: got %v err %v", got.ID, err)
	}
	// The thief did not rebind it either.
	if got, err := workers.GetByID(ctx, row.ID); err != nil || got.ContainerID == nil {
		t.Fatalf("row unbound or missing: got %v err %v", got.ID, err)
	} else if *got.ContainerID != "worker-claim-owner" {
		t.Fatalf("row container_id = %s, want the owner's", *got.ContainerID)
	}
}

// newClaimRow inserts a dashboard-style PENDING row and returns it. The caller
// owns deletion; the test's cleanup deletes by name.
func newClaimRow(t *testing.T, workers *WorkerRepo, name string) domain.Worker {
	t.Helper()
	ctx := context.Background()

	created, err := workers.Create(ctx, domain.Worker{
		Name:         name,
		DesiredState: domain.DesiredRunning,
		Source:       domain.SourceManual,
		Region:       "ID", // CHECK worker_region_iso: exactly two uppercase letters
		Status:       domain.WorkerPending,
		Generation:   1,
		// CHECK worker_location_coords_pairing: set together or not at all.
		Location:  ptr("Jakarta, Indonesia"),
		Latitude:  ptr(-6.2088),
		Longitude: ptr(106.8456),
	})
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	return created
}
