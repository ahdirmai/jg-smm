package port

import (
	"context"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
)

// User is an authenticated principal.
type User struct {
	ID           string
	Email        string
	Name         string
	PasswordHash string
	Role         domain.Role
	CreatedAt    time.Time
}

// UserStore reads and writes users.
type UserStore interface {
	GetByEmail(ctx context.Context, email string) (User, error)
	GetByID(ctx context.Context, id string) (User, error)
	Create(ctx context.Context, email, name, passwordHash string, role domain.Role) (User, error)
}

// AuthSession is a persisted refresh token. Plaintext tokens are never stored.
type AuthSession struct {
	ID        string
	UserID    string
	ExpiresAt time.Time
	RevokedAt *time.Time
}

// SessionStore persists refresh-token sessions.
type SessionStore interface {
	// CreateSession stores a new session keyed by the token hash.
	CreateSession(ctx context.Context, userID string, tokenHash []byte, userAgent, ip string, expiresAt time.Time) (AuthSession, error)
	// GetActive returns the session + user for a live token hash, or
	// domain.ErrUnauthorized.
	GetActive(ctx context.Context, tokenHash []byte) (AuthSession, User, error)
	// Touch records last-used time for an active session.
	Touch(ctx context.Context, id string) error
	// Revoke marks one session revoked by token hash; missing/expired is a no-op.
	Revoke(ctx context.Context, tokenHash []byte, reason string) error
	// RevokeAll revokes every active session for a user.
	RevokeAll(ctx context.Context, userID, reason string) error
}

// TokenIssuer signs and verifies short-lived access tokens. Refresh tokens are
// opaque random strings minted by the service, not by the issuer.
type TokenIssuer interface {
	Issue(userID string, role domain.Role, ttl time.Duration) (string, error)
	Verify(token string) (Claims, error)
}

// Claims is the verified content of an access token.
type Claims struct {
	UserID string
	Role   domain.Role
}
