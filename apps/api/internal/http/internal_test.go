package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/http/oapigen"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
	"github.com/ahdirmai/jg-smm/apps/api/internal/service"
)

// stubClock is a fixed port.Clock for deterministic tests.
type stubClock struct{ t time.Time }

func (c stubClock) Now() time.Time { return c.t }

// In-memory stores for the handler tests. They implement the ports the
// JobService consumes; nothing is persisted.
type fakeWorkerStore struct {
	workers map[string]domain.Worker
}

func newMemWorkerStoreForHTTP() *fakeWorkerStore {
	return &fakeWorkerStore{workers: map[string]domain.Worker{}}
}

func (s *fakeWorkerStore) GetByID(_ context.Context, id string) (domain.Worker, error) {
	w, ok := s.workers[id]
	if !ok {
		return domain.Worker{}, domain.ErrNotFound
	}
	return w, nil
}
func (s *fakeWorkerStore) GetByName(_ context.Context, name string) (domain.Worker, error) {
	for _, w := range s.workers {
		if w.Name == name {
			return w, nil
		}
	}
	return domain.Worker{}, domain.ErrNotFound
}
func (s *fakeWorkerStore) GetByContainerID(_ context.Context, containerID string) (domain.Worker, error) {
	for _, w := range s.workers {
		if w.ContainerID != nil && *w.ContainerID == containerID {
			return w, nil
		}
	}
	return domain.Worker{}, domain.ErrNotFound
}
func (s *fakeWorkerStore) Claim(_ context.Context, claim port.WorkerClaim) (domain.Worker, error) {
	if w, err := s.GetByContainerID(context.Background(), claim.ContainerID); err == nil {
		return w, nil
	}
	var oldest *domain.Worker
	for _, w := range s.workers {
		if w.Status == domain.WorkerPending && (w.ContainerID == nil || *w.ContainerID == "") {
			if oldest == nil || w.CreatedAt.Before(oldest.CreatedAt) {
				picked := w
				oldest = &picked
			}
		}
	}
	if oldest == nil {
		return domain.Worker{}, domain.ErrNotFound
	}
	picked := *oldest
	picked.ContainerID = &claim.ContainerID
	s.workers[picked.ID] = picked
	return picked, nil
}
func (s *fakeWorkerStore) List(_ context.Context, _ port.WorkerFilter) ([]domain.Worker, error) {
	out := make([]domain.Worker, 0, len(s.workers))
	for _, w := range s.workers {
		out = append(out, w)
	}
	return out, nil
}
func (s *fakeWorkerStore) Create(_ context.Context, w domain.Worker) (domain.Worker, error) {
	s.workers[w.ID] = w
	return w, nil
}
func (s *fakeWorkerStore) Update(_ context.Context, w domain.Worker) (domain.Worker, error) {
	s.workers[w.ID] = w
	return w, nil
}
func (s *fakeWorkerStore) Delete(_ context.Context, id string) error {
	delete(s.workers, id)
	return nil
}
func (s *fakeWorkerStore) RecordHeartbeat(_ context.Context, hb domain.Heartbeat, snap port.WorkerSnapshot) error {
	if w, ok := s.workers[hb.WorkerID]; ok {
		w.Status = snap.Status
		w.BrowserStatus = snap.BrowserStatus
		w.QueueDepth = snap.QueueDepth
		w.LastHeartbeat = &hb.TS
		s.workers[hb.WorkerID] = w
	}
	return nil
}

type fakeAccountStore struct {
	accounts map[string]domain.Account
}

func newMemAccountStoreForHTTP() *fakeAccountStore {
	return &fakeAccountStore{accounts: map[string]domain.Account{}}
}

func (s *fakeAccountStore) GetByID(_ context.Context, id string) (domain.Account, error) {
	a, ok := s.accounts[id]
	if !ok {
		return domain.Account{}, domain.ErrNotFound
	}
	return a, nil
}
func (s *fakeAccountStore) List(_ context.Context, _ port.AccountFilter) ([]domain.Account, error) {
	out := make([]domain.Account, 0, len(s.accounts))
	for _, a := range s.accounts {
		out = append(out, a)
	}
	return out, nil
}
func (s *fakeAccountStore) Create(_ context.Context, a domain.Account) (domain.Account, error) {
	s.accounts[a.ID] = a
	return a, nil
}
func (s *fakeAccountStore) Update(_ context.Context, a domain.Account) (domain.Account, error) {
	s.accounts[a.ID] = a
	return a, nil
}
func (s *fakeAccountStore) Assign(_ context.Context, id, wid string) (domain.Account, error) {
	a := s.accounts[id]
	a.WorkerID = &wid
	s.accounts[id] = a
	return a, nil
}
func (s *fakeAccountStore) Unassign(_ context.Context, id string) (domain.Account, error) {
	a := s.accounts[id]
	a.WorkerID = nil
	s.accounts[id] = a
	return a, nil
}
func (s *fakeAccountStore) Delete(_ context.Context, id string) error {
	delete(s.accounts, id)
	return nil
}
func (s *fakeAccountStore) ListByWorker(_ context.Context, wid string) ([]domain.Account, error) {
	var out []domain.Account
	for _, a := range s.accounts {
		if a.WorkerID != nil && *a.WorkerID == wid {
			out = append(out, a)
		}
	}
	return out, nil
}
func (s *fakeAccountStore) CountByWorker(_ context.Context, wid string) (int, error) {
	n := 0
	for _, a := range s.accounts {
		if a.WorkerID != nil && *a.WorkerID == wid {
			n++
		}
	}
	return n, nil
}

type fakeProvisionLogStore struct{}

func (fakeProvisionLogStore) Append(_ context.Context, _ domain.ProvisionLog) error { return nil }
func (fakeProvisionLogStore) ListByWorker(_ context.Context, _ string, _ int) ([]domain.ProvisionLog, error) {
	return nil, nil
}

// fakeProxyGroupStore is the minimal port.ProxyGroupStore for handler tests.
// Duplicate names surface as domain.ErrConflict, mirroring the DB unique.
type fakeProxyGroupStore struct {
	groups map[string]domain.ProxyGroup
}

func newMemProxyStoreForHTTP() *fakeProxyGroupStore {
	return &fakeProxyGroupStore{groups: map[string]domain.ProxyGroup{}}
}

func (s *fakeProxyGroupStore) GetByID(_ context.Context, id string) (domain.ProxyGroup, error) {
	g, ok := s.groups[id]
	if !ok {
		return domain.ProxyGroup{}, domain.ErrNotFound
	}
	return g, nil
}

func (s *fakeProxyGroupStore) List(_ context.Context) ([]domain.ProxyGroup, error) {
	out := make([]domain.ProxyGroup, 0, len(s.groups))
	for _, g := range s.groups {
		out = append(out, g)
	}
	return out, nil
}

func (s *fakeProxyGroupStore) Create(_ context.Context, g domain.ProxyGroup) (domain.ProxyGroup, error) {
	for _, existing := range s.groups {
		if existing.Name == g.Name {
			return domain.ProxyGroup{}, domain.ErrConflict
		}
	}
	if g.ID == "" {
		g.ID = "pg-http-1"
	}
	s.groups[g.ID] = g
	return g, nil
}

func (s *fakeProxyGroupStore) Update(_ context.Context, g domain.ProxyGroup) (domain.ProxyGroup, error) {
	s.groups[g.ID] = g
	return g, nil
}

func (s *fakeProxyGroupStore) Delete(_ context.Context, id string) error {
	delete(s.groups, id)
	return nil
}

func newTestInternal() (*InternalHandler, *service.JobService, *fakeWorkerStore, *fakeAccountStore) {
	workers := newMemWorkerStoreForHTTP()
	accounts := newMemAccountStoreForHTTP()
	jobs := service.NewJobService(workers, accounts, nil, nil, stubClock{t: time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)}, nil, nil, nil)
	return NewInternalHandler(jobs), jobs, workers, accounts
}

// call runs a request against a fresh router with only the internal routes.
func call(h *InternalHandler, method, path, body string) (int, map[string]any) {
	e := echo.New()
	e.HideBanner = true
	h.Register(e)
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set(echo.HeaderContentType, "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestHeartbeatAccepted(t *testing.T) {
	h, _, workers, _ := newTestInternal()
	ctx := context.Background()
	_, _ = workers.Create(ctx, domain.Worker{ID: "w1", Name: "farm-01", Status: domain.WorkerIdle})

	code, body := call(h, http.MethodPost, "/internal/heartbeat", `{"workerId":"w1","queueDepth":3,"browserStatus":"ready"}`)
	if code != http.StatusOK {
		t.Fatalf("want 200, got %d: %v", code, body)
	}
	if body["accepted"] != true {
		t.Fatalf("want accepted=true, got %v", body)
	}
	w, _ := workers.GetByID(ctx, "w1")
	if w.BrowserStatus != "ready" || w.QueueDepth != 3 {
		t.Fatalf("worker snapshot not applied: %+v", w)
	}
	if w.LastHeartbeat == nil {
		t.Fatal("last_heartbeat should be set")
	}
}

func TestHeartbeatRejectsNegativeQueueDepth(t *testing.T) {
	h, _, _, _ := newTestInternal()
	code, _ := call(h, http.MethodPost, "/internal/heartbeat", `{"workerId":"w1","queueDepth":-5}`)
	if code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", code)
	}
}

func TestHeartbeatRequiresWorkerID(t *testing.T) {
	h, _, _, _ := newTestInternal()
	code, _ := call(h, http.MethodPost, "/internal/heartbeat", `{"queueDepth":1}`)
	if code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", code)
	}
}

func TestHeartbeatUnknownWorkerIs404(t *testing.T) {
	h, _, _, _ := newTestInternal()
	code, _ := call(h, http.MethodPost, "/internal/heartbeat", `{"workerId":"nope"}`)
	if code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", code)
	}
}

func TestAccountCallbackValidatesStatus(t *testing.T) {
	h, _, _, _ := newTestInternal()
	code, _ := call(h, http.MethodPost, "/internal/account-callback", `{"accountId":"a1","authStatus":"BOGUS"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", code)
	}
}

func TestAccountCallbackMarksAuthenticated(t *testing.T) {
	h, _, _, accounts := newTestInternal()
	ctx := context.Background()
	acc, _ := accounts.Create(ctx, domain.Account{
		ID: "a1", Platform: domain.PlatformInstagram, Username: "foo",
		AuthStatus: domain.AuthAuthenticating, Status: domain.AccountPending,
	})
	handle := "foo.verified"
	code, body := call(h, http.MethodPost, "/internal/account-callback",
		`{"accountId":"`+acc.ID+`","authStatus":"AUTHENTICATED","handle":"foo.verified"}`)
	if code != http.StatusOK {
		t.Fatalf("want 200, got %d: %v", code, body)
	}
	got, _ := accounts.GetByID(ctx, acc.ID)
	if got.AuthStatus != domain.AuthAuthenticated {
		t.Fatalf("auth status = %q", got.AuthStatus)
	}
	if got.Handle == nil || *got.Handle != handle {
		t.Fatalf("handle = %v", got.Handle)
	}
	if got.Status != domain.AccountActive {
		t.Fatalf("status = %q, want ACTIVE", got.Status)
	}
	if got.LastVerified == nil {
		t.Fatal("last_verified should be set")
	}
}

func TestActionCallbackScreenshotPathBase(t *testing.T) {
	h, _, _, _ := newTestInternal()
	code, body := call(h, http.MethodPost, "/internal/action-callback",
		`{"attemptId":"att1","status":"SUCCESS","screenshotPath":"/etc/passwd"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("want 400 for non-png, got %d: %v", code, body)
	}
	// A .png path with traversal is accepted after path.Base (verdict is 202 due
	// to deferred P3 persistence, proving sanitisation passed).
	code, body = call(h, http.MethodPost, "/internal/action-callback",
		`{"attemptId":"att1","status":"SUCCESS","screenshotPath":"../../evidence/shot.png"}`)
	if code != http.StatusAccepted {
		t.Fatalf("want 202 for sanitised .png, got %d: %v", code, body)
	}
}

func TestActionCallbackRejectsInvalidStatus(t *testing.T) {
	h, _, _, _ := newTestInternal()
	code, _ := call(h, http.MethodPost, "/internal/action-callback",
		`{"attemptId":"att1","status":"NOPE"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", code)
	}
}

func TestActionCallbackRequiresAttemptID(t *testing.T) {
	h, _, _, _ := newTestInternal()
	code, _ := call(h, http.MethodPost, "/internal/action-callback", `{"status":"SUCCESS"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", code)
	}
}

func TestTruncatePtr(t *testing.T) {
	if got := truncatePtr(nil, 10); got != nil {
		t.Fatal("nil input must stay nil")
	}
	long := strings.Repeat("x", 20)
	got := truncatePtr(&long, 10)
	if got == nil || len(*got) != 10 {
		t.Fatalf("want 10 runes, got %v", got)
	}
	empty := "   "
	if got := truncatePtr(&empty, 10); got != nil {
		t.Fatalf("blank input must become nil, got %v", *got)
	}
}

func TestBodyLimit(t *testing.T) {
	h, _, _, _ := newTestInternal()
	e := echo.New()
	e.HideBanner = true
	h.Register(e)

	huge := strings.Repeat("a", MaxCallbackBytes+1024)
	req := httptest.NewRequest(http.MethodPost, "/internal/heartbeat",
		bytes.NewBufferString(`{"workerId":"w1","error":"`+huge+`"}`))
	req.Header.Set(echo.HeaderContentType, "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("want 413, got %d", rec.Code)
	}
}

// Ensure the oapigen enum constants we rely on compile-time match the domain.
func TestEnumParity(t *testing.T) {
	if string(oapigen.AttemptStatusSUCCESS) != string(domain.AttemptSuccess) {
		t.Fatal("AttemptStatus SUCCESS mismatch")
	}
	if string(oapigen.AuthStatusAUTHENTICATED) != string(domain.AuthAuthenticated) {
		t.Fatal("AuthStatus AUTHENTICATED mismatch")
	}
}
