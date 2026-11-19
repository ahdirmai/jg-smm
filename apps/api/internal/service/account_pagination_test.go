package service

import (
	"context"
	"strconv"
	"testing"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

type pagedAccountStore struct {
	*fakeAccountStore
	rows []domain.Account
}

func (s *pagedAccountStore) List(_ context.Context, f port.AccountFilter) ([]domain.Account, error) {
	if f.Offset >= len(s.rows) {
		return nil, nil
	}
	end := f.Offset + f.Limit
	if end > len(s.rows) {
		end = len(s.rows)
	}
	return s.rows[f.Offset:end], nil
}
func TestAccountListIncludesOlderLoginResults(t *testing.T) {
	store := &pagedAccountStore{fakeAccountStore: newFakeAccountStore()}
	for i := 0; i < 486; i++ {
		store.rows = append(store.rows, domain.Account{ID: strconv.Itoa(i), AuthStatus: domain.AuthPending})
	}
	store.rows[485].AuthStatus = domain.AuthAuthenticated
	svc := NewAccountService(store, newFakeWorkerStore(), nil, AccountConfig{})
	rows, err := svc.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 486 {
		t.Fatalf("got %d accounts, want 486", len(rows))
	}
	if rows[485].AuthStatus != domain.AuthAuthenticated {
		t.Fatal("older verified account missing")
	}
}

// --- stateless pool (000024): embedded fake covers the base store; ListAll is
// overridden so a pagination test can hand in a whole fleet unpaginated.
func (s *pagedAccountStore) ListAll(_ context.Context) ([]domain.Account, error) {
	return s.rows, nil
}
