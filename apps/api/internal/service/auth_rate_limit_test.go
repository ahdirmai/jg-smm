package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// TestLoginRateLimit covers the auth-audit HIGH: the credential endpoint had no
// attempt cap, so it was a brute-force oracle. The 11th attempt from one IP in
// a minute is refused even with the right password, and a second IP is not
// penalised for the first one's flood.
func TestLoginRateLimit(t *testing.T) {
	clock := newStepClock()
	users := &fakeUsers{byEmail: map[string]port.User{}}
	sessions := newFakeSessions()
	svc := NewAuthService(users, sessions, fakeIssuer{}, clock)

	// A real bcrypt hash: the service verifies it, so a wrong password must
	// fail on the credential, not on a stub.
	hash, err := hashPassword("good-password")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	users.byEmail["owner@smm.local"] = port.User{ID: "u1", Email: "owner@smm.local", PasswordHash: hash, Role: domain.RoleOwner}

	// Fill the window without burning a credential: a wrong password counts,
	// so the cap covers failures, not just successes.
	for i := 0; i < loginMaxPerIP; i++ {
		if _, err := svc.Login(context.Background(), "owner@smm.local", "wrong", "test", "10.0.0.1"); !errors.Is(err, domain.ErrUnauthorized) {
			t.Fatalf("attempt %d: want unauthorized, got %v", i, err)
		}
	}

	// The cap is hit: even the correct password is refused.
	if _, err := svc.Login(context.Background(), "owner@smm.local", "good-password", "test", "10.0.0.1"); !errors.Is(err, domain.ErrRateLimited) {
		t.Fatalf("want rate limited after %d attempts, got %v", loginMaxPerIP, err)
	}

	// A different source IP is unaffected — a shared NAT must not be a
	// lockout switch.
	if _, err := svc.Login(context.Background(), "owner@smm.local", "good-password", "test", "10.0.0.2"); err != nil {
		t.Fatalf("second IP should still be able to log in: %v", err)
	}

	// The window rolls: after it passes, the same IP can try again.
	clock.advance(loginWindow + time.Second)
	if _, err := svc.Login(context.Background(), "owner@smm.local", "good-password", "test", "10.0.0.1"); err != nil {
		t.Fatalf("want a login after the window rolls, got %v", err)
	}
}

// An absent client IP fails open: a proxy that forwarded nothing must not
// lock the whole deployment out.
func TestLoginRateLimitFailsOpen(t *testing.T) {
	clock := newStepClock()
	users := &fakeUsers{byEmail: map[string]port.User{}}
	svc := NewAuthService(users, newFakeSessions(), fakeIssuer{}, clock)

	hash, err := hashPassword("good-password")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	users.byEmail["owner@smm.local"] = port.User{ID: "u1", Email: "owner@smm.local", PasswordHash: hash, Role: domain.RoleOwner}

	for i := 0; i < loginMaxPerIP*3; i++ {
		if _, err := svc.Login(context.Background(), "owner@smm.local", "good-password", "test", ""); err != nil {
			t.Fatalf("attempt %d with no client IP should succeed, got %v", i, err)
		}
	}
}
