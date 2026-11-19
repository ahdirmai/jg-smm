package service

import (
	"context"
	"errors"
	"testing"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// AccountsByRegion groups every account by ITS OWN city, sorts real cities
// alphabetically with "unassigned" last, and does not truncate the fleet.
//
// It used to read worker.region through the account's worker. In the stateless
// pool that join means nothing — a worker is shared and disposable — so the city
// lives on the account (000024). The old version also read a single 200-row
// page, which silently dropped 286 of a 486-account fleet.
func TestAccountsByRegion(t *testing.T) {
	accounts := newFakeAccountStore()
	svc := NewAccountService(accounts, newFakeWorkerStore(), nil, AccountConfig{})

	withCity := func(id string, platform domain.Platform, city string) domain.Account {
		a := accountFixture(id, platform)
		a.Location = new(city)
		a.Latitude = new(-6.2)
		a.Longitude = new(106.8)
		return a
	}

	accounts.seed(withCity("a1", domain.PlatformInstagram, "Surabaya"))
	accounts.seed(withCity("a2", domain.PlatformInstagram, "Jakarta"))
	accounts.seed(withCity("a3", domain.PlatformThreads, "Jakarta"))
	accounts.seed(withCity("a4", domain.PlatformInstagram, "Makassar"))
	accounts.seed(accountFixture("a5", domain.PlatformInstagram)) // no city -> unassigned

	groups, err := svc.AccountsByRegion(context.Background())
	if err != nil {
		t.Fatalf("by-region: %v", err)
	}

	gotOrder := make([]string, len(groups))
	counts := map[string]int{}
	for i, g := range groups {
		gotOrder[i] = g.Region
		counts[g.Region] = len(g.Accounts)
	}
	want := []string{"Jakarta", "Makassar", "Surabaya", UnassignedRegion}
	if len(gotOrder) != len(want) {
		t.Fatalf("regions = %v, want %v", gotOrder, want)
	}
	for i := range want {
		if gotOrder[i] != want[i] {
			t.Fatalf("region order = %v, want %v", gotOrder, want)
		}
	}
	if counts["Jakarta"] != 2 {
		t.Fatalf("Jakarta accounts = %d, want 2", counts["Jakarta"])
	}
	if counts[UnassignedRegion] != 1 {
		t.Fatalf("unassigned accounts = %d, want 1", counts[UnassignedRegion])
	}
}

// The rollup must not stop at one page: a 486-account fleet grouped by region
// has to report all 486.
func TestAccountsByRegionDoesNotTruncate(t *testing.T) {
	accounts := newFakeAccountStore()
	svc := NewAccountService(accounts, newFakeWorkerStore(), nil, AccountConfig{})

	const n = 250 // past the old 200-row page
	for i := range n {
		a := accountFixture("a"+itoa(i), domain.PlatformInstagram)
		a.Location = new("Jakarta")
		accounts.seed(a)
	}
	groups, err := svc.AccountsByRegion(context.Background())
	if err != nil {
		t.Fatalf("by-region: %v", err)
	}
	total := 0
	for _, g := range groups {
		total += len(g.Accounts)
	}
	if total != n {
		t.Fatalf("grouped %d accounts, want %d: the rollup truncated the fleet", total, n)
	}
}

// A NEEDS_INPUT row blocks a re-login only while its browser is PROVABLY alive.
//
// The status plus a lease used to be treated as proof, so a worker that slept or
// was rebuilt left the account permanently telling the operator to "submit the
// code" into a browser that no longer existed. The guard now probes the worker;
// a dead one releases the stale session and lets the login proceed.
func TestPoolLoginRetryRulesForParkedAccounts(t *testing.T) {
	newSvc := func() (*AccountService, *fakeAccountStore) {
		accounts := newFakeAccountStore()
		workers := newFakeWorkerStore()
		svc := NewAccountService(accounts, workers, nil, AccountConfig{
			PoolMode: "stateless",
			Pool:     &PoolDispatch{},
		})
		return svc, accounts
	}

	// Parked AND leased, but nothing can confirm the browser: the login must NOT
	// be refused with "submit the code instead".
	svc, accounts := newSvc()
	stale := accountFixture("acc-stale", domain.PlatformInstagram)
	stale.AuthStatus = domain.AuthNeedsInput
	stale.WorkerID = new("w-1")
	accounts.seed(stale)
	if _, err := svc.Login(context.Background(), stale.ID); errors.Is(err, domain.ErrConflict) {
		t.Fatalf("a parked login with no live browser must be retryable, got %v", err)
	}

	// Parked with NO lease at all: nothing holds it, so a retry proceeds.
	svc2, accounts2 := newSvc()
	unleased := accountFixture("acc-unleased", domain.PlatformInstagram)
	unleased.AuthStatus = domain.AuthNeedsInput
	accounts2.seed(unleased)
	if _, err := svc2.Login(context.Background(), unleased.ID); errors.Is(err, domain.ErrConflict) {
		t.Fatalf("an unleased parked login must be retryable, got %v", err)
	}
}

// parkedLoginIsLive must answer "does THIS account own the display", not "is
// the machine up". A healthy worker holding nothing (or someone else's context)
// cannot be showing the operator's 2FA prompt, and treating liveness as proof
// left dead parked logins blocking every retry.
func TestParkedLoginIsLiveChecksOwnership(t *testing.T) {
	cases := []struct {
		name string
		held string
		want bool
	}{
		{"held by this account", "acc-parked", true},
		{"idle worker", "", false},
		{"held by another account", "acc-other", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			held := tc.held
			pool := &PoolDispatch{RPC: rpcHolding{held: held}}
			workers := newFakeWorkerStore()
			endpoint := "http://127.0.0.1:18090"
			workers.seed(domain.Worker{ID: "w-1", Name: "smm-worker-01", EndpointURL: &endpoint})

			accounts := newFakeAccountStore()
			svc := NewAccountService(accounts, workers, nil, AccountConfig{PoolMode: "stateless", Pool: pool})

			acc := domain.Account{ID: "acc-parked", WorkerID: new("w-1")}
			if got := svc.parkedLoginIsLive(context.Background(), acc); got != tc.want {
				t.Fatalf("parkedLoginIsLive = %v, want %v (worker holds %q)", got, tc.want, held)
			}
		})
	}
}

// rpcHolding is a WorkerRPC whose display is owned by a fixed account.
type rpcHolding struct{ held string }

func (r rpcHolding) ExecuteAction(context.Context, string, port.ExecuteActionRequest) error {
	return nil
}
func (r rpcHolding) StartLogin(context.Context, string, port.AccountRuntimeSpec) error { return nil }
func (r rpcHolding) SubmitAuthInput(context.Context, string, string, string, string) error {
	return nil
}
func (r rpcHolding) ClearAuth(context.Context, string, string) error { return nil }
func (r rpcHolding) OpenBrowser(context.Context, string, port.AccountRuntimeSpec, string) error {
	return nil
}
func (r rpcHolding) PingKeepalive(context.Context, string) error { return nil }
func (r rpcHolding) BusyAccount(context.Context, string) (string, error) {
	return r.held, nil
}
