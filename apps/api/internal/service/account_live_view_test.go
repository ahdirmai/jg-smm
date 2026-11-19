package service

import (
	"context"
	"errors"
	"testing"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
)

// Closing a live view must free the account's SQL lease.
//
// It did not exist: the Redis slot expired on its TTL but account.worker_id is
// a row someone has to write, so every account that had ever been viewed stayed
// leased forever and answered "already has a live session" from then on.
func TestCloseLiveViewReleasesLease(t *testing.T) {
	accounts := newFakeAccountStore()
	acc := accountFixture("acc-open", domain.PlatformInstagram)
	acc.AuthStatus = domain.AuthAuthenticated
	acc.WorkerID = new("w-1")
	accounts.seed(acc)

	var released []string
	pool := &PoolDispatch{
		Unlease: func(_ context.Context, accountID, workerID string) error {
			released = append(released, accountID+"@"+workerID)
			_ = accounts.ReleaseLease(context.Background(), accountID, workerID)
			return nil
		},
		Slots: slotsRecorder{probe: &authReleaseProbe{keepalives: make(chan string, 1)}},
	}

	svc := NewAccountService(accounts, newFakeWorkerStore(), nil, AccountConfig{
		PoolMode: "stateless",
		Pool:     pool,
	})

	if err := svc.CloseLiveView(context.Background(), acc.ID); err != nil {
		t.Fatalf("close: %v", err)
	}
	if len(released) != 1 || released[0] != "acc-open@w-1" {
		t.Fatalf("lease not released: %v", released)
	}
	got, _ := accounts.GetByID(context.Background(), acc.ID)
	if got.WorkerID != nil {
		t.Fatalf("account still leased after close: %v", *got.WorkerID)
	}
}

// Closing a session that holds nothing is a no-op, not an error: the operator
// may close a view twice, or close one the worker already dropped.
func TestCloseLiveViewIsIdempotent(t *testing.T) {
	accounts := newFakeAccountStore()
	accounts.seed(accountFixture("acc-idle", domain.PlatformInstagram))
	svc := NewAccountService(accounts, newFakeWorkerStore(), nil, AccountConfig{PoolMode: "stateless"})

	if err := svc.CloseLiveView(context.Background(), "acc-idle"); err != nil {
		t.Fatalf("closing an idle account must succeed: %v", err)
	}
	if err := svc.CloseLiveView(context.Background(), "acc-idle"); err != nil {
		t.Fatalf("closing twice must succeed: %v", err)
	}
}

// Closing a PARKED login must settle it, not just free the worker.
//
// The row stayed NEEDS_INPUT with worker_id NULL, so every later attempt was
// refused with "waiting for operator input; submit the code instead" — for a
// browser that had just been closed. There was no way out but a TTL.
func TestCloseLiveViewSettlesParkedLogin(t *testing.T) {
	accounts := newFakeAccountStore()
	acc := accountFixture("acc-parked", domain.PlatformInstagram)
	acc.AuthStatus = domain.AuthNeedsInput
	acc.WorkerID = new("w-3")
	accounts.seed(acc)

	pool := &PoolDispatch{
		Unlease: func(_ context.Context, accountID, workerID string) error {
			return accounts.ReleaseLease(context.Background(), accountID, workerID)
		},
		Slots: slotsRecorder{probe: &authReleaseProbe{keepalives: make(chan string, 1)}},
		RPC:   rpcRecorder{probe: &authReleaseProbe{keepalives: make(chan string, 1)}},
	}
	svc := NewAccountService(accounts, newFakeWorkerStore(), nil, AccountConfig{
		PoolMode: "stateless",
		Pool:     pool,
	})

	if err := svc.CloseLiveView(context.Background(), acc.ID); err != nil {
		t.Fatalf("close: %v", err)
	}
	got, _ := accounts.GetByID(context.Background(), acc.ID)
	if got.WorkerID != nil {
		t.Fatalf("lease survived close: %v", *got.WorkerID)
	}
	if got.AuthStatus == domain.AuthNeedsInput {
		t.Fatalf("parked login must be settled, still %q", got.AuthStatus)
	}

	// And a new login is no longer refused by the parked-login guard.
	if _, err := svc.Login(context.Background(), acc.ID); errors.Is(err, domain.ErrConflict) {
		t.Fatalf("login still refused after the parked session closed: %v", err)
	}
}

// A worker that reports "busy" with no DB lease behind it must be recoverable.
//
// This is the state that produced "conflict: worker busy (/internal/start-login)"
// while the account table was empty of leases: the Redis slot and the worker's
// in-memory context outlived the row that explained them.
func TestCloseLiveViewClearsOrphanedPoolState(t *testing.T) {
	accounts := newFakeAccountStore()
	acc := accountFixture("acc-orphan", domain.PlatformInstagram)
	accounts.seed(acc) // no WorkerID: the row is already clean

	probe := newAuthProbe()
	pool := &PoolDispatch{
		Unlease: func(context.Context, string, string) error { return nil },
		Slots:   slotsRecorder{probe: probe},
		RPC:     rpcRecorder{probe: probe},
		Workers: func(context.Context) ([]domain.Worker, error) {
			ep := "http://127.0.0.1:18090"
			return []domain.Worker{{ID: "w-9", Name: "smm-worker-09", EndpointURL: &ep}}, nil
		},
	}
	svc := NewAccountService(accounts, newFakeWorkerStore(), nil, AccountConfig{
		PoolMode: "stateless",
		Pool:     pool,
	})

	if err := svc.CloseLiveView(context.Background(), acc.ID); err != nil {
		t.Fatalf("close: %v", err)
	}
	// The worker's browser must have been asked to clear, or it keeps answering
	// 409 with the account's cookies still loaded.
	if len(probe.cleared) != 1 || probe.cleared[0] != "http://127.0.0.1:18090@acc-orphan" {
		t.Fatalf("orphaned browser not cleared: %v", probe.cleared)
	}
}

// busyProbe is a worker RPC whose healthz names whatever account a test wants.
type busyProbe struct {
	rpcRecorder
	busy string
}

func (b busyProbe) BusyAccount(context.Context, string) (string, error) { return b.busy, nil }

// preemptionFixture builds a serial login machine whose display currently shows
// heldID, and an account that wants to start a login on it.
func preemptionFixture(t *testing.T, heldStatus domain.AuthStatus, heldLeased bool) (*AccountService, *fakeAccountStore, busyProbe) {
	t.Helper()
	accounts := newFakeAccountStore()
	endpoint := "http://127.0.0.1:18090"
	workers := newFakeWorkerStore()
	workers.seed(domain.Worker{ID: "w-login", Name: "smm-login-01", EndpointURL: &endpoint})

	held := accountFixture("acc-held", domain.PlatformInstagram)
	held.AuthStatus = heldStatus
	if heldLeased {
		held.WorkerID = new("w-login")
	}
	accounts.seed(held)
	accounts.seed(accountFixture("acc-new", domain.PlatformInstagram))

	probe := busyProbe{rpcRecorder{probe: newAuthProbe()}, held.ID}
	slots := &fakeSlots{held: map[string]string{"w-login": "auth:" + held.ID}}
	pool := &PoolDispatch{
		LoginWorkerName: "smm-login-01",
		Workers:         func(context.Context) ([]domain.Worker, error) { return workers.all(), nil },
		Slots:           slots,
		Unlease:         func(_ context.Context, accountID, workerID string) error { return accounts.ReleaseLease(context.Background(), accountID, workerID) },
		RPC:             probe,
	}
	svc := NewAccountService(accounts, workers, nil, AccountConfig{PoolMode: "stateless", Pool: pool})
	return svc, accounts, probe
}

// Starting a login on the serial machine preempts a PARKED session: the old
// browser is closed, its lease and slot released, and its row settled so the
// dashboard stops pointing at a browser that no longer exists.
func TestLeasePreemptsParkedLogin(t *testing.T) {
	svc, accounts, _ := preemptionFixture(t, domain.AuthNeedsInput, true)

	if _, _, err := svc.leaseWorker(context.Background(), mustAccount(t, accounts, "acc-new")); err != nil {
		t.Fatalf("lease after parked session: %v", err)
	}
	held, _ := accounts.GetByID(context.Background(), "acc-held")
	if held.WorkerID != nil {
		t.Fatalf("held account still leased: %v", *held.WorkerID)
	}
	if held.AuthStatus != domain.AuthFailed {
		t.Fatalf("held account status = %q, want %q", held.AuthStatus, domain.AuthFailed)
	}
}

// A login that is still running is NOT preempted: killing it mid-flight leaves
// a half-authenticated account and throws away the worker's navigation.
func TestLeaseLeavesAuthenticatingLoginAlone(t *testing.T) {
	svc, accounts, _ := preemptionFixture(t, domain.AuthAuthenticating, true)

	_, _, err := svc.leaseWorker(context.Background(), mustAccount(t, accounts, "acc-new"))
	if !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("expected the machine to stay busy, got %v", err)
	}
	held, _ := accounts.GetByID(context.Background(), "acc-held")
	if held.WorkerID == nil {
		t.Fatalf("mid-login account was preempted")
	}
}

func mustAccount(t *testing.T, s *fakeAccountStore, id string) domain.Account {
	t.Helper()
	a, err := s.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("seed account %s: %v", id, err)
	}
	return a
}

// A lease left behind by a SETTLED login (AUTHENTICATED / FAILED) must not
// block the account from logging in again. The row still points at a worker
// whose display is free, so every later attempt failed with a bare "conflict"
// and there was no way out short of a manual DB edit.
func TestLoginClearsStaleLeaseOnSettledAccount(t *testing.T) {
	accounts := newFakeAccountStore()
	endpoint := "http://127.0.0.1:18090"
	workers := newFakeWorkerStore()
	workers.seed(domain.Worker{ID: "w-login", Name: "smm-login-01", EndpointURL: &endpoint})

	acc := accountFixture("acc-settled", domain.PlatformInstagram)
	acc.AuthStatus = domain.AuthAuthenticated
	acc.WorkerID = new("w-login")
	accounts.seed(acc)

	// Nobody is showing: the lease is a leftover, not a live session.
	probe := busyProbe{rpcRecorder{probe: newAuthProbe()}, ""}
	slots := &fakeSlots{held: map[string]string{}}
	pool := &PoolDispatch{
		LoginWorkerName: "smm-login-01",
		Workers:         func(context.Context) ([]domain.Worker, error) { return workers.all(), nil },
		Slots:           slots,
		Unlease: func(_ context.Context, accountID, workerID string) error {
			return accounts.ReleaseLease(context.Background(), accountID, workerID)
		},
		RPC: probe,
	}
	svc := NewAccountService(accounts, workers, nil, AccountConfig{PoolMode: "stateless", Pool: pool, Sealer: stubSealer{}})

	if _, err := svc.Login(context.Background(), "acc-settled"); err != nil {
		t.Fatalf("login on a settled account with a stale lease: %v", err)
	}
	got, _ := accounts.GetByID(context.Background(), "acc-settled")
	if got.AuthStatus != domain.AuthAuthenticating {
		t.Fatalf("auth status = %q, want AUTHENTICATING", got.AuthStatus)
	}
}
