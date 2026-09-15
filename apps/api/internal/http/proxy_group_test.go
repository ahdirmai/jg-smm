package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/adapter"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/port"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/service"
)

func newProxyGroupTestServer(t *testing.T, role domain.Role) (*httptest.Server, *service.ProxyGroupService) {
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
	authSvc := service.NewAuthService(users, sessions, issuer, adapter.SystemClock{})

	// The proxy group service gets the same XOR stub the service tests use; it
	// proves the pool key is never stored or returned as plaintext.
	svc := service.NewProxyGroupService(
		newMemProxyStoreForHTTP(),
		newMemAccountStoreForHTTP(),
		newMemWorkerStoreForHTTP(),
		service.ProxyGroupConfig{Sealer: xorSealerHTTP{}},
	)

	e := NewRouter(Dependencies{
		Health:      NewHealthHandler(service.NewHealthService(nil)),
		Auth:        NewAuthHandler(authSvc, false),
		ProxyGroups: NewProxyGroupHandler(svc),
	})
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)
	return srv, svc
}

// xorSealerHTTP mirrors service.stubSealer (unexported) for the HTTP layer.
type xorSealerHTTP struct{}

func (xorSealerHTTP) Seal(p []byte) ([]byte, error) {
	out := make([]byte, len(p))
	for i, b := range p {
		out[i] = b ^ 0x5a
	}
	return out, nil
}

func (s xorSealerHTTP) Open(c []byte) ([]byte, error) {
	return s.Seal(c) // symmetric xor
}

const createProxyGroupBody = `{
  "name": "sg-res-01",
  "region": "SG",
  "provider": "brightdata",
  "poolKey": "PLAINTEXT-KEY",
  "maxConcurrency": 5,
  "dailyBudgetMb": 512
}`

func TestProxyGroupCreateAndList(t *testing.T) {
	srv, _ := newProxyGroupTestServer(t, domain.RoleOperator)
	cookies := loginCookieJar(t, srv.URL)

	resp, err := doWithCookies("POST", srv.URL, "/api/proxy-groups", cookies, strings.NewReader(createProxyGroupBody))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", resp.StatusCode)
	}

	var created map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created["name"] != "sg-res-01" {
		t.Fatalf("name = %v", created["name"])
	}
	// The response must not carry the pool key back out.
	if _, ok := created["poolKey"]; ok {
		t.Fatal("response must not include poolKey")
	}

	list, err := doWithCookies("GET", srv.URL, "/api/proxy-groups", cookies, nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	defer list.Body.Close()
	if list.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d", list.StatusCode)
	}
	var got map[string]any
	if err := json.NewDecoder(list.Body).Decode(&got); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	rows, _ := got["proxyGroups"].([]any)
	if len(rows) != 1 {
		t.Fatalf("len(proxyGroups) = %d, want 1", len(rows))
	}
}

func TestProxyGroupCreateValidation(t *testing.T) {
	srv, _ := newProxyGroupTestServer(t, domain.RoleOperator)
	cookies := loginCookieJar(t, srv.URL)

	bad := `{"name":"","region":"SINGAPORE","provider":"","poolKey":"","maxConcurrency":0,"dailyBudgetMb":0}`
	resp, err := doWithCookies("POST", srv.URL, "/api/proxy-groups", cookies, strings.NewReader(bad))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("validation status = %d, want 400", resp.StatusCode)
	}
}

func TestProxyGroupDelete(t *testing.T) {
	srv, _ := newProxyGroupTestServer(t, domain.RoleOperator)
	cookies := loginCookieJar(t, srv.URL)

	create, err := doWithCookies("POST", srv.URL, "/api/proxy-groups", cookies, strings.NewReader(createProxyGroupBody))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var created map[string]any
	_ = json.NewDecoder(create.Body).Decode(&created)
	create.Body.Close()
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("no id from create")
	}

	del, err := doWithCookies("DELETE", srv.URL, "/api/proxy-groups/"+id, cookies, nil)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	defer del.Body.Close()
	if del.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", del.StatusCode)
	}
}

func TestProxyGroupAnalystForbidden(t *testing.T) {
	srv, _ := newProxyGroupTestServer(t, domain.RoleAnalyst)
	cookies := loginCookieJar(t, srv.URL)
	resp, err := doWithCookies("POST", srv.URL, "/api/proxy-groups", cookies, strings.NewReader(createProxyGroupBody))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("analyst status = %d, want 403", resp.StatusCode)
	}
}

func TestProxyGroupAnonymousUnauthorized(t *testing.T) {
	srv, _ := newProxyGroupTestServer(t, domain.RoleOperator)
	resp, err := http.Post(srv.URL+"/api/proxy-groups", "application/json", strings.NewReader(createProxyGroupBody))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d, want 401", resp.StatusCode)
	}
}
