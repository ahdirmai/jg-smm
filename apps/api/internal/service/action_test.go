package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
)

// ActionService tests. What is proven here is the enqueue contract — the floor
// on what a queue page is allowed to submit — and that the list read pairs a
// job with its latest verdict. The SQL is proven by the repository tests; the
// store here is a scripted fake over the shared one.

// actionStore overrides only the queue read/write slices of the shared fake:
// enqueue needs deterministic IDs and list needs status-filtered reads. The
// rest of port.ActionStore is inherited.
type actionStore struct {
	*fakeActionStore
	createErr error
	listErr   error
	seq       atomic.Int64
}

func newActionStore() *actionStore {
	return &actionStore{fakeActionStore: newFakeActionStore()}
}

func (s *actionStore) CreateActionJob(_ context.Context, j domain.ActionJob) (domain.ActionJob, error) {
	if s.createErr != nil {
		return domain.ActionJob{}, s.createErr
	}
	// Distinct IDs: the list test pairs a job to its log by ID, so a batch of
	// same-account jobs must not collide.
	j.ID = "job-" + j.AccountID + "-" + itoa(int(s.seq.Add(1)))
	s.pending = append(s.pending, j)
	return j, nil
}

func (s *actionStore) ListActionJobs(_ context.Context, limit *int, _ *int) ([]domain.ActionJob, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.page(s.pending, limit), nil
}

func (s *actionStore) ListActionJobsByStatus(_ context.Context, st domain.JobStatus, limit *int, _ *int) ([]domain.ActionJob, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	out := make([]domain.ActionJob, 0, len(s.pending))
	for _, j := range s.pending {
		if j.Status == st {
			out = append(out, j)
		}
	}
	return s.page(out, limit), nil
}

// page trims a slice to limit, mirroring the SQL LIMIT the service passes.
func (s *actionStore) page(rows []domain.ActionJob, limit *int) []domain.ActionJob {
	if limit == nil || *limit >= len(rows) {
		return rows
	}
	return rows[:*limit]
}

type actionAccounts struct {
	acc    domain.Account
	getErr error
}

func (a *actionAccounts) GetByID(_ context.Context, id string) (domain.Account, error) {
	if a.getErr != nil {
		return domain.Account{}, a.getErr
	}
	a.acc.ID = id
	if a.acc.WorkerID == nil {
		w := "worker-1"
		a.acc.WorkerID = &w
	}
	return a.acc, nil
}

type actionTargets struct {
	ups targetUpsert
}

type targetUpsert = struct {
	id  string
	err error
}

func (t *actionTargets) UpsertTarget(_ context.Context, tg domain.Target) (domain.Target, error) {
	if t.ups.err != nil {
		return domain.Target{}, t.ups.err
	}
	tg.ID = t.ups.id
	return tg, nil
}

func newActionService(store *actionStore, accs *actionAccounts, tgts *actionTargets) *ActionService {
	return NewActionService(store, accs, tgts, fixedClock{}, nil)
}

// itoa keeps the test free of strconv import noise.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func baseItem() ActionItem {
	return ActionItem{AccountID: "acc-1", TargetURL: "https://www.instagram.com/p/abc/", Type: domain.JobTypeActionLike}
}

// --- enqueue: the happy path -------------------------------------------------

func TestActionEnqueueCreatesPendingJobs(t *testing.T) {
	store := newActionStore()
	accs := &actionAccounts{acc: domain.Account{Platform: domain.PlatformInstagram, Status: domain.AccountActive}}
	svc := newActionService(store, accs, &actionTargets{ups: targetUpsert{id: "tgt-1"}})

	jobs, err := svc.Enqueue(t.Context(), []ActionItem{
		{AccountID: "acc-1", TargetURL: "https://www.instagram.com/p/abc/", Type: domain.JobTypeActionLike},
		{AccountID: "acc-1", TargetURL: "https://www.threads.net/p/def/", Type: domain.JobTypeActionComment},
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("want 2 jobs, got %d", len(jobs))
	}
	if len(store.pending) != len(jobs) {
		t.Fatalf("store has %d rows, want %d", len(store.pending), len(jobs))
	}
	for _, j := range jobs {
		if j.Status != domain.JobStatusPending {
			t.Errorf("job %s status = %s, want pending", j.ID, j.Status)
		}
		if j.TargetID != "tgt-1" {
			t.Errorf("job %s target = %s, want tgt-1", j.ID, j.TargetID)
		}
		// The account binds the job to its worker so the scheduler can route it.
		if j.WorkerID != "worker-1" {
			t.Errorf("job %s worker = %q, want worker-1", j.ID, j.WorkerID)
		}
	}
	if jobs[0].ID == jobs[1].ID {
		t.Error("two jobs share an ID; list pairing would be ambiguous")
	}
}

// --- enqueue: rejection is all-or-nothing -----------------------------------

func TestActionEnqueueRejectsBatchOverCap(t *testing.T) {
	store := newActionStore()
	svc := newActionService(store, &actionAccounts{}, &actionTargets{})

	tooMany := make([]ActionItem, MaxActionBatch+1)
	for i := range tooMany {
		tooMany[i] = baseItem()
	}
	if _, err := svc.Enqueue(t.Context(), tooMany); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("over-cap error = %v, want ErrValidation", err)
	}
	if got := len(store.pending); got != 0 {
		t.Errorf("wrote %d jobs for a rejected batch, want 0", got)
	}
}

func TestActionEnqueueRejectsEmpty(t *testing.T) {
	store := newActionStore()
	svc := newActionService(store, &actionAccounts{}, &actionTargets{})
	if _, err := svc.Enqueue(t.Context(), nil); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("empty error = %v, want ErrValidation", err)
	}
	if got := len(store.pending); got != 0 {
		t.Errorf("wrote %d jobs for an empty batch, want 0", got)
	}
}

func TestActionEnqueueRejectsNonActionType(t *testing.T) {
	store := newActionStore()
	svc := newActionService(store, &actionAccounts{}, &actionTargets{})
	item := baseItem()
	item.Type = domain.JobTypeScrapeMetric
	if _, err := svc.Enqueue(t.Context(), []ActionItem{item}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("scrape type error = %v, want ErrValidation", err)
	}
}

// --- enqueue: the SSRF floor -------------------------------------------------

func TestActionEnqueueRejectsBadSchemes(t *testing.T) {
	store := newActionStore()
	svc := newActionService(store, &actionAccounts{}, &actionTargets{})
	for _, raw := range []string{
		"file:///etc/passwd",
		"ftp://example.com/x",
		"javascript:alert(1)",
		"  ",
		"/no/host",
	} {
		item := baseItem()
		item.TargetURL = raw
		if _, err := svc.Enqueue(t.Context(), []ActionItem{item}); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("target %q: error = %v, want ErrValidation", raw, err)
		}
	}
}

func TestActionEnqueueRejectsUnusableAccount(t *testing.T) {
	store := newActionStore()
	accs := &actionAccounts{acc: domain.Account{Platform: domain.PlatformInstagram, Status: domain.AccountDead}}
	svc := newActionService(store, accs, &actionTargets{ups: targetUpsert{id: "tgt-1"}})

	if _, err := svc.Enqueue(t.Context(), []ActionItem{baseItem()}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("dead account error = %v, want ErrValidation", err)
	}
	if len(store.pending) != 0 {
		t.Errorf("wrote %d jobs for a dead account, want 0", len(store.pending))
	}
}

func TestActionEnqueueWrapsLookupFailures(t *testing.T) {
	svc := newActionService(newActionStore(), &actionAccounts{getErr: domain.ErrNotFound}, &actionTargets{})
	// A store error must not leak the driver sentinel unwrapped.
	if _, err := svc.Enqueue(t.Context(), []ActionItem{baseItem()}); err == nil {
		t.Fatal("want error for missing account")
	}
}

// --- list: verdict pairing ---------------------------------------------------

func TestActionListPairsLatestVerdict(t *testing.T) {
	store := newActionStore()
	accs := &actionAccounts{acc: domain.Account{Platform: domain.PlatformInstagram, Status: domain.AccountActive}}
	svc := newActionService(store, accs, &actionTargets{ups: targetUpsert{id: "tgt-1"}})

	jobs, err := svc.Enqueue(t.Context(), []ActionItem{
		{AccountID: "acc-1", TargetURL: "https://www.instagram.com/p/abc/", Type: domain.JobTypeActionComment},
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	store.pending[0].Status = domain.JobStatusSuccess
	store.logs = []domain.ActionLog{{
		ActionJobID:  jobs[0].ID,
		RenderedText: "nice shot!",
	}}

	views, err := svc.List(t.Context(), nil, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("want 1 view, got %d", len(views))
	}
	if views[0].RenderedText != "nice shot!" {
		t.Errorf("rendered text = %q, want %q", views[0].RenderedText, "nice shot!")
	}
	if views[0].Job.Status != domain.JobStatusSuccess {
		t.Errorf("status = %s, want success", views[0].Job.Status)
	}
}

func TestActionListFilterByStatus(t *testing.T) {
	store := newActionStore()
	accs := &actionAccounts{acc: domain.Account{Platform: domain.PlatformInstagram, Status: domain.AccountActive}}
	svc := newActionService(store, accs, &actionTargets{ups: targetUpsert{id: "tgt-1"}})

	if _, err := svc.Enqueue(t.Context(), []ActionItem{
		{AccountID: "acc-1", TargetURL: "https://www.instagram.com/p/abc/", Type: domain.JobTypeActionLike},
		{AccountID: "acc-2", TargetURL: "https://www.instagram.com/p/xyz/", Type: domain.JobTypeActionLike},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	store.pending[1].Status = domain.JobStatusSuccess

	if views, err := svc.List(t.Context(), nil, 0); err != nil || len(views) != 2 {
		t.Fatalf("unfiltered list = %d, err %v; want 2", len(views), err)
	}

	success := domain.JobStatusSuccess
	got, err := svc.List(t.Context(), &success, 0)
	if err != nil {
		t.Fatalf("list filtered: %v", err)
	}
	if len(got) != 1 || got[0].Job.Status != domain.JobStatusSuccess {
		t.Fatalf("filter returned %d rows: %+v", len(got), got)
	}
}

func TestActionListEmpty(t *testing.T) {
	svc := newActionService(newActionStore(), &actionAccounts{}, &actionTargets{})
	views, err := svc.List(t.Context(), nil, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if views != nil {
		t.Errorf("want nil views for empty queue, got %d", len(views))
	}
}
