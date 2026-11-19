package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// PoolDispatch is everything the scheduler needs to run the stateless fleet.
// Grouped in one struct because all four parts are meaningless alone: a push
// target without a lease is a request to the wrong machine, and a lease without
// a lock lets two workers act as one account.
type PoolDispatch struct {
	// Workers lists the fleet candidates. A port method (not a stored slice) so
	// a worker removed from the table stops being a candidate immediately.
	Workers func(ctx context.Context) ([]domain.Worker, error)
	Slots   port.WorkerSlotCoordinator
	Lock    port.AccountExecutionLock
	RPC     port.WorkerRPC
	// Leases pins and frees the SQL lease an interactive session holds. Kept as
	// funcs (not the full store) so a test can drive the pool without a database.
	Lease   func(ctx context.Context, accountID, workerID string) (domain.Account, error)
	Unlease func(ctx context.Context, accountID, workerID string) error
	// Accounts reads the current row, to see whether a lease already exists.
	Accounts func(ctx context.Context, id string) (domain.Account, error)
	// Proxy resolves the account's per-context egress; nil means direct dial,
	// which pins the account to one worker (see dispatchOne).
	Proxy    ProxyForAccount
	Sealer   port.Sealer
	Sessions port.BrowserSessionStore
	// LoginWorkerName pins EVERY interactive session (login, CAPTCHA, live
	// view) to one machine. Empty disables the split and the fleet is uniform.
	//
	// Why one: a login is the moment a platform establishes a device
	// fingerprint, and showing it a different machine per account reads as an
	// account farm. Actions are the opposite — they are ordinary page work that
	// benefits from fanning out. Keeping the two roles on separate machines also
	// means a parked CAPTCHA cannot be evicted by an action job.
	LoginWorkerName string
	// LockTTL bounds the account lock; SlotTTL bounds the worker slot. Both are
	// longer than a dispatch so a slow action does not free its slot early.
	LockTTL time.Duration
	SlotTTL time.Duration
	Log     *slog.Logger
}

func (p *PoolDispatch) log() *slog.Logger {
	if p.Log != nil {
		return p.Log
	}
	return slog.Default()
}

// ProxyForAccount resolves one account's egress. Declared here (not in port)
// because it is a single-method seam the adapter already satisfies.
type ProxyForAccount interface {
	ForAccount(ctx context.Context, accountID string, proxyGroupID *string) (*port.ProxySpec, error)
}

// NewPoolDispatch builds the dispatcher. It returns nil when the essentials are
// missing, so a half-configured boot falls back to the legacy path instead of
// dispatching actions into nothing.
func NewPoolDispatch(p PoolDispatch) *PoolDispatch {
	if p.Workers == nil || p.Slots == nil || p.Lock == nil || p.RPC == nil {
		return nil
	}
	return &p
}

// Acquire leases the pair (account lock + worker slot) a dispatch will use and
// returns the worker to push to. The caller releases both through the returned
// func: on a failed push immediately, on success when the verdict is terminal.
//
// The split from Push exists so the scheduler can decide WHAT to send (it owns
// the target lookup and the composed text) while this file owns WHERE it goes.
func (p *PoolDispatch) Acquire(ctx context.Context, job domain.ActionJob, acc domain.Account) (domain.Worker, func(), error) {
	if p == nil {
		return domain.Worker{}, nil, errors.New("pool dispatch not configured")
	}
	log := p.log()

	locked, err := p.Lock.AcquireAccount(ctx, acc.ID, job.ID, p.LockTTL)
	if err != nil {
		return domain.Worker{}, nil, fmt.Errorf("account lock: %w", err)
	}
	if !locked {
		return domain.Worker{}, nil, domain.ErrConflict
	}
	release := func() {
		if err := p.Lock.ReleaseAccount(context.WithoutCancel(ctx), acc.ID, job.ID); err != nil {
			log.Warn("pool: release account lock failed", "accountId", acc.ID, "err", err)
		}
	}

	candidates, err := p.Workers(ctx)
	if err != nil {
		release()
		return domain.Worker{}, nil, fmt.Errorf("list workers: %w", err)
	}
	// Actions never touch the login machine — not even as a fallback when the
	// action pool is fully busy. The login box carries the operator's parked
	// browser, and a job landing there answers 409 until the human is done;
	// "every action worker is busy" is backpressure, not a reason to borrow
	// the one machine that must stay interactive.
	if p.LoginWorkerName != "" {
		actions := make([]domain.Worker, 0, len(candidates))
		for _, w := range candidates {
			if w.Name != p.LoginWorkerName {
				actions = append(actions, w)
			}
		}
		candidates = actions
	}
	// The worker a login/CAPTCHA parked on must not be borrowed for an action:
	// one display, one account.
	if acc.WorkerID != nil {
		rest := make([]domain.Worker, 0, len(candidates))
		for _, w := range candidates {
			if w.ID != *acc.WorkerID {
				rest = append(rest, w)
			}
		}
		candidates = rest
	}
	if len(candidates) == 0 {
		release()
		return domain.Worker{}, nil, domain.ErrUnavailable
	}

	proxy, err := p.proxyFor(ctx, acc)
	if err != nil {
		release()
		return domain.Worker{}, nil, err
	}
	// A proxyless account egresses from whatever machine runs it, so it is
	// pinned to its affinity worker: falling through would present a new IP on
	// every job. With its own residential proxy the IP travels with the account
	// and any free worker is equivalent.
	worker, err := p.Slots.AcquireWorkerSlot(ctx, acc.ID, candidates, port.SlotAction, proxy != nil, p.SlotTTL)
	if err != nil {
		release()
		return domain.Worker{}, nil, err
	}
	slotRelease := func() {
		if err := p.Slots.ReleaseWorkerSlot(context.WithoutCancel(ctx), worker.ID, acc.ID); err != nil {
			log.Warn("pool: release worker slot failed", "workerId", worker.ID, "err", err)
		}
	}
	if worker.EndpointURL == nil || *worker.EndpointURL == "" {
		slotRelease()
		release()
		return domain.Worker{}, nil, fmt.Errorf("%w: worker %s has no endpoint url", domain.ErrUnavailable, worker.Name)
	}
	return worker, func() {
		slotRelease()
		release()
	}, nil
}

// Push sends the action over HTTPS. A failure releases nothing itself; the
// caller owns the release func and calls it on the error branch.
func (p *PoolDispatch) Push(ctx context.Context, worker domain.Worker, req port.ExecuteActionRequest) error {
	if worker.EndpointURL == nil || *worker.EndpointURL == "" {
		return fmt.Errorf("%w: worker %s has no endpoint url", domain.ErrUnavailable, worker.Name)
	}
	if err := p.RPC.ExecuteAction(ctx, *worker.EndpointURL, req); err != nil {
		return fmt.Errorf("push action to %s: %w", worker.Name, err)
	}
	p.log().Info("action pushed to pool worker",
		"job", req.JobID, "worker", worker.Name, "account", req.Runtime.AccountID)
	return nil
}

// RuntimeSpec builds the per-context identity for an account. Every field is
// resolved HERE, once, and shipped with the job: a boxd worker remembers nothing
// between requests, so anything missing from this struct does not exist there.
func (p *PoolDispatch) RuntimeSpec(ctx context.Context, acc domain.Account) (port.AccountRuntimeSpec, error) {
	spec := port.AccountRuntimeSpec{
		AccountID: acc.ID,
		Platform:  string(acc.Platform),
		Timezone:  acc.Timezone,
	}
	if spec.Timezone == "" {
		spec.Timezone = domain.DefaultTimezone
	}
	if acc.Location != nil && acc.Latitude != nil && acc.Longitude != nil {
		spec.Geolocation = &port.GeoPoint{Latitude: *acc.Latitude, Longitude: *acc.Longitude}
	}
	if acc.UserAgent != nil {
		spec.UserAgent = *acc.UserAgent
	}
	if p.Sessions != nil && p.Sealer != nil {
		sealed, err := p.Sessions.GetState(ctx, acc.ID)
		if err != nil {
			return spec, fmt.Errorf("read session: %w", err)
		}
		if len(sealed) > 0 {
			plain, err := p.Sealer.Open(sealed)
			if err != nil {
				return spec, fmt.Errorf("unseal session: %w", err)
			}
			spec.StorageState = json.RawMessage(plain)
		}
	}
	proxy, err := p.proxyFor(ctx, acc)
	if err != nil {
		return spec, err
	}
	spec.Proxy = proxy
	return spec, nil
}

// OpenBrowser navigates a manual live-view context on the worker, so the
// operator's noVNC window shows the account profile rather than a blank tab.
func (p *PoolDispatch) OpenBrowser(ctx context.Context, worker domain.Worker, spec port.AccountRuntimeSpec) error {
	if worker.EndpointURL == nil || *worker.EndpointURL == "" {
		return fmt.Errorf("%w: worker %s has no endpoint url", domain.ErrUnavailable, worker.Name)
	}
	target := ""
	if spec.Platform == string(domain.PlatformInstagram) {
		target = "https://www.instagram.com/"
	} else if spec.Platform == string(domain.PlatformThreads) {
		target = "https://www.threads.net/"
	}
	if err := p.RPC.OpenBrowser(ctx, *worker.EndpointURL, spec, target); err != nil {
		return fmt.Errorf("open browser on %s: %w", worker.Name, err)
	}
	return nil
}

// StartLogin opens the headful login context on the leased worker.
func (p *PoolDispatch) StartLogin(ctx context.Context, worker domain.Worker, spec port.AccountRuntimeSpec) error {
	if worker.EndpointURL == nil || *worker.EndpointURL == "" {
		return fmt.Errorf("%w: worker %s has no endpoint url", domain.ErrUnavailable, worker.Name)
	}
	if err := p.RPC.StartLogin(ctx, *worker.EndpointURL, spec); err != nil {
		return fmt.Errorf("start login on %s: %w", worker.Name, err)
	}
	return nil
}

// SubmitAuthInput types a 2FA / checkpoint code into the parked context.
func (p *PoolDispatch) SubmitAuthInput(ctx context.Context, worker domain.Worker, accountID string, platform domain.Platform, code string) error {
	if worker.EndpointURL == nil || *worker.EndpointURL == "" {
		return fmt.Errorf("%w: worker %s has no endpoint url", domain.ErrUnavailable, worker.Name)
	}
	if err := p.RPC.SubmitAuthInput(ctx, *worker.EndpointURL, accountID, string(platform), code); err != nil {
		return fmt.Errorf("submit auth input on %s: %w", worker.Name, err)
	}
	return nil
}

// proxyFor resolves the account's own egress, or nil for direct dial.
func (p *PoolDispatch) proxyFor(ctx context.Context, acc domain.Account) (*port.ProxySpec, error) {
	if p.Proxy == nil {
		return nil, nil
	}
	proxy, err := p.Proxy.ForAccount(ctx, acc.ID, acc.ProxyGroupID)
	if err != nil {
		return nil, fmt.Errorf("resolve proxy: %w", err)
	}
	return proxy, nil
}

// ReleaseSession hands back everything an interactive session held: the worker
// slot (so the next job may use the machine), the account's SQL lease (so the
// account is schedulable again), and — for a settled login — the browser the
// worker left parked, which would otherwise keep the account's cookies in a
// context nobody will ever close.
//
// Safe to call for an account that holds nothing: both releases are guarded and
// a worker without an endpoint is skipped.
func (p *PoolDispatch) ReleaseSession(ctx context.Context, accountID string, worker domain.Worker, clearBrowser bool) {
	if p == nil {
		return
	}
	log := p.log()
	bg := context.WithoutCancel(ctx)
	if p.Unlease != nil && worker.ID != "" {
		if err := p.Unlease(bg, accountID, worker.ID); err != nil {
			log.Warn("pool: release account lease failed", "accountId", accountID, "workerId", worker.ID, "err", err)
		}
	}
	if p.Slots != nil && worker.ID != "" {
		if err := p.Slots.ReleaseWorkerSlot(bg, worker.ID, accountID); err != nil {
			log.Warn("pool: release worker slot failed", "workerId", worker.ID, "err", err)
		}
	}
	if clearBrowser && (worker.EndpointURL == nil || *worker.EndpointURL == "" || p.RPC == nil) {
		log.Warn("pool: not clearing worker browser", "accountId", accountID,
			"hasEndpoint", worker.EndpointURL != nil, "hasRPC", p.RPC != nil, "workerId", worker.ID)
	}
	if clearBrowser && worker.EndpointURL != nil && *worker.EndpointURL != "" && p.RPC != nil {
		log.Info("pool: clearing worker browser", "accountId", accountID, "workerId", worker.Name, "endpoint", *worker.EndpointURL)
		if err := p.RPC.ClearAuth(bg, *worker.EndpointURL, accountID); err != nil {
			// The context is closed on the worker's own idle path; this is the
			// fast path, not the only one.
			log.Warn("pool: clear auth context failed", "accountId", accountID, "workerId", worker.Name, "err", err)
		}
	}
}

// KeepLoginAlive pings the worker until the login stops being NEEDS_INPUT or
// the bound elapses. A boxd microVM sleeps after ~5 idle minutes, and an
// operator reading a 2FA code can take longer than that — without the ping the
// screen goes dark mid-challenge and the parked context is lost.
//
// Runs in its own goroutine; ctx cancellation (shutdown) ends it.
func (p *PoolDispatch) KeepLoginAlive(ctx context.Context, accountID string, worker domain.Worker, interval, max time.Duration) {
	if p == nil || p.RPC == nil || worker.EndpointURL == nil || *worker.EndpointURL == "" {
		return
	}
	if interval <= 0 {
		interval = 120 * time.Second
	}
	if max <= 0 {
		max = 10 * time.Minute
	}
	go func() {
		deadline := time.Now().Add(max)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			if time.Now().After(deadline) {
				p.log().Info("pool: login keepalive expired", "accountId", accountID, "workerId", worker.Name)
				return
			}
			// The account leaving NEEDS_INPUT means the operator (or the poll)
			// settled it; RecordAuthOutcome has already released the leases.
			if p.Accounts != nil {
				acc, err := p.Accounts(ctx, accountID)
				if err != nil || acc.AuthStatus != domain.AuthNeedsInput || acc.WorkerID == nil || *acc.WorkerID != worker.ID {
					return
				}
			}
			if err := p.RPC.PingKeepalive(ctx, *worker.EndpointURL); err != nil {
				p.log().Warn("pool: keepalive ping failed", "accountId", accountID, "workerId", worker.Name, "err", err)
			}
		}
	}()
}

// ClearAccountFromPool is the recovery path for a worker that believes it is
// busy with an account nothing points at any more.
//
// It sweeps the account's Redis slots AND asks every pool worker to drop the
// account's browser context, because "busy" lives in the WORKER's memory: a slot
// released without that call leaves a machine that answers 409 to every
// dispatch with no lease anywhere to explain it.
func (p *PoolDispatch) ClearAccountFromPool(ctx context.Context, accountID string) (int, error) {
	if p == nil {
		return 0, nil
	}
	cleared := 0
	if sweeper, ok := p.Slots.(interface {
		ReleaseAccountSlots(context.Context, string) (int, error)
	}); ok {
		n, err := sweeper.ReleaseAccountSlots(ctx, accountID)
		if err != nil {
			return cleared, err
		}
		cleared = n
	}
	if p.RPC != nil && p.Workers != nil {
		workers, err := p.Workers(ctx)
		if err == nil {
			bg := context.WithoutCancel(ctx)
			for _, w := range workers {
				if w.EndpointURL == nil || *w.EndpointURL == "" {
					continue
				}
				// ClearAuth on a worker that never held the account is a no-op
				// there; the cost is one tiny request per machine.
				if err := p.RPC.ClearAuth(bg, *w.EndpointURL, accountID); err != nil {
					p.log().Warn("pool: clear orphaned browser failed",
						"accountId", accountID, "worker", w.Name, "err", err)
				}
			}
		}
	}
	return cleared, nil
}

// LoginCandidates returns the machines an INTERACTIVE session may use.
//
// With LoginWorkerName set there is exactly one: every login, CAPTCHA and live
// view lands on the same machine, so a platform sees one consistent device for
// account setup instead of a different box per account. Returns nil when no
// dedicated login worker is configured, and callers fall back to the whole pool.
func (p *PoolDispatch) LoginCandidates(ctx context.Context) ([]domain.Worker, error) {
	if p == nil || p.LoginWorkerName == "" {
		return nil, nil
	}
	all, err := p.Workers(ctx)
	if err != nil {
		return nil, err
	}
	for _, w := range all {
		if w.Name == p.LoginWorkerName {
			return []domain.Worker{w}, nil
		}
	}
	// Configured but absent from the pool: fall back so a typo degrades to the
	// old behaviour instead of making every login impossible.
	p.log().Warn("pool: configured login worker is not in the pool; using the fleet",
		"loginWorker", p.LoginWorkerName)
	return nil, nil
}
