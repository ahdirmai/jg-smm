// Package poolmgr implements the pool coordination ports on Redis: the
// per-account execution lock and the worker slot lease.
//
// Why Redis for a lease whose truth lives in Postgres: the SQL lease
// (account.worker_id) is the authority but it costs a round trip and a row
// write on every dispatch, while these keys expire on their own — a worker that
// dies mid-job releases its slot in TTL seconds without any sweeper. The two are
// layered: Redis decides who to TRY, AcquireLease decides who WINS.
package poolmgr

import (
	"context"
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// KeyPrefix namespaces every key so a shared Redis cannot collide with the
// action queue / cooldown keyspaces.
const KeyPrefix = "smm:"

// Lock is the Redis implementation of port.AccountExecutionLock.
type Lock struct {
	client redis.Cmdable
}

// NewLock binds the lock to a Redis client.
func NewLock(client redis.Cmdable) *Lock { return &Lock{client: client} }

var _ port.AccountExecutionLock = (*Lock)(nil)

func accountKey(accountID string) string { return KeyPrefix + "lock:account:" + accountID }

func slotKey(workerID string) string { return KeyPrefix + "slot:worker:" + workerID }

// AcquireAccount takes the account's exclusive lock. SET NX EX is atomic, so
// two schedulers ticking at the same instant cannot both win.
func (l *Lock) AcquireAccount(ctx context.Context, accountID, token string, ttl time.Duration) (bool, error) {
	if ttl <= 0 {
		return false, fmt.Errorf("poolmgr: account lock ttl must be positive")
	}
	ok, err := l.client.SetNX(ctx, accountKey(accountID), token, ttl).Result()
	if err != nil {
		return false, fmt.Errorf("poolmgr: acquire account lock: %w", err)
	}
	return ok, nil
}

// releaseIfOwned deletes key only when its value is still ours. The Lua script
// makes the compare-and-delete atomic; a plain GET-then-DEL leaks a lock when
// the TTL expires between the two calls and a new holder takes it.
const releaseIfOwned = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("DEL", KEYS[1])
end
return 0`

// ReleaseAccount drops the lock if this token still owns it. Releasing someone
// else's lock would let two workers act as one account, so a stale release is a
// no-op rather than an unconditional delete.
func (l *Lock) ReleaseAccount(ctx context.Context, accountID, token string) error {
	if err := l.client.Eval(ctx, releaseIfOwned, []string{accountKey(accountID)}, token).Err(); err != nil {
		return fmt.Errorf("poolmgr: release account lock: %w", err)
	}
	return nil
}

// Slots is the Redis implementation of port.WorkerSlotCoordinator.
type Slots struct {
	client redis.Cmdable
}

// NewSlots binds the coordinator to a Redis client.
func NewSlots(client redis.Cmdable) *Slots { return &Slots{client: client} }

var _ port.WorkerSlotCoordinator = (*Slots)(nil)

// AffinityIndex is the consistent worker preference for an account: the same
// account always hashes to the same machine, so a proxyless account keeps one
// egress IP across jobs. FNV-1a because it is stable across processes and runs,
// unlike Go's randomised map hash.
func AffinityIndex(accountID string, n int) int {
	if n <= 0 {
		return 0
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(accountID))
	return int(h.Sum32() % uint32(n))
}

// slotValue records what holds a worker, so a release can verify ownership and
// a log can say why a slot is busy. "<kind>:<accountID>".
func slotValue(kind port.SlotKind, accountID string) string {
	return string(kind) + ":" + accountID
}

// AcquireWorkerSlot reserves a free worker, preferring the account's affinity
// index. Order: affinity worker first, then the rest in ring order.
//
// A worker already holding a slot of either kind is unavailable: one display,
// one browser. A proxyless account (pinned, no fallback) returns
// domain.ErrUnavailable instead of moving to a different egress IP.
func (s *Slots) AcquireWorkerSlot(ctx context.Context, accountID string, candidates []domain.Worker, kind port.SlotKind, allowFallback bool, ttl time.Duration) (domain.Worker, error) {
	if len(candidates) == 0 {
		return domain.Worker{}, domain.ErrUnavailable
	}
	if ttl <= 0 {
		return domain.Worker{}, fmt.Errorf("poolmgr: worker slot ttl must be positive")
	}
	// Pack proxied accounts onto the lowest-index free action worker first
	// (start = 0) so small batches wake 1 microVM instead of fanning out across
	// every sleeping VM in the fleet. Proxyless accounts stay pinned to their
	// deterministic AffinityIndex so their egress IP never changes.
	start := 0
	tries := len(candidates)
	if !allowFallback {
		// Pinned: only the affinity worker is acceptable. Trying others would
		// move a proxyless account to a different egress IP.
		start = AffinityIndex(accountID, len(candidates))
		tries = 1
	}
	for i := range tries {
		w := candidates[(start+i)%len(candidates)]
		ok, err := s.client.SetNX(ctx, slotKey(w.ID), slotValue(kind, accountID), ttl).Result()
		if err != nil {
			return domain.Worker{}, fmt.Errorf("poolmgr: acquire worker slot: %w", err)
		}
		if ok {
			return w, nil
		}
	}
	return domain.Worker{}, domain.ErrUnavailable
}

// ReleaseWorkerSlot frees the worker if this account still holds it.
func (s *Slots) ReleaseWorkerSlot(ctx context.Context, workerID, accountID string) error {
	// The value is "<kind>:<accountID>" and the kind is not known here, so the
	// script matches on the suffix rather than reconstructing the string.
	const script = `
local v = redis.call("GET", KEYS[1])
if v and string.sub(v, -string.len(ARGV[1]) - 1) == ":" .. ARGV[1] then
	return redis.call("DEL", KEYS[1])
end
return 0`
	if err := s.client.Eval(ctx, script, []string{slotKey(workerID)}, accountID).Err(); err != nil {
		return fmt.Errorf("poolmgr: release worker slot: %w", err)
	}
	return nil
}

// ClearWorkerSlot frees the worker whatever account holds it. The caller must
// have proved the display is idle; this exists because the slot's value names
// its holder, and a stale holder that no longer has a lease cannot be named by
// the account trying to use the machine.
func (s *Slots) ClearWorkerSlot(ctx context.Context, workerID string) error {
	if err := s.client.Del(ctx, slotKey(workerID)).Err(); err != nil {
		return fmt.Errorf("poolmgr: clear worker slot: %w", err)
	}
	return nil
}

// ClearAllWorkerSlots drops every worker slot in the fleet.
//
// Recovery only: after a machine's browsers are closed, the slots describing
// them are stale by definition, and each one keeps its worker "busy" to the
// scheduler until the TTL expires. Callers must have already cleared the
// browsers; this is the bookkeeping that follows.
func (s *Slots) ClearAllWorkerSlots(ctx context.Context) (int, error) {
	keys, err := s.client.Keys(ctx, slotKey("*")).Result()
	if err != nil {
		return 0, fmt.Errorf("poolmgr: scan worker slots: %w", err)
	}
	if len(keys) == 0 {
		return 0, nil
	}
	n, err := s.client.Del(ctx, keys...).Result()
	if err != nil {
		return 0, fmt.Errorf("poolmgr: clear worker slots: %w", err)
	}
	return int(n), nil
}

// ReleaseAccountSlots drops EVERY worker slot this account holds, whatever the
// kind and whoever the worker is.
//
// ReleaseWorkerSlot needs the worker id, which is exactly what is missing when
// a session left a slot behind and its DB lease is already gone: the caller has
// nothing to point at, the worker answers "busy" forever, and the only cure was
// the TTL. This is the recovery path.
func (s *Slots) ReleaseAccountSlots(ctx context.Context, accountID string) (int, error) {
	keys, err := s.client.Keys(ctx, slotKey("*")).Result()
	if err != nil {
		return 0, fmt.Errorf("poolmgr: scan worker slots: %w", err)
	}
	released := 0
	for _, k := range keys {
		val, err := s.client.Get(ctx, k).Result()
		if err != nil {
			continue
		}
		// Values are "<kind>:<accountID>".
		if strings.HasSuffix(val, ":"+accountID) {
			n, err := s.client.Del(ctx, k).Result()
			if err != nil {
				return released, fmt.Errorf("poolmgr: release worker slot: %w", err)
			}
			released += int(n)
		}
	}
	return released, nil
}
