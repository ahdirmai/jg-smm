package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// memUserStore is the in-memory port.UserStore for user-service tests.
type memUserStore struct {
	byID  map[string]port.User
	email map[string]string // email -> id
}

func newMemUserStore() *memUserStore {
	return &memUserStore{byID: map[string]port.User{}, email: map[string]string{}}
}

func (s *memUserStore) GetByEmail(_ context.Context, email string) (port.User, error) {
	if id, ok := s.email[email]; ok {
		return s.byID[id], nil
	}
	return port.User{}, domain.ErrNotFound
}
func (s *memUserStore) GetByID(_ context.Context, id string) (port.User, error) {
	if u, ok := s.byID[id]; ok {
		return u, nil
	}
	return port.User{}, domain.ErrNotFound
}
func (s *memUserStore) Create(_ context.Context, email, name, hash string, role domain.Role) (port.User, error) {
	if _, ok := s.email[email]; ok {
		return port.User{}, domain.ErrConflict
	}
	u := port.User{ID: "u-" + email, Email: email, Name: name, PasswordHash: hash, Role: role}
	s.byID[u.ID] = u
	s.email[email] = u.ID
	return u, nil
}
func (s *memUserStore) List(_ context.Context, _, _ int) ([]port.User, error) {
	out := make([]port.User, 0, len(s.byID))
	for _, u := range s.byID {
		out = append(out, u)
	}
	return out, nil
}
func (s *memUserStore) Update(_ context.Context, id, name string, role domain.Role) (port.User, error) {
	u, ok := s.byID[id]
	if !ok {
		return port.User{}, domain.ErrNotFound
	}
	u.Name, u.Role = name, role
	s.byID[id] = u
	return u, nil
}
func (s *memUserStore) Delete(_ context.Context, id string) error {
	u, ok := s.byID[id]
	if !ok {
		return domain.ErrNotFound
	}
	delete(s.byID, id)
	delete(s.email, u.Email)
	return nil
}
func (s *memUserStore) CountOwnersExcept(_ context.Context, id string) (int64, error) {
	var n int64
	for _, u := range s.byID {
		if u.Role == domain.RoleOwner && u.ID != id {
			n++
		}
	}
	return n, nil
}

// revokingSessions records who got revoked so a role change can be asserted.
type revokingSessions struct{ revoked []string }

func (s *revokingSessions) CreateSession(_ context.Context, _ string, _ []byte, _, _ string, _ time.Time) (port.AuthSession, error) {
	return port.AuthSession{}, nil
}
func (s *revokingSessions) GetActive(_ context.Context, _ []byte) (port.AuthSession, port.User, error) {
	return port.AuthSession{}, port.User{}, domain.ErrUnauthorized
}
func (s *revokingSessions) Touch(_ context.Context, _ string) error { return nil }
func (s *revokingSessions) Revoke(_ context.Context, _ []byte, _ string) error {
	s.revoked = append(s.revoked, "one")
	return nil
}
func (s *revokingSessions) RevokeAll(_ context.Context, userID, _ string) error {
	s.revoked = append(s.revoked, userID)
	return nil
}

// Create rejects a short password and a bad email before any crypto or store
// work, so the caller gets a 400 rather than a weak row.
func TestUserServiceCreateRejectsBadInput(t *testing.T) {
	svc := NewUserService(newMemUserStore(), nil, UserConfig{})

	cases := []struct {
		name string
		in   UserInput
	}{
		{"short password", UserInput{Email: "a@b.co", Name: "A", Password: "short", Role: domain.RoleOperator}},
		{"bad email", UserInput{Email: "not-an-email", Name: "A", Password: "longenough", Role: domain.RoleOperator}},
		{"blank name", UserInput{Email: "a@b.co", Name: " ", Password: "longenough", Role: domain.RoleOperator}},
		{"unknown role", UserInput{Email: "a@b.co", Name: "A", Password: "longenough", Role: domain.Role("SUPERUSER")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.Create(context.Background(), tc.in); err == nil {
				t.Fatalf("expected validation error, got nil")
			} else if !errors.Is(err, domain.ErrValidation) {
				t.Fatalf("expected ErrValidation, got %v", err)
			}
		})
	}
}

// A duplicate email is a conflict (409), not a 500.
func TestUserServiceCreateDuplicateEmail(t *testing.T) {
	store := newMemUserStore()
	svc := NewUserService(store, nil, UserConfig{})
	in := UserInput{Email: "dup@smm.local", Name: "Dup", Password: "longenough", Role: domain.RoleOperator}

	if _, err := svc.Create(context.Background(), in); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := svc.Create(context.Background(), in)
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
}

// The last OWNER cannot be demoted or removed: the team must stay adminnable.
func TestUserServiceGuardsLastOwner(t *testing.T) {
	store := newMemUserStore()
	svc := NewUserService(store, nil, UserConfig{})

	owner, err := svc.Create(context.Background(), UserInput{
		Email: "owner@smm.local", Name: "Owner", Password: "longenough", Role: domain.RoleOwner,
	})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}

	analyst := domain.RoleAnalyst
	if _, err := svc.Update(context.Background(), owner.ID, "Owner", &analyst, "actor"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("demote last owner: expected ErrConflict, got %v", err)
	}
	if err := svc.Remove(context.Background(), owner.ID, "actor"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("remove last owner: expected ErrConflict, got %v", err)
	}

	// With a second owner present, demoting the first is allowed.
	if _, err := svc.Create(context.Background(), UserInput{
		Email: "owner2@smm.local", Name: "Owner2", Password: "longenough", Role: domain.RoleOwner,
	}); err != nil {
		t.Fatalf("create second owner: %v", err)
	}
	if _, err := svc.Update(context.Background(), owner.ID, "Owner", &analyst, "actor"); err != nil {
		t.Fatalf("demote with a second owner present: %v", err)
	}
}

// An actor cannot delete their own account mid-session.
func TestUserServiceCannotRemoveSelf(t *testing.T) {
	svc := NewUserService(newMemUserStore(), nil, UserConfig{})
	u, err := svc.Create(context.Background(), UserInput{
		Email: "self@smm.local", Name: "Self", Password: "longenough", Role: domain.RoleOperator,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.Remove(context.Background(), u.ID, u.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("remove self: expected ErrConflict, got %v", err)
	}
}

// A role change revokes the user's refresh sessions so the new role applies
// immediately instead of waiting out a 30d token.
func TestUserServiceRoleChangeRevokesSessions(t *testing.T) {
	store := newMemUserStore()
	sessions := &revokingSessions{}
	svc := NewUserService(store, sessions, UserConfig{})

	u, err := svc.Create(context.Background(), UserInput{
		Email: "promote@smm.local", Name: "Promote", Password: "longenough", Role: domain.RoleOperator,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	owner := domain.RoleOwner
	if _, err := svc.Update(context.Background(), u.ID, u.Name, &owner, "actor"); err != nil {
		t.Fatalf("update: %v", err)
	}
	if len(sessions.revoked) != 1 || sessions.revoked[0] != u.ID {
		t.Fatalf("expected sessions for %s revoked, got %v", u.ID, sessions.revoked)
	}
}
