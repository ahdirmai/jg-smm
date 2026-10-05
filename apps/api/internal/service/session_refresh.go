package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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
	// Clock is injectable; defaults to time.Now.
	Clock func() time.Time
	// Logger defaults to slog.Default.
	Logger *slog.Logger
	// Sealer unseals the account password for the dispatch (P1-11 extension);
	// nil keeps the legacy manual-only behavior.
	Sealer port.Sealer
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
	now := s.clock()
	// A refresh is due once its cookie falls inside the lead window. The
	// deadline is therefore "now + lead": anything expiring before it.
	deadline := now.Add(s.cfg.Lead)
	due, err := s.accounts.ListDueForRefresh(ctx, deadline, now.Add(-s.cfg.RetryAfter))
	if err != nil {
		return fmt.Errorf("list due refreshes: %w", err)
	}
	dispatched := 0
	for _, acc := range due {
		if dispatched >= s.cfg.TickBudget {
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

// dispatch marks the account in flight and publishes the login instruction. The
// DB write comes first (DEVELOPMENT_RULE §transport): a message that arrives
// for an account still marked AUTHENTICATED would be re-dispatched on the next
// tick, and the second login would close the first context mid-flight.
func (s *SessionRefresher) dispatch(ctx context.Context, acc domain.Account) error {
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
