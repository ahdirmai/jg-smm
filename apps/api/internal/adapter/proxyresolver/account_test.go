package proxyresolver

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ahdirmai/jg-smm/apps/api/internal/adapter/crypto"
	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
)

type memPoolGroups struct {
	groups map[string]domain.ProxyGroup
}

func (m memPoolGroups) GetByID(_ context.Context, id string) (domain.ProxyGroup, error) {
	g, ok := m.groups[id]
	if !ok {
		return domain.ProxyGroup{}, domain.ErrNotFound
	}
	return g, nil
}

func sealerForTest(t *testing.T) *crypto.AESGCM {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	s, err := crypto.NewAESGCM(key)
	if err != nil {
		t.Fatalf("sealer: %v", err)
	}
	return s
}

func sealedGroup(t *testing.T, s *crypto.AESGCM, id, poolKey string) domain.ProxyGroup {
	t.Helper()
	sealed, err := s.Seal([]byte(poolKey))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	return domain.ProxyGroup{ID: id, PoolKeyEnc: sealed}
}

// A proxyless account resolves to nil: the caller must pin it to one worker
// rather than let its egress IP change per job.
func TestForAccountWithoutGroupIsNil(t *testing.T) {
	s := sealerForTest(t)
	r := New(nil, s)
	store := memPoolGroups{groups: map[string]domain.ProxyGroup{}}
	r = r.WithGroups(store)

	got, err := r.ForAccount(t.Context(), "acct-1", nil)
	if err != nil || got != nil {
		t.Fatalf("no group: got %+v err %v, want nil nil", got, err)
	}
	missing := "11111111-1111-1111-1111-111111111111"
	got, err = r.ForAccount(t.Context(), "acct-1", &missing)
	if err != nil || got != nil {
		t.Fatalf("unknown group: got %+v err %v, want nil nil", got, err)
	}
}

// The sticky placeholder becomes a token derived from the account id, so every
// session for one account lands on the same residential IP.
func TestForAccountSubstitutesStickyPlaceholder(t *testing.T) {
	s := sealerForTest(t)
	group := sealedGroup(t, s, "g1", "http://user-{account_id}:pw@gate.example.com:8080")
	r := New(nil, s)
	store := memPoolGroups{groups: map[string]domain.ProxyGroup{"g1": group}}
	r = r.WithGroups(store)
	gid := "g1"

	const accountID = "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	spec, err := r.ForAccount(t.Context(), accountID, &gid)
	if err != nil {
		t.Fatalf("ForAccount: %v", err)
	}
	if spec == nil {
		t.Fatalf("expected a spec")
	}
	if strings.Contains(spec.Username, "{account_id}") || strings.Contains(spec.Username, "{session}") {
		t.Fatalf("placeholder left in username: %q", spec.Username)
	}
	want := "user-" + stickyToken(accountID)
	if spec.Username != want {
		t.Fatalf("username = %q, want %q", spec.Username, want)
	}
	if len(stickyToken(accountID)) != 12 {
		t.Fatalf("sticky token must be 12 chars, got %q", stickyToken(accountID))
	}
	if stickyToken(accountID) != stickyToken(accountID) {
		t.Fatalf("sticky token must be deterministic")
	}
	// {session} is the other spelling providers document.
	group2 := sealedGroup(t, s, "g2", "http://sess-{session}:pw@gate.example.com:8080")
	store.groups["g2"] = group2
	gid2 := "g2"
	spec2, err := r.ForAccount(t.Context(), accountID, &gid2)
	if err != nil {
		t.Fatalf("ForAccount {session}: %v", err)
	}
	if strings.Contains(spec2.Username, "{session}") {
		t.Fatalf("{session} placeholder left in username: %q", spec2.Username)
	}
}

// socks5 with credentials is silently unauthenticated in Chromium, which would
// egress from the worker's own datacentre IP. Refuse it explicitly.
func TestForAccountRejectsSocks5WithAuth(t *testing.T) {
	s := sealerForTest(t)
	group := sealedGroup(t, s, "g1", "socks5://user:pass@gate.example.com:1080")
	r := New(nil, s)
	store := memPoolGroups{groups: map[string]domain.ProxyGroup{"g1": group}}
	r = r.WithGroups(store)
	gid := "g1"

	_, err := r.ForAccount(t.Context(), "acct-1", &gid)
	if !errors.Is(err, ErrSocksAuthUnsupported) {
		t.Fatalf("err = %v, want ErrSocksAuthUnsupported", err)
	}

	// The same proxy without credentials is fine.
	open := sealedGroup(t, s, "g2", "socks5://gate.example.com:1080")
	store.groups["g2"] = open
	gid2 := "g2"
	spec, err := r.ForAccount(t.Context(), "acct-1", &gid2)
	if err != nil {
		t.Fatalf("anonymous socks5 must be accepted: %v", err)
	}
	if spec.Server != "socks5://gate.example.com:1080" {
		t.Fatalf("server = %q", spec.Server)
	}
}
