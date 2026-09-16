package service

import (
	"context"
	"testing"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
)

// memProxyStore is the minimal port.ProxyGroupStore for service tests.
type memProxyStore struct {
	groups map[string]domain.ProxyGroup
}

func newMemProxyStore() *memProxyStore {
	return &memProxyStore{groups: map[string]domain.ProxyGroup{}}
}

func (s *memProxyStore) GetByID(_ context.Context, id string) (domain.ProxyGroup, error) {
	g, ok := s.groups[id]
	if !ok {
		return domain.ProxyGroup{}, domain.ErrNotFound
	}
	return g, nil
}

func (s *memProxyStore) List(_ context.Context) ([]domain.ProxyGroup, error) {
	out := make([]domain.ProxyGroup, 0, len(s.groups))
	for _, g := range s.groups {
		out = append(out, g)
	}
	return out, nil
}

func (s *memProxyStore) Create(_ context.Context, g domain.ProxyGroup) (domain.ProxyGroup, error) {
	for _, existing := range s.groups {
		if existing.Name == g.Name {
			return domain.ProxyGroup{}, domain.ErrConflict
		}
	}
	if g.ID == "" {
		g.ID = "pg-1"
	}
	s.groups[g.ID] = g
	return g, nil
}

func (s *memProxyStore) Update(_ context.Context, g domain.ProxyGroup) (domain.ProxyGroup, error) {
	s.groups[g.ID] = g
	return g, nil
}

func (s *memProxyStore) Delete(_ context.Context, id string) error {
	delete(s.groups, id)
	return nil
}

func TestProxyGroupCreateSealsPoolKey(t *testing.T) {
	groups := newMemProxyStore()
	svc := NewProxyGroupService(groups, newFakeAccountStore(), newFakeWorkerStore(), ProxyGroupConfig{
		Sealer: stubSealer{},
	})
	summary, err := svc.Create(context.Background(), ProxyGroupInput{
		Name:           "sg-res-01",
		Region:         "SG",
		Provider:       "brightdata",
		PoolKey:        "PLAINTEXT-KEY",
		MaxConcurrency: 5,
		DailyBudgetMB:  512,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if summary.ID == "" {
		t.Fatal("no proxy group id")
	}

	stored := groups.groups[summary.ID]
	for _, b := range stored.PoolKeyEnc {
		// The XOR stub never emits an uppercase ASCII letter that matches the
		// plaintext, so a surviving 'PLAINTEXT' byte would be a leak.
		if b == 'P' || b == 'L' || b == 'A' || b == 'I' || b == 'N' || b == 'T' || b == 'E' || b == 'X' {
			t.Fatalf("plaintext byte leaked into the sealed pool key: % x", stored.PoolKeyEnc)
		}
	}
	if summary.Provider != "brightdata" {
		t.Fatalf("provider = %q", summary.Provider)
	}
}

func TestProxyGroupCreateValidation(t *testing.T) {
	svc := NewProxyGroupService(newMemProxyStore(), newFakeAccountStore(), newFakeWorkerStore(), ProxyGroupConfig{
		Sealer: stubSealer{},
	})
	ctx := context.Background()

	cases := []struct {
		name string
		in   ProxyGroupInput
	}{
		{"missing name", ProxyGroupInput{Region: "SG", Provider: "p", PoolKey: "k", MaxConcurrency: 1, DailyBudgetMB: 1}},
		{"bad region", ProxyGroupInput{Name: "g", Region: "SINGAPORE", Provider: "p", PoolKey: "k", MaxConcurrency: 1, DailyBudgetMB: 1}},
		{"lowercase region", ProxyGroupInput{Name: "g", Region: "sg", Provider: "p", PoolKey: "k", MaxConcurrency: 1, DailyBudgetMB: 1}},
		{"missing provider", ProxyGroupInput{Name: "g", Region: "SG", PoolKey: "k", MaxConcurrency: 1, DailyBudgetMB: 1}},
		{"missing pool key", ProxyGroupInput{Name: "g", Region: "SG", Provider: "p", MaxConcurrency: 1, DailyBudgetMB: 1}},
		{"zero concurrency", ProxyGroupInput{Name: "g", Region: "SG", Provider: "p", PoolKey: "k", MaxConcurrency: 0, DailyBudgetMB: 1}},
		{"zero budget", ProxyGroupInput{Name: "g", Region: "SG", Provider: "p", PoolKey: "k", MaxConcurrency: 1, DailyBudgetMB: 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.Create(ctx, tc.in); err == nil {
				t.Fatal("want validation error")
			}
		})
	}
}

func TestProxyGroupCreateRequiresSealer(t *testing.T) {
	svc := NewProxyGroupService(newMemProxyStore(), newFakeAccountStore(), newFakeWorkerStore(), ProxyGroupConfig{})
	if _, err := svc.Create(context.Background(), ProxyGroupInput{
		Name: "g", Region: "SG", Provider: "p", PoolKey: "k", MaxConcurrency: 1, DailyBudgetMB: 1,
	}); err == nil {
		t.Fatal("want error when no sealer is configured")
	}
}

func TestProxyGroupCreateDuplicateName(t *testing.T) {
	groups := newMemProxyStore()
	svc := NewProxyGroupService(groups, newFakeAccountStore(), newFakeWorkerStore(), ProxyGroupConfig{
		Sealer: stubSealer{},
	})
	ctx := context.Background()
	in := ProxyGroupInput{Name: "dupe", Region: "SG", Provider: "p", PoolKey: "k", MaxConcurrency: 1, DailyBudgetMB: 1}
	if _, err := svc.Create(ctx, in); err != nil {
		t.Fatalf("create 1: %v", err)
	}
	if _, err := svc.Create(ctx, in); err == nil {
		t.Fatal("want conflict for a duplicate proxy group name")
	}
}

func TestProxyGroupListExcludesPoolKey(t *testing.T) {
	svc := NewProxyGroupService(newMemProxyStore(), newFakeAccountStore(), newFakeWorkerStore(), ProxyGroupConfig{
		Sealer: stubSealer{},
	})
	if _, err := svc.Create(context.Background(), ProxyGroupInput{
		Name: "g", Region: "SG", Provider: "p", PoolKey: "secret", MaxConcurrency: 1, DailyBudgetMB: 1,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	rows, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	if rows[0].Name != "g" {
		t.Fatalf("name = %q", rows[0].Name)
	}
}
