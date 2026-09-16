package http

import (
	"context"
	"encoding/json"
	"io"
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

// containerFakes is a port.WorkerStore + AccountStore pair for container tests.
type containerWorkerStore struct {
	workers map[string]domain.Worker
}

func newContainerWorkerStore() *containerWorkerStore {
	return &containerWorkerStore{workers: map[string]domain.Worker{}}
}

func (s *containerWorkerStore) GetByID(_ context.Context, id string) (domain.Worker, error) {
	w, ok := s.workers[id]
	if !ok {
		return domain.Worker{}, domain.ErrNotFound
	}
	return w, nil
}
func (s *containerWorkerStore) GetByName(_ context.Context, name string) (domain.Worker, error) {
	for _, w := range s.workers {
		if w.Name == name {
			return w, nil
		}
	}
	return domain.Worker{}, domain.ErrNotFound
}
func (s *containerWorkerStore) List(_ context.Context, _ port.WorkerFilter) ([]domain.Worker, error) {
	out := make([]domain.Worker, 0, len(s.workers))
	for _, w := range s.workers {
		out = append(out, w)
	}
	return out, nil
}
func (s *containerWorkerStore) Create(_ context.Context, w domain.Worker) (domain.Worker, error) {
	for _, existing := range s.workers {
		if existing.Name == w.Name {
			return domain.Worker{}, domain.ErrConflict
		}
	}
	if w.ID == "" {
		w.ID = "w-" + w.Name
	}
	s.workers[w.ID] = w
	return w, nil
}
func (s *containerWorkerStore) Update(_ context.Context, w domain.Worker) (domain.Worker, error) {
	if _, ok := s.workers[w.ID]; !ok {
		return domain.Worker{}, domain.ErrNotFound
	}
	s.workers[w.ID] = w
	return w, nil
}
func (s *containerWorkerStore) Delete(_ context.Context, id string) error {
	delete(s.workers, id)
	return nil
}
func (s *containerWorkerStore) RecordHeartbeat(_ context.Context, _ domain.Heartbeat, _ port.WorkerSnapshot) error {
	return nil
}

type containerAccountStore struct{ accounts map[string]domain.Account }

func newContainerAccountStore() *containerAccountStore {
	return &containerAccountStore{accounts: map[string]domain.Account{}}
}

func (s *containerAccountStore) GetByID(_ context.Context, id string) (domain.Account, error) {
	a, ok := s.accounts[id]
	if !ok {
		return domain.Account{}, domain.ErrNotFound
	}
	return a, nil
}
func (s *containerAccountStore) List(_ context.Context, _ port.AccountFilter) ([]domain.Account, error) {
	return nil, nil
}
func (s *containerAccountStore) Create(_ context.Context, a domain.Account) (domain.Account, error) {
	s.accounts[a.ID] = a
	return a, nil
}
func (s *containerAccountStore) Update(_ context.Context, a domain.Account) (domain.Account, error) {
	s.accounts[a.ID] = a
	return a, nil
}
func (s *containerAccountStore) Assign(_ context.Context, accountID, workerID string) (domain.Account, error) {
	a, ok := s.accounts[accountID]
	if !ok {
		return domain.Account{}, domain.ErrNotFound
	}
	a.WorkerID = &workerID
	s.accounts[accountID] = a
	return a, nil
}
func (s *containerAccountStore) Unassign(_ context.Context, accountID string) (domain.Account, error) {
	a, ok := s.accounts[accountID]
	if !ok {
		return domain.Account{}, domain.ErrNotFound
	}
	wid := a.WorkerID
	a.WorkerID = nil
	s.accounts[accountID] = a
	a.WorkerID = wid
	return a, nil
}
func (s *containerAccountStore) Delete(_ context.Context, id string) error { return nil }
func (s *containerAccountStore) ListByWorker(_ context.Context, workerID string) ([]domain.Account, error) {
	var out []domain.Account
	for _, a := range s.accounts {
		if a.WorkerID != nil && *a.WorkerID == workerID {
			out = append(out, a)
		}
	}
	return out, nil
}
func (s *containerAccountStore) CountByWorker(_ context.Context, workerID string) (int, error) {
	n := 0
	for _, a := range s.accounts {
		if a.WorkerID != nil && *a.WorkerID == workerID {
			n++
		}
	}
	return n, nil
}

// newContainerTestServer builds a full router with real auth + container API.
// role controls RBAC for the /api group.
func newContainerTestServer(t *testing.T, role domain.Role) *httptest.Server {
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

	workers := newContainerWorkerStore()
	accounts := newContainerAccountStore()
	packer := service.NewPacker(workers, accounts, service.PackerConfig{MaxPerContainer: 2})
	containerSvc := service.NewContainerService(workers, accounts, packer, service.ContainerConfig{
		Clock: containerClock{},
	})

	e := NewRouter(Dependencies{
		Health:     NewHealthHandler(service.NewHealthService(nil)),
		Auth:       NewAuthHandler(authSvc, false),
		Containers: NewContainerHandler(containerSvc),
	})
	return httptest.NewServer(e)
}

type containerClock struct{}

func (containerClock) Now() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

// loginCookieJar logs in and returns the session cookies.
func loginCookieJar(t *testing.T, base string) []*http.Cookie {
	t.Helper()
	body := strings.NewReader(`{"email":"user@example.com","password":"pw-123456789"}`)
	resp, err := http.Post(base+"/api/auth/login", "application/json", body)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", resp.StatusCode)
	}
	return resp.Cookies()
}

func doWithCookies(method, base, path string, cookies []*http.Cookie, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, base+path, body)
	if err != nil {
		return nil, err
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return http.DefaultClient.Do(req)
}

func TestContainerCreateAndList(t *testing.T) {
	srv := newContainerTestServer(t, domain.RoleOperator)
	defer srv.Close()
	cookies := loginCookieJar(t, srv.URL)

	resp, err := doWithCookies(http.MethodPost, srv.URL, "/api/containers", cookies,
		strings.NewReader(`{"region":"ID"}`))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", resp.StatusCode)
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["source"] != "MANUAL" {
		t.Errorf("source = %v, want MANUAL", got["source"])
	}
	if got["desiredState"] != "RUNNING" {
		t.Errorf("desiredState = %v, want RUNNING", got["desiredState"])
	}
	if got["region"] != "ID" {
		t.Errorf("region = %v, want ID", got["region"])
	}
	if got["generation"] != float64(1) {
		t.Errorf("generation = %v, want 1", got["generation"])
	}
	if _, ok := got["id"].(string); !ok || got["id"] == "" {
		t.Errorf("id missing: %v", got["id"])
	}

	// List reflects the created container.
	resp2, err := doWithCookies(http.MethodGet, srv.URL, "/api/containers", cookies, nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want 200", resp2.StatusCode)
	}
	var list map[string]any
	if err := json.NewDecoder(resp2.Body).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	items, ok := list["containers"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("containers = %v, want 1 item", list["containers"])
	}
}

func TestContainerCreateRejectsBadRegion(t *testing.T) {
	srv := newContainerTestServer(t, domain.RoleOperator)
	defer srv.Close()
	cookies := loginCookieJar(t, srv.URL)

	resp, err := doWithCookies(http.MethodPost, srv.URL, "/api/containers", cookies,
		strings.NewReader(`{"region":"INDO"}`))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestContainerDelete(t *testing.T) {
	srv := newContainerTestServer(t, domain.RoleOperator)
	defer srv.Close()
	cookies := loginCookieJar(t, srv.URL)

	resp, err := doWithCookies(http.MethodPost, srv.URL, "/api/containers", cookies,
		strings.NewReader(`{"name":"c1","region":"ID"}`))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var got map[string]any
	json.NewDecoder(resp.Body).Decode(&got)
	resp.Body.Close()
	id, _ := got["id"].(string)

	resp2, err := doWithCookies(http.MethodDelete, srv.URL, "/api/containers/"+id, cookies, nil)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", resp2.StatusCode)
	}

	// List is now empty.
	resp3, err := doWithCookies(http.MethodGet, srv.URL, "/api/containers", cookies, nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	defer resp3.Body.Close()
	var list map[string]any
	json.NewDecoder(resp3.Body).Decode(&list)
	if items, _ := list["containers"].([]any); len(items) != 0 {
		t.Fatalf("containers after delete = %v, want empty", list["containers"])
	}
}

// TestContainerRBAC proves the act permission gates the container API: an
// ANALYST (read-only) cannot create containers, an OPERATOR can.
func TestContainerRBAC(t *testing.T) {
	t.Run("analyst denied writes", func(t *testing.T) {
		srv := newContainerTestServer(t, domain.RoleAnalyst)
		defer srv.Close()
		cookies := loginCookieJar(t, srv.URL)
		resp, err := doWithCookies(http.MethodPost, srv.URL, "/api/containers", cookies,
			strings.NewReader(`{"region":"ID"}`))
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("analyst status = %d, want 403", resp.StatusCode)
		}
	})

	// P5-04: the /api group used to require `act`, which locked STRATEGIST and
	// ANALYST out of the read endpoints too. The group floor is now `read`, so
	// a read-only role can list containers — it just cannot create or delete.
	// An empty list is still a 200, which is exactly the point: the old code
	// returned 403 here.
	t.Run("analyst allowed reads", func(t *testing.T) {
		srv := newContainerTestServer(t, domain.RoleAnalyst)
		defer srv.Close()
		cookies := loginCookieJar(t, srv.URL)

		resp, err := doWithCookies(http.MethodGet, srv.URL, "/api/containers", cookies, nil)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("analyst read status = %d, want 200", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), "containers") {
			t.Fatalf("analyst read body = %s, want a container list", body)
		}
	})

	t.Run("strategist allowed reads, denied writes", func(t *testing.T) {
		srv := newContainerTestServer(t, domain.RoleStrategist)
		defer srv.Close()
		cookies := loginCookieJar(t, srv.URL)

		list, err := doWithCookies(http.MethodGet, srv.URL, "/api/containers", cookies, nil)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		list.Body.Close()
		if list.StatusCode != http.StatusOK {
			t.Fatalf("strategist read status = %d, want 200", list.StatusCode)
		}

		del, err := doWithCookies(http.MethodDelete, srv.URL, "/api/containers/nope", cookies, nil)
		if err != nil {
			t.Fatalf("delete: %v", err)
		}
		del.Body.Close()
		if del.StatusCode != http.StatusForbidden {
			t.Fatalf("strategist delete status = %d, want 403", del.StatusCode)
		}
	})

	t.Run("anonymous denied", func(t *testing.T) {
		srv := newContainerTestServer(t, domain.RoleOperator)
		defer srv.Close()
		resp, err := http.Post(srv.URL+"/api/containers", "application/json",
			strings.NewReader(`{"region":"ID"}`))
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("anonymous status = %d, want 401", resp.StatusCode)
		}
	})
}
