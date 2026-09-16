package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/adapter"
	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
	"github.com/ahdirmai/jg-smm/apps/api/internal/service"
)

// --- minimal fakes to drive the real AuthService + handler ---

type memUsers struct{ m map[string]port.User }

func (u *memUsers) GetByEmail(_ context.Context, email string) (port.User, error) {
	if x, ok := u.m[email]; ok {
		return x, nil
	}
	return port.User{}, domain.ErrNotFound
}
func (u *memUsers) GetByID(_ context.Context, id string) (port.User, error) {
	for _, x := range u.m {
		if x.ID == id {
			return x, nil
		}
	}
	return port.User{}, domain.ErrNotFound
}
func (u *memUsers) Create(_ context.Context, email, name, hash string, role domain.Role) (port.User, error) {
	x := port.User{ID: "new", Email: email, Name: name, PasswordHash: hash, Role: role}
	u.m[email] = x
	return x, nil
}

type memSessions struct{ active map[string]port.User }

func (s *memSessions) CreateSession(_ context.Context, userID string, hash []byte, _, _ string, _ time.Time) (port.AuthSession, error) {
	s.active[string(hash)] = port.User{ID: userID}
	return port.AuthSession{ID: "s1", UserID: userID}, nil
}
func (s *memSessions) GetActive(_ context.Context, hash []byte) (port.AuthSession, port.User, error) {
	u, ok := s.active[string(hash)]
	if !ok {
		return port.AuthSession{}, port.User{}, domain.ErrUnauthorized
	}
	return port.AuthSession{ID: "s1", UserID: u.ID}, u, nil
}
func (s *memSessions) Touch(context.Context, string) error { return nil }
func (s *memSessions) Revoke(_ context.Context, hash []byte, _ string) error {
	delete(s.active, string(hash))
	return nil
}
func (s *memSessions) RevokeAll(context.Context, string, string) error { return nil }

func newAuthTestServer(t *testing.T, role domain.Role) *httptest.Server {
	t.Helper()
	hash, err := service.HashPassword("pw-123456789")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	users := &memUsers{m: map[string]port.User{
		"user@example.com": {ID: "u1", Email: "user@example.com", Name: "U", PasswordHash: hash, Role: role},
	}}
	sessions := &memSessions{active: map[string]port.User{}}
	issuer, _ := adapter.NewJWTIssuer("0123456789abcdef0123456789abcdef", "smm-test")
	svc := service.NewAuthService(users, sessions, issuer, adapter.SystemClock{})

	e := NewRouter(Dependencies{
		Health: NewHealthHandler(service.NewHealthService(nil)),
		Auth:   NewAuthHandler(svc, false),
	})
	return httptest.NewServer(e)
}

func TestAuthFlowAndRBAC(t *testing.T) {
	t.Run("operator can reach admin", func(t *testing.T) {
		srv := newAuthTestServer(t, domain.RoleOwner) // owner hits admin
		defer srv.Close()
		client := &http.Client{}
		if code := loginAndHit(t, client, srv.URL, "/api/admin/ping"); code != http.StatusOK {
			t.Fatalf("admin ping = %d, want 200", code)
		}
	})

	t.Run("operator denied admin", func(t *testing.T) {
		srv := newAuthTestServer(t, domain.RoleOperator)
		defer srv.Close()
		client := &http.Client{}
		if code := loginAndHit(t, client, srv.URL, "/api/admin/ping"); code != http.StatusForbidden {
			t.Fatalf("admin ping = %d, want 403", code)
		}
	})

	t.Run("anonymous denied", func(t *testing.T) {
		srv := newAuthTestServer(t, domain.RoleOwner)
		defer srv.Close()
		resp, err := http.Get(srv.URL + "/api/admin/ping")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("anonymous = %d, want 401", resp.StatusCode)
		}
	})
}

// loginAndHit logs in (storing the returned cookies) then requests path.
func loginAndHit(t *testing.T, client *http.Client, base, path string) int {
	t.Helper()
	body := strings.NewReader(`{"email":"user@example.com","password":"pw-123456789"}`)
	resp, err := client.Post(base+"/api/auth/login", "application/json", body)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodGet, base+path, nil)
	for _, c := range resp.Cookies() {
		req.AddCookie(c)
	}
	out, err := client.Do(req)
	if err != nil {
		t.Fatalf("admin request: %v", err)
	}
	defer out.Body.Close()
	return out.StatusCode
}
