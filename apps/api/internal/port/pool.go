package port

import (
	"context"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
)

// SlotKind is what a worker is currently doing. It matters because only one of
// them may be evicted: an action slot is disposable (the job is rescheduled),
// while an auth lease is a human staring at a CAPTCHA — closing it would throw
// the operator's work away.
type SlotKind string

const (
	// SlotAction is one action job running on the worker.
	SlotAction SlotKind = "action"
	// SlotAuthLease is an interactive login / CAPTCHA / live-view session. It
	// holds the whole Xvfb display until the operator (or the login poll) ends
	// it, so no action may take that worker meanwhile.
	SlotAuthLease SlotKind = "auth"
)

// AccountExecutionLock serialises work on ONE account across the whole fleet.
// Two workers acting as the same account at once is the fastest way to have the
// platform terminate it, so this is the outermost gate on every dispatch.
type AccountExecutionLock interface {
	// AcquireAccount takes the account's lock for ttl. It reports false (not an
	// error) when another holder has it: "busy" is the normal answer, not a
	// failure.
	AcquireAccount(ctx context.Context, accountID, token string, ttl time.Duration) (bool, error)
	// ReleaseAccount drops the lock, but only if token still owns it.
	ReleaseAccount(ctx context.Context, accountID, token string) error
}

// WorkerSlotCoordinator tracks which pool worker is free.
//
// Affinity is the reason this exists as its own concept: a worker without a
// residential proxy egresses from its own datacentre IP, and hopping that
// account between machines would present a new IP on every job. So an account
// always PREFERS the worker its id hashes to, and only falls through to another
// worker when the account carries its own proxy (the IP travels with the
// account, so any machine is equivalent).
type WorkerSlotCoordinator interface {
	// AcquireWorkerSlot reserves a free worker from candidates. allowFallback
	// permits trying workers after the affinity one; false pins the account to
	// its affinity worker and returns domain.ErrUnavailable if that is busy.
	// A worker holding a SlotAuthLease is never handed out for SlotAction.
	AcquireWorkerSlot(ctx context.Context, accountID string, candidates []domain.Worker, kind SlotKind, allowFallback bool, ttl time.Duration) (domain.Worker, error)
	// ReleaseWorkerSlot frees the worker, only if accountID still holds it.
	ReleaseWorkerSlot(ctx context.Context, workerID, accountID string) error
	// ClearWorkerSlot frees the worker regardless of which account holds it.
	// Only for a caller that has already proved the machine is idle (the worker
	// reports no busy account): on a serial login machine an owner-scoped
	// release cannot name the stale holder, so the slot outlives its account.
	ClearWorkerSlot(ctx context.Context, workerID string) error
}
