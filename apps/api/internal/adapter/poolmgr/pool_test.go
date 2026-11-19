package poolmgr

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

// A slot whose account no longer has a DB lease is invisible to
// ReleaseWorkerSlot (it needs the worker id, which is the thing that went
// missing). ReleaseAccountSlots is the recovery sweep that finds it by account
// alone — the state behind "conflict: worker busy" with an empty lease table.
// A slot held by a DIFFERENT account cannot be freed by an owner-scoped
// release, so on a serial login machine it blocks every other account until its
// TTL — while the machine itself answers "idle". ClearWorkerSlot is the
// unconditional path the reclaim branch uses after proving the display is free.
func TestClearWorkerSlotDropsForeignOwner(t *testing.T) {
	client := &fakeCmdable{data: map[string]string{
		slotKey("w-1"): "auth:acc-previous",
	}}
	slots := NewSlots(client)

	if err := slots.ClearWorkerSlot(context.Background(), "w-1"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, still := client.data[slotKey("w-1")]; still {
		t.Fatalf("clear must free the slot whatever account holds it")
	}
}

func TestReleaseAccountSlotsFindsStraySlots(t *testing.T) {
	client := &fakeCmdable{data: map[string]string{
		slotKey("w-1"): "auth:acc-a",
		slotKey("w-2"): "auth:acc-a",
		slotKey("w-3"): "auth:acc-b",
	}}
	slots := NewSlots(client)

	n, err := slots.ReleaseAccountSlots(context.Background(), "acc-a")
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 2 {
		t.Fatalf("released %d slots, want 2", n)
	}
	if _, ok := client.data[slotKey("w-3")]; !ok {
		t.Fatalf("another account's slot was removed")
	}
	if len(client.data) != 1 {
		t.Fatalf("left %d keys, want 1", len(client.data))
	}
}

// fakeCmdable implements only what the sweep touches.
type fakeCmdable struct {
	redis.Cmdable
	data map[string]string
}

func (f *fakeCmdable) Keys(_ context.Context, pattern string) *redis.StringSliceCmd {
	prefix := strings.TrimSuffix(pattern, "*")
	var out []string
	for k := range f.data {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	cmd := redis.NewStringSliceCmd(context.Background())
	cmd.SetVal(out)
	return cmd
}

func (f *fakeCmdable) Get(_ context.Context, key string) *redis.StringCmd {
	cmd := redis.NewStringCmd(context.Background())
	if v, ok := f.data[key]; ok {
		cmd.SetVal(v)
	} else {
		cmd.SetErr(redis.Nil)
	}
	return cmd
}

func (f *fakeCmdable) Del(_ context.Context, keys ...string) *redis.IntCmd {
	var n int64
	for _, k := range keys {
		if _, ok := f.data[k]; ok {
			delete(f.data, k)
			n++
		}
	}
	cmd := redis.NewIntCmd(context.Background())
	cmd.SetVal(n)
	return cmd
}
