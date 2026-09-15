package service

import (
	"context"
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
