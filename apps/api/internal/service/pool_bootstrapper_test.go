package service

import (
	"context"
	"testing"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
)

// A 486-account fleet may not be pinned to one city because the hash "usually"
// spreads: the point of the backfill is distribution, so prove it happens.
func TestBackfillLocatesEveryAccountDeterministically(t *testing.T) {
	store := newFakeAccountStore()
	for i := range 50 {
		_, _ = store.Create(context.Background(), domain.Account{
			ID:       "acct-" + string(rune('a'+i%26)) + "-" + itoa(i),
			Platform: domain.PlatformInstagram,
			Username: "u" + itoa(i),
			Status:   domain.AccountActive,
		})
	}
	bs := NewPoolBootstrapper(store, nil, nil)

	n, err := bs.BackfillAccounts(context.Background())
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if n != 50 {
		t.Fatalf("backfilled %d accounts, want 50", n)
	}

	cities := map[string]int{}
	for _, a := range store.accounts {
		if a.Location == nil || a.Latitude == nil || a.Longitude == nil {
			t.Fatalf("account %s still unlocated", a.ID)
		}
		if a.Timezone != domain.TimezoneForCity(*a.Location) {
			t.Fatalf("account %s: timezone %q does not match city %q", a.ID, a.Timezone, *a.Location)
		}
		if a.UserAgent == nil || *a.UserAgent != domain.DefaultDesktopUserAgent {
			t.Fatalf("account %s has no default user agent", a.ID)
		}
		cities[*a.Location]++
	}
	if len(cities) < 2 {
		t.Fatalf("every account landed in one city: %v", cities)
	}

	// A second pass must change nothing: the identity is frozen per account.
	before := map[string]string{}
	for id, a := range store.accounts {
		before[id] = *a.Location
	}
	n, err = bs.BackfillAccounts(context.Background())
	if err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	if n != 0 {
		t.Fatalf("second pass touched %d accounts; it must be idempotent", n)
	}
	for id, a := range store.accounts {
		if *a.Location != before[id] {
			t.Fatalf("account %s moved from %s to %s", id, before[id], *a.Location)
		}
	}
}

func TestParsePoolEndpoints(t *testing.T) {
	defs, err := ParsePoolEndpoints("smm-worker-01=https://rpc.a.boxd.sh|https://a.boxd.sh/novnc, smm-worker-02=https://rpc.b.boxd.sh")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(defs) != 2 {
		t.Fatalf("got %d defs", len(defs))
	}
	if defs[0].Name != "smm-worker-01" || defs[0].RPCURL != "https://rpc.a.boxd.sh" ||
		defs[0].NoVNCURL != "https://a.boxd.sh/novnc" {
		t.Fatalf("first def wrong: %+v", defs[0])
	}
	if defs[1].NoVNCURL != "" {
		t.Fatalf("second def must have no novnc: %+v", defs[1])
	}

	for _, bad := range []string{"no-equals", "name=", "=https://x"} {
		if _, err := ParsePoolEndpoints(bad); err == nil {
			t.Fatalf("%q must be rejected", bad)
		}
	}
	if defs, err := ParsePoolEndpoints(""); err != nil || len(defs) != 0 {
		t.Fatalf("empty config must yield no defs, got %v %v", defs, err)
	}
}
