package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// P2-06: the two ACs are "job triggers on time" and "3 failures ->
// quarantined". Both are proven here over the in-memory fakes, so the loop's
// scheduling and the streak's arithmetic are pinned without a DB or Redis.

var refreshStart = time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

// fakeRefreshStore is the narrow store the refresher needs. It implements the
// due query's contract (inside the lead window, skip in-flight) rather than
// mirroring SQL, so the test asserts the LOOP against the query's meaning.
type fakeRefreshStore struct {
	accounts *fakeAccountStore
	begun    []string
	// now is the clock the due filter and the write path share, so a test moves
	// time by advancing it rather than by sleeping.
	now *time.Time
}

func (f *fakeRefreshStore) ListDueForRefresh(_ context.Context, deadline, retryAfter time.Time) ([]domain.Account, error) {
	var out []domain.Account
	for _, a := range f.accounts.accounts {
		switch {
		case a.CookieExpiryAt == nil:
			continue // undated session cookie: not schedulable
		case !a.CookieExpiryAt.Before(deadline) && !a.CookieExpiryAt.Equal(deadline):
			continue // outside the lead window
		case a.AuthStatus != domain.AuthAuthenticated && a.AuthStatus != domain.AuthFailed:
			continue // already mid-login
		case a.Status != domain.AccountActive:
			continue // quarantined/archived: out of the pool
		case a.WorkerID == nil:
			continue // nowhere to log in
		case a.LastChecked != nil && a.LastChecked.After(retryAfter):
			continue // inside the retry backoff
		}
		out = append(out, a)
	}
	return out, nil
}

func (f *fakeRefreshStore) BeginRefresh(_ context.Context, accountID string) error {
	f.begun = append(f.begun, accountID)
	a, ok := f.accounts.accounts[accountID]
	if !ok {
		return domain.ErrNotFound
	}
	now := *f.now
	a.AuthStatus = domain.AuthAuthenticating
	a.LastChecked = &now
	f.accounts.accounts[accountID] = a
	return nil
}

// GetPasswordEnc delegates to the underlying account store: the refresher's
// delivery uses the same narrow read as the operator login.
func (f *fakeRefreshStore) GetPasswordEnc(ctx context.Context, id string) ([]byte, error) {
	return f.accounts.GetPasswordEnc(ctx, id)
}

// ListStaleLoginAttempts mirrors the query's contract: only AUTHENTICATING rows
// whose last_checked_at is at or before the cutoff. The cutoff being the thing
// under test, the fake keeps the comparison honest rather than filtering on a
// hardcoded age.
func (f *fakeRefreshStore) ListStaleLoginAttempts(_ context.Context, olderThan time.Time) ([]domain.Account, error) {
	var out []domain.Account
	for _, a := range f.accounts.accounts {
		if a.AuthStatus != domain.AuthAuthenticating {
			continue
		}
		if a.LastChecked == nil || a.LastChecked.After(olderThan) {
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

// ReclaimStaleLoginAttempt mirrors the guarded UPDATE: it only fires when the
// row is still AUTHENTICATING and still carries the timestamp it was listed at,
// and reports whether it did. The FAILED (not AUTHENTICATED) target is part of
// the contract — a reclaimed attempt has an UNPROVEN session.
func (f *fakeRefreshStore) ReclaimStaleLoginAttempt(_ context.Context, accountID string, expectedLastChecked time.Time) (bool, error) {
	a, ok := f.accounts.accounts[accountID]
	if !ok {
		return false, domain.ErrNotFound
	}
	if a.AuthStatus != domain.AuthAuthenticating {
		return false, nil
	}
	if a.LastChecked == nil || !a.LastChecked.Equal(expectedLastChecked) {
		return false, nil
	}
	a.AuthStatus = domain.AuthFailed
	errMsg := "login attempt abandoned: no callback from the worker"
	a.LastError = &errMsg
	a.CookieExpiryAt = nil
	f.accounts.accounts[accountID] = a
	return true, nil
}

// refreshFixture wires the refresher + the JobService that consumes its
// callbacks over one store and one clock — the full loop the AC describes.
// Moving `now` forward stands in for the passage of time.
func refreshFixture(t *testing.T) (*SessionRefresher, *JobService, *fakeRefreshStore, *recordingPublisher) {
	t.Helper()
	accounts := newFakeAccountStore()
	now := refreshStart
	store := &fakeRefreshStore{accounts: accounts, now: &now}
	pub := &recordingPublisher{}
	refresher := NewSessionRefresher(store, pub, SessionRefreshConfig{
		Clock:  func() time.Time { return now },
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Sealer: stubSealer{},
	})
	jobs := NewJobService(nil, accounts, nil, nil, fixedClockFn(func() time.Time { return now }),
		nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return refresher, jobs, store, pub
}

// fixedClockFn adapts a closure to port.Clock, so the refresher and the job
// service read the same moving time.
type fixedClockFn func() time.Time

func (f fixedClockFn) Now() time.Time { return f() }

// seedExpiring inserts an authenticated account whose cookie dies in `in`.
func seedExpiring(accounts *fakeAccountStore, id string, in time.Duration) domain.Account {
	expiry := refreshStart.Add(in)
	worker := "worker-1"
	enc, _ := stubSealer{}.Seal([]byte("refresh-pw"))
	return accounts.seedLocked(domain.Account{
		ID:             id,
		Platform:       domain.PlatformInstagram,
		Username:       "growthco-" + id,
		PasswordEnc:    enc,
		AuthStatus:     domain.AuthAuthenticated,
		Status:         domain.AccountActive,
		WorkerID:       &worker,
		CookieExpiryAt: &expiry,
	})
}

// TestSessionRefreshTriggersInsideLeadWindow is the first AC: an account whose
// cookie dies inside the lead window is dispatched, one outside it is not.
func TestSessionRefreshTriggersInsideLeadWindow(t *testing.T) {
	refresher, _, store, pub := refreshFixture(t)

	due := seedExpiring(store.accounts, "acc-due", 6*24*time.Hour) // inside 7d
	seedExpiring(store.accounts, "acc-far", 30*24*time.Hour)       // outside
	undated := store.accounts.seedLocked(domain.Account{           // no expiry
		ID: "acc-undated", Platform: domain.PlatformThreads, Username: "u",
		AuthStatus: domain.AuthAuthenticated, Status: domain.AccountActive,
		WorkerID: func() *string { s := "worker-1"; return &s }(),
	})

	if err := refresher.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	if len(pub.control) != 1 {
		t.Fatalf("control messages = %d, want 1 (%v)", len(pub.control), store.begun)
	}
	var msg controlMessage
	if err := json.Unmarshal(pub.last(), &msg); err != nil {
		t.Fatalf("control body: %v", err)
	}
	if msg.Type != "auth-login" || msg.AccountID != due.ID {
		t.Fatalf("control = %+v, want auth-login for %s", msg, due.ID)
	}
	// The refresh delivers the unsealed credential (P1-11 extension) so the
	// worker's autofill can complete a re-login with no human in the loop.
	if msg.Payload["username"] != due.Username || msg.Payload["password"] != "refresh-pw" {
		t.Fatalf("payload = %v, want the unsealed credentials", msg.Payload)
	}
	// The DB write precedes the publish, so a crash between the two cannot
	// leave a message in flight for an account still marked AUTHENTICATED.
	if got := store.accounts.get(due.ID).AuthStatus; got != domain.AuthAuthenticating {
		t.Errorf("auth_status = %q, want AUTHENTICATING", got)
	}
	if store.accounts.get("acc-far").AuthStatus != domain.AuthAuthenticated {
		t.Error("account outside the lead window was dispatched")
	}
	if store.accounts.get(undated.ID).AuthStatus != domain.AuthAuthenticated {
		t.Error("undated session cookie was dispatched; it cannot be scheduled")
	}
}

// TestSessionRefreshBacksOffAfterDispatch: a second tick must not re-dispatch
// an account already mid-login (the context would be closed under the first
// login) nor one inside the retry window.
func TestSessionRefreshBacksOffAfterDispatch(t *testing.T) {
	refresher, _, store, pub := refreshFixture(t)
	seedExpiring(store.accounts, "acc-1", 6*24*time.Hour)

	if err := refresher.Tick(context.Background()); err != nil {
		t.Fatalf("first tick: %v", err)
	}
	if err := refresher.Tick(context.Background()); err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if len(pub.control) != 1 {
		t.Fatalf("control messages = %d, want 1 (re-dispatched mid-login)", len(pub.control))
	}
}

// TestSessionRefreshQuarantinesAfterThreeFailures is the second AC: three
// consecutive failed refreshes take the account out of the packable pool, and
// one good session in between resets the streak.
func TestSessionRefreshQuarantinesAfterThreeFailures(t *testing.T) {
	refresher, jobs, store, pub := refreshFixture(t)
	seedExpiring(store.accounts, "acc-1", 6*24*time.Hour)
	ctx := context.Background()
	fail := func() {
		for _, id := range store.begun {
			if err := jobs.RecordAuthOutcome(ctx, AuthOutcome{
				AccountID:  id,
				AuthStatus: domain.AuthFailed,
				Error:      strPtr("session expired"),
			}); err != nil {
				t.Fatalf("record failure: %v", err)
			}
		}
		store.begun = nil
	}

	for i := 1; i <= 3; i++ {
		// Move past the retry backoff, otherwise the tick is suppressed by the
		// gate and this would assert nothing.
		*store.now = store.now.Add(7 * time.Hour)
		if err := refresher.Tick(ctx); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
		if len(store.begun) == 0 {
			t.Fatalf("tick %d dispatched nothing", i)
		}
		fail()
		acc := store.accounts.get("acc-1")
		if acc.Status == domain.AccountQuarantined && i < 3 {
			t.Fatalf("quarantined after %d failures, want 3", i)
		}
		if i < 3 && acc.RefreshFailures != i {
			t.Fatalf("failures = %d after tick %d, want %d", acc.RefreshFailures, i, i)
		}
	}

	acc := store.accounts.get("acc-1")
	if acc.Status != domain.AccountQuarantined {
		t.Fatalf("status = %q after 3 failures, want QUARANTINED", acc.Status)
	}
	if acc.IsPackable() {
		t.Error("quarantined account still packable")
	}

	// A quarantined account is out of the due set: the loop must stop paying
	// for it, and only an operator Resume puts it back.
	before := len(pub.control)
	if err := refresher.Tick(ctx); err != nil {
		t.Fatalf("tick after quarantine: %v", err)
	}
	if len(pub.control) != before {
		t.Error("quarantined account was re-dispatched")
	}
}

// TestSessionRefreshSuccessResetsStreak: the AC counts CONSECUTIVE failures, so
// a verified login two failures in must clear the count rather than leave the
// account one failure from quarantine.
func TestSessionRefreshSuccessResetsStreak(t *testing.T) {
	refresher, jobs, store, _ := refreshFixture(t)
	seedExpiring(store.accounts, "acc-1", 6*24*time.Hour)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if err := refresher.Tick(ctx); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
		if err := jobs.RecordAuthOutcome(ctx, AuthOutcome{
			AccountID: "acc-1", AuthStatus: domain.AuthFailed, Error: strPtr("expired"),
		}); err != nil {
			t.Fatalf("record failure: %v", err)
		}
	}
	if got := store.accounts.get("acc-1").RefreshFailures; got != 2 {
		t.Fatalf("failures = %d, want 2", got)
	}

	next := refreshStart.Add(30 * 24 * time.Hour)
	if err := jobs.RecordAuthOutcome(ctx, AuthOutcome{
		AccountID:       "acc-1",
		AuthStatus:      domain.AuthAuthenticated,
		CookieExpiresAt: &next,
	}); err != nil {
		t.Fatalf("record success: %v", err)
	}
	acc := store.accounts.get("acc-1")
	if acc.RefreshFailures != 0 {
		t.Errorf("failures = %d after a verified login, want 0", acc.RefreshFailures)
	}
	if acc.Status != domain.AccountActive {
		t.Errorf("status = %q, want ACTIVE", acc.Status)
	}
	if acc.CookieExpiryAt == nil || !acc.CookieExpiryAt.Equal(next) {
		t.Errorf("cookie expiry = %v, want the freshly reported %v", acc.CookieExpiryAt, next)
	}
}

// TestSessionRefreshSuccessClearsRecordedError: a verified login must clear
// `last_error`, not just the failure streak. The column is written on every
// failure but was never cleared on success, so an account that failed once and
// then logged in cleanly kept the message forever — AUTHENTICATED rows in the
// live DB carried "login did not complete" from a stale attempt.
func TestSessionRefreshSuccessClearsRecordedError(t *testing.T) {
	_, jobs, store, _ := refreshFixture(t)
	seedExpiring(store.accounts, "acc-1", 6*24*time.Hour)
	ctx := context.Background()

	if err := jobs.RecordAuthOutcome(ctx, AuthOutcome{
		AccountID: "acc-1", AuthStatus: domain.AuthFailed, Error: strPtr("login did not complete"),
	}); err != nil {
		t.Fatalf("record failure: %v", err)
	}
	if got := store.accounts.get("acc-1"); got.LastError == nil {
		t.Fatal("last_error not recorded on failure; fixture is not exercising the bug")
	}

	next := refreshStart.Add(30 * 24 * time.Hour)
	if err := jobs.RecordAuthOutcome(ctx, AuthOutcome{
		AccountID:       "acc-1",
		AuthStatus:      domain.AuthAuthenticated,
		CookieExpiresAt: &next,
	}); err != nil {
		t.Fatalf("record success: %v", err)
	}
	if got := store.accounts.get("acc-1").LastError; got != nil {
		t.Errorf("last_error = %q after a verified login, want cleared", *got)
	}
}

// TestSessionRefreshParkedLoginIsNotAFailure: a login parked for 2FA is the
// operator's turn, not a dead session. Counting it would march a healthy
// account toward quarantine while a human is mid-typing.
func TestSessionRefreshParkedLoginIsNotAFailure(t *testing.T) {
	_, jobs, store, _ := refreshFixture(t)
	seedExpiring(store.accounts, "acc-1", 6*24*time.Hour)

	if err := jobs.RecordAuthOutcome(context.Background(), AuthOutcome{
		AccountID: "acc-1", AuthStatus: domain.AuthNeedsInput, Error: strPtr("2FA required"),
	}); err != nil {
		t.Fatalf("record needs-input: %v", err)
	}
	acc := store.accounts.get("acc-1")
	if acc.RefreshFailures != 0 {
		t.Errorf("failures = %d after a parked login, want 0", acc.RefreshFailures)
	}
	if acc.Status != domain.AccountActive {
		t.Errorf("status = %q after a parked login, want ACTIVE", acc.Status)
	}
}

// TestSessionRefreshIgnoresOnboardingFailure: a first login that fails (no
// expiry ever recorded) is an onboarding problem, not a dying session, and must
// not count toward the P2-06 streak.
func TestSessionRefreshIgnoresOnboardingFailure(t *testing.T) {
	_, jobs, store, _ := refreshFixture(t)
	worker := "worker-1"
	store.accounts.seedLocked(domain.Account{
		ID: "acc-new", Platform: domain.PlatformInstagram, Username: "new",
		AuthStatus: domain.AuthAuthenticating, Status: domain.AccountActive,
		WorkerID: &worker,
	})

	if err := jobs.RecordAuthOutcome(context.Background(), AuthOutcome{
		AccountID: "acc-new", AuthStatus: domain.AuthFailed, Error: strPtr("bad password"),
	}); err != nil {
		t.Fatalf("record failure: %v", err)
	}
	acc := store.accounts.get("acc-new")
	if acc.RefreshFailures != 0 {
		t.Errorf("failures = %d on a first-time login failure, want 0", acc.RefreshFailures)
	}
	if acc.Status == domain.AccountQuarantined {
		t.Error("never-authenticated account was quarantined by the refresh counter")
	}
}

// TestSessionRefreshReclaimsAbandonedLogin: a row stranded in AUTHENTICATING —
// the worker was killed, the control message landed before the subscriber was
// up, the callback was lost — is invisible to the due query, which excludes
// AUTHENTICATING precisely so it does not stampede a login in flight. Without a
// sweep the row stays there forever and the account silently drops out of every
// scheduler the system has. The sweep must return it to FAILED so the due query
// sees it again.
func TestSessionRefreshReclaimsAbandonedLogin(t *testing.T) {
	refresher, _, store, _ := refreshFixture(t)
	worker := "worker-1"

	// Stamped well outside the human-interaction window: no operator is still
	// sitting on this one.
	stale := refreshStart.Add(-2 * StaleLoginTimeout)
	store.accounts.seedLocked(domain.Account{
		ID: "acc-stuck", Platform: domain.PlatformInstagram, Username: "stuck",
		AuthStatus: domain.AuthAuthenticating, Status: domain.AccountActive,
		WorkerID: &worker, LastChecked: &stale,
	})

	if err := refresher.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	acc := store.accounts.get("acc-stuck")
	if acc.AuthStatus != domain.AuthFailed {
		t.Errorf("authStatus = %q after reclaim, want FAILED (an unproven session)", acc.AuthStatus)
	}
	if acc.LastError == nil || *acc.LastError == "" {
		t.Error("reclaimed account carries no error explaining why it was reclaimed")
	}
	// A stale expiry left in place would make the next dispatch look like a
	// refresh of a live session and count toward the quarantine streak.
	if acc.CookieExpiryAt != nil {
		t.Errorf("cookieExpiryAt = %v after reclaim, want cleared", acc.CookieExpiryAt)
	}
}

// TestSessionRefreshLeavesRecentLoginAlone: a login inside the human window is
// the OPERATOR's turn. Sweeping it would kill the very login the live view is
// waiting on, so the bound must sit well clear of any plausible 2FA/checkpoint
// interaction.
func TestSessionRefreshLeavesRecentLoginAlone(t *testing.T) {
	refresher, _, store, _ := refreshFixture(t)
	worker := "worker-1"

	recent := refreshStart.Add(-2 * time.Minute)
	store.accounts.seedLocked(domain.Account{
		ID: "acc-live", Platform: domain.PlatformInstagram, Username: "live",
		AuthStatus: domain.AuthAuthenticating, Status: domain.AccountActive,
		WorkerID: &worker, LastChecked: &recent,
	})

	if err := refresher.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	if acc := store.accounts.get("acc-live"); acc.AuthStatus != domain.AuthAuthenticating {
		t.Errorf("authStatus = %q, want the in-flight login left untouched", acc.AuthStatus)
	}
}

// TestSessionRefreshReclaimDoesNotStompARealOutcome: the sweep lists stale rows
// and then writes, and a worker can report back in that gap. The write is
// timestamp-guarded for exactly that reason — a real verdict must win over the
// sweep's guess, or a successful login would be recorded as abandoned.
func TestSessionRefreshReclaimDoesNotStompARealOutcome(t *testing.T) {
	_, _, store, _ := refreshFixture(t)
	worker := "worker-1"

	// Listed as stale, but the row has since moved on (the worker reported),
	// so the guard timestamp no longer matches.
	moved := refreshStart.Add(-3 * StaleLoginTimeout)
	store.accounts.seedLocked(domain.Account{
		ID: "acc-race", Platform: domain.PlatformInstagram, Username: "race",
		AuthStatus: domain.AuthAuthenticating, Status: domain.AccountActive,
		WorkerID: &worker, LastChecked: &moved,
	})

	// The sweep lists it at `moved`, then the worker's real outcome lands.
	store.accounts.seedLocked(domain.Account{
		ID: "acc-race", Platform: domain.PlatformInstagram, Username: "race",
		AuthStatus: domain.AuthAuthenticated, Status: domain.AccountActive,
		WorkerID: &worker, LastChecked: &moved,
	})

	reclaimed, err := store.ReclaimStaleLoginAttempt(context.Background(), "acc-race", moved)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if reclaimed {
		t.Error("sweep reclaimed an account that had already reported a real outcome")
	}
	if acc := store.accounts.get("acc-race"); acc.AuthStatus != domain.AuthAuthenticated {
		t.Errorf("authStatus = %q, want AUTHENTICATED preserved", acc.AuthStatus)
	}
}

// TestSessionRefreshRespectsFleetLoginCeiling: TickBudget bounds one tick, not
// concurrency. With ticks repeating every few seconds and a login taking
// minutes, an ungated loop starts every due account long before the first
// resolves — 30 headful logins at Instagram from one IP, which is what gets the
// range rate-limited. The gate must hold the fleet at the ceiling.
func TestSessionRefreshRespectsFleetLoginCeiling(t *testing.T) {
	accounts := newFakeAccountStore()
	now := refreshStart
	store := &fakeRefreshStore{accounts: accounts, now: &now}
	pub := &recordingPublisher{}
	refresher := NewSessionRefresher(store, pub, SessionRefreshConfig{
		Clock:               func() time.Time { return now },
		Logger:              slog.New(slog.NewTextHandler(io.Discard, nil)),
		Sealer:              stubSealer{},
		MaxConcurrentLogins: 3,
		InFlightLogins:      func(context.Context) (int, error) { return 2, nil },
	})
	// Ten accounts are due; two logins are already in flight, so only one more
	// may start this tick.
	for i := range 10 {
		seedExpiring(accounts, "acc-"+string(rune('a'+i)), 6*24*time.Hour)
	}

	if err := refresher.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(store.begun) != 1 {
		t.Errorf("dispatched %d logins with 2 in flight at a ceiling of 3, want 1", len(store.begun))
	}
}

// TestSessionRefreshStopsAtCeiling: once the fleet is full, a tick dispatches
// nothing rather than one more.
func TestSessionRefreshStopsAtCeiling(t *testing.T) {
	accounts := newFakeAccountStore()
	now := refreshStart
	store := &fakeRefreshStore{accounts: accounts, now: &now}
	pub := &recordingPublisher{}
	refresher := NewSessionRefresher(store, pub, SessionRefreshConfig{
		Clock:               func() time.Time { return now },
		Logger:              slog.New(slog.NewTextHandler(io.Discard, nil)),
		Sealer:              stubSealer{},
		MaxConcurrentLogins: 3,
		InFlightLogins:      func(context.Context) (int, error) { return 3, nil },
	})
	seedExpiring(accounts, "acc-full", 6*24*time.Hour)

	if err := refresher.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(store.begun) != 0 {
		t.Errorf("dispatched %d logins at a full ceiling, want 0", len(store.begun))
	}
}

// TestSessionRefreshFailsClosedWhenCountUnavailable: if the in-flight count
// cannot be read, the safe move is to dispatch nothing. Guessing would be
// exactly the stampede the gate exists to prevent, and the accounts stay due.
func TestSessionRefreshFailsClosedWhenCountUnavailable(t *testing.T) {
	accounts := newFakeAccountStore()
	now := refreshStart
	store := &fakeRefreshStore{accounts: accounts, now: &now}
	pub := &recordingPublisher{}
	refresher := NewSessionRefresher(store, pub, SessionRefreshConfig{
		Clock:               func() time.Time { return now },
		Logger:              slog.New(slog.NewTextHandler(io.Discard, nil)),
		Sealer:              stubSealer{},
		MaxConcurrentLogins: 3,
		InFlightLogins:      func(context.Context) (int, error) { return 0, errors.New("db down") },
	})
	seedExpiring(accounts, "acc-x", 6*24*time.Hour)

	if err := refresher.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(store.begun) != 0 {
		t.Errorf("dispatched %d logins without a usable in-flight count, want 0", len(store.begun))
	}
}

// The refresher's transport is the same port.Publisher the account service
// publishes auth-login on; recordingPublisher already models it.
var _ port.Publisher = (*recordingPublisher)(nil)
