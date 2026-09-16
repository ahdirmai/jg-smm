package service

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// fakeAccountStore is a minimal port.AccountStore for packer/account tests.
// It mirrors the real DB invariants (000003_accounts.up.sql):
//   - UNIQUE (platform, username)  -> enforced in Create/seed via uniqueKeys.
//   - UNIQUE (worker_id, platform) -> enforced in Assign via byWorker.
type fakeAccountStore struct {
	mu         sync.Mutex
	accounts   map[string]domain.Account
	byWorker   map[string][]string // workerID -> accountIDs in insertion order
	uniqueKeys map[string]bool     // "platform|username"
	failAssign bool
	nextID     int
}

func newFakeAccountStore() *fakeAccountStore {
	return &fakeAccountStore{
		accounts:   map[string]domain.Account{},
		byWorker:   map[string][]string{},
		uniqueKeys: map[string]bool{},
	}
}

func fakeAccountUniqueKey(platform domain.Platform, username string) string {
	return string(platform) + "|" + username
}

// seed inserts a pre-existing row (with a caller-chosen id) exactly as a
// migration or a prior create would have left it.
func (s *fakeAccountStore) seed(a domain.Account) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seedLocked(a)
}

// seedLocked stores a row and returns the stored value (with any generated id
// and created-at filled in). It is the single write path so Create and seed
// can never disagree about bookkeeping.
func (s *fakeAccountStore) seedLocked(a domain.Account) domain.Account {
	if a.ID == "" {
		s.nextID++
		a.ID = fmt.Sprintf("acc-%d", s.nextID)
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	s.accounts[a.ID] = a
	s.uniqueKeys[fakeAccountUniqueKey(a.Platform, a.Username)] = true
	if a.WorkerID != nil {
		s.byWorker[*a.WorkerID] = append(s.byWorker[*a.WorkerID], a.ID)
	}
	return a
}

func (s *fakeAccountStore) get(id string) domain.Account {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.accounts[id]
}

func (s *fakeAccountStore) GetByID(ctx context.Context, id string) (domain.Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.accounts[id]
	if !ok {
		return domain.Account{}, domain.ErrNotFound
	}
	return a, nil
}

func (s *fakeAccountStore) List(ctx context.Context, f port.AccountFilter) ([]domain.Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]domain.Account, 0, len(s.accounts))
	for _, a := range s.accounts {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *fakeAccountStore) Create(ctx context.Context, a domain.Account) (domain.Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.uniqueKeys[fakeAccountUniqueKey(a.Platform, a.Username)] {
		return domain.Account{}, domain.ErrConflict
	}
	return s.seedLocked(a), nil
}

func (s *fakeAccountStore) Update(ctx context.Context, a domain.Account) (domain.Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.accounts[a.ID]; !ok {
		return domain.Account{}, domain.ErrNotFound
	}
	s.accounts[a.ID] = a
	return a, nil
}

func (s *fakeAccountStore) Assign(ctx context.Context, accountID, workerID string) (domain.Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failAssign {
		return domain.Account{}, errFake
	}
	a, ok := s.accounts[accountID]
	if !ok {
		return domain.Account{}, domain.ErrNotFound
	}
	// UNIQUE(worker_id, platform): reject a second account of a platform.
	for _, otherID := range s.byWorker[workerID] {
		if s.accounts[otherID].Platform == a.Platform {
			return domain.Account{}, domain.ErrConflict
		}
	}
	a.WorkerID = &workerID
	s.accounts[accountID] = a
	s.byWorker[workerID] = append(s.byWorker[workerID], accountID)
	return a, nil
}

func (s *fakeAccountStore) Unassign(ctx context.Context, accountID string) (domain.Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.accounts[accountID]
	if !ok {
		return domain.Account{}, domain.ErrNotFound
	}
	if a.WorkerID != nil {
		var kept []string
		for _, id := range s.byWorker[*a.WorkerID] {
			if id != accountID {
				kept = append(kept, id)
			}
		}
		s.byWorker[*a.WorkerID] = kept
	}
	// Return the account WITH its prior worker id so Release can reap that
	// container; the caller treats the row as unassigned.
	prior := a.WorkerID
	a.WorkerID = nil
	s.accounts[accountID] = a
	return domain.Account{
		ID:       a.ID,
		Platform: a.Platform,
		Username: a.Username,
		Status:   a.Status,
		WorkerID: prior, // informational; the row above is already cleared
	}, nil
}

func (s *fakeAccountStore) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.accounts[id]
	if !ok {
		return domain.ErrNotFound
	}
	delete(s.accounts, id)
	delete(s.uniqueKeys, fakeAccountUniqueKey(a.Platform, a.Username))
	if a.WorkerID != nil {
		var kept []string
		for _, otherID := range s.byWorker[*a.WorkerID] {
			if otherID != id {
				kept = append(kept, otherID)
			}
		}
		s.byWorker[*a.WorkerID] = kept
	}
	return nil
}

func (s *fakeAccountStore) ListByWorker(ctx context.Context, workerID string) ([]domain.Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.Account
	for _, id := range s.byWorker[workerID] {
		out = append(out, s.accounts[id])
	}
	return out, nil
}

func (s *fakeAccountStore) CountByWorker(ctx context.Context, workerID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.byWorker[workerID]), nil
}

// ---- tests ----

func TestPackIntoExistingSlot(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	store.seed(workerFixture("w1", domain.DesiredRunning, 1, intPtr(1)))

	p := NewPacker(store, accounts, PackerConfig{MaxPerContainer: 2, AutoCreate: true})
	accounts.seed(accountFixture("a1", domain.PlatformInstagram))

	updated, w, err := p.Pack(context.Background(), "a1", domain.PlatformInstagram)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	if w.ID != "w1" {
		t.Fatalf("landed on %s, want w1", w.ID)
	}
	if updated.WorkerID == nil || *updated.WorkerID != "w1" {
		t.Fatalf("account worker_id = %v, want w1", updated.WorkerID)
	}
}

func TestPackSkipsDuplicatePlatformAndUsesNextContainer(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	store.seed(workerFixture("w1", domain.DesiredRunning, 1, intPtr(1)))
	store.seed(workerFixture("w2", domain.DesiredRunning, 1, intPtr(1)))
	// w1 already hosts Instagram.
	accounts.seed(accountFixtureWithWorker("a0", domain.PlatformInstagram, "w1"))

	p := NewPacker(store, accounts, PackerConfig{MaxPerContainer: 2, AutoCreate: false})
	accounts.seed(accountFixture("a1", domain.PlatformInstagram))

	updated, w, err := p.Pack(context.Background(), "a1", domain.PlatformInstagram)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	if w.ID != "w2" {
		t.Fatalf("landed on %s, want w2 (w1 hosts the platform)", w.ID)
	}
	if *updated.WorkerID != "w2" {
		t.Fatalf("account worker_id = %v, want w2", updated.WorkerID)
	}
}

func TestPackSkipsFullContainer(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	store.seed(workerFixture("w1", domain.DesiredRunning, 1, intPtr(1)))
	store.seed(workerFixture("w2", domain.DesiredRunning, 1, intPtr(1)))
	// w1 is at capacity (max 1 per container here).
	accounts.seed(accountFixtureWithWorker("a0", domain.PlatformInstagram, "w1"))

	p := NewPacker(store, accounts, PackerConfig{MaxPerContainer: 1, AutoCreate: false})
	accounts.seed(accountFixture("a1", domain.PlatformThreads))

	updated, w, err := p.Pack(context.Background(), "a1", domain.PlatformThreads)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	if w.ID != "w2" {
		t.Fatalf("landed on %s, want w2 (w1 is full)", w.ID)
	}
	if *updated.WorkerID != "w2" {
		t.Fatalf("account worker_id = %v, want w2", updated.WorkerID)
	}
}

func TestPackNoSlotWithoutAutoCreate(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	store.seed(workerFixture("w1", domain.DesiredRunning, 1, intPtr(1)))
	accounts.seed(accountFixtureWithWorker("a0", domain.PlatformInstagram, "w1"))

	p := NewPacker(store, accounts, PackerConfig{MaxPerContainer: 1, AutoCreate: false})
	accounts.seed(accountFixture("a1", domain.PlatformInstagram))

	if _, _, err := p.Pack(context.Background(), "a1", domain.PlatformInstagram); err == nil {
		t.Fatal("want ErrNoSlot when no container qualifies and auto-create is off")
	}
}

func TestPackAutoCreatesWhenNoSlot(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	// Empty fleet: nothing can host the account yet.
	p := NewPacker(store, accounts, PackerConfig{MaxPerContainer: 2, AutoCreate: true})
	accounts.seed(accountFixture("a1", domain.PlatformInstagram))

	updated, w, err := p.Pack(context.Background(), "a1", domain.PlatformInstagram)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	if w.Source != domain.SourceAuto {
		t.Fatalf("auto-created source = %q, want AUTO", w.Source)
	}
	if w.DesiredState != domain.DesiredRunning {
		t.Fatalf("desired state = %q, want RUNNING", w.DesiredState)
	}
	if w.Generation != 1 {
		t.Fatalf("generation = %d, want 1", w.Generation)
	}
	if updated.WorkerID == nil || *updated.WorkerID != w.ID {
		t.Fatalf("account worker_id = %v, want %s", updated.WorkerID, w.ID)
	}
	if store.get(w.ID).ID != w.ID {
		t.Fatalf("auto container not persisted: %s", w.ID)
	}
}

func TestPackRejectsUnpackableAccount(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	p := NewPacker(store, accounts, PackerConfig{MaxPerContainer: 2, AutoCreate: true})

	dead := accountFixture("a1", domain.PlatformInstagram)
	dead.Status = domain.AccountDead
	accounts.seed(dead)

	if _, _, err := p.Pack(context.Background(), "a1", domain.PlatformInstagram); err == nil {
		t.Fatal("want error for a dead account")
	}
}

func TestPackRejectsAlreadyPackedAccount(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	p := NewPacker(store, accounts, PackerConfig{MaxPerContainer: 2, AutoCreate: true})
	accounts.seed(accountFixtureWithWorker("a1", domain.PlatformInstagram, "w9"))

	if _, _, err := p.Pack(context.Background(), "a1", domain.PlatformInstagram); err == nil {
		t.Fatal("want error for an already-packed account")
	}
}

func TestPackMissingAccount(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	p := NewPacker(store, accounts, PackerConfig{MaxPerContainer: 2, AutoCreate: true})
	if _, _, err := p.Pack(context.Background(), "ghost", domain.PlatformInstagram); err == nil {
		t.Fatal("want error for a missing account")
	}
}

// TestPackRecoversFromLostRace: two packers race for one slot; the store's
// conflict must not fail the call, the second account goes elsewhere.
func TestPackRecoversFromLostRace(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	store.seed(workerFixture("w1", domain.DesiredRunning, 1, intPtr(1)))
	store.seed(workerFixture("w2", domain.DesiredRunning, 1, intPtr(1)))
	accounts.seed(accountFixture("a1", domain.PlatformInstagram))
	accounts.seed(accountFixture("a2", domain.PlatformInstagram))

	p := NewPacker(store, accounts, PackerConfig{MaxPerContainer: 2, AutoCreate: false})

	// a1 takes w1 first, so a2's candidate walk hits a conflict on w1.
	if _, _, err := p.Pack(context.Background(), "a1", domain.PlatformInstagram); err != nil {
		t.Fatalf("pack a1: %v", err)
	}
	updated, w, err := p.Pack(context.Background(), "a2", domain.PlatformInstagram)
	if err != nil {
		t.Fatalf("pack a2: %v", err)
	}
	if w.ID != "w2" {
		t.Fatalf("a2 landed on %s, want w2 after losing the race on w1", w.ID)
	}
	if *updated.WorkerID != "w2" {
		t.Fatalf("a2 worker_id = %v, want w2", updated.WorkerID)
	}
}

func TestReapEmptyAutoContainer(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	store.seed(workerFixtureWithSource("w1", domain.SourceAuto, domain.DesiredRunning, 1, intPtr(1)))

	p := NewPacker(store, accounts, PackerConfig{MaxPerContainer: 2})
	if err := p.ReapEmpty(context.Background(), "w1"); err != nil {
		t.Fatalf("reap: %v", err)
	}
	if _, ok := store.workers["w1"]; ok {
		t.Fatal("empty AUTO container should be reaped")
	}
}

func TestReapKeepsManualContainer(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	store.seed(workerFixtureWithSource("w1", domain.SourceManual, domain.DesiredRunning, 1, intPtr(1)))

	p := NewPacker(store, accounts, PackerConfig{MaxPerContainer: 2})
	if err := p.ReapEmpty(context.Background(), "w1"); err != nil {
		t.Fatalf("reap: %v", err)
	}
	if _, ok := store.workers["w1"]; !ok {
		t.Fatal("empty MANUAL container must be kept")
	}
}

func TestReapKeepsNonEmptyAutoContainer(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	store.seed(workerFixtureWithSource("w1", domain.SourceAuto, domain.DesiredRunning, 1, intPtr(1)))
	accounts.seed(accountFixtureWithWorker("a1", domain.PlatformInstagram, "w1"))

	p := NewPacker(store, accounts, PackerConfig{MaxPerContainer: 2})
	if err := p.ReapEmpty(context.Background(), "w1"); err != nil {
		t.Fatalf("reap: %v", err)
	}
	if _, ok := store.workers["w1"]; !ok {
		t.Fatal("non-empty AUTO container must be kept")
	}
}

func TestReapMissingWorkerIsNoOp(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	p := NewPacker(store, accounts, PackerConfig{MaxPerContainer: 2})
	if err := p.ReapEmpty(context.Background(), "ghost"); err != nil {
		t.Fatalf("reap missing: %v", err)
	}
}

func TestReleaseUnassignsAndReaps(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	store.seed(workerFixtureWithSource("w1", domain.SourceAuto, domain.DesiredRunning, 1, intPtr(1)))
	accounts.seed(accountFixtureWithWorker("a1", domain.PlatformInstagram, "w1"))

	p := NewPacker(store, accounts, PackerConfig{MaxPerContainer: 2})
	if err := p.Release(context.Background(), "a1"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if wid := accounts.get("a1").WorkerID; wid != nil {
		t.Fatalf("account still assigned to %s", *wid)
	}
	if _, ok := store.workers["w1"]; ok {
		t.Fatal("empty AUTO container should be reaped on release")
	}
}

func TestReleaseKeepsManualContainer(t *testing.T) {
	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	store.seed(workerFixtureWithSource("w1", domain.SourceManual, domain.DesiredRunning, 1, intPtr(1)))
	accounts.seed(accountFixtureWithWorker("a1", domain.PlatformInstagram, "w1"))

	p := NewPacker(store, accounts, PackerConfig{MaxPerContainer: 2})
	if err := p.Release(context.Background(), "a1"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, ok := store.workers["w1"]; !ok {
		t.Fatal("MANUAL container must survive release")
	}
}

func TestNewWorkerNameUnique(t *testing.T) {
	clock := systemClock{}
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		name := newWorkerName(clock)
		if name == "" {
			t.Fatal("empty worker name")
		}
		if seen[name] {
			t.Fatalf("duplicate worker name after %d tries: %s", i, name)
		}
		seen[name] = true
	}
}

// packScaleCase is one shape of the P5-01 scale test: a fleet of accounts, a
// per-container cap, and the number of containers the fleet must spread over.
type packScaleCase struct {
	name           string
	platforms      []domain.Platform
	maxPer         int
	wantContainers int
}

// runPackScale packs totalAccounts accounts (one platform set per container)
// and asserts the invariants that hold at production scale.
func runPackScale(t *testing.T, tc packScaleCase) {
	t.Helper()

	store := newFakeWorkerStore()
	accounts := newFakeAccountStore()

	platformsPerContainer := len(tc.platforms)
	totalAccounts := platformsPerContainer * tc.wantContainers

	p := NewPacker(store, accounts, PackerConfig{
		MaxPerContainer: tc.maxPer,
		AutoCreate:      true,
		// Hundreds of packs would flood the test log; the decisions are
		// asserted below rather than read off stdout.
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	for i := 0; i < totalAccounts; i++ {
		id := fmt.Sprintf("acc-%03d", i)
		accounts.seed(domain.Account{
			ID:         id,
			Platform:   tc.platforms[i%platformsPerContainer],
			Username:   "user-" + id,
			AuthStatus: domain.AuthAuthenticating,
			Status:     domain.AccountActive,
		})
	}

	if err := t.Run("packs the whole fleet", func(t *testing.T) {
		for i := 0; i < totalAccounts; i++ {
			id := fmt.Sprintf("acc-%03d", i)
			if _, _, err := p.Pack(context.Background(), id, accounts.get(id).Platform); err != nil {
				t.Fatalf("pack %s: %v", id, err)
			}
		}
	}); !err {
		return
	}

	fleet := func(t *testing.T) []domain.Worker {
		t.Helper()
		out, err := store.List(context.Background(), port.WorkerFilter{})
		if err != nil {
			t.Fatalf("list workers: %v", err)
		}
		return out
	}

	t.Run("spreads across exactly the expected fleet size", func(t *testing.T) {
		if got := len(fleet(t)); got != tc.wantContainers {
			t.Fatalf("fleet size = %d, want %d", got, tc.wantContainers)
		}
	})

	t.Run("honors the per-container account cap", func(t *testing.T) {
		for _, w := range fleet(t) {
			count, err := accounts.CountByWorker(context.Background(), w.ID)
			if err != nil {
				t.Fatalf("count on %s: %v", w.ID, err)
			}
			if count > tc.maxPer {
				t.Fatalf("container %s holds %d accounts, cap is %d", w.ID, count, tc.maxPer)
			}
		}
	})

	t.Run("hosts at most one account per platform per container", func(t *testing.T) {
		for _, w := range fleet(t) {
			hosted, err := accounts.ListByWorker(context.Background(), w.ID)
			if err != nil {
				t.Fatalf("list accounts on %s: %v", w.ID, err)
			}
			seen := map[domain.Platform]bool{}
			for _, a := range hosted {
				if seen[a.Platform] {
					t.Fatalf("container %s hosts two %s accounts", w.ID, a.Platform)
				}
				seen[a.Platform] = true
			}
		}
	})

	t.Run("leaves no account unpacked", func(t *testing.T) {
		all, err := accounts.List(context.Background(), port.AccountFilter{})
		if err != nil {
			t.Fatalf("list accounts: %v", err)
		}
		for _, a := range all {
			if a.WorkerID == nil {
				t.Fatalf("account %s was never packed onto a container", a.ID)
			}
		}
	})

	t.Run("reaps auto-created containers once they drain", func(t *testing.T) {
		all, err := accounts.List(context.Background(), port.AccountFilter{})
		if err != nil {
			t.Fatalf("list accounts: %v", err)
		}
		for _, a := range all {
			if err := p.Release(context.Background(), a.ID); err != nil {
				t.Fatalf("release %s: %v", a.ID, err)
			}
		}
		if got := len(fleet(t)); got != 0 {
			t.Fatalf("%d AUTO containers survived a full drain", got)
		}
	})
}

// TestPackScaleFiftyContainers is the P5-01 scale test. It packs a fleet far
// larger than one container can hold and asserts the two packing invariants
// (per-container account cap, one account per platform per container) hold
// across the whole fleet, then that AUTO containers are reaped once they drain.
//
// Two shapes cover the quota both as shipped and as documented:
//   - the PRD shape: 100 accounts over the 2 MVP platforms at quota 2 (the
//     "100 akun -> ~50 container" figure in ../../../../docs/PRD.md and the P5-01 ticket), and
//   - the all-platform shape: 350 accounts over all 7 platforms at quota 7,
//     which is the same ~50 containers once every platform is live.
func TestPackScaleFiftyContainers(t *testing.T) {
	cases := []packScaleCase{
		{
			name:           "mvp_2_platforms_quota_2",
			platforms:      domain.MVPPlatforms,
			maxPer:         2,
			wantContainers: 50,
		},
		{
			name:           "all_7_platforms_quota_7",
			platforms:      domain.AllPlatforms,
			maxPer:         7,
			wantContainers: 50,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runPackScale(t, tc)
		})
	}
}
