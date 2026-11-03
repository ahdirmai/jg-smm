package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// SessionRefresher (P2-06) is the API-side loop that renews worker sessions
// before their platform cookies die. It dispatches a headful `auth-login` over
// the control channel — the same message an operator's "Log in" button sends —
// and lets the existing /internal/account-callback path record the verdict.
//
// Why this shape, and not a `session_refresh` row in `scrape_job`:
//   - the ticket says "re-login via control", and the control channel is the
//     only path that can carry an interactive login. A scrape job has no worker
//     context and no way to reach a browser.
//   - the credential rides the dispatch: `authLoginPayload` unseals it once so
//     the worker's autofill can complete a re-login with no human in the loop
//     (the same delivery as an operator's Log in). Without it a dead session
//     would park at needs_input until an operator sat in the noVNC view; the
//     live-cookie case stays autonomous because the worker hydrates the session
//     it already has on disk (core/auth.ts).
//
// Failure accounting lives here rather than on the job queue because there is
// no job: a refresh either verifies (reset the streak) or does not (count it,
// and quarantine at the threshold).
type SessionRefresher struct {
	accounts  narrowRefreshStore
	transport port.Publisher
	cfg       SessionRefreshConfig
	clock     func() time.Time
	log       *slog.Logger
	sealer    port.Sealer
	// waker makes sure the target container is RUNNING before the control
	// message goes out. Optional: nil keeps a scheduler that only publishes,
	// which is right for a static driver where nothing can be started.
	waker *Waker
	// lastDispatch is the wall-clock time of the previous dispatch, for
	// Spacing. Guarded by dispatchMu so two ticks cannot interleave.
	dispatchMu  sync.Mutex
	lastDispatch time.Time
}

// narrowRefreshStore is the slice of the account store this loop needs. Narrow
// on purpose (ISP), matching narrowAccount/narrowTarget next door: a full
// port.AccountStore fake would be a dozen methods that say nothing about a
// scheduler that only lists, claims and counts.
type narrowRefreshStore interface {
	// ListDueForRefresh returns accounts whose cookie is due within the lead
	// time and that are not already mid-login.
	ListDueForRefresh(ctx context.Context, deadline, retryAfter time.Time) ([]domain.Account, error)
	// BeginRefresh marks a refresh in flight so the next tick does not
	// re-dispatch the same account while the worker is still logging in.
	BeginRefresh(ctx context.Context, accountID string) error
	// GetPasswordEnc reads the write-only credential blob so the refresh can
	// deliver it to the worker's autofill (same delivery as an operator login).
	GetPasswordEnc(ctx context.Context, id string) ([]byte, error)
	// ListStaleLoginAttempts returns rows stuck in AUTHENTICATING whose
	// callback never arrived, so the sweep can return them to a schedulable
	// state. Nothing else in the system can see them: the due query excludes
	// AUTHENTICATING precisely so it does not disturb a login in flight.
	ListStaleLoginAttempts(ctx context.Context, olderThan time.Time) ([]domain.Account, error)
	// ReclaimStaleLoginAttempt flips one such row back to FAILED, guarded on
	// the timestamp it was listed at. It reports false when the row moved on
	// in between — a real outcome arrived, and it wins.
	ReclaimStaleLoginAttempt(ctx context.Context, accountID string, expectedLastChecked time.Time) (bool, error)
}

// SessionRefreshConfig bounds the loop. Zero values fall back to defaults.
type SessionRefreshConfig struct {
	// Lead is how far ahead of expiry a refresh is dispatched. The ticket's
	// "7 hari sebelum expiry". Refreshing early is cheap and safe: the login
	// simply re-proves a live session.
	Lead time.Duration
	// RetryAfter spaces retries of a failed refresh, measured from the last
	// dispatch. Without it a hard failure (dead password, platform block) would
	// burn three attempts in as many ticks and quarantine on the spot.
	RetryAfter time.Duration
	// MaxFailures is the consecutive-failure count that quarantines an account
	// (the AC's "gagal 3x -> quarantined").
	MaxFailures int
	// TickBudget caps how many refreshes one tick dispatches.
	TickBudget int
	// TickTimeout bounds one tick's wall-clock time. Spacing makes a tick sleep
	// between dispatches, so a tick that is meant to drain a fleet runs far
	// longer than a single pass over the due list; without a bound the loop's
	// own interval (which only starts counting after the tick returns) would
	// mean the tick never yields to shutdown.
	TickTimeout time.Duration
	// MaxConcurrentLogins caps how many logins may be unresolved across the
	// whole fleet at once. TickBudget alone does not bound concurrency: it caps
	// one tick, and ticks repeat every few seconds, so with a login that takes
	// minutes to resolve the real ceiling is "however many the scheduler could
	// start before the first finished". That is how 30 headful logins came to
	// hit Instagram from one datacentre IP at the same time — the pattern that
	// gets a range rate-limited, and the rate-limiting is silent (navigations
	// start returning ERR_HTTP_RESPONSE_CODE_FAILURE while the identical URL
	// from an ordinary host returns 200).
	//
	// Zero disables the gate, which is right for a local stack serving one
	// account and wrong for a fleet on one egress.
	MaxConcurrentLogins int
	// InFlightLogins reports how many logins are currently unresolved. Injected
	// because the loop deliberately keeps its store interface narrow, and the
	// count lives in the account table's AUTHENTICATING rows.
	InFlightLogins func(ctx context.Context) (int, error)
	// Spacing is the minimum quiet time between two dispatches, measured from
	// the last one this process sent. MaxConcurrentLogins bounds how many
	// logins run AT ONCE; it does not bound the RATE. One login at a time still
	// stamps the platform every few seconds as each resolves, which from
	// Instagram's side is a steady machine pattern no human produces. Spacing
	// makes the fleet look like what it is not: a burst. Zero disables it.
	Spacing time.Duration
	// Clock is injectable; defaults to time.Now.
	Clock func() time.Time
	// Logger defaults to slog.Default.
	Logger *slog.Logger
	// Sealer unseals the account password for the dispatch (P1-11 extension);
	// nil keeps the legacy manual-only behavior.
	Sealer port.Sealer
	// Waker resumes the account's container before dispatch. Without it a
	// refresh is published onto a dead Redis channel: the worker is PAUSED
	// (that is the fleet's default), nobody is subscribed, and the message is
	// dropped silently while the account sits in AUTHENTICATING until the
	// 30-minute stale sweep gives up on it.
	Waker *Waker
}

func (c SessionRefreshConfig) withDefaults() SessionRefreshConfig {
	if c.Lead <= 0 {
		c.Lead = 7 * 24 * time.Hour
	}
	if c.RetryAfter <= 0 {
		c.RetryAfter = 6 * time.Hour
	}
	if c.MaxFailures <= 0 {
		c.MaxFailures = 3
	}
	if c.TickBudget <= 0 {
		c.TickBudget = 20
	}
	if c.Clock == nil {
		c.Clock = time.Now
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	return c
}

// NewSessionRefresher builds the loop.
func NewSessionRefresher(
	accounts narrowRefreshStore,
	transport port.Publisher,
	cfg SessionRefreshConfig,
) *SessionRefresher {
	cfg = cfg.withDefaults()
	return &SessionRefresher{
		accounts:  accounts,
		transport: transport,
		cfg:       cfg,
		clock:     cfg.Clock,
		log:       cfg.Logger,
		sealer:    cfg.Sealer,
		waker:     cfg.Waker,
	}
}

// Run loops until ctx is cancelled. One tick dispatches every due refresh, so
// the loop is idempotent: a dispatch that fails mid-tick is retried next time,
// and an account already in flight is excluded by the due query.
func (s *SessionRefresher) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Hour
	}
	s.log.Info("session refresher started",
		"interval", interval, "lead", s.cfg.Lead, "maxFailures", s.cfg.MaxFailures)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.log.Info("session refresher stopped")
			return
		case <-t.C:
			if err := s.Tick(ctx); err != nil {
				// One bad tick must never kill the loop; the next one retries.
				s.log.Error("session refresher tick failed", "err", err)
			}
		}
	}
}

// Tick dispatches one round of due refreshes. Exported for tests so the loop
// does not have to be time-driven to be exercised.
func (s *SessionRefresher) Tick(ctx context.Context) error {
	// Reclaim abandoned logins FIRST. A row stuck in AUTHENTICATING is excluded
	// by the due query below — deliberately, so an in-flight login is not
	// stamped out from under the operator — which means a login whose callback
	// never arrived would otherwise sit in that state forever, invisible to
	// every other query in the system. Recovering before dispatching also lets
	// a reclaimed account be re-dispatched on this very tick rather than the
	// next one.
	if err := s.reclaimStaleLogins(ctx); err != nil {
		// Never fatal: the recovery sweep is a safety net, and a failure to
		// sweep must not stop the refreshes that are actually due.
		s.log.Warn("session refresh: stale-login sweep failed", "err", err)
	}

	now := s.clock()
	// A refresh is due once its cookie falls inside the lead window. The
	// deadline is therefore "now + lead": anything expiring before it.
	deadline := now.Add(s.cfg.Lead)
	due, err := s.accounts.ListDueForRefresh(ctx, deadline, now.Add(-s.cfg.RetryAfter))
	if err != nil {
		return fmt.Errorf("list due refreshes: %w", err)
	}

	// Fleet-wide concurrency gate. Read BEFORE the loop so one tick cannot
	// overshoot the ceiling by counting only its own dispatches: the logins
	// already in flight from earlier ticks are the ones that matter.
	budget := s.cfg.TickBudget
	if s.cfg.MaxConcurrentLogins > 0 && s.cfg.InFlightLogins != nil {
		inFlight, err := s.cfg.InFlightLogins(ctx)
		if err != nil {
			// Failing closed is the right call here: dispatching without
			// knowing the fleet's load is exactly how the stampede happens,
			// and the accounts stay due for the next tick regardless.
			s.log.Warn("session refresh: in-flight count failed; skipping this tick", "err", err)
			return nil
		}
		if remaining := s.cfg.MaxConcurrentLogins - inFlight; remaining <= 0 {
			s.log.Info("session refresh: fleet login ceiling reached",
				"inFlight", inFlight, "max", s.cfg.MaxConcurrentLogins)
			return nil
		} else if remaining < budget {
			budget = remaining
		}
	}

	dispatched := 0
	for _, acc := range due {
		if dispatched >= budget {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// An account with no container has nowhere to log in; the due query
		// filters these, but the pointer is still dereferenced defensively
		// rather than trusted, since a claim could have unassigned it since.
		if acc.WorkerID == nil || *acc.WorkerID == "" {
			continue
		}
		// Pace the fleet: MaxConcurrentLogins caps how many run at once, not how
		// fast they start. Without this, one-at-a-time still fires a fresh
		// login every few seconds as each resolves.
		if !s.waitForSpacing(ctx) {
			return ctx.Err()
		}
		if err := s.dispatch(ctx, acc); err != nil {
			// Per-account failure, not a tick failure: log and continue so one
			// bad account cannot starve the rest of the fleet.
			s.log.Warn("session refresh dispatch failed",
				"accountId", acc.ID, "workerId", *acc.WorkerID, "err", err)
			continue
		}
		dispatched++
	}
	return nil
}

// waitForSpacing blocks until Spacing has elapsed since the last dispatch. It
// reports false when ctx was cancelled while waiting, so a shutdown does not
// hang for the remainder of the gap.
func (s *SessionRefresher) waitForSpacing(ctx context.Context) bool {
	if s.cfg.Spacing <= 0 {
		return ctx.Err() == nil
	}
	s.dispatchMu.Lock()
	wait := time.Until(s.lastDispatch.Add(s.cfg.Spacing))
	s.dispatchMu.Unlock()
	if wait > 0 {
		t := time.NewTimer(wait)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return false
		case <-t.C:
		}
	}
	s.dispatchMu.Lock()
	s.lastDispatch = s.clock()
	s.dispatchMu.Unlock()
	return true
}

// StaleLoginTimeout is how long an AUTHENTICATING row may sit without a
// callback before the sweep reclaims it.
//
// Deliberately far longer than the worker's own 180s poll: a login through the
// noVNC live view is the OPERATOR's turn, and a human working through a 2FA
// prompt or a checkpoint legitimately holds the row for minutes. Reclaiming at
// the worker's own bound would fight the human it was built to wait for. This
// is "no plausible human is still there", not "the worker is slow".
const StaleLoginTimeout = 30 * time.Minute

// reclaimStaleLogins returns abandoned AUTHENTICATING accounts to FAILED so the
// normal due query picks them up again. See StaleLoginTimeout for why the bound
// is generous, and the query docs for why the target state is FAILED.
func (s *SessionRefresher) reclaimStaleLogins(ctx context.Context) error {
	stale, err := s.accounts.ListStaleLoginAttempts(ctx, s.clock().Add(-StaleLoginTimeout))
	if err != nil {
		return fmt.Errorf("list stale login attempts: %w", err)
	}
	for _, acc := range stale {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		claimedAt := time.Time{}
		if acc.LastChecked != nil {
			claimedAt = *acc.LastChecked
		}
		reclaimed, err := s.accounts.ReclaimStaleLoginAttempt(ctx, acc.ID, claimedAt)
		if err != nil {
			s.log.Warn("session refresh: reclaim failed", "accountId", acc.ID, "err", err)
			continue
		}
		if !reclaimed {
			// The worker reported back between the list and the write. Its
			// outcome is the authority; say nothing and leave the row alone.
			continue
		}
		s.log.Warn("session refresh: reclaimed abandoned login attempt",
			"accountId", acc.ID,
			"platform", acc.Platform,
			"claimedAt", claimedAt,
		)
	}
	return nil
}

// dispatch marks the account in flight and publishes the login instruction. The
// DB write comes first (DEVELOPMENT_RULE §transport): a message that arrives
// for an account still marked AUTHENTICATED would be re-dispatched on the next
// tick, and the second login would close the first context mid-flight.
func (s *SessionRefresher) dispatch(ctx context.Context, acc domain.Account) error {
	// Bring the container up BEFORE marking the row in flight. A published
	// control message has no delivery guarantee: a PAUSED worker has no
	// subscriber, so the login would be dropped while the row said work was
	// underway. Waking first also means a wake failure leaves the row in its
	// current state for the next tick instead of stranding it in
	// AUTHENTICATING.
	if s.waker != nil {
		if err := s.waker.Wake(ctx, *acc.WorkerID); err != nil {
			return fmt.Errorf("wake worker: %w", err)
		}
	}
	if err := s.accounts.BeginRefresh(ctx, acc.ID); err != nil {
		return fmt.Errorf("mark refresh in flight: %w", err)
	}
	enc, err := s.accounts.GetPasswordEnc(ctx, acc.ID)
	if err != nil {
		return fmt.Errorf("get credential: %w", err)
	}
	msg, err := json.Marshal(controlMessage{
		Type:      "auth-login",
		AccountID: acc.ID,
		Platform:  string(acc.Platform),
		Payload:   authLoginPayload(s.sealer, acc, enc, s.log),
	})
	if err != nil {
		return fmt.Errorf("marshal control: %w", err)
	}
	if err := s.transport.PublishControl(ctx, *acc.WorkerID, msg); err != nil {
		return fmt.Errorf("publish auth-login: %w", err)
	}
	s.log.Info("session refresh dispatched",
		"accountId", acc.ID,
		"workerId", *acc.WorkerID,
		"expiresAt", acc.CookieExpiryAt,
		"failures", acc.RefreshFailures,
	)
	return nil
}
