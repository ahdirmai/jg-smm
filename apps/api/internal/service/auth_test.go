package service

import (
	"context"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// --- fakes ---

type fakeUsers struct{ byEmail map[string]port.User }

func (f *fakeUsers) GetByEmail(_ context.Context, email string) (port.User, error) {
	if u, ok := f.byEmail[email]; ok {
		return u, nil
	}
	return port.User{}, domain.ErrNotFound
}

func (f *fakeUsers) GetByID(_ context.Context, id string) (port.User, error) {
	for _, u := range f.byEmail {
		if u.ID == id {
			return u, nil
		}
	}
	return port.User{}, domain.ErrNotFound
}

func (f *fakeUsers) Create(_ context.Context, email, name, hash string, role domain.Role) (port.User, error) {
	u := port.User{ID: "u-new", Email: email, Name: name, PasswordHash: hash, Role: role}
	f.byEmail[email] = u
	return u, nil
}

type fakeSessions struct {
	active  map[string]port.AuthSession
	revoked []string
}

func newFakeSessions() *fakeSessions {
	return &fakeSessions{active: map[string]port.AuthSession{}}
}

func (f *fakeSessions) CreateSession(_ context.Context, userID string, hash []byte, _, _ string, exp time.Time) (port.AuthSession, error) {
	s := port.AuthSession{ID: "sess-" + string(hash[:2]), UserID: userID, ExpiresAt: exp}
	f.active[string(hash)] = s
	return s, nil
}

func (f *fakeSessions) GetActive(_ context.Context, hash []byte) (port.AuthSession, port.User, error) {
	s, ok := f.active[string(hash)]
	if !ok {
		return port.AuthSession{}, port.User{}, domain.ErrUnauthorized
	}
	return s, port.User{ID: s.UserID, Role: domain.RoleOperator}, nil
}

func (f *fakeSessions) Touch(context.Context, string) error { return nil }

func (f *fakeSessions) Revoke(_ context.Context, hash []byte, reason string) error {
	f.revoked = append(f.revoked, reason)
	delete(f.active, string(hash))
	return nil
}

func (f *fakeSessions) RevokeAll(context.Context, string, string) error { return nil }

type fakeIssuer struct{}

func (fakeIssuer) Issue(userID string, role domain.Role, _ time.Duration) (string, error) {
	return "access:" + userID + ":" + string(role), nil
}

func (fakeIssuer) Verify(string) (port.Claims, error) { return port.Claims{}, nil }

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

// --- tests ---

func newAuthFixture(t *testing.T) (*AuthService, *fakeUsers, *fakeSessions) {
	t.Helper()
	hash, err := hashPassword("s3cret-password")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	users := &fakeUsers{byEmail: map[string]port.User{
		"op@example.com": {ID: "u1", Email: "op@example.com", Name: "Op", PasswordHash: hash, Role: domain.RoleOperator},
	}}
	sessions := newFakeSessions()
	svc := NewAuthService(users, sessions, fakeIssuer{}, fixedClock{t: time.Unix(1_700_000_000, 0)})
	return svc, users, sessions
}

func TestLoginSuccess(t *testing.T) {
	svc, _, sessions := newAuthFixture(t)
	sess, err := svc.Login(context.Background(), "op@example.com", "s3cret-password", "ua", "1.2.3.4")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if sess.AccessToken == "" || sess.RefreshToken == "" {
		t.Error("expected both tokens to be set")
	}
	if len(sessions.active) != 1 {
		t.Errorf("expected 1 active session, got %d", len(sessions.active))
	}
}

func TestLoginWrongPassword(t *testing.T) {
	svc, _, _ := newAuthFixture(t)
	if _, err := svc.Login(context.Background(), "op@example.com", "nope", "ua", ""); err != domain.ErrUnauthorized {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

func TestLoginUnknownEmailIsUnauthorized(t *testing.T) {
	svc, _, _ := newAuthFixture(t)
	if _, err := svc.Login(context.Background(), "ghost@example.com", "x", "ua", ""); err != domain.ErrUnauthorized {
		t.Fatalf("err = %v, want ErrUnauthorized (no user enumeration)", err)
	}
}

func TestRefreshRotatesToken(t *testing.T) {
	svc, _, sessions := newAuthFixture(t)
	sess, _ := svc.Login(context.Background(), "op@example.com", "s3cret-password", "ua", "")

	refreshed, err := svc.Refresh(context.Background(), sess.RefreshToken, "ua", "")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if refreshed.RefreshToken == sess.RefreshToken {
		t.Error("refresh must rotate to a new refresh token")
	}
	if len(sessions.revoked) != 1 || sessions.revoked[0] != "rotated" {
		t.Errorf("old token should be revoked as rotated, got %v", sessions.revoked)
	}
}

func TestLogoutRevokes(t *testing.T) {
	svc, _, sessions := newAuthFixture(t)
	sess, _ := svc.Login(context.Background(), "op@example.com", "s3cret-password", "ua", "")
	if err := svc.Logout(context.Background(), sess.RefreshToken); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if len(sessions.active) != 0 {
		t.Error("session should be gone after logout")
	}
}

func TestLogoutEmptyTokenIsNoop(t *testing.T) {
	svc, _, _ := newAuthFixture(t)
	if err := svc.Logout(context.Background(), ""); err != nil {
		t.Fatalf("empty logout: %v", err)
	}
}
