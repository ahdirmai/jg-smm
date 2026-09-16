package service

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
)

// stubSealer is a port.Sealer that reverses plaintext so tests can prove the\n// stored blob is never the plaintext credential.
type stubSealer struct{}

func (stubSealer) Seal(p []byte) ([]byte, error) {
	out := make([]byte, len(p))
	for i, b := range p {
		out[i] = b ^ 0x5a
	}
	return out, nil
}

func (stubSealer) Open(c []byte) ([]byte, error) {
	return stubSealer{}.Seal(c) // symmetric xor
}

func TestAccountCreateSealsCredential(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	packer := NewPacker(store, accounts, PackerConfig{MaxPerContainer: 2, AutoCreate: true})
	svc := NewAccountService(accounts, store, packer, AccountConfig{Sealer: stubSealer{}})

	ctx := context.Background()
	summary, worker, err := svc.Create(ctx, AccountInput{
		Platform: domain.PlatformInstagram,
		Username: "growthco",
		Password: "s3cret-pw",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if summary.ID == "" {
		t.Fatal("no account id")
	}
	if worker == nil {
		t.Fatal("account should be packed into a container")
	}

	// The stored blob must not contain the plaintext password.
	stored := accounts.accounts[summary.ID]
	for i := range stored.PasswordEnc {
		if stored.PasswordEnc[i] == 's' {
			t.Fatalf("plaintext byte leaked into the sealed blob: % x", stored.PasswordEnc)
		}
	}
	if stored.AuthStatus != domain.AuthAuthenticating {
		t.Fatalf("authStatus = %q, want AUTHENTICATING", stored.AuthStatus)
	}
	if stored.Status != domain.AccountActive {
		t.Fatalf("status = %q, want ACTIVE", stored.Status)
	}
	if stored.WorkerID == nil || *stored.WorkerID != worker.ID {
		t.Fatalf("worker_id = %v, want %s", stored.WorkerID, worker.ID)
	}
}

func TestAccountCreateValidation(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	svc := NewAccountService(accounts, store, nil, AccountConfig{Sealer: stubSealer{}})
	ctx := context.Background()

	cases := []struct {
		name string
		in   AccountInput
	}{
		{"missing username", AccountInput{Platform: domain.PlatformInstagram, Password: "pw"}},
		{"missing password", AccountInput{Platform: domain.PlatformInstagram, Username: "u"}},
		{"bad platform", AccountInput{Platform: domain.Platform("myspace"), Username: "u", Password: "pw"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := svc.Create(ctx, tc.in); err == nil {
				t.Fatal("want validation error")
			}
		})
	}
}

func TestAccountCreateRequiresSealer(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	svc := NewAccountService(accounts, store, nil, AccountConfig{})
	if _, _, err := svc.Create(context.Background(), AccountInput{
		Platform: domain.PlatformInstagram, Username: "u", Password: "pw",
	}); err == nil {
		t.Fatal("want error when no sealer is configured")
	}
}

func TestAccountCreateDuplicate(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	packer := NewPacker(store, accounts, PackerConfig{MaxPerContainer: 2})
	svc := NewAccountService(accounts, store, packer, AccountConfig{Sealer: stubSealer{}})
	ctx := context.Background()

	in := AccountInput{Platform: domain.PlatformInstagram, Username: "dupe", Password: "pw"}
	if _, _, err := svc.Create(ctx, in); err != nil {
		t.Fatalf("create 1: %v", err)
	}
	accounts.uniqueKeys["instagram|dupe"] = true
	if _, _, err := svc.Create(ctx, in); err == nil {
		t.Fatal("want conflict for a duplicate platform+username")
	}
}

func TestAccountListExcludesCredentials(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	packer := NewPacker(store, accounts, PackerConfig{MaxPerContainer: 2})
	svc := NewAccountService(accounts, store, packer, AccountConfig{Sealer: stubSealer{}})
	ctx := context.Background()

	if _, _, err := svc.Create(ctx, AccountInput{
		Platform: domain.PlatformInstagram, Username: "u", Password: "s3cret-pw",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	views, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("len(views) = %d, want 1", len(views))
	}
	if views[0].Username != "u" {
		t.Fatalf("username = %q, want u", views[0].Username)
	}
}

func TestAccountPauseAndResume(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	svc := NewAccountService(accounts, store, nil, AccountConfig{Sealer: stubSealer{}})
	ctx := context.Background()

	accounts.seed(accountFixture("a1", domain.PlatformInstagram))

	paused, err := svc.Pause(ctx, "a1")
	if err != nil {
		t.Fatalf("pause: %v", err)
	}
	if paused.Status != domain.AccountPaused {
		t.Fatalf("status = %q, want PAUSED", paused.Status)
	}
	if got := accounts.get("a1").Status; got != domain.AccountPaused {
		t.Fatalf("stored status = %q, want PAUSED", got)
	}

	resumed, err := svc.Resume(ctx, "a1")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed.Status != domain.AccountActive {
		t.Fatalf("status = %q, want ACTIVE", resumed.Status)
	}
	if got := accounts.get("a1").Status; got != domain.AccountActive {
		t.Fatalf("stored status = %q, want ACTIVE", got)
	}
}

func TestAccountPauseIsIdempotentOnArchived(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	svc := NewAccountService(accounts, store, nil, AccountConfig{Sealer: stubSealer{}})
	ctx := context.Background()

	archived := accountFixture("a1", domain.PlatformInstagram)
	archived.Status = domain.AccountArchived
	accounts.seed(archived)

	if _, err := svc.Pause(ctx, "a1"); err != nil {
		t.Fatalf("pause on archived should be a no-op: %v", err)
	}
	if got := accounts.get("a1").Status; got != domain.AccountArchived {
		t.Fatalf("archived account must not be paused, got %q", got)
	}
}

func TestAccountResumeOnlyPaused(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	svc := NewAccountService(accounts, store, nil, AccountConfig{Sealer: stubSealer{}})
	ctx := context.Background()

	active := accountFixture("a1", domain.PlatformInstagram)
	accounts.seed(active)

	if _, err := svc.Resume(ctx, "a1"); err != nil {
		t.Fatalf("resume on active should be a no-op: %v", err)
	}
}

func TestAccountRemoveReleasesSlot(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	packer := NewPacker(store, accounts, PackerConfig{MaxPerContainer: 2})
	svc := NewAccountService(accounts, store, packer, AccountConfig{Sealer: stubSealer{}})
	ctx := context.Background()

	summary, _, err := svc.Create(ctx, AccountInput{
		Platform: domain.PlatformInstagram, Username: "u", Password: "pw",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.Remove(ctx, summary.ID); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, ok := accounts.accounts[summary.ID]; ok {
		t.Fatal("account row should be removed")
	}
}

func TestAccountPauseMissing(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	svc := NewAccountService(accounts, store, nil, AccountConfig{Sealer: stubSealer{}})
	if _, err := svc.Pause(context.Background(), "ghost"); err == nil {
		t.Fatal("want error for a missing account")
	}
}

func TestAccountImport(t *testing.T) {
	newSvc := func() *AccountService {
		store := newFakeWorkerStore()
		accounts := newFakeAccountStore()
		packer := NewPacker(store, accounts, PackerConfig{MaxPerContainer: 10, AutoCreate: true})
		return NewAccountService(accounts, store, packer, AccountConfig{Sealer: stubSealer{}})
	}

	row := func(u string) AccountInput {
		return AccountInput{Platform: domain.PlatformInstagram, Username: u, Password: "pw"}
	}

	t.Run("queues valid rows and reports invalid ones", func(t *testing.T) {
		svc := newSvc()
		res, err := svc.Import(context.Background(), "1.2.3.4", []AccountInput{
			row("ok-1"),
			{Platform: domain.PlatformInstagram, Username: "", Password: "pw"}, // invalid: no username
			row("ok-2"),
		})
		if err != nil {
			t.Fatalf("import: %v", err)
		}
		if res.Queued != 2 {
			t.Errorf("queued = %d, want 2", res.Queued)
		}
		if len(res.Invalid) != 1 || res.Invalid[0].Row != 1 {
			t.Errorf("invalid = %+v, want row 1", res.Invalid)
		}
		if res.RateLimited {
			t.Error("rate limited without exceeding the budget")
		}
	})

	t.Run("rejects an empty import", func(t *testing.T) {
		svc := newSvc()
		if _, err := svc.Import(context.Background(), "1.2.3.4", nil); err == nil {
			t.Fatal("want validation error for an empty import")
		}
	})

	t.Run("rejects an over-cap import", func(t *testing.T) {
		svc := newSvc()
		rows := make([]AccountInput, MaxImportRows+1)
		for i := range rows {
			rows[i] = row("u-" + strconv.Itoa(i))
		}
		if _, err := svc.Import(context.Background(), "1.2.3.4", rows); err == nil {
			t.Fatal("want validation error over the import cap")
		}
	})

	t.Run("rate limits above the budget", func(t *testing.T) {
		svc := newSvc()
		// ImportRateLimit per minute per caller; the 11th call is refused.
		for i := 0; i < ImportRateLimit; i++ {
			if _, err := svc.Import(context.Background(), "1.2.3.4", []AccountInput{row("rl-" + strconv.Itoa(i))}); err != nil {
				t.Fatalf("import %d: unexpected error: %v", i, err)
			}
		}
		res, err := svc.Import(context.Background(), "1.2.3.4", []AccountInput{row("rl-over")})
		if !errors.Is(err, domain.ErrRateLimited) {
			t.Fatalf("over-budget error = %v, want ErrRateLimited", err)
		}
		if !res.RateLimited {
			t.Error("rateLimited flag not set on refusal")
		}
		if res.Queued != 0 {
			t.Errorf("over-budget import queued %d rows, want 0", res.Queued)
		}
	})

	t.Run("rate limit is per caller", func(t *testing.T) {
		svc := newSvc()
		for i := 0; i < ImportRateLimit; i++ {
			if _, err := svc.Import(context.Background(), "1.2.3.4", []AccountInput{row("a-" + strconv.Itoa(i))}); err != nil {
				t.Fatalf("import %d: %v", i, err)
			}
		}
		// A different caller is a fresh budget.
		if _, err := svc.Import(context.Background(), "5.6.7.8", []AccountInput{row("b-1")}); err != nil {
			t.Fatalf("second caller import: %v", err)
		}
	})
}

// TestAccountResumeResetsQuarantine covers the P5-02 manual reset: an
// auto-quarantined account is out of the pool until an operator explicitly
// resumes it, and that resume must restore the score, otherwise the account
// re-enters the pool still sitting under the quarantine threshold.
func TestAccountResumeResetsQuarantine(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	svc := NewAccountService(accounts, store, nil, AccountConfig{Sealer: stubSealer{}})
	ctx := context.Background()

	quarantined := accountFixture("q1", domain.PlatformInstagram)
	quarantined.Status = domain.AccountQuarantined
	quarantined.HealthScore = 12
	accounts.seed(quarantined)

	resumed, err := svc.Resume(ctx, "q1")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed.Status != domain.AccountActive {
		t.Fatalf("status = %q, want ACTIVE", resumed.Status)
	}
	if resumed.HealthScore != DefaultHealthScorePolicy.ScoreMax {
		t.Errorf("healthScore = %d, want %d after a manual reset", resumed.HealthScore, DefaultHealthScorePolicy.ScoreMax)
	}
	if stored, _ := accounts.GetByID(ctx, "q1"); stored.Status != domain.AccountActive {
		t.Errorf("stored status = %q, want ACTIVE", stored.Status)
	}
}
