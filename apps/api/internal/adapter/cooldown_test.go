package adapter

import (
	"context"
	"testing"
	"time"
)

// P3-09 cooldown gate tests. Real Redis (SMM_TEST_REDIS=1) because the
// guarantee is the atomicity of SET NX — a fake cannot prove two racers do not
// both pass.

func newTestGate(t *testing.T) *CooldownGate {
	t.Helper()
	c := newTestRedis(t)
	return NewCooldownGate(c, "test-cooldown")
}

func TestCooldownAcquireOnceThenBlock(t *testing.T) {
	gate := newTestGate(t)
	ctx := context.Background()
	const account = "acct-1"
	const target = "https://instagram.com/p/abc"

	// First acquire wins.
	ok, err := gate.Acquire(ctx, account, target, time.Minute)
	if err != nil {
		t.Fatalf("acquire 1: %v", err)
	}
	if !ok {
		t.Fatal("first acquire must win the slot")
	}

	// Second acquire inside the window is refused — the double action is
	// prevented.
	ok, err = gate.Acquire(ctx, account, target, time.Minute)
	if err != nil {
		t.Fatalf("acquire 2: %v", err)
	}
	if ok {
		t.Fatal("second acquire inside the window must be refused")
	}
}

func TestCooldownDifferentTargetsIndependent(t *testing.T) {
	gate := newTestGate(t)
	ctx := context.Background()

	if _, err := gate.Acquire(ctx, "acct-2", "https://instagram.com/p/one", time.Minute); err != nil {
		t.Fatalf("acquire one: %v", err)
	}
	// A different target under the same account is unaffected: cooldown is per
	// (account, target), not per account.
	ok, err := gate.Acquire(ctx, "acct-2", "https://instagram.com/p/two", time.Minute)
	if err != nil {
		t.Fatalf("acquire two: %v", err)
	}
	if !ok {
		t.Fatal("a different target must not be cooled down")
	}

	// A different account on the same target is also unaffected: cooldown never
	// starves a second account that has not acted yet.
	ok, err = gate.Acquire(ctx, "acct-3", "https://instagram.com/p/one", time.Minute)
	if err != nil {
		t.Fatalf("acquire other account: %v", err)
	}
	if !ok {
		t.Fatal("a different account must not be cooled down")
	}
}

func TestCooldownExpires(t *testing.T) {
	gate := newTestGate(t)
	ctx := context.Background()

	// A short window proves the TTL actually lapses and the slot is released
	// without any explicit Release call.
	if _, err := gate.Acquire(ctx, "acct-4", "https://instagram.com/p/exp", 500*time.Millisecond); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	time.Sleep(700 * time.Millisecond)
	ok, err := gate.Acquire(ctx, "acct-4", "https://instagram.com/p/exp", 500*time.Millisecond)
	if err != nil {
		t.Fatalf("reacquire after ttl: %v", err)
	}
	if !ok {
		t.Fatal("the slot must be reclaimable once the window lapses")
	}
}

func TestCooldownRejectsInvalidInput(t *testing.T) {
	gate := newTestGate(t)
	ctx := context.Background()
	if _, err := gate.Acquire(ctx, "", "target", time.Minute); err == nil {
		t.Error("expected error for empty account")
	}
	if _, err := gate.Acquire(ctx, "acct", "", time.Minute); err == nil {
		t.Error("expected error for empty target")
	}
	if _, err := gate.Acquire(ctx, "acct", "target", 0); err == nil {
		t.Error("expected error for zero window")
	}
}
