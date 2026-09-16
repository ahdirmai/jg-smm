package adapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// CooldownGate implements port.CooldownGate on Redis with SET ... NX PX: the
// first writer wins the slot and every later one inside the window is told to
// back off. NX is the whole trick — it is an atomic compare-and-set, so two
// enqueues racing on the same target cannot both pass.
type CooldownGate struct {
	client    *redis.Client
	keyPrefix string
}

// NewCooldownGate binds the gate to a redis client. keyPrefix scopes the
// cooldown keyspace so it never collides with the transport queues.
func NewCooldownGate(client *redis.Client, keyPrefix string) *CooldownGate {
	if keyPrefix == "" {
		keyPrefix = "cooldown"
	}
	return &CooldownGate{client: client, keyPrefix: keyPrefix}
}

var _ port.CooldownGate = (*CooldownGate)(nil)

// Acquire claims the cooldown slot. The target key is hashed (not the account:
// the account id is already a safe identifier) so an arbitrary-length target
// URL cannot overflow the key and two different targets never share a slot.
func (g *CooldownGate) Acquire(ctx context.Context, accountID, targetKey string, window time.Duration) (bool, error) {
	if accountID == "" || targetKey == "" {
		// An empty account or target has no meaningful cooldown to enforce;
		// refusing here keeps a malformed enqueue from silently poisoning the
		// keyspace with a wildcard-like key.
		return false, fmt.Errorf("cooldown: account and target are required")
	}
	if window <= 0 {
		return false, fmt.Errorf("cooldown: window must be positive, got %s", window)
	}
	key := g.key(accountID, targetKey)
	ok, err := g.client.SetNX(ctx, key, "1", window).Result()
	if err != nil {
		return false, fmt.Errorf("cooldown.SetNX %s: %w", key, err)
	}
	return ok, nil
}

// key is stable per (prefix, kind, account, target). The kind separates a
// comment cooldown from a like cooldown when the two are ever given different
// windows: the ticket scopes the gate to comment actions.
func (g *CooldownGate) key(accountID, targetKey string) string {
	sum := sha256.Sum256([]byte(targetKey))
	return fmt.Sprintf("%s:comment:%s:%s", g.keyPrefix, accountID, hex.EncodeToString(sum[:]))
}
