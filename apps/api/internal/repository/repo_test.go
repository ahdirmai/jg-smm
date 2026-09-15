package repository

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/port"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/repository/sqlcgen"
)

// These tests exercise the generated SQL against a real Postgres instance. They
// are skipped unless SMM_TEST_DB=1 is set (mirrors the SMM_TEST_REDIS convention)
// so CI on a host without a database still passes.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("SMM_TEST_DB") != "1" {
		t.Skip("set SMM_TEST_DB=1 to run repository integration tests")
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://smm:smm@localhost:5432/smm?sslmode=disable"
	}
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
	return NewWorkerRepo(q), NewAccountRepo(q), NewProvisionLogRepo(q)
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

// TestAccountAssignProvesUniqueConstraint is the core invariant: two same-platform
// accounts cannot share one container.
func TestAccountAssignProvesUniqueConstraint(t *testing.T) {
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
	t.Cleanup(func() {
		_ = accounts.Delete(ctx, "00000000-0000-0000-0000-0000000000a1")
		_ = accounts.Delete(ctx, "00000000-0000-0000-0000-0000000000a2")
		_ = workers.Delete(ctx, w.ID)
	})
	if _, err := workers.Create(ctx, w); err != nil {
		t.Fatalf("create worker: %v", err)
	}

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
	if _, err := accounts.Create(ctx, ig1); err != nil {
		t.Fatalf("create ig1: %v", err)
	}
	if _, err := accounts.Create(ctx, ig2); err != nil {
		t.Fatalf("create ig2: %v", err)
	}

	// First IG account packs fine.
	if _, err := accounts.Assign(ctx, ig1.ID, w.ID); err != nil {
		t.Fatalf("assign ig1: %v", err)
	}

	// A SECOND instagram in the SAME container must be rejected (the invariant).
	if _, err := accounts.Assign(ctx, ig2.ID, w.ID); err != domain.ErrConflict {
		t.Fatalf("expected ErrConflict for same-platform second account, got %v", err)
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

	// CountByWorker + ListByWorker reflect the packed account.
	if n, err := accounts.CountByWorker(ctx, w.ID); err != nil || n != 1 {
		t.Fatalf("count by worker = %d, err %v", n, err)
	}
	hosted, err := accounts.ListByWorker(ctx, w.ID)
	if err != nil || len(hosted) != 1 || hosted[0].ID != ig1.ID {
		t.Fatalf("list by worker wrong: %d rows", len(hosted))
	}

	// Unassigned-only filter sees ig2.
	unassigned, err := accounts.List(ctx, port.AccountFilter{UnassignedOnly: true, Limit: 100})
	if err != nil {
		t.Fatalf("list unassigned: %v", err)
	}
	found := false
	for _, a := range unassigned {
		if a.ID == ig2.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("ig2 not returned by unassigned-only filter")
	}

	// Unassign then remove.
	if _, err := accounts.Unassign(ctx, ig1.ID); err != nil {
		t.Fatalf("unassign: %v", err)
	}
	if hosted, _ := accounts.ListByWorker(ctx, w.ID); len(hosted) != 0 {
		t.Fatalf("expected 0 hosted after unassign, got %d", len(hosted))
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
	if _, err := workers.Create(ctx, w); err != nil {
		t.Fatalf("create worker: %v", err)
	}

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
