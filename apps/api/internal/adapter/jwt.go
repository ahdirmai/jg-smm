package adapter

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// JWTIssuer signs HS256 access tokens with a shared secret.
type JWTIssuer struct {
	secret []byte
	issuer string
}

// NewJWTIssuer builds an issuer. The secret must be non-empty (validated by
// config); the constructor re-checks to fail fast in tests.
func NewJWTIssuer(secret, issuer string) (*JWTIssuer, error) {
	if len(secret) < 16 {
		return nil, errors.New("adapter.jwt: secret must be at least 16 bytes")
	}
	return &JWTIssuer{secret: []byte(secret), issuer: issuer}, nil
}

var _ port.TokenIssuer = (*JWTIssuer)(nil)

type accessClaims struct {
	Role string `json:"role"`
	jwt.RegisteredClaims
}

// Issue mints a signed access token for the user.
func (j *JWTIssuer) Issue(userID string, role domain.Role, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := accessClaims{
		Role: string(role),
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			Issuer:    j.issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(j.secret)
	if err != nil {
		return "", fmt.Errorf("adapter.jwt: sign: %w", err)
	}
	return signed, nil
}

// Verify parses and validates an access token, returning domain.ErrUnauthorized
// on any failure (expired, wrong signature, wrong alg).
func (j *JWTIssuer) Verify(token string) (port.Claims, error) {
	parsed, err := jwt.ParseWithClaims(token, &accessClaims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return j.secret, nil
	}, jwt.WithIssuer(j.issuer), jwt.WithValidMethods([]string{"HS256"}))
	if err != nil {
		return port.Claims{}, domain.ErrUnauthorized
	}
	claims, ok := parsed.Claims.(*accessClaims)
	if !ok || !parsed.Valid {
		return port.Claims{}, domain.ErrUnauthorized
	}
	role := domain.Role(claims.Role)
	if !role.Valid() {
		return port.Claims{}, domain.ErrUnauthorized
	}
	return port.Claims{UserID: claims.Subject, Role: role}, nil
}
