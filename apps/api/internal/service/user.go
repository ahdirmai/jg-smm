package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// UserService is the team CRUD (P6-11): the single-team MVP has one roster of
// users; an OWNER administers it. Passwords are argon2id-hashed before the
// store and never returned by any method.
type UserService struct {
	users    port.UserStore
	sessions port.SessionStore
	clock    port.Clock
	logger   *slog.Logger
}

// UserConfig tunes the user service.
type UserConfig struct {
	Clock  port.Clock
	Logger *slog.Logger
}

// NewUserService wires the user store. sessions is used to revoke a changed
// user's refresh tokens so a role change applies immediately.
func NewUserService(users port.UserStore, sessions port.SessionStore, cfg UserConfig) *UserService {
	if cfg.Clock == nil {
		cfg.Clock = systemClock{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &UserService{users: users, sessions: sessions, clock: cfg.Clock, logger: cfg.Logger}
}

// UserInput is the create payload. Password is plaintext in memory only.
type UserInput struct {
	Email    string
	Name     string
	Password string
	Role     domain.Role
}

// Validate rejects input before any crypto or DB work.
func (in UserInput) Validate() error {
	if !validEmail(in.Email) {
		return fmt.Errorf("%w: email must be a valid address", domain.ErrValidation)
	}
	if strings.TrimSpace(in.Name) == "" {
		return fmt.Errorf("%w: name is required", domain.ErrValidation)
	}
	if len(in.Password) < 8 {
		return fmt.Errorf("%w: password must be at least 8 characters", domain.ErrValidation)
	}
	if !in.Role.Valid() {
		return fmt.Errorf("%w: unknown role %q", domain.ErrValidation, in.Role)
	}
	return nil
}

// UserSummary is the secret-free read model.
type UserSummary struct {
	ID        string
	Email     string
	Name      string
	Role      domain.Role
	CreatedAt string
}

// List returns the whole roster (the MVP is single-team, so the page is small).
func (s *UserService) List(ctx context.Context) ([]UserSummary, error) {
	rows, err := s.users.List(ctx, 200, 0)
	if err != nil {
		return nil, fmt.Errorf("user: list: %w", err)
	}
	out := make([]UserSummary, 0, len(rows))
	for _, u := range rows {
		out = append(out, toUserView(u))
	}
	return out, nil
}

// Create adds a user with a provisional password the operator shares out of
// band. Duplicate email -> ErrConflict.
func (s *UserService) Create(ctx context.Context, in UserInput) (UserSummary, error) {
	if err := in.Validate(); err != nil {
		return UserSummary{}, err
	}
	hash, err := HashPassword(in.Password)
	if err != nil {
		return UserSummary{}, fmt.Errorf("user: hash password: %w", err)
	}
	saved, err := s.users.Create(ctx, strings.ToLower(strings.TrimSpace(in.Email)),
		strings.TrimSpace(in.Name), hash, in.Role)
	if err != nil {
		if err == domain.ErrConflict {
			return UserSummary{}, fmt.Errorf("%w: %s already exists", domain.ErrConflict, in.Email)
		}
		return UserSummary{}, fmt.Errorf("user: create: %w", err)
	}
	s.logger.Info("user created", "userId", saved.ID, "email", saved.Email, "role", saved.Role)
	return toUserView(saved), nil
}

// Update changes a user's name and/or role. A role change revokes that user's
// refresh sessions so the new role is enforced on the next login. Demoting the
// last OWNER is refused to keep the team adminnable.
func (s *UserService) Update(ctx context.Context, id string, name string, role *domain.Role, actorID string) (UserSummary, error) {
	if strings.TrimSpace(name) == "" && role == nil {
		return UserSummary{}, fmt.Errorf("%w: nothing to update", domain.ErrValidation)
	}
	if role != nil && !role.Valid() {
		return UserSummary{}, fmt.Errorf("%w: unknown role %q", domain.ErrValidation, *role)
	}

	existing, err := s.users.GetByID(ctx, id)
	if err != nil {
		return UserSummary{}, fmt.Errorf("user: update: %w", err)
	}

	nextName := existing.Name
	if strings.TrimSpace(name) != "" {
		nextName = strings.TrimSpace(name)
	}
	nextRole := existing.Role
	if role != nil {
		// Guard the last OWNER: if this change demotes an owner and no other
		// owner would remain, refuse.
		if existing.Role == domain.RoleOwner && *role != domain.RoleOwner {
			if count, _ := s.users.CountOwnersExcept(ctx, id); count == 0 {
				return UserSummary{}, fmt.Errorf("%w: cannot demote the last owner", domain.ErrConflict)
			}
		}
		nextRole = *role
	}

	updated, err := s.users.Update(ctx, id, nextName, nextRole)
	if err != nil {
		return UserSummary{}, fmt.Errorf("user: update: %w", err)
	}

	// A role change must not wait for a 30d refresh token to expire.
	if role != nil && *role != existing.Role && s.sessions != nil {
		if err := s.sessions.RevokeAll(ctx, id, "role_changed"); err != nil {
			s.logger.Warn("revoke sessions after role change failed", "userId", id, "err", err)
		}
	}
	s.logger.Info("user updated", "userId", updated.ID, "by", actorID)
	return toUserView(updated), nil
}

// Remove deletes a user. The last OWNER cannot be removed, and an actor cannot
// remove themselves (they would lock themselves out mid-session).
func (s *UserService) Remove(ctx context.Context, id, actorID string) error {
	if id == actorID {
		return fmt.Errorf("%w: cannot remove your own account", domain.ErrConflict)
	}
	existing, err := s.users.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("user: delete: %w", err)
	}
	if existing.Role == domain.RoleOwner {
		if count, _ := s.users.CountOwnersExcept(ctx, id); count == 0 {
			return fmt.Errorf("%w: cannot remove the last owner", domain.ErrConflict)
		}
	}
	if err := s.users.Delete(ctx, id); err != nil {
		return fmt.Errorf("user: delete: %w", err)
	}
	s.logger.Info("user removed", "userId", id, "by", actorID)
	return nil
}

func toUserView(u port.User) UserSummary {
	return UserSummary{
		ID:        u.ID,
		Email:     u.Email,
		Name:      u.Name,
		Role:      u.Role,
		CreatedAt: u.CreatedAt.Format(timeRFC3339),
	}
}

// validEmail is the cheap RFC-ish guard the DB CHECK also enforces (lowercase);
// the service rejects early so a bad address never reaches the store.
func validEmail(s string) bool {
	at := strings.IndexByte(s, '@')
	if at <= 0 || at == len(s)-1 {
		return false
	}
	return strings.Contains(s[at+1:], ".")
}
