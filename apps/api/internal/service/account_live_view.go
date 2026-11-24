package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// LiveView is a short-lived, signed window onto one account's browser.
type LiveView struct {
	URL       string
	WorkerID  string
	ExpiresAt time.Time
}

// evictFromLoginMachine terminates the session holding the serial login
// machine so a different account can use it.
//
// The browser is closed FIRST: while its context is alive the worker keeps the
// old account's cookies and answers 409 to every dispatch, so releasing the
// lease before that would hand the machine out still "busy". The row is then
// settled to FAILED (it is not waiting on an operator any more) and both the
// SQL lease and the Redis slot are dropped — a row left NEEDS_INPUT with a
// lease is the one-way trap that made the account unloginable.
func (s *AccountService) evictFromLoginMachine(ctx context.Context, w domain.Worker, heldID string, by domain.Account) {
	if w.EndpointURL != nil && *w.EndpointURL != "" && s.pool.RPC != nil {
		if err := s.pool.RPC.ClearAuth(ctx, *w.EndpointURL, heldID); err != nil {
			s.logger.Warn("preempt: clear browser failed", "heldAccount", heldID, "worker", w.Name, "err", err)
		}
	}
	if s.pool.Slots != nil {
		if err := s.pool.Slots.ReleaseWorkerSlot(ctx, w.ID, heldID); err != nil {
			s.logger.Warn("preempt: release slot failed", "heldAccount", heldID, "worker", w.Name, "err", err)
		}
	}
	holder, err := s.accounts.GetByID(ctx, heldID)
	if err != nil {
		s.logger.Warn("preempt: load held account failed", "heldAccount", heldID, "err", err)
		return
	}
	if holder.WorkerID != nil {
		if s.pool.Unlease != nil {
			if err := s.pool.Unlease(ctx, heldID, *holder.WorkerID); err != nil {
				s.logger.Warn("preempt: release lease failed", "heldAccount", heldID, "err", err)
			}
		} else if err := s.accounts.ReleaseLease(ctx, heldID, *holder.WorkerID); err != nil {
			s.logger.Warn("preempt: release lease failed", "heldAccount", heldID, "err", err)
		}
	}
	// Settle the row: it is no longer waiting for an operator, and leaving it
	// NEEDS_INPUT would keep the dashboard pointing at a browser that is gone.
	msg := fmt.Sprintf("login preempted: %s started a login on the same machine", by.Username)
	holder.WorkerID = nil
	holder.AuthStatus = domain.AuthFailed
	holder.LastError = &msg
	if _, err := s.accounts.Update(ctx, holder); err != nil {
		s.logger.Warn("preempt: settle held account failed", "heldAccount", heldID, "err", err)
		return
	}
	s.logger.Warn("login machine preempted",
		"heldAccount", holder.Username, "heldStatus", holder.AuthStatus, "by", by.Username, "worker", w.Name)
	s.publishAccount(ctx, toAccountView(holder))
}

// releaseStaleLeasesOnIdleWorker drops DB leases pointing at a login machine
// that just reported itself idle (busy:false). A worker restart or crash clears
// its in-memory browser without clearing account.worker_id in Postgres; without
// this sweep, the next account on the same platform hits the unique
// (worker_id, platform) constraint and fails with a bare 409 conflict forever.
func (s *AccountService) releaseStaleLeasesOnIdleWorker(ctx context.Context, w domain.Worker, incoming domain.Account) {
	leased, err := s.accounts.ListByWorker(ctx, w.ID)
	if err != nil {
		s.logger.Warn("login machine: list stale leases failed", "worker", w.Name, "err", err)
		return
	}
	for _, stale := range leased {
		if stale.ID == incoming.ID {
			continue
		}
		if s.pool.Slots != nil {
			_ = s.pool.Slots.ReleaseWorkerSlot(ctx, w.ID, stale.ID)
		}
		if s.pool.Unlease != nil {
			if err := s.pool.Unlease(ctx, stale.ID, w.ID); err != nil {
				s.logger.Warn("login machine: clear stale lease failed", "staleAccount", stale.ID, "worker", w.Name, "err", err)
				continue
			}
		} else if err := s.accounts.ReleaseLease(ctx, stale.ID, w.ID); err != nil {
			s.logger.Warn("login machine: clear stale lease failed", "staleAccount", stale.ID, "worker", w.Name, "err", err)
			continue
		}
		s.logger.Warn("login machine: cleared stale DB lease on idle worker",
			"staleAccount", stale.Username, "staleStatus", stale.AuthStatus, "worker", w.Name)
		if stale.AuthStatus == domain.AuthNeedsInput || stale.AuthStatus == domain.AuthAuthenticating {
			stale.WorkerID = nil
			stale.AuthStatus = domain.AuthFailed
			msg := fmt.Sprintf("login cleared: %s was idle after restart", w.Name)
			stale.LastError = &msg
			if _, err := s.accounts.Update(ctx, stale); err != nil {
				s.logger.Warn("login machine: settle stale account failed", "staleAccount", stale.ID, "err", err)
				continue
			}
			s.publishAccount(ctx, toAccountView(stale))
		}
	}
}

// leaseWorker gives the account a pool worker for an interactive session.
//
// The account's SQL lease (account.worker_id) is the authority; the Redis slot
// is the fast pre-check in front of it. AcquireLease is idempotent for the same
// worker, so a second live-view click on a session that is already parked reuses
// it instead of failing.
func (s *AccountService) leaseWorker(ctx context.Context, acc domain.Account) (domain.Worker, func(), error) {
	if s.pool == nil {
		return domain.Worker{}, nil, fmt.Errorf("%w: worker pool is not configured", domain.ErrUnavailable)
	}
	// Reuse the lease the account already holds instead of racing for a second
	// one. The old order took a slot FIRST and only then noticed the account was
	// already leased elsewhere, which both refused the operator AND left the
	// freshly-taken slot orphaned on a worker nobody would ever release — the
	// next attempt then failed with "worker busy" on a machine with no lease to
	// explain it.
	if acc.WorkerID != nil {
		if s.workers != nil {
			if held, err := s.workers.GetByID(ctx, *acc.WorkerID); err == nil {
				release := func() {
					if err := s.pool.Slots.ReleaseWorkerSlot(context.WithoutCancel(ctx), held.ID, acc.ID); err != nil {
						s.logger.Warn("account service: release worker slot failed", "workerId", held.ID, "err", err)
					}
				}
				return held, release, nil
			}
		}
		// The lease points at a worker row that no longer exists (a purged
		// container, a recreated fleet). Drop it and lease afresh rather than
		// trapping the account on a machine it can never reach.
		if err := s.pool.Unlease(ctx, acc.ID, *acc.WorkerID); err != nil {
			s.logger.Warn("account service: clear dangling lease failed", "accountId", acc.ID, "err", err)
		}
	}

	// Interactive sessions get the LOGIN machine (or the whole fleet when none
	// is configured) — never an action worker, which must stay free for jobs.
	candidates, err := s.pool.LoginCandidates(ctx)
	if err != nil {
		return domain.Worker{}, nil, fmt.Errorf("account service: list pool workers: %w", err)
	}
	if len(candidates) == 0 {
		candidates, err = s.pool.Workers(ctx)
		if err != nil {
			return domain.Worker{}, nil, fmt.Errorf("account service: list pool workers: %w", err)
		}
	}
	// A single login machine is a SERIAL resource: one display, one account.
	// A new login therefore PREEMPTS whatever is parked there — an operator who
	// decides to log in another account should not be told to go finish someone
	// else's CAPTCHA first.
	//
	// One exception: a session still driving a real login (AUTHENTICATING) is
	// mid-flight work whose browser is being navigated right now; killing it
	// leaves a half-authenticated account and teaches nothing. Everything else
	// (a parked CAPTCHA, an abandoned attempt, a row with no lease) yields.
	if len(candidates) == 1 && s.pool.RPC != nil && candidates[0].EndpointURL != nil {
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		held, herr := s.pool.RPC.BusyAccount(probeCtx, *candidates[0].EndpointURL)
		cancel()
		if herr == nil && held == "" {
			s.releaseStaleLeasesOnIdleWorker(ctx, candidates[0], acc)
		} else if herr == nil && held != "" && held != acc.ID {
			holder, gerr := s.accounts.GetByID(ctx, held)
			switch {
			case gerr != nil:
				s.logger.Warn("login machine held an unknown account; evicting",
					"heldAccount", held, "worker", candidates[0].Name)
				s.evictFromLoginMachine(ctx, candidates[0], held, acc)
			case holder.AuthStatus == domain.AuthAuthenticating:
				s.logger.Info("login machine is mid-login; leaving it alone",
					"heldAccount", held, "worker", candidates[0].Name)
			default:
				s.logger.Warn("login machine held another account; preempting",
					"heldAccount", held, "heldStatus", holder.AuthStatus, "worker", candidates[0].Name)
				s.evictFromLoginMachine(ctx, candidates[0], held, acc)
			}
		}
	}

	worker, err := s.pool.Slots.AcquireWorkerSlot(ctx, acc.ID, candidates, port.SlotAuthLease, true, s.pool.SlotTTL)
	if err != nil {
		// The slot says busy but the DB lease may already be gone: a serial
		// login machine is the one place where a stale slot blocks EVERYTHING,
		// and the operator was told "every pool worker is busy" while the
		// machine answered busy:false. Verify, and reclaim the account's own
		// stray slot rather than making them wait out the TTL.
		if errors.Is(err, domain.ErrUnavailable) && len(candidates) == 1 {
			held := ""
			if s.pool.RPC != nil && candidates[0].EndpointURL != nil {
				probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				held, _ = s.pool.RPC.BusyAccount(probeCtx, *candidates[0].EndpointURL)
				cancel()
			}
			// Reclaim when the machine is NOT actively showing another account.
			// A slot whose account has no lease is the leftover of a finished
			// attempt, and on a serial login machine that leftover blocks every
			// other account until its TTL runs out.
			otherBusy := held != "" && held != acc.ID
			if otherBusy {
				holder, gerr := s.accounts.GetByID(ctx, held)
				otherBusy = gerr == nil && holder.WorkerID != nil && *holder.WorkerID == candidates[0].ID
			}
			if !otherBusy {
				s.logger.Info("reclaiming stale login slot", "accountId", acc.ID, "heldBy", held)
				// The slot may be held by a DIFFERENT account than this one:
				// CloseLiveView used to free only the account's own slot, so an
				// earlier attempt left a foreign slot on an idle machine. A
				// ReleaseWorkerSlot keyed by acc.ID is a no-op there, and the
				// second acquire fails with "every pool worker is busy" for a
				// machine that is answering idle. The probe above already
				// proved nobody is using the display, so drop whatever holds it.
				if rerr := s.pool.Slots.ClearWorkerSlot(ctx, candidates[0].ID); rerr != nil {
					s.logger.Warn("reclaim failed", "err", rerr)
				}
				worker, err = s.pool.Slots.AcquireWorkerSlot(ctx, acc.ID, candidates, port.SlotAuthLease, true, s.pool.SlotTTL)
			}
		}
		if err != nil {
			// A bare ErrConflict reaches the operator as "conflict" with no way
			// to tell "this account already has a live session" from "the fleet
			// is busy". Both are retryable, but only one means "you already have it".
			if errors.Is(err, domain.ErrUnavailable) {
				return domain.Worker{}, nil, fmt.Errorf(
					"%w: every pool worker is busy; retry when a session finishes", domain.ErrUnavailable)
			}
			return domain.Worker{}, nil, err
		}
	}

	release := func() {
		if err := s.pool.Slots.ReleaseWorkerSlot(context.WithoutCancel(ctx), worker.ID, acc.ID); err != nil {
			s.logger.Warn("account service: release worker slot failed", "workerId", worker.ID, "err", err)
		}
	}
	if _, err := s.accounts.AcquireLease(ctx, acc.ID, worker.ID); err != nil {
		release()
		return domain.Worker{}, nil, err
	}
	return worker, release, nil
}

// OpenLiveView leases a worker, opens the account profile there, and returns a
// signed URL to its screen.
//
// The ticket is the ONLY lock on that screen. On boxd the noVNC view is on a
// public hostname; without a ticket anyone who guesses it gets a WebSocket to a
// browser logged into an operator account. The URL expires in minutes because it
// is a bearer credential and lands in browser history.
func (s *AccountService) OpenLiveView(ctx context.Context, accountID string) (LiveView, error) {
	if s.novncSecret == "" {
		return LiveView{}, fmt.Errorf("%w: live view is not configured (no ticket secret)", domain.ErrUnavailable)
	}
	acc, err := s.accounts.GetByID(ctx, accountID)
	if err != nil {
		return LiveView{}, fmt.Errorf("account service: get: %w", err)
	}

	spec, err := s.poolRuntime(ctx, acc)
	if err != nil {
		return LiveView{}, err
	}

	// A legacy account already sits on a specific container: reuse it.
	var worker domain.Worker
	var release func()
	if s.pool == nil {
		if acc.WorkerID == nil {
			return LiveView{}, fmt.Errorf("%w: account has no worker to view", domain.ErrConflict)
		}
		w, err := s.workers.GetByID(ctx, *acc.WorkerID)
		if err != nil {
			return LiveView{}, fmt.Errorf("account service: worker: %w", err)
		}
		worker = w
		release = func() {}
	} else {
		worker, release, err = s.leaseWorker(ctx, acc)
		if err != nil {
			return LiveView{}, err
		}
	}

	base := ""
	if worker.NoVNCService != nil {
		base = *worker.NoVNCService
	}
	if base == "" {
		release()
		// Name the fix: a pool worker is seeded from WORKER_POOL_ENDPOINTS, and
		// a deployment that lists only the RPC URL leaves every live view
		// unusable with nothing in the message to act on.
		return LiveView{}, fmt.Errorf(
			"%w: worker %s has no live-view URL; add its |novnc-url to WORKER_POOL_ENDPOINTS (name=rpcUrl|novncUrl)",
			domain.ErrConflict, worker.Name)
	}

	if s.pool != nil {
		if err := s.pool.OpenBrowser(ctx, worker, spec); err != nil {
			release()
			return LiveView{}, err
		}
	} else if s.control != nil {
		// Legacy: the control channel carries the navigation.
		msg, err := json.Marshal(controlMessage{
			Type:      "browser-open",
			AccountID: acc.ID,
			Platform:  string(acc.Platform),
		})
		if err != nil {
			release()
			return LiveView{}, fmt.Errorf("account service: marshal browser-open: %w", err)
		}
		if err := s.control.PublishControl(ctx, worker.ID, msg); err != nil {
			release()
			return LiveView{}, fmt.Errorf("account service: publish browser-open: %w", err)
		}
	}

	expires := TicketExpiry(s.clock.Now(), s.novncTicketTTL)
	ticket, err := SignLiveViewTicket(s.novncSecret, worker.Name, expires)
	if err != nil {
		release()
		return LiveView{}, err
	}
	s.logger.Info("live view opened", "accountId", acc.ID, "workerId", worker.ID, "expiresAt", expires)
	return LiveView{
		URL:       BuildProtectedNoVNCURL(base, ticket),
		WorkerID:  worker.ID,
		ExpiresAt: expires,
	}, nil
}

// poolRuntime builds the account's context identity, or an empty spec when no
// pool is configured (the legacy worker reads its own stored session).
func (s *AccountService) poolRuntime(ctx context.Context, acc domain.Account) (port.AccountRuntimeSpec, error) {
	if s.pool == nil {
		return port.AccountRuntimeSpec{
			AccountID: acc.ID,
			Platform:  string(acc.Platform),
			Timezone:  acc.Timezone,
		}, nil
	}
	return s.pool.RuntimeSpec(ctx, acc)
}

// CloseLiveView ends an interactive session: the browser context is closed on
// the worker, the account's SQL lease is freed and the worker slot is returned.
//
// Without this a live view held a lease FOREVER. The Redis slot expired on its
// TTL, but account.worker_id is a row someone has to write, so the account
// became permanently unviewable and unschedulable — the operator saw
// "this account already has a live session" for a session they had closed.
func (s *AccountService) CloseLiveView(ctx context.Context, accountID string) error {
	acc, err := s.accounts.GetByID(ctx, accountID)
	if err != nil {
		return fmt.Errorf("account service: get: %w", err)
	}
	if acc.WorkerID == nil {
		// The account row is clean, but the WORKER may still be parked on it:
		// a crashed close, a re-leased slot, or an operator closing twice all
		// leave `smm:slot:worker:*` holding this account with no row pointing
		// back. The worker then answers 409 to every later dispatch with
		// "worker busy", and nothing in the DB can explain why.
		if s.pool != nil {
			if cleared, err := s.pool.ClearAccountFromPool(ctx, accountID); err != nil {
				return fmt.Errorf("account service: clear pool slots: %w", err)
			} else if cleared > 0 {
				s.logger.Info("cleared orphaned pool slots for a closed session",
					"accountId", accountID, "slots", cleared)
			}
		}
		// A parked login with no lease is still a trap: the next attempt is
		// refused with "submit the code", but there is no browser left to
		// submit it into. Settle the status even when the lease was already
		// gone — this is the idempotent second click the operator reaches for
		// after a crash.
		if acc.AuthStatus == domain.AuthNeedsInput || acc.AuthStatus == domain.AuthAuthenticating {
			acc.AuthStatus = domain.AuthFailed
			msg := "login cancelled: operator released the session"
			acc.LastError = &msg
			if _, err := s.accounts.Update(ctx, acc); err != nil {
				return fmt.Errorf("account service: settle parked login: %w", err)
			}
			s.publishAccount(ctx, toAccountView(acc))
		}
		return nil
	}
	workerID := *acc.WorkerID
	worker := domain.Worker{ID: workerID}
	if s.workers != nil {
		if found, err := s.workers.GetByID(ctx, workerID); err == nil {
			worker = found
		}
	}
	if s.pool != nil {
		if worker.EndpointURL == nil || *worker.EndpointURL == "" {
			// Without the endpoint the browser context cannot be closed, and the
			// worker stays "busy" with the account's cookies until it restarts.
			// Say so instead of silently leaving a locked display behind.
			s.logger.Warn("live view closed without clearing the worker browser: worker has no endpoint",
				"accountId", accountID, "workerId", workerID)
		}
		s.pool.ReleaseSession(ctx, acc.ID, worker, true)
	} else {
		if err := s.accounts.ReleaseLease(ctx, acc.ID, workerID); err != nil {
			return fmt.Errorf("account service: release lease: %w", err)
		}
	}
	// ReleaseSession frees the lease and the slot THIS account holds on THIS
	// worker, keyed by acc.ID. A slot left behind by an earlier attempt (a
	// dispatch that failed after the slot was taken, or a close that ran while
	// the account was leased elsewhere) is keyed by nothing this call can name,
	// so it survives and the next login on a serial login machine fails with
	// "every pool worker is busy" while the machine itself answers idle.
	if s.pool != nil {
		if cleared, err := s.pool.ClearAccountFromPool(ctx, accountID); err != nil {
			s.logger.Warn("live view: sweep account slots failed", "accountId", accountID, "err", err)
		} else if cleared > 0 {
			s.logger.Info("live view: swept orphaned pool slots",
				"accountId", accountID, "slots", cleared)
		}
	}
	// A parked login outlives its display: closing the view frees the worker but
	// the account stays NEEDS_INPUT, so the NEXT attempt is refused with
	// "waiting for operator input; submit the code instead" forever — there is no
	// browser left to submit a code into. Clearing the lease must therefore also
	// settle the status, or the row is a one-way trap.
	acc.WorkerID = nil
	if acc.AuthStatus == domain.AuthNeedsInput || acc.AuthStatus == domain.AuthAuthenticating {
		acc.AuthStatus = domain.AuthFailed
		msg := "login cancelled: operator closed the live view"
		acc.LastError = &msg
		if _, err := s.accounts.Update(ctx, acc); err != nil {
			return fmt.Errorf("account service: settle parked login: %w", err)
		}
		s.logger.Info("parked login cancelled with its live view",
			"accountId", accountID, "workerId", workerID)
		s.publishAccount(ctx, toAccountView(acc))
		return nil
	}
	s.publishAccount(ctx, toAccountView(acc))
	return nil
}
