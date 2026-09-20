package service

import (
	"context"
	"testing"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
)

// AccountsByRegion groups accounts by their worker's region, puts an account
// with no worker (or a worker with a blank region) under "unassigned", and
// sorts real regions alphabetically with unassigned last.
func TestAccountsByRegion(t *testing.T) {
	accounts := newFakeAccountStore()
	workers := newFakeWorkerStore()
	svc := NewAccountService(accounts, workers, nil, AccountConfig{})

	workers.seed(domain.Worker{ID: "w-id", Region: "ID"})
	workers.seed(domain.Worker{ID: "w-sg", Region: "SG"})
	workers.seed(domain.Worker{ID: "w-blank", Region: ""})

	accounts.seed(accountFixtureWithWorker("a1", domain.PlatformInstagram, "w-sg"))
	accounts.seed(accountFixtureWithWorker("a2", domain.PlatformInstagram, "w-id"))
	accounts.seed(accountFixtureWithWorker("a3", domain.PlatformThreads, "w-id"))
	accounts.seed(accountFixtureWithWorker("a4", domain.PlatformInstagram, "w-blank")) // blank region -> unassigned
	accounts.seed(accountFixture("a5", domain.PlatformInstagram))                      // no worker -> unassigned

	groups, err := svc.AccountsByRegion(context.Background())
	if err != nil {
		t.Fatalf("by-region: %v", err)
	}

	// Order: ID, SG, unassigned (real regions alphabetical, unassigned last).
	gotOrder := make([]string, len(groups))
	counts := map[string]int{}
	for i, g := range groups {
		gotOrder[i] = g.Region
		counts[g.Region] = len(g.Accounts)
	}
	want := []string{"ID", "SG", UnassignedRegion}
	if len(gotOrder) != len(want) {
		t.Fatalf("regions = %v, want %v", gotOrder, want)
	}
	for i := range want {
		if gotOrder[i] != want[i] {
			t.Fatalf("region order = %v, want %v", gotOrder, want)
		}
	}
	if counts["ID"] != 2 {
		t.Fatalf("ID accounts = %d, want 2", counts["ID"])
	}
	if counts["SG"] != 1 {
		t.Fatalf("SG accounts = %d, want 1", counts["SG"])
	}
	if counts[UnassignedRegion] != 2 {
		t.Fatalf("unassigned accounts = %d, want 2 (blank-region worker + no worker)", counts[UnassignedRegion])
	}
}
