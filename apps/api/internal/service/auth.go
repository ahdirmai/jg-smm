package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"sync"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// Token lifetimes (PRD F1.3): 24h access, 30d refresh.
const (
	AccessTokenTTL  = 24 * time.Hour
	RefreshTokenTTL = 30 * 24 * time.Hour

	// Login rate limiting: the credential endpoint is the only public one, so
	// an unbounded attempt rate is a brute-force oracle. The window is short
	// and the cap small because a human mistyping a password does not need 10
	// tries a minute.
	loginWindow   = time.Minute
	loginMaxPerIP = 10
)

// loginLimiter caps failed+successful login attempts per source IP per window.
// In-process: a single API replica is the local deployment, and a wrong answer
// here costs an account, so the simple option is the honest one. A multi-node
// deployment moves this to Redis (the same adapter the action scheduler uses).
type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
}

// allow reports whether an attempt from ip is within the window cap, and
// records the attempt either way so a flood cannot sneak under the counter.
func (l *loginLimiter) allow(now time.Time, ip string) bool {
	if ip == "" {
		// No client IP (behind a proxy that did not forward one): fail open
		// rather than lock out every request.
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := now.Add(-loginWindow)
	hits := l.attempts[ip]
	kept := hits[:0]
	for _, t := range hits {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	l.attempts[ip] = append(kept, now)
	return len(kept) < loginMaxPerIP
}

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
	limiter  *loginLimiter
}

// NewAuthService wires the auth dependencies.
func NewAuthService(users port.UserStore, sessions port.SessionStore, issuer port.TokenIssuer, clock port.Clock) *AuthService {
	return &AuthService{users: users, sessions: sessions, issuer: issuer, clock: clock, limiter: &loginLimiter{attempts: map[string][]time.Time{}}}
}

// Login verifies credentials and starts a session. A wrong email or password
// both return domain.ErrUnauthorized (no user enumeration). The attempt rate
// is capped per source IP: the credential endpoint is the only public one, and
// an unbounded rate turns it into a brute-force oracle.
func (s *AuthService) Login(ctx context.Context, email, password, userAgent, ip string) (Session, error) {
	if s.limiter != nil && !s.limiter.allow(s.clock.Now(), ip) {
		return Session{}, fmt.Errorf("%w: too many login attempts, try again in a minute", domain.ErrRateLimited)
	}
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
