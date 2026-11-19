package service

import (
	"context"
	"errors"
	"math"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// TestContainerCreateManual exercises P1-19: a manual container is created
// RUNNING, MANUAL, and generation 1 — the state the reconciler provisions from.
func TestContainerCreateManual(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	svc := NewContainerService(store, accounts, nil, ContainerConfig{})

	w, err := svc.Create(context.Background(), "", "ID", "Jakarta", nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if w.Source != domain.SourceManual {
		t.Fatalf("source = %q, want MANUAL", w.Source)
	}
	if w.DesiredState != domain.DesiredRunning {
		t.Fatalf("desiredState = %q, want RUNNING", w.DesiredState)
	}
	if w.Generation != 1 {
		t.Fatalf("generation = %d, want 1", w.Generation)
	}
	if w.Region != "ID" {
		t.Fatalf("region = %q, want ID", w.Region)
	}
	if w.Name == "" {
		t.Fatal("name should be generated when omitted")
	}
	if store.get(w.ID).ID != w.ID {
		t.Fatalf("container not persisted: %s", w.ID)
	}
}

func TestContainerCreateValidatesRegion(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	svc := NewContainerService(store, accounts, nil, ContainerConfig{})

	if _, err := svc.Create(context.Background(), "c1", "", "Jakarta", nil); err == nil {
		t.Fatal("want error for missing region")
	}
	if _, err := svc.Create(context.Background(), "c1", "IDN", "Jakarta", nil); err == nil {
		t.Fatal("want error for a 3-letter region")
	}
	// The DB check constraint is region ~ '^[A-Z]{2}$'; lowercase must be
	// rejected here rather than surfacing as a driver error.
	if _, err := svc.Create(context.Background(), "c1", "id", "Jakarta", nil); err == nil {
		t.Fatal("want error for a lowercase region")
	}
}

func TestContainerCreateRejectsLongName(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	svc := NewContainerService(store, accounts, nil, ContainerConfig{})

	long := make([]byte, 65)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := svc.Create(context.Background(), string(long), "ID", "Jakarta", nil); err == nil {
		t.Fatal("want error for a name over 64 chars")
	}
}

func TestContainerCreateDuplicateName(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	svc := NewContainerService(store, accounts, nil, ContainerConfig{})

	if _, err := svc.Create(context.Background(), "dupe", "ID", "Jakarta", nil); err != nil {
		t.Fatalf("create 1: %v", err)
	}
	// Simulate the store rejecting a second row with the same name.
	store.duplicateNames["dupe"] = true
	if _, err := svc.Create(context.Background(), "dupe", "ID", "Jakarta", nil); err == nil {
		t.Fatal("want conflict for a duplicate name")
	}
}

func TestContainerListIncludesAccounts(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	packer := NewPacker(store, accounts, PackerConfig{MaxPerContainer: 2})
	svc := NewContainerService(store, accounts, packer, ContainerConfig{})

	w, err := svc.Create(context.Background(), "c1", "ID", "Jakarta", nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	accounts.seed(accountFixtureWithWorker("a1", domain.PlatformInstagram, w.ID))
	accounts.seed(accountFixtureWithWorker("a2", domain.PlatformThreads, w.ID))

	views, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("len(views) = %d, want 1", len(views))
	}
	if len(views[0].Accounts) != 2 {
		t.Fatalf("accounts = %d, want 2", len(views[0].Accounts))
	}
}

func TestContainerDeleteReleasesAccounts(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	packer := NewPacker(store, accounts, PackerConfig{MaxPerContainer: 2})
	svc := NewContainerService(store, accounts, packer, ContainerConfig{})

	w, err := svc.Create(context.Background(), "c1", "ID", "Jakarta", nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	accounts.seed(accountFixtureWithWorker("a1", domain.PlatformInstagram, w.ID))

	if err := svc.Delete(context.Background(), w.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := store.workers[w.ID]; ok {
		t.Fatal("container row should be removed")
	}
	if wid := accounts.get("a1").WorkerID; wid != nil {
		t.Fatalf("account still assigned to %s", *wid)
	}
}

func TestContainerDeleteMissing(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	svc := NewContainerService(store, accounts, nil, ContainerConfig{})

	if err := svc.Delete(context.Background(), "ghost"); err == nil {
		t.Fatal("want error for a missing container")
	}
}

// Location is required and must come from the reference list; the point is
// frozen inside the city radius (domain.City.RandomPoint).
func TestContainerCreateValidatesLocation(t *testing.T) {
	store := newFakeWorkerStore()
	svc := NewContainerService(store, newFakeAccountStore(), nil, ContainerConfig{})

	if _, err := svc.Create(context.Background(), "c1", "ID", "", nil); err == nil {
		t.Fatal("want error for missing location")
	}
	if _, err := svc.Create(context.Background(), "c1", "ID", "Nowhere", nil); err == nil {
		t.Fatal("want error for an unknown location")
	}
	w, err := svc.Create(context.Background(), "c1", "ID", "jakarta", nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if w.Location == nil || *w.Location != "jakarta" {
		t.Errorf("Location = %v, want the city as submitted", w.Location)
	}
	if w.Latitude == nil || w.Longitude == nil {
		t.Fatal("want a frozen coordinate attached to the worker")
	}
	city, ok := domain.FindCity("Jakarta")
	if !ok {
		t.Fatal("Jakarta missing from domain.Cities")
	}
	if !inRange(*w.Latitude, city.Latitude, city.RadiusKm) ||
		!inRange(*w.Longitude, city.Longitude, city.RadiusKm) {
		t.Errorf("point (%.5f, %.5f) outside %s radius", *w.Latitude, *w.Longitude, city.Name)
	}
}

// inRange checks the point sits within radiusKm of the centre, using the same
// equirectangular km/degree conversion as RandomPoint.
func inRange(point, centre float64, radiusKm float64) bool {
	const tol = 0.5 // slack for the 5-decimal rounding
	return math.Abs(point-centre)*111.32 <= radiusKm+tol
}

// TestContainerLogsReturnsAuditTrail covers R-06/F-07: a PENDING card can show
// the provisioner's own record instead of an unexplained spinner.
func TestContainerLogsReturnsAuditTrail(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	logs := newFakeProvisionLogStore()
	svc := NewContainerService(store, accounts, nil, ContainerConfig{Logs: logs})

	now := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)
	logs.append(domain.ProvisionLog{
		ID: "l1", WorkerID: "w1", Op: domain.OpCreate, Generation: 1,
		Status: domain.ProvisionFailed, Error: ptrString("image not found"), TS: now,
	})
	logs.append(domain.ProvisionLog{
		ID: "l2", WorkerID: "w1", Op: domain.OpCreate, Generation: 1,
		Status: domain.ProvisionApplied, TS: now.Add(time.Second),
	})

	out, err := svc.Logs(context.Background(), "w1", 0)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("logs = %d entries, want 2", len(out))
	}
	// Newest first: the failure explains the card only if it is at the top.
	if out[0].ID != "l2" {
		t.Errorf("first entry = %s, want the newest (l2)", out[0].ID)
	}
}

// Without a store the call is a nil slice, not an error: the card renders
// "nothing recorded" instead of 500-ing before the DB is wired.
func TestContainerLogsWithoutStore(t *testing.T) {
	svc := NewContainerService(newFakeWorkerStore(), newFakeAccountStore(), nil, ContainerConfig{})
	out, err := svc.Logs(context.Background(), "w1", 0)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if out != nil {
		t.Fatalf("want nil, got %d entries", len(out))
	}
}

// fakeProvisionLogStore is the in-memory provision_log for service tests.
type fakeProvisionLogStore struct {
	mu   sync.Mutex
	logs []domain.ProvisionLog
}

func newFakeProvisionLogStore() *fakeProvisionLogStore {
	return &fakeProvisionLogStore{}
}

func (s *fakeProvisionLogStore) append(l domain.ProvisionLog) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logs = append(s.logs, l)
}

func (s *fakeProvisionLogStore) Append(_ context.Context, l domain.ProvisionLog) error {
	s.append(l)
	return nil
}

func (s *fakeProvisionLogStore) ListByWorker(_ context.Context, workerID string, limit int) ([]domain.ProvisionLog, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]domain.ProvisionLog, 0, len(s.logs))
	for i := len(s.logs) - 1; i >= 0; i-- {
		if s.logs[i].WorkerID == workerID {
			out = append(out, s.logs[i])
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// clearRPC records which endpoints were asked to drop every browser.
type clearRPC struct {
	fakeRPC
	cleared []string
	failOn  string
}

func (c *clearRPC) ClearAuth(_ context.Context, endpointURL, accountID string) error {
	if endpointURL == c.failOn {
		return errors.New("dial tcp: no such host")
	}
	c.cleared = append(c.cleared, endpointURL+"#"+accountID)
	return nil
}

// Clearing sessions must reach every pool worker with an EMPTY account id: the
// case it exists for is a display stuck busy with an account no lease names, so
// a per-account clear cannot address it.
func TestClearSessionsAsksEveryPoolWorker(t *testing.T) {
	store := newFakeWorkerStore()
	ep1, ep2 := "http://w1:8090", "http://w2:8090"
	store.seed(domain.Worker{ID: "w1", Name: "smm-worker-01", EndpointURL: &ep1})
	store.seed(domain.Worker{ID: "w2", Name: "smm-worker-02", EndpointURL: &ep2})
	store.seed(domain.Worker{ID: "w3", Name: "legacy"}) // no endpoint: not reachable

	rpc := &clearRPC{}
	svc := NewContainerService(store, newFakeAccountStore(), nil, ContainerConfig{RPC: rpc})

	res := svc.ClearSessions(context.Background())
	if len(res.Cleared) != 2 {
		t.Fatalf("cleared %v, want both pool workers", res.Cleared)
	}
	if len(rpc.cleared) != 2 || rpc.cleared[0] != ep1+"#" || rpc.cleared[1] != ep2+"#" {
		t.Fatalf("clear must be called with an empty account id: %v", rpc.cleared)
	}
}

// After the browsers are closed their slots are stale by definition; leaving
// them makes every cleared worker look busy until a TTL expires.
type sweepSlots struct{ cleared int }

func (s *sweepSlots) AcquireWorkerSlot(context.Context, string, []domain.Worker, port.SlotKind, bool, time.Duration) (domain.Worker, error) {
	return domain.Worker{}, domain.ErrUnavailable
}
func (s *sweepSlots) ReleaseWorkerSlot(context.Context, string, string) error { return nil }
func (s *sweepSlots) ClearWorkerSlot(context.Context, string) error           { return nil }
func (s *sweepSlots) ClearAllWorkerSlots(context.Context) (int, error) {
	s.cleared++
	return 3, nil
}

func TestClearSessionsSweepsWorkerSlots(t *testing.T) {
	store := newFakeWorkerStore()
	ep := "http://w1:8090"
	store.seed(domain.Worker{ID: "w1", Name: "smm-worker-01", EndpointURL: &ep})
	slots := &sweepSlots{}
	svc := NewContainerService(store, newFakeAccountStore(), nil, ContainerConfig{RPC: &clearRPC{}, Slots: slots})

	svc.ClearSessions(context.Background())
	if slots.cleared != 1 {
		t.Fatalf("slot sweep calls = %d, want 1", slots.cleared)
	}
}

// One unreachable machine must not hide the result for the rest.
func TestClearSessionsReportsFailures(t *testing.T) {
	store := newFakeWorkerStore()
	ep1, ep2 := "http://w1:8090", "http://w2:8090"
	store.seed(domain.Worker{ID: "w1", Name: "smm-worker-01", EndpointURL: &ep1})
	store.seed(domain.Worker{ID: "w2", Name: "smm-worker-02", EndpointURL: &ep2})

	rpc := &clearRPC{failOn: ep2}
	svc := NewContainerService(store, newFakeAccountStore(), nil, ContainerConfig{RPC: rpc})

	res := svc.ClearSessions(context.Background())
	if len(res.Cleared) != 1 || res.Cleared[0] != "smm-worker-01" {
		t.Fatalf("cleared = %v", res.Cleared)
	}
	if len(res.Failed) != 1 || res.Failed[0].Worker != "smm-worker-02" {
		t.Fatalf("failed = %v", res.Failed)
	}
}

func TestOpenWorkerLiveViewMintsATicket(t *testing.T) {
	store := newFakeWorkerStore()
	store.seed(domain.Worker{
		ID: "w-live", Name: "smm-worker-06",
		NoVNCService: new("https://smm-worker-06.boxd.sh/novnc"),
	})
	svc := NewContainerService(store, newFakeAccountStore(), nil, ContainerConfig{
		NoVNCSecret: ticketSecret, NoVNCTicketTTL: time.Minute,
	})
	got, err := svc.OpenWorkerLiveView(context.Background(), "w-live")
	if err != nil {
		t.Fatalf("open worker live view: %v", err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	path := u.Query().Get("path")
	ticket := strings.TrimPrefix(path, "novnc/websockify?ticket=")
	if ticket == path || VerifyLiveViewTicket(ticketSecret, ticket, "smm-worker-06", time.Now()) != nil {
		t.Fatalf("URL does not carry a valid ticket: %s", got)
	}
}
