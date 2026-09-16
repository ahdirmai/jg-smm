package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
	"github.com/ahdirmai/jg-smm/apps/api/internal/repository/sqlcgen"
)

// AuthRepo persists users and refresh-token sessions.
type AuthRepo struct {
	q *sqlcgen.Queries
}

// NewAuthRepo binds the repo to a sqlc query handle.
func NewAuthRepo(q *sqlcgen.Queries) *AuthRepo { return &AuthRepo{q: q} }

var (
	_ port.UserStore    = (*AuthRepo)(nil)
	_ port.SessionStore = (*AuthRepo)(nil)
)

// GetByEmail returns the user with the given email (case-insensitive).
func (r *AuthRepo) GetByEmail(ctx context.Context, email string) (port.User, error) {
	row, err := r.q.GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return port.User{}, domain.ErrNotFound
	}
	if err != nil {
		return port.User{}, fmt.Errorf("repository.auth.GetByEmail: %w", err)
	}
	return toUser(row.ID, row.Email, row.Name, row.PasswordHash, row.Role, row.CreatedAt), nil
}

// GetByID returns the user with the given id.
func (r *AuthRepo) GetByID(ctx context.Context, id string) (port.User, error) {
	row, err := r.q.GetUserByID(ctx, uuidValue(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return port.User{}, domain.ErrNotFound
	}
	if err != nil {
		return port.User{}, fmt.Errorf("repository.auth.GetByID: %w", err)
	}
	return toUser(row.ID, row.Email, row.Name, row.PasswordHash, row.Role, row.CreatedAt), nil
}

// Create inserts a user. A duplicate email maps to domain.ErrConflict.
func (r *AuthRepo) Create(ctx context.Context, email, name, passwordHash string, role domain.Role) (port.User, error) {
	row, err := r.q.CreateUser(ctx, sqlcgen.CreateUserParams{
		Lower:        email,
		Name:         name,
		PasswordHash: passwordHash,
		Role:         sqlcgen.Role(role),
	})
	if err != nil {
		if isUniqueViolation(err) {
			return port.User{}, domain.ErrConflict
		}
		return port.User{}, fmt.Errorf("repository.auth.Create: %w", err)
	}
	// CreateUser returns a reduced row (no password_hash); reflect that.
	return port.User{
		ID:        uuidString(row.ID),
		Email:     row.Email,
		Name:      row.Name,
		Role:      domain.Role(row.Role),
		CreatedAt: row.CreatedAt.Time,
	}, nil
}

// List returns a page of users, newest first. Password hashes are never loaded.
func (r *AuthRepo) List(ctx context.Context, limit, offset int) ([]port.User, error) {
	l, o := pageBounds(limit, offset)
	rows, err := r.q.ListUsers(ctx, sqlcgen.ListUsersParams{Limit: l, Offset: o})
	if err != nil {
		return nil, fmt.Errorf("repository.auth.List: %w", err)
	}
	out := make([]port.User, 0, len(rows))
	for _, row := range rows {
		out = append(out, port.User{
			ID:        uuidString(row.ID),
			Email:     row.Email,
			Name:      row.Name,
			Role:      domain.Role(row.Role),
			CreatedAt: row.CreatedAt.Time,
		})
	}
	return out, nil
}

// Update changes a user's name and role. A missing user is ErrNotFound.
func (r *AuthRepo) Update(ctx context.Context, id, name string, role domain.Role) (port.User, error) {
	row, err := r.q.UpdateUser(ctx, sqlcgen.UpdateUserParams{
		ID:   uuidValue(id),
		Name: name,
		Role: sqlcgen.Role(role),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return port.User{}, domain.ErrNotFound
	}
	if err != nil {
		return port.User{}, fmt.Errorf("repository.auth.Update: %w", err)
	}
	return port.User{
		ID:        uuidString(row.ID),
		Email:     row.Email,
		Name:      row.Name,
		Role:      domain.Role(row.Role),
		CreatedAt: row.CreatedAt.Time,
	}, nil
}

// Delete removes a user. Sessions cascade (ON DELETE CASCADE), so every
// refresh token dies with the account.
func (r *AuthRepo) Delete(ctx context.Context, id string) error {
	if err := r.q.DeleteUser(ctx, uuidValue(id)); err != nil {
		return fmt.Errorf("repository.auth.Delete: %w", err)
	}
	return nil
}

// CountOwnersExcept counts OWNER-role users other than the given id.
func (r *AuthRepo) CountOwnersExcept(ctx context.Context, id string) (int64, error) {
	count, err := r.q.CountOwnersExcept(ctx, uuidValue(id))
	if err != nil {
		return 0, fmt.Errorf("repository.auth.CountOwnersExcept: %w", err)
	}
	return count, nil
}

// CreateSession stores a new refresh-token session.
func (r *AuthRepo) CreateSession(ctx context.Context, userID string, tokenHash []byte, userAgent, ip string, expiresAt time.Time) (port.AuthSession, error) {
	row, err := r.q.CreateAuthSession(ctx, sqlcgen.CreateAuthSessionParams{
		UserID:    uuidValue(userID),
		TokenHash: tokenHash,
		UserAgent: textOrNull(userAgent),
		Ip:        inetOrNull(ip),
		ExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true},
	})
	if err != nil {
		return port.AuthSession{}, fmt.Errorf("repository.auth.CreateSession: %w", err)
	}
	return port.AuthSession{
		ID:        uuidString(row.ID),
		UserID:    uuidString(row.UserID),
		ExpiresAt: row.ExpiresAt.Time,
	}, nil
}

// GetActive returns the session + user for a live token hash.
func (r *AuthRepo) GetActive(ctx context.Context, tokenHash []byte) (port.AuthSession, port.User, error) {
	row, err := r.q.GetActiveAuthSession(ctx, tokenHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return port.AuthSession{}, port.User{}, domain.ErrUnauthorized
	}
	if err != nil {
		return port.AuthSession{}, port.User{}, fmt.Errorf("repository.auth.GetActive: %w", err)
	}
	sess := port.AuthSession{
		ID:        uuidString(row.ID),
		UserID:    uuidString(row.UserID),
		ExpiresAt: row.ExpiresAt.Time,
	}
	user := port.User{
		ID:    uuidString(row.UserID),
		Email: row.Email,
		Name:  row.Name,
		Role:  domain.Role(row.Role),
	}
	return sess, user, nil
}

// Touch records last-used time for a session.
func (r *AuthRepo) Touch(ctx context.Context, id string) error {
	if err := r.q.TouchAuthSession(ctx, uuidValue(id)); err != nil {
		return fmt.Errorf("repository.auth.Touch: %w", err)
	}
	return nil
}

// Revoke marks one session revoked by token hash.
func (r *AuthRepo) Revoke(ctx context.Context, tokenHash []byte, reason string) error {
	if err := r.q.RevokeAuthSession(ctx, sqlcgen.RevokeAuthSessionParams{
		TokenHash: tokenHash,
		RevokedReason: sqlcgen.NullAuthSessionRevokedReason{
			AuthSessionRevokedReason: sqlcgen.AuthSessionRevokedReason(reason),
			Valid:                    true,
		},
	}); err != nil {
		return fmt.Errorf("repository.auth.Revoke: %w", err)
	}
	return nil
}

// RevokeAll revokes every active session for a user.
func (r *AuthRepo) RevokeAll(ctx context.Context, userID, reason string) error {
	if err := r.q.RevokeAllUserSessions(ctx, sqlcgen.RevokeAllUserSessionsParams{
		UserID: uuidValue(userID),
		RevokedReason: sqlcgen.NullAuthSessionRevokedReason{
			AuthSessionRevokedReason: sqlcgen.AuthSessionRevokedReason(reason),
			Valid:                    true,
		},
	}); err != nil {
		return fmt.Errorf("repository.auth.RevokeAll: %w", err)
	}
	return nil
}

func toUser(id pgtype.UUID, email, name, hash string, role sqlcgen.Role, createdAt pgtype.Timestamptz) port.User {
	return port.User{
		ID:           uuidString(id),
		Email:        email,
		Name:         name,
		PasswordHash: hash,
		Role:         domain.Role(role),
		CreatedAt:    createdAt.Time,
	}
}
