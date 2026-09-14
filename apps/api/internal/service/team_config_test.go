package service

import (
	"context"
	"errors"
	"testing"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/port"
)

// fakeTeamStore is an in-memory port.TeamConfigStore.
type fakeTeamStore struct {
	cfg      *port.TeamConfig
	upserts  int
	lastName string
}

func (f *fakeTeamStore) Get(context.Context) (port.TeamConfig, error) {
	if f.cfg == nil {
		return port.TeamConfig{}, domain.ErrNotFound
	}
	return *f.cfg, nil
}

func (f *fakeTeamStore) Upsert(_ context.Context, name string) (port.TeamConfig, error) {
	f.upserts++
	f.lastName = name
	f.cfg = &port.TeamConfig{ID: "team-1", Name: name}
	return *f.cfg, nil
}

func TestTeamConfigGetBootstrapsDefault(t *testing.T) {
	store := &fakeTeamStore{}
	svc := NewTeamConfigService(store)

	got, err := svc.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "Team" {
		t.Errorf("default name = %q, want Team", got.Name)
	}
	if store.upserts != 1 {
		t.Errorf("upserts = %d, want 1", store.upserts)
	}
}

func TestTeamConfigGetReturnsExisting(t *testing.T) {
	store := &fakeTeamStore{cfg: &port.TeamConfig{ID: "team-1", Name: "Acme"}}
	svc := NewTeamConfigService(store)

	got, err := svc.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "Acme" {
		t.Errorf("name = %q, want Acme", got.Name)
	}
	if store.upserts != 0 {
		t.Errorf("upserts = %d, want 0", store.upserts)
	}
}

func TestTeamConfigRenameValidation(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid", "Growth Team", false},
		{"empty", "", true},
		{"too long", string(make([]byte, 121)), true},
		{"control char", "bad\x01name", true},
	}
	store := &fakeTeamStore{}
	svc := NewTeamConfigService(store)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Rename(context.Background(), tc.input)
			if tc.wantErr {
				if !errors.Is(err, domain.ErrValidation) {
					t.Fatalf("err = %v, want ErrValidation", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if store.lastName != tc.input {
				t.Errorf("stored name = %q, want %q", store.lastName, tc.input)
			}
		})
	}
}
