package transport

import (
	"context"
	"fmt"
)

// CommitFunc is a unit of work that persists to the database and returns only
// after the write is durably committed.
type CommitFunc func(ctx context.Context) error

// OrderedDispatcher enforces the transport ordering contract: the DB write MUST
// commit before any bytes hit Redis. It is the only sanctioned way to pair a
// state change with a job/control message (DEVELOPMENT_RULE §transport).
//
// Usage:
//
//	err := disp.Dispatch(ctx,
//	    func(ctx context.Context) error { return accountStore.Create(ctx, a) }, // commit first
//	    func(ctx context.Context) error { return publisher.Enqueue(ctx, w.ID, job) },
//	)
//
// If the commit fails, the effect never runs. If the commit succeeds but the
// effect fails, the error is returned with a wrapped context so the caller can
// retry the effect (the DB state is already durable and idempotent to re-dispatch).
type OrderedDispatcher struct{}

// Dispatch runs commit, then effect, in that order.
func (OrderedDispatcher) Dispatch(ctx context.Context, commit CommitFunc, effect CommitFunc) error {
	if commit == nil || effect == nil {
		return fmt.Errorf("transport.dispatch: commit and effect are required")
	}
	if err := commit(ctx); err != nil {
		return fmt.Errorf("transport.dispatch: commit failed, effect suppressed: %w", err)
	}
	if err := effect(ctx); err != nil {
		return fmt.Errorf("transport.dispatch: effect failed after commit (retryable): %w", err)
	}
	return nil
}
