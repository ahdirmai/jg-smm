package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// poolAffinity mirrors the coordinator's preference hash, so a test can predict
// which machine an account is pinned to.
func poolAffinity(accountID string, n int) int {
	h := fnv32a(accountID)
	return int(h % uint32(n))
}

// --- fakes -----------------------------------------------------------------

type fakeLock struct {
	mu     sync.Mutex
	held   map[string]string
	failed bool
}

func newFakeLock() *fakeLock { return &fakeLock{held: map[string]string{}} }

func (f *fakeLock) AcquireAccount(_ context.Context, accountID, token string, _ time.Duration) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failed {
		return false, errors.New("redis down")
	}
	if holder, ok := f.held[accountID]; ok && holder != token {
		return false, nil
	}
	f.held[accountID] = token
	return true, nil
}

func (f *fakeLock) ReleaseAccount(_ context.Context, accountID, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.held[accountID] == token {
		delete(f.held, accountID)
	}
	return nil
}

type fakeSlots struct {
	mu      sync.Mutex
	held    map[string]string
	order   []string
	lastPin bool
}

func newFakeSlots() *fakeSlots { return &fakeSlots{held: map[string]string{}} }

func (f *fakeSlots) AcquireWorkerSlot(_ context.Context, accountID string, candidates []domain.Worker, _ port.SlotKind, allowFallback bool, _ time.Duration) (domain.Worker, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastPin = !allowFallback
	start := poolAffinity(accountID, len(candidates))
	tries := 1
	if allowFallback {
		tries = len(candidates)
	}
	for i := range tries {
		w := candidates[(start+i)%len(candidates)]
		if _, busy := f.held[w.ID]; busy {
			continue
		}
		f.held[w.ID] = accountID
		f.order = append(f.order, w.Name)
		return w, nil
	}
	return domain.Worker{}, domain.ErrUnavailable
}

func (f *fakeSlots) ReleaseWorkerSlot(_ context.Context, workerID, accountID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.held[workerID] == accountID {
		delete(f.held, workerID)
	}
	return nil
}

func (f *fakeSlots) ClearWorkerSlot(_ context.Context, workerID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.held, workerID)
	return nil
}

type fakeRPC struct {
	mu   sync.Mutex
	sent []port.ExecuteActionRequest
	err  error
}

func (f *fakeRPC) ExecuteAction(_ context.Context, endpointURL string, req port.ExecuteActionRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, req)
	return nil
}
func (f *fakeRPC) StartLogin(context.Context, string, port.AccountRuntimeSpec) error { return nil }
func (f *fakeRPC) SubmitAuthInput(context.Context, string, string, string, string) error {
	return nil
}
func (f *fakeRPC) ClearAuth(context.Context, string, string) error { return nil }
func (f *fakeRPC) OpenBrowser(context.Context, string, port.AccountRuntimeSpec, string) error {
	return nil
}
func (f *fakeRPC) PingKeepalive(context.Context, string) error { return nil }

type fakeProxy struct{ spec *port.ProxySpec }

func (f fakeProxy) ForAccount(context.Context, string, *string) (*port.ProxySpec, error) {
	return f.spec, nil
}

func poolFixture(t *testing.T, workers int, proxy *port.ProxySpec) (*PoolDispatch, *fakeLock, *fakeSlots, *fakeRPC) {
	t.Helper()
	list := make([]domain.Worker, 0, workers)
	for i := range workers {
		name := "smm-worker-0" + itoa(i+1)
		url := "http://" + name + ":8090"
		list = append(list, domain.Worker{ID: "w" + itoa(i+1), Name: name, EndpointURL: &url})
	}
	lock, slots, rpc := newFakeLock(), newFakeSlots(), &fakeRPC{}
	p := &PoolDispatch{
		Workers: func(context.Context) ([]domain.Worker, error) { return list, nil },
		Slots:   slots,
		Lock:    lock,
		RPC:     rpc,
		Proxy:   fakeProxy{spec: proxy},
		LockTTL: time.Minute,
		SlotTTL: time.Minute,
	}
	return p, lock, slots, rpc
}

func poolAccount() domain.Account {
	return domain.Account{
		ID:         "acct-1",
		Platform:   domain.PlatformInstagram,
		AuthStatus: domain.AuthAuthenticated,
		Location:   new("Makassar"),
		Latitude:   new(-5.1477),
		Longitude:  new(119.4327),
		Timezone:   "Asia/Makassar",
	}
}

// --- tests -----------------------------------------------------------------

// A proxyless account must stay on its affinity worker: falling through to
// another machine changes its egress IP, which is the exact tell the pool is
// meant to avoid.
func TestPoolAcquirePinsProxylessAccountToAffinityWorker(t *testing.T) {
	p, _, slots, _ := poolFixture(t, 10, nil)
	acc := poolAccount()
	job := domain.ActionJob{ID: "job-1", AccountID: acc.ID}

	w, release, err := p.Acquire(context.Background(), job, acc)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer release()
	if !slots.lastPin {
		t.Fatalf("a proxyless account must pin the affinity worker (allowFallback=false)")
	}
	want := poolAffinity(acc.ID, 10)
	if got := int(w.ID[1] - '1'); got != want {
		t.Fatalf("leased worker %s (index %d), want affinity index %d", w.Name, got, want)
	}
}

// With its own residential proxy the IP travels with the account, so any free
// worker is acceptable.
func TestPoolAcquireAllowsFallbackWithProxy(t *testing.T) {
	p, _, slots, _ := poolFixture(t, 3, &port.ProxySpec{Server: "http://gate:8080"})
	acc := poolAccount()
	job := domain.ActionJob{ID: "job-1", AccountID: acc.ID}

	_, release, err := p.Acquire(context.Background(), job, acc)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer release()
	if slots.lastPin {
		t.Fatalf("a proxied account must be allowed to fall back")
	}
}

// The account lock is the outer gate: a second dispatch for the same account is
// a conflict, not a second browser.
func TestPoolAcquireSerialisesOneAccount(t *testing.T) {
	p, _, _, _ := poolFixture(t, 2, nil)
	acc := poolAccount()

	_, release, err := p.Acquire(context.Background(), domain.ActionJob{ID: "job-a"}, acc)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if _, _, err := p.Acquire(context.Background(), domain.ActionJob{ID: "job-b"}, acc); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("second concurrent dispatch must conflict, got %v", err)
	}

	// Once released, the next job is admitted.
	release()
	_, release2, err := p.Acquire(context.Background(), domain.ActionJob{ID: "job-c"}, acc)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	release2()
}

// A worker already parked on an interactive session is never handed to an
// action: closing that context would destroy the operator's CAPTCHA work.
func TestPoolAcquireSkipsWorkerHeldByLogin(t *testing.T) {
	p, _, slots, _ := poolFixture(t, 3, &port.ProxySpec{Server: "http://gate:8080"})
	acc := poolAccount()
	job := domain.ActionJob{ID: "job-1", AccountID: acc.ID}
	acc.WorkerID = new("w2") // a login is parked on w2

	for range 3 {
		w, release, err := p.Acquire(context.Background(), job, acc)
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		if w.ID == "w2" {
			t.Fatalf("leased the worker holding a login session")
		}
		release()
		_ = slots
	}
}

// When all ten workers are busy the job waits: ErrUnavailable, and nothing was
// pushed.
func TestPoolAcquireReportsUnavailableWhenFleetIsFull(t *testing.T) {
	p, _, slots, rpc := poolFixture(t, 2, &port.ProxySpec{Server: "http://gate:8080"})
	slots.held["w1"] = "other-1"
	slots.held["w2"] = "other-2"

	_, _, err := p.Acquire(context.Background(), domain.ActionJob{ID: "job-1"}, poolAccount())
	if !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if len(rpc.sent) != 0 {
		t.Fatalf("nothing may be pushed to a full fleet")
	}
	// The account lock must not be left behind by the failed acquire.
	if _, ok := p.Lock.(*fakeLock).held["acct-1"]; ok {
		t.Fatalf("a failed acquire leaked the account lock")
	}
}

// The identity every context needs travels with the request: GPS, timezone,
// user agent, storage state and proxy. Nothing is left for the worker to guess.
func TestPoolPushCarriesFullRuntimeSpec(t *testing.T) {
	p, _, _, rpc := poolFixture(t, 1, &port.ProxySpec{Server: "http://gate:8080", Username: "u"})
	acc := poolAccount()
	acc.UserAgent = new(domain.DefaultDesktopUserAgent)
	job := domain.ActionJob{ID: "job-1", AccountID: acc.ID}

	w, release, err := p.Acquire(context.Background(), job, acc)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	spec, err := p.RuntimeSpec(context.Background(), acc)
	if err != nil {
		release()
		t.Fatalf("runtime: %v", err)
	}
	req := port.ExecuteActionRequest{JobID: job.ID, Action: "comment", TargetURL: "https://ig/p/1", Runtime: spec}
	if err := p.Push(context.Background(), w, req); err != nil {
		release()
		t.Fatalf("push: %v", err)
	}
	release()

	if len(rpc.sent) != 1 {
		t.Fatalf("pushed %d requests", len(rpc.sent))
	}
	got := rpc.sent[0].Runtime
	if got.AccountID != acc.ID || got.Timezone != "Asia/Makassar" {
		t.Fatalf("runtime identity wrong: %+v", got)
	}
	if got.Geolocation == nil || got.Geolocation.Latitude != -5.1477 {
		t.Fatalf("geolocation missing: %+v", got.Geolocation)
	}
	if got.UserAgent != domain.DefaultDesktopUserAgent {
		t.Fatalf("user agent missing: %q", got.UserAgent)
	}
	if got.Proxy == nil || got.Proxy.Server != "http://gate:8080" {
		t.Fatalf("proxy missing: %+v", got.Proxy)
	}
}

// A worker row without an endpoint is unusable, and must fail loudly instead of
// pushing to an empty URL.
func TestPoolPushRequiresEndpoint(t *testing.T) {
	p, _, _, _ := poolFixture(t, 1, nil)
	if err := p.Push(context.Background(), domain.Worker{ID: "w1", Name: "smm-worker-01"}, port.ExecuteActionRequest{}); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

// BusyAccount reports no owner unless a test wires one.
func (f *fakeRPC) BusyAccount(context.Context, string) (string, error) {
	return "", nil
}

// Actions must never be scheduled onto the login machine.
//
// That machine holds the operator's parked browser; a job arriving there would
// answer 409 forever and the CAPTCHA screen would be evicted.
func TestAcquireExcludesLoginWorker(t *testing.T) {
	p, _, _, rpc := poolFixture(t, 3, &port.ProxySpec{Server: "http://gate:8080"})
	p.LoginWorkerName = "smm-worker-01"

	acc := poolAccount()
	job := domain.ActionJob{ID: "job-1", AccountID: acc.ID}

	for range 6 {
		w, release, err := p.Acquire(context.Background(), job, acc)
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		if w.Name == "smm-worker-01" {
			t.Fatalf("action leased the LOGIN machine")
		}
		release()
	}
	_ = rpc
}

// Interactive sessions use ONLY the login machine.
func TestLoginCandidatesIsTheLoginWorker(t *testing.T) {
	p, _, _, _ := poolFixture(t, 3, nil)
	p.LoginWorkerName = "smm-worker-02"

	got, err := p.LoginCandidates(context.Background())
	if err != nil {
		t.Fatalf("login candidates: %v", err)
	}
	if len(got) != 1 || got[0].Name != "smm-worker-02" {
		t.Fatalf("login candidates = %v, want only smm-worker-02", got)
	}

	// No configured login worker: nil, so callers fall back to the fleet.
	p.LoginWorkerName = ""
	if got, _ := p.LoginCandidates(context.Background()); got != nil {
		t.Fatalf("expected nil without a configured login worker, got %v", got)
	}
}

// A pool that is ONE login machine has no action capacity, and must say so.
//
// The earlier filter only dropped the login worker when another candidate was
// left ("if len(actions) > 0"), so with the login box as the only worker the
// filter was skipped and jobs were dispatched straight onto it — evicting the
// operator's parked CAPTCHA.
func TestAcquireRefusesWhenOnlyTheLoginWorkerExists(t *testing.T) {
	p, _, _, _ := poolFixture(t, 1, &port.ProxySpec{Server: "http://gate:8080"})
	p.LoginWorkerName = "smm-worker-01"

	acc := poolAccount()
	job := domain.ActionJob{ID: "job-1", AccountID: acc.ID}

	_, release, err := p.Acquire(context.Background(), job, acc)
	if !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable (no action worker)", err)
	}
	if release != nil {
		t.Fatal("a refused acquire must not hand back a release func")
	}
}
