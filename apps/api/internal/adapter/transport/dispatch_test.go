package transport

import (
	"context"
	"errors"
	"testing"
)

// TestDispatch_CommitBeforeEffect proves the ordering guarantee: the effect must
// never run when the commit fails, and must run exactly once when it succeeds.
func TestDispatch_CommitBeforeEffect(t *testing.T) {
	d := OrderedDispatcher{}
	var order []string

	commit := func(context.Context) error { order = append(order, "commit"); return nil }
	effect := func(context.Context) error { order = append(order, "effect"); return nil }

	if err := d.Dispatch(context.Background(), commit, effect); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(order) != 2 || order[0] != "commit" || order[1] != "effect" {
		t.Fatalf("want [commit effect], got %v", order)
	}
}

func TestDispatch_SuppressesEffectWhenCommitFails(t *testing.T) {
	d := OrderedDispatcher{}
	boom := errors.New("db down")
	var effectRan bool

	err := d.Dispatch(context.Background(),
		func(context.Context) error { return boom },
		func(context.Context) error { effectRan = true; return nil },
	)
	if err == nil {
		t.Fatal("want error when commit fails")
	}
	if effectRan {
		t.Fatal("effect must not run when the commit fails")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("error should wrap the commit error, got %v", err)
	}
}

func TestDispatch_EffectFailureAfterCommit(t *testing.T) {
	d := OrderedDispatcher{}
	boom := errors.New("redis down")

	err := d.Dispatch(context.Background(),
		func(context.Context) error { return nil },
		func(context.Context) error { return boom },
	)
	if !errors.Is(err, boom) {
		t.Fatalf("effect error should wrap the cause, got %v", err)
	}
}

func TestDispatch_RejectsNilFuncs(t *testing.T) {
	d := OrderedDispatcher{}
	if err := d.Dispatch(context.Background(), nil, func(context.Context) error { return nil }); err == nil {
		t.Fatal("want error for nil commit")
	}
	if err := d.Dispatch(context.Background(), func(context.Context) error { return nil }, nil); err == nil {
		t.Fatal("want error for nil effect")
	}
}
