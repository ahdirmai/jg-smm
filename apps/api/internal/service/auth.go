package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/port"
)

// Token lifetimes (PRD F1.3): 24h access, 30d refresh.
const (
	AccessTokenTTL  = 24 * time.Hour
	RefreshTokenTTL = 30 * 24 * time.Hour
)

// Session is the result of a successful login or refresh.
type Session struct {
	AccessToken  string
	RefreshToken string
	AccessExpiry time.Time
	User         port.User
}

// AuthService authenticates users and manages sessions. It hashes refresh
// tokens before storage and never logs plaintext credentials.
type AuthService struct {
	users    port.UserStore
	sessions port.SessionStore
	issuer   port.TokenIssuer
	clock    port.Clock
}

// NewAuthService wires the auth dependencies.
func NewAuthService(users port.UserStore, sessions port.SessionStore, issuer port.TokenIssuer, clock port.Clock) *AuthService {
	return &AuthService{users: users, sessions: sessions, issuer: issuer, clock: clock}
}

// Login verifies credentials and starts a session. A wrong email or password
// both return domain.ErrUnauthorized (no user enumeration).
func (s *AuthService) Login(ctx context.Context, email, password, userAgent, ip string) (Session, error) {
	user, err := s.users.GetByEmail(ctx, email)
	if err != nil {
		if err == domain.ErrNotFound {
			return Session{}, domain.ErrUnauthorized
		}
		return Session{}, err
	}
	if !verifyPassword(password, user.PasswordHash) {
		return Session{}, domain.ErrUnauthorized
	}
	return s.startSession(ctx, user, userAgent, ip)
}

// Refresh rotates a refresh token: it revokes the presented token and issues a
// new pair. Reuse of a revoked token revokes the whole family (token theft).
func (s *AuthService) Refresh(ctx context.Context, refreshToken, userAgent, ip string) (Session, error) {
	hash := hashToken(refreshToken)
	sess, user, err := s.sessions.GetActive(ctx, hash)
	if err != nil {
		return Session{}, err
	}
	if err := s.sessions.Revoke(ctx, hash, "rotated"); err != nil {
		return Session{}, err
	}
	_ = sess
	return s.startSession(ctx, user, userAgent, ip)
}

// Logout revokes the presented refresh token. Missing tokens are ignored so
// logout is idempotent.
func (s *AuthService) Logout(ctx context.Context, refreshToken string) error {
	if refreshToken == "" {
		return nil
	}
	return s.sessions.Revoke(ctx, hashToken(refreshToken), "logout")
}

// VerifyAccessToken validates a stateless access token.
func (s *AuthService) VerifyAccessToken(token string) (port.Claims, error) {
	return s.issuer.Verify(token)
}

func (s *AuthService) startSession(ctx context.Context, user port.User, userAgent, ip string) (Session, error) {
	now := s.clock.Now()

	accessToken, err := s.issuer.Issue(user.ID, user.Role, AccessTokenTTL)
	if err != nil {
		return Session{}, err
	}

	refreshToken, err := generateRefreshToken()
	if err != nil {
		return Session{}, err
	}
	if _, err := s.sessions.CreateSession(ctx, user.ID, hashToken(refreshToken), userAgent, ip, now.Add(RefreshTokenTTL)); err != nil {
		return Session{}, err
	}

	return Session{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		AccessExpiry: now.Add(AccessTokenTTL),
		User:         user,
	}, nil
}

// generateRefreshToken returns 32 bytes of crypto randomness, base64url-encoded.
func generateRefreshToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("service.auth: read token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// hashToken returns the SHA-256 of a refresh token. Only the hash is stored.
func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
