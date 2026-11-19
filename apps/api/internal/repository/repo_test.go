package repository

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
	"github.com/ahdirmai/jg-smm/apps/api/internal/repository/sqlcgen"
)

// testDSN resolves the Postgres these tests may destroy.
//
// Every test in this package TRUNCATEs its tables. The helpers here used to
// default to `localhost:24543/smm`, which is the dev stack's published port —
// so `SMM_TEST_DB=1` with no DATABASE_URL silently wiped the running database.
// That is no longer reachable:
//
//   - SMM_TEST_DB=1 is required (skip, not fail, so a host with no DB still
//     passes CI);
//   - DATABASE_URL must be set explicitly — there is no default to fall into;
//   - and the database name must contain "test", so a real database cannot be
//     wiped by accident.
func testDSN(t *testing.T) string {
	t.Helper()
	if os.Getenv("SMM_TEST_DB") != "1" {
		t.Skip("set SMM_TEST_DB=1 to run repository integration tests")
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Fatalf("SMM_TEST_DB=1 but DATABASE_URL is empty: these tests TRUNCATE, so the target must be named explicitly — point it at a throwaway database whose name contains \"test\"")
	}
	name := dsn
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if i := strings.IndexAny(name, "?"); i >= 0 {
		name = name[:i]
	}
	if !strings.Contains(strings.ToLower(name), "test") {
		t.Fatalf("refusing to TRUNCATE database %q: the name must contain \"test\" so a real one cannot be wiped by accident", name)
	}
	return dsn
}

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := testDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func newTestRepos(t *testing.T) (worker *WorkerRepo, account *AccountRepo, logs *ProvisionLogRepo) {
	pool := testPool(t)
	q := sqlcgen.New(pool)
	return NewWorkerRepo(q, pool), NewAccountRepo(q), NewProvisionLogRepo(q)
}

// TestWorkerRoundTrip proves create/get/update/delete and the enum round trip
// (domain lower -> Postgres UPPER enum -> domain lower).
func TestWorkerRoundTrip(t *testing.T) {
	workers, _, _ := newTestRepos(t)
	ctx := context.Background()

	w := domain.Worker{
		ID:             "00000000-0000-0000-0000-000000000001",
		Name:           "repo-test-worker",
		ControlChannel: ptr(domain.ControlChannel("00000000-0000-0000-0000-000000000001")),
		ActionQueue:    ptr(domain.ActionQueue("00000000-0000-0000-0000-000000000001")),
		SessionPVC:     ptr(domain.SessionPVCName("00000000-0000-0000-0000-000000000001")),
		DesiredState:   domain.DesiredRunning,
		Source:         domain.SourceManual,
		Region:         "ID",
		Status:         domain.WorkerIdle,
		Generation:     1,
	}
	t.Cleanup(func() { _ = workers.Delete(ctx, w.ID) })

	created, err := workers.Create(ctx, w)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Status != domain.WorkerIdle || created.DesiredState != domain.DesiredRunning {
		t.Fatalf("enum round trip mismatch: %+v", created)
	}
	w.ID = created.ID
	if created.SessionPVC == nil || *created.SessionPVC != "smm-session-00000000-0000-0000-0000-000000000001" {
		t.Fatalf("session pvc not round-tripped: %v", created.SessionPVC)
	}

	got, err := workers.GetByID(ctx, w.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != w.Name {
		t.Fatalf("name mismatch: %s", got.Name)
	}

	// Update generation + observed gen (the reconciler's idempotency keys).
	got.Generation = 2
	og := 2
	got.ObservedGen = &og
	got.Status = domain.WorkerReady
	updated, err := workers.Update(ctx, got)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Generation != 2 || updated.ObservedGen == nil || *updated.ObservedGen != 2 {
		t.Fatalf("generation not persisted: %+v", updated)
	}

	// List filter (Go-side): only READY.
	ready := domain.WorkerReady
	list, err := workers.List(ctx, port.WorkerFilter{Status: &ready, Limit: 100})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].ID != w.ID {
		t.Fatalf("list filter wrong: %d rows", len(list))
	}

	// Duplicate name -> conflict.
	if _, err := workers.Create(ctx, w); err != domain.ErrConflict {
		t.Fatalf("expected ErrConflict, got %v", err)
	}

	// Heartbeat: worker row touched, sample appended.
	hb := domain.Heartbeat{WorkerID: w.ID, TS: time.Now().UTC(), CPU: 0.5, Mem: 0.6, JobsDone: 3}
	if err := workers.RecordHeartbeat(ctx, hb, port.WorkerSnapshot{
		Status:        domain.WorkerBusy,
		BrowserStatus: "ready",
		QueueDepth:    2,
	}); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	after, err := workers.GetByID(ctx, w.ID)
	if err != nil {
		t.Fatalf("get after heartbeat: %v", err)
	}
	if after.Status != domain.WorkerBusy || after.QueueDepth != 2 || after.LastHeartbeat == nil {
		t.Fatalf("heartbeat not reflected: %+v", after)
	}
}

// TestAccountLeaseProvesExclusivity is the core pool invariant. Placement
// became a lease in 000024: an account is idle (worker_id NULL) unless an
// interactive session holds a worker, and a worker hosts at most one account.
// Two Instagram accounts may therefore BOTH sit idle — the old
// UNIQUE(worker_id, platform) would have capped the fleet at 14 of them — but
// they may never hold the same worker at once.
func TestAccountLeaseProvesExclusivity(t *testing.T) {
	workers, accounts, _ := newTestRepos(t)
	ctx := context.Background()

	w := domain.Worker{
		ID:           "00000000-0000-0000-0000-000000000002",
		Name:         "repo-test-worker-2",
		DesiredState: domain.DesiredRunning,
		Source:       domain.SourceManual,
		Region:       "ID",
		Status:       domain.WorkerIdle,
		Generation:   1,
	}
	createdWorker, err := workers.Create(ctx, w)
	if err != nil {
		t.Fatalf("create worker: %v", err)
	}
	w.ID = createdWorker.ID
	t.Cleanup(func() { _ = workers.Delete(ctx, w.ID) })

	ig1 := domain.Account{
		ID:          "00000000-0000-0000-0000-0000000000a1",
		Platform:    domain.PlatformInstagram,
		Username:    "repo_test_ig_one",
		PasswordEnc: []byte("cipher-1"),
		Status:      domain.AccountPending,
	}
	ig2 := domain.Account{
		ID:          "00000000-0000-0000-0000-0000000000a2",
		Platform:    domain.PlatformInstagram,
		Username:    "repo_test_ig_two",
		PasswordEnc: []byte("cipher-2"),
		Status:      domain.AccountPending,
	}
	created1, err := accounts.Create(ctx, ig1)
	if err != nil {
		t.Fatalf("create ig1: %v", err)
	}
	ig1.ID = created1.ID
	t.Cleanup(func() { _ = accounts.Delete(ctx, ig1.ID) })
	created2, err := accounts.Create(ctx, ig2)
	if err != nil {
		t.Fatalf("create ig2: %v", err)
	}
	ig2.ID = created2.ID
	t.Cleanup(func() { _ = accounts.Delete(ctx, ig2.ID) })

	// Both accounts are idle and schedulable; neither holds a worker.
	if created1.WorkerID != nil || created2.WorkerID != nil {
		t.Fatalf("a fresh account must hold no lease: %v / %v", created1.WorkerID, created2.WorkerID)
	}

	leased, err := accounts.AcquireLease(ctx, ig1.ID, w.ID)
	if err != nil {
		t.Fatalf("acquire ig1: %v", err)
	}
	if leased.WorkerID == nil || *leased.WorkerID != w.ID {
		t.Fatalf("lease not persisted: %v", leased.WorkerID)
	}

	// Idempotent re-dispatch of the same account on the same worker.
	if _, err := accounts.AcquireLease(ctx, ig1.ID, w.ID); err != nil {
		t.Fatalf("re-acquire same pair should be a no-op, got %v", err)
	}

	// THE INVARIANT: one Xvfb display, one account. A second account on the
	// same worker conflicts even though the old platform rule would allow it
	// to wait for a different platform slot.
	if _, err := accounts.AcquireLease(ctx, ig2.ID, w.ID); err != domain.ErrConflict {
		t.Fatalf("expected ErrConflict for a second account on one worker, got %v", err)
	}

	// password_enc must never be readable: GetByID returns no ciphertext.
	got, err := accounts.GetByID(ctx, ig1.ID)
	if err != nil {
		t.Fatalf("get ig1: %v", err)
	}
	if got.Platform != domain.PlatformInstagram {
		t.Fatalf("platform round trip wrong: %q", got.Platform)
	}
	if len(got.PasswordEnc) != 0 {
		t.Fatalf("password ciphertext leaked through GetByID")
	}

	if n, err := accounts.CountByWorker(ctx, w.ID); err != nil || n != 1 {
		t.Fatalf("count by worker = %d, err %v", n, err)
	}
	hosted, err := accounts.ListByWorker(ctx, w.ID)
	if err != nil || len(hosted) != 1 || hosted[0].ID != ig1.ID {
		t.Fatalf("list by worker wrong: %d rows", len(hosted))
	}

	// Release, then the same worker is free for the next account.
	if err := accounts.ReleaseLease(ctx, ig1.ID, w.ID); err != nil {
		t.Fatalf("release ig1: %v", err)
	}
	freed, err := accounts.GetByID(ctx, ig1.ID)
	if err != nil {
		t.Fatalf("get after release: %v", err)
	}
	if freed.WorkerID != nil {
		t.Fatalf("worker_id still set after release: %v", freed.WorkerID)
	}
	if _, err := accounts.AcquireLease(ctx, ig2.ID, w.ID); err != nil {
		t.Fatalf("worker should be free for ig2 after release: %v", err)
	}

	// A stale release (wrong worker) must not free a current lease.
	if err := accounts.ReleaseLease(ctx, ig2.ID, "00000000-0000-0000-0000-0000000000ff"); err != nil {
		t.Fatalf("stale release must be a no-op, not an error: %v", err)
	}
	still, err := accounts.GetByID(ctx, ig2.ID)
	if err != nil {
		t.Fatalf("get ig2: %v", err)
	}
	if still.WorkerID == nil || *still.WorkerID != w.ID {
		t.Fatalf("a release from another worker freed the lease: %v", still.WorkerID)
	}

	if _, err := accounts.Unassign(ctx, ig2.ID); err != nil {
		t.Fatalf("unassign: %v", err)
	}
	if hosted, _ := accounts.ListByWorker(ctx, w.ID); len(hosted) != 0 {
		t.Fatalf("expected 0 hosted after unassign, got %d", len(hosted))
	}
}

// TestWorkerEndpointURLRoundTrip proves endpoint_url survives Create, the manual
// claim scan (workerCols/scanWorkerRow) and the by-name pool upsert — the column
// the whole push dispatcher dials.
func TestWorkerEndpointURLRoundTrip(t *testing.T) {
	workers, _, _ := newTestRepos(t)
	ctx := context.Background()
	name := "repo-test-worker-endpoint-" + p2Suffix(t)
	endpoint := "https://rpc." + name + ".boxd.sh"
	novnc := "https://" + name + ".boxd.sh/novnc"

	created, err := workers.Create(ctx, domain.Worker{
		Name:         name,
		DesiredState: domain.DesiredRunning,
		Source:       domain.SourceManual,
		Region:       "ID",
		Status:       domain.WorkerIdle,
		Generation:   1,
		EndpointURL:  &endpoint,
		NoVNCService: &novnc,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = workers.Delete(ctx, created.ID) })
	if created.EndpointURL == nil || *created.EndpointURL != endpoint {
		t.Fatalf("endpoint_url not persisted on create: %v", created.EndpointURL)
	}

	// Create path binds container_id, which is what GetByContainerID dials
	// through scanWorkerRow.
	container := "worker-" + name
	created.ContainerID = &container
	if _, err := workers.Update(ctx, created); err != nil {
		t.Fatalf("update: %v", err)
	}
	scanned, err := workers.GetByContainerID(ctx, container)
	if err != nil {
		t.Fatalf("get by container id: %v", err)
	}
	if scanned.EndpointURL == nil || *scanned.EndpointURL != endpoint {
		t.Fatalf("scanWorkerRow dropped endpoint_url: %v", scanned.EndpointURL)
	}

	// The by-name upsert re-points the row and forces RUNNING, without minting a
	// second row.
	created.DesiredState = domain.DesiredPaused
	if _, err := workers.Update(ctx, created); err != nil {
		t.Fatalf("pause: %v", err)
	}
	moved := "https://rpc.moved." + name + ".boxd.sh"
	upserted, err := workers.UpsertPoolWorker(ctx, domain.Worker{
		Name:        name,
		Region:      "ID",
		EndpointURL: &moved,
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if upserted.ID != created.ID {
		t.Fatalf("upsert created a new row: %s != %s", upserted.ID, created.ID)
	}
	if upserted.DesiredState != domain.DesiredRunning {
		t.Fatalf("upsert must assert RUNNING, got %v", upserted.DesiredState)
	}
	if upserted.EndpointURL == nil || *upserted.EndpointURL != moved {
		t.Fatalf("upsert did not re-point endpoint_url: %v", upserted.EndpointURL)
	}
	// A nil novnc on the upsert keeps the stored one rather than blanking it.
	if upserted.NoVNCService == nil || *upserted.NoVNCService != novnc {
		t.Fatalf("upsert must keep a stored novnc_service, got %v", upserted.NoVNCService)
	}
}

// TestProvisionLogAppend proves the audit trail records CREATE ops.
func TestProvisionLogAppend(t *testing.T) {
	workers, _, logs := newTestRepos(t)
	ctx := context.Background()

	w := domain.Worker{
		ID:           "00000000-0000-0000-0000-000000000003",
		Name:         "repo-test-worker-3",
		DesiredState: domain.DesiredRunning,
		Source:       domain.SourceAuto,
		Region:       "SG",
		Status:       domain.WorkerPending,
		Generation:   1,
	}
	t.Cleanup(func() { _ = workers.Delete(ctx, w.ID) })
	created, err := workers.Create(ctx, w)
	if err != nil {
		t.Fatalf("create worker: %v", err)
	}
	w.ID = created.ID

	if err := logs.Append(ctx, domain.ProvisionLog{
		WorkerID:   w.ID,
		Op:         domain.OpCreate,
		Generation: 1,
		Status:     domain.ProvisionApplied,
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	rows, err := logs.ListByWorker(ctx, w.ID, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 || rows[0].Op != domain.OpCreate || rows[0].Status != domain.ProvisionApplied {
		t.Fatalf("provision log row wrong: %+v", rows)
	}
}

// A batch created with a created_by that no longer exists must surface as
// ErrUnauthorized, not a raw FK violation leaking through as a 502. This is the
// stale-session case: the JWT still verifies (it is signed by us) but its user
// was deleted by a database reset, so the operator must be told to sign in
// again rather than shown an opaque "Bad Gateway".
func TestCreateKeywordBatchStaleSessionIsUnauthorized(t *testing.T) {
	pool := testPool(t)
	repo := NewKeywordBatchRepo(sqlcgen.New(pool))

	missing := "00000000-0000-0000-0000-000000000000"
	_, err := repo.CreateKeywordBatch(context.Background(), domain.KeywordBatch{
		Platform:  domain.PlatformInstagram,
		Keywords:  []string{"stale-session-test"},
		MaxPosts:  1,
		ActorID:   "test/actor",
		Status:    "PENDING",
		CreatedBy: &missing,
	})
	if !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized for a dangling created_by, got %v", err)
	}
}

// The raw-error classification the mapping above depends on: 23503 is a foreign
// key violation, and it must not be confused with the 23505 unique violation
// the same file already handles.
func TestForeignKeyViolationClassification(t *testing.T) {
	pgErr := &pgconn.PgError{Code: "23503"}
	wrapped := fmt.Errorf("repository.keyword_batch.Create: %w", pgErr)
	if !isForeignKeyViolation(wrapped) {
		t.Error("23503 wrapped in fmt.Errorf should still be recognised")
	}
	if isUniqueViolation(wrapped) {
		t.Error("23503 must not match the unique-violation classifier")
	}
	if isForeignKeyViolation(errors.New("not a pg error")) {
		t.Error("a plain error must not classify as an FK violation")
	}
}
