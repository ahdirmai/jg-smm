package port

import (
	"context"
	"time"
)

// CooldownGate (P3-09) prevents a double action: the same account must not act
// on the same target twice inside a cooldown window. It is a distributed
// compare-and-set over Redis, not a DB column, because the gate must hold
// across API replicas and be cheap on the hot enqueue path.
//
// Contract:
//   - Acquire returns true when this caller won the slot (no recent action),
//     false when one is still cooling down (a double action was prevented).
//   - The key expires on its own; there is no Release. A caller that wins and
//     then fails to enqueue simply lets the TTL lapse — the worst case is a
//     60s pause, never a permanent lock.
type CooldownGate interface {
	// Acquire tries to claim the cooldown slot for (accountID, targetKey)
	// for the given window. False means cooled down: do not act.
	Acquire(ctx context.Context, accountID, targetKey string, window time.Duration) (bool, error)
}
