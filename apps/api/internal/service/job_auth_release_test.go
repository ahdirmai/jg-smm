package service

import (
	"context"
	"testing"
	"time"

	"sync"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// poolAuthFixture builds a JobService in stateless mode with a leased account.
// The worker store is real enough to answer GetByID, which is how the release
// path finds the endpoint and name to hand back.
type authReleaseProbe struct {
	releasedAccounts []string
	releasedWorkers  []string
	cleared          []string
	keepalives       chan string
	// status is what the keepalive loop sees: it stops the moment the account
	// is no longer parked, so a test moves it to settle. Guarded: the loop runs
	// on its own goroutine.
	mu     sync.Mutex
	status domain.AuthStatus
}

func (p *authReleaseProbe) setStatus(s domain.AuthStatus) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status = s
}

func (p *authReleaseProbe) currentStatus() domain.AuthStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

func newAuthProbe() *authReleaseProbe {
	return &authReleaseProbe{keepalives: make(chan string, 8), status: domain.AuthNeedsInput}
}

func poolForAuth(probe *authReleaseProbe) *PoolDispatch {
	return &PoolDispatch{
		Unlease: func(_ context.Context, accountID, workerID string) error {
			probe.releasedAccounts = append(probe.releasedAccounts, accountID+"@"+workerID)
			return nil
		},
		Slots: slotsRecorder{probe: probe},
		RPC:   rpcRecorder{probe: probe},
		Accounts: func(_ context.Context, id string) (domain.Account, error) {
			return domain.Account{ID: id, AuthStatus: probe.currentStatus(), WorkerID: new("w-1")}, nil
		},
	}
}

type slotsRecorder struct{ probe *authReleaseProbe }

func (s slotsRecorder) AcquireWorkerSlot(context.Context, string, []domain.Worker, port.SlotKind, bool, time.Duration) (domain.Worker, error) {
	return domain.Worker{}, domain.ErrUnavailable
}

func (s slotsRecorder) ReleaseWorkerSlot(_ context.Context, workerID, accountID string) error {
	s.probe.releasedWorkers = append(s.probe.releasedWorkers, workerID+"@"+accountID)
	return nil
}

func (s slotsRecorder) ClearWorkerSlot(context.Context, string) error { return nil }

type rpcRecorder struct{ probe *authReleaseProbe }

func (r rpcRecorder) ExecuteAction(context.Context, string, port.ExecuteActionRequest) error {
	return nil
}

func (r rpcRecorder) StartLogin(context.Context, string, port.AccountRuntimeSpec) error { return nil }
func (r rpcRecorder) SubmitAuthInput(context.Context, string, string, string, string) error {
	return nil
}
func (r rpcRecorder) ClearAuth(_ context.Context, endpointURL, accountID string) error {
	r.probe.cleared = append(r.probe.cleared, endpointURL+"@"+accountID)
	return nil
}
func (r rpcRecorder) OpenBrowser(context.Context, string, port.AccountRuntimeSpec, string) error {
	return nil
}
func (r rpcRecorder) PingKeepalive(_ context.Context, endpointURL string) error {
	select {
	case r.probe.keepalives <- endpointURL:
	default:
	}
	return nil
}

func authFixture(t *testing.T, mode string) (*JobService, *fakeAccountStore, *fakeWorkerStore, domain.Account) {
	t.Helper()
	accounts := newFakeAccountStore()
	workers := newFakeWorkerStore()
	acc := accountFixture("acc-lease", domain.PlatformInstagram)
	acc.AuthStatus = domain.AuthAuthenticating
	acc.WorkerID = new("w-1")
	accounts.seed(acc)

	endpoint := "http://127.0.0.1:18090"
	workers.seed(domain.Worker{
		ID: "w-1", Name: "smm-worker-01", DesiredState: domain.DesiredRunning,
		Status: domain.WorkerBusy, EndpointURL: &endpoint,
	})

	jobs := NewJobService(workers, accounts, nil, nil, fixedClock{t: time.Now()}, nil, nil, nil)
	return jobs, accounts, workers, acc
}

// F4: a successful login must hand the worker AND the account back. Before the
// fix only the OTP-skip branch released anything, so worker_id and the Redis
// slot lingered until the 660s TTL while the worker kept the parked browser.
func TestPoolAuthSuccessReleasesSession(t *testing.T) {
	jobs, accounts, _, acc := authFixture(t, "stateless")
	probe := newAuthProbe()
	jobs.WithPool(PoolJobDeps{Mode: "stateless", Pool: poolForAuth(probe)})

	handle := "acct"
	if err := jobs.RecordAuthOutcome(context.Background(), AuthOutcome{
		AccountID: acc.ID, AuthStatus: domain.AuthAuthenticated, Handle: &handle,
	}); err != nil {
		t.Fatalf("record: %v", err)
	}

	if len(probe.releasedAccounts) != 1 || probe.releasedAccounts[0] != "acc-lease@w-1" {
		t.Fatalf("account lease not released: %v", probe.releasedAccounts)
	}
	if len(probe.releasedWorkers) != 1 {
		t.Fatalf("worker slot not released: %v", probe.releasedWorkers)
	}
	if len(probe.cleared) != 1 || probe.cleared[0] != "http://127.0.0.1:18090@acc-lease" {
		t.Fatalf("worker browser not cleared: %v", probe.cleared)
	}
	if got, _ := accounts.GetByID(context.Background(), acc.ID); got.AuthStatus != domain.AuthAuthenticated {
		t.Fatalf("status not recorded: %v", got.AuthStatus)
	}
}

// F4: a plain failure (not an OTP skip) is just as terminal.
func TestPoolAuthFailureNonOTPReleasesSession(t *testing.T) {
	jobs, _, _, acc := authFixture(t, "stateless")
	probe := newAuthProbe()
	jobs.WithPool(PoolJobDeps{Mode: "stateless", Pool: poolForAuth(probe)})

	reason := "password rejected"
	if err := jobs.RecordAuthOutcome(context.Background(), AuthOutcome{
		AccountID: acc.ID, AuthStatus: domain.AuthFailed, Error: &reason,
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if len(probe.releasedAccounts) != 1 || len(probe.cleared) != 1 {
		t.Fatalf("failed login must release and clear: acct=%v cleared=%v", probe.releasedAccounts, probe.cleared)
	}
}

// F4: NEEDS_INPUT is parked, not settled — the lease must SURVIVE so the
// operator's 2FA screen keeps working, and a keepalive must be started.
func TestPoolAuthNeedsInputKeepsLeaseAndPings(t *testing.T) {
	jobs, _, _, acc := authFixture(t, "stateless")
	probe := newAuthProbe()
	jobs.WithPool(PoolJobDeps{Mode: "stateless", Pool: poolForAuth(probe)})
	jobs.loginKeepaliveEvery = time.Millisecond
	jobs.loginKeepaliveMax = 50 * time.Millisecond

	reason := "otp_required: enter the code"
	if err := jobs.RecordAuthOutcome(context.Background(), AuthOutcome{
		AccountID: acc.ID, AuthStatus: domain.AuthNeedsInput, Error: &reason,
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if len(probe.releasedAccounts) != 0 {
		t.Fatalf("a parked login must keep its lease, released %v", probe.releasedAccounts)
	}
	select {
	case endpoint := <-probe.keepalives:
		if endpoint != "http://127.0.0.1:18090" {
			t.Fatalf("keepalive sent to %q", endpoint)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("no keepalive was sent while the login was parked")
	}
	// Settling the login ends the loop: an operator who finished the CAPTCHA
	// must not leave a goroutine pinging a worker forever.
	probe.setStatus(domain.AuthAuthenticated)
}

// F1: in pool mode an OTP failure must NEVER pause the shared worker, and this
// must hold even when no scheduler wired the pool at all (the bug: WithPool was
// only called inside the ACTION_INTERVAL_SECONDS>0 block).
func TestPoolOTPFailureNeverPausesWorkerWithoutScheduler(t *testing.T) {
	jobs, _, _, acc := authFixture(t, "stateless")
	jobs.WithPool(PoolJobDeps{Mode: "stateless", Pool: poolForAuth(newAuthProbe())})

	reason := "otp_required: skipped"
	if err := jobs.RecordAuthOutcome(context.Background(), AuthOutcome{
		AccountID: acc.ID, AuthStatus: domain.AuthFailed, Error: &reason,
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if jobs.isPool() != true {
		t.Fatalf("pool mode is off, so the shared worker would be pausable")
	}
}

// BusyAccount reports no owner unless a test wires one.
func (r rpcRecorder) BusyAccount(context.Context, string) (string, error) {
	return "", nil
}
