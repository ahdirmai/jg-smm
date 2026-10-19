package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
)

// P3 action scheduler tests. The scheduler is policy: which jobs survive the
// gates and reach a worker. Every store/gate is a scripted fake because the SQL
// and the Redis atomicity are proven in their own packages; what is proven here
// is the decision table.

// --- scripted fakes -------------------------------------------------------

type schedTargets struct {
	url string
	// comment is what GetComment returns; nil models "no comment resolved",
	// which must leave the job without an expected mention rather than fail it.
	comment *domain.Comment
	// commentID is what GetTarget reports as the target's linked comment. The
	// scheduler only looks the author up when it is set.
	commentID *string
}

func (s *schedTargets) GetTarget(_ context.Context, id string) (domain.Target, error) {
	return domain.Target{
		ID: id, URL: s.url, Platform: domain.PlatformInstagram,
		// commentID links the reply's target comment; the scheduler resolves the
		// author through it, so a test that wants a resolved handle must set it.
		CommentID: s.commentID,
	}, nil
}

// GetComment backs the reply-tagging path: the scheduler resolves a reply
// target's author so the published job can carry the handle the worker must see
// tagged. comment is injectable so a test can pin the resolved handle.
func (s *schedTargets) GetComment(_ context.Context, id string) (domain.Comment, error) {
	if s.comment == nil {
		return domain.Comment{}, domain.ErrNotFound
	}
	return *s.comment, nil
}

type schedAccounts struct {
	acc      domain.Account
	workerID string
}

func (a *schedAccounts) GetByID(_ context.Context, id string) (domain.Account, error) {
	if a.acc.ID == "" {
		a.acc.ID = id
	}
	if a.acc.Platform == "" {
		a.acc.Platform = domain.PlatformInstagram
	}
	if a.acc.WorkerID == nil {
		a.acc.WorkerID = &a.workerID
	}
	return a.acc, nil
}

type schedCooldown struct {
	allow bool
	err   error
	calls int
	// denyPace refuses only the per-account pacing slot (targetKey ==
	// accountPaceKey) while letting the per-target cooldown pass, so a test can
	// isolate Gate 3 from Gate 1.
	denyPace  bool
	paceCalls int
}

func (c *schedCooldown) Acquire(_ context.Context, accountID, targetKey string, window time.Duration) (bool, error) {
	c.calls++
	if targetKey == accountPaceKey {
		c.paceCalls++
		if c.denyPace {
			return false, nil
		}
	}
	if c.err != nil {
		return false, c.err
	}
	return c.allow, nil
}

type schedLimits struct {
	allow      bool
	retryAfter time.Duration
	err        error
	platforms  []string
}

func (l *schedLimits) Allow(_ context.Context, platform string, limit int, window time.Duration) (bool, time.Duration, error) {
	l.platforms = append(l.platforms, platform)
	if l.err != nil {
		return false, 0, l.err
	}
	return l.allow, l.retryAfter, nil
}

type schedTransport struct {
	jobs []workerActionJob
	err  error
}

func (t *schedTransport) Enqueue(_ context.Context, workerID string, job []byte) error {
	if t.err != nil {
		return t.err
	}
	var w workerActionJob
	if err := json.Unmarshal(job, &w); err != nil {
		return err
	}
	t.jobs = append(t.jobs, w)
	return nil
}
func (t *schedTransport) PublishControl(_ context.Context, workerID string, msg []byte) error {
	return nil
}

// schedComposer scripts the compose step (P3-03): it returns a fixed rendered
// text, or the error the test wants the loop to react to (ErrBannedPattern,
// ErrTemplatePoolEmpty). nil means "no composer wired" (local play).
type schedComposer struct {
	text   string
	err    error
	values map[string]string
}

func (c *schedComposer) Pick(_ context.Context, _ domain.Platform, _ string, values map[string]string) (PickOutcome, error) {
	if values != nil {
		c.values = values
	}
	if c.err != nil {
		return PickOutcome{}, c.err
	}
	return PickOutcome{TemplateID: "tmpl-1", RenderedText: c.text}, nil
}

// schedTemplates is a scripted port.TemplateStore for the scheduler's operator-
// text screen (M-1). Only the pool read is implemented: the scheduler never
// writes templates, and a screen test needs nothing else from the interface.
type schedTemplates struct {
	rows []domain.CommentTemplate
	err  error
}

func (s *schedTemplates) ListTemplates(_ context.Context, platform domain.Platform, _ bool, _, _ *int) ([]domain.CommentTemplate, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := make([]domain.CommentTemplate, 0, len(s.rows))
	for _, t := range s.rows {
		if t.Platform == platform {
			out = append(out, t)
		}
	}
	return out, nil
}

func (s *schedTemplates) CreateTemplate(_ context.Context, t domain.CommentTemplate) (domain.CommentTemplate, error) {
	return t, nil
}
func (s *schedTemplates) GetTemplate(_ context.Context, _ string) (domain.CommentTemplate, error) {
	return domain.CommentTemplate{}, domain.ErrNotFound
}
func (s *schedTemplates) UpdateTemplate(_ context.Context, t domain.CommentTemplate) (domain.CommentTemplate, error) {
	return t, nil
}
func (s *schedTemplates) DeleteTemplate(_ context.Context, _ string) error { return nil }
func (s *schedTemplates) PickForTarget(_ context.Context, _ domain.Platform, _ string) ([]domain.CommentTemplate, error) {
	return nil, nil
}

// newSched builds a scheduler over the scripted fakes with deterministic time.
// templates is the pool read the operator-text screen needs; nil means "no
// denylist source available", which composeText treats as "compose instead".
func newSched(t *testing.T, actions *fakeActionStore, cd *schedCooldown, lm *schedLimits, tr *schedTransport, composer narrowComposer) *ActionScheduler {
	t.Helper()
	now := time.Unix(2_000_000, 0).UTC()
	s := NewActionScheduler(
		actions,
		&schedAccounts{workerID: "worker-1"},
		&schedTargets{url: "https://instagram.com/p/x"},
		composer, cd, lm, tr, nil, nil,
		ActionSchedulerConfig{
			TickBudget: 5,
			Cooldown:   60 * time.Second,
			RateWindow: time.Hour,
			RateLimits: map[string]int{"instagram": 30, "threads": 15},
			Clock:      func() time.Time { return now },
		},
	)
	return s
}

// aDueAction inserts a claimable job directly in PENDING, due now.
func aDueAction(actions *fakeActionStore, id string) domain.ActionJob {
	return domain.ActionJob{
		ID:          id,
		Type:        domain.JobTypeActionComment,
		TargetID:    "tgt-1",
		AccountID:   "acct-1",
		WorkerID:    "worker-1",
		Status:      domain.JobStatusPending,
		ScheduledAt: time.Unix(1_000_000, 0).UTC(), // before the fixed clock
	}
}

// --- decision table -------------------------------------------------------

// The happy path: both gates pass and the job reaches the worker's queue with
// the wire shape the worker BLPOPs.
func TestActionSchedulerPublishes(t *testing.T) {
	store := newFakeActionStore()
	store.logByAttempt = map[string]domain.ActionLog{}
	cd := &schedCooldown{allow: true}
	lm := &schedLimits{allow: true}
	tr := &schedTransport{}
	s := newSched(t, store, cd, lm, tr, nil)

	// Seed a PENDING job the claim query will return.
	seedPending(store, "job-1")

	if err := s.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(tr.jobs) != 1 {
		t.Fatalf("one job must reach the transport, got %d", len(tr.jobs))
	}
	got := tr.jobs[0]
	if got.ID != "job-1" || got.Action != "comment" || got.Platform != "instagram" {
		t.Fatalf("wire shape mismatch: %+v", got)
	}
	if got.TargetURL != "https://instagram.com/p/x" {
		t.Fatalf("the target URL must be resolved, got %q", got.TargetURL)
	}
}

// Per-account pacing (Gate 3) refuses: a second action on the account inside
// the pace window is held back even though the per-target cooldown passed. This
// is the burst-velocity bound that keeps an account from machine-gunning a
// platform (the pattern that got a fresh account suspended).
func TestActionSchedulerAccountPaceBlocks(t *testing.T) {
	store := newFakeActionStore()
	store.logByAttempt = map[string]domain.ActionLog{}
	cd := &schedCooldown{allow: true, denyPace: true}
	lm := &schedLimits{allow: true}
	tr := &schedTransport{}
	now := time.Unix(2_000_000, 0).UTC()
	s := NewActionScheduler(
		store,
		&schedAccounts{workerID: "worker-1"},
		&schedTargets{url: "https://instagram.com/p/x"},
		nil, cd, lm, tr, nil, nil,
		ActionSchedulerConfig{
			TickBudget:  5,
			Cooldown:    60 * time.Second,
			AccountPace: 8 * time.Minute,
			RateWindow:  time.Hour,
			RateLimits:  map[string]int{"instagram": 30, "threads": 15},
			Clock:       func() time.Time { return now },
		},
	)
	seedPending(store, "job-pace")

	if err := s.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(tr.jobs) != 0 {
		t.Fatalf("a paced account must not publish, got %d jobs", len(tr.jobs))
	}
	if cd.paceCalls == 0 {
		t.Fatal("the pace gate must have been consulted")
	}
}

// With pacing enabled and the slot free, the job still publishes: the gate only
// blocks a burst, never a first action.
func TestActionSchedulerAccountPaceAllowsWhenFree(t *testing.T) {
	store := newFakeActionStore()
	store.logByAttempt = map[string]domain.ActionLog{}
	cd := &schedCooldown{allow: true, denyPace: false}
	lm := &schedLimits{allow: true}
	tr := &schedTransport{}
	now := time.Unix(2_000_000, 0).UTC()
	s := NewActionScheduler(
		store,
		&schedAccounts{workerID: "worker-1"},
		&schedTargets{url: "https://instagram.com/p/x"},
		nil, cd, lm, tr, nil, nil,
		ActionSchedulerConfig{
			TickBudget:  5,
			Cooldown:    60 * time.Second,
			AccountPace: 8 * time.Minute,
			RateWindow:  time.Hour,
			RateLimits:  map[string]int{"instagram": 30, "threads": 15},
			Clock:       func() time.Time { return now },
		},
	)
	seedPending(store, "job-pace-ok")

	if err := s.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(tr.jobs) != 1 {
		t.Fatalf("a free pace slot must publish, got %d jobs", len(tr.jobs))
	}
	if cd.paceCalls != 1 {
		t.Fatalf("the pace gate must be claimed exactly once, got %d", cd.paceCalls)
	}
}

// Cooldown refuses: the job goes back with a delay, never to the worker.
func TestActionSchedulerCooldownBlocks(t *testing.T) {
	store := newFakeActionStore()
	cd := &schedCooldown{allow: false}
	lm := &schedLimits{allow: true}
	tr := &schedTransport{}
	s := newSched(t, store, cd, lm, tr, nil)
	seedPending(store, "job-c")

	if err := s.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(tr.jobs) != 0 {
		t.Fatal("a cooled-down job must not be published")
	}
	if len(store.rescheduled) != 1 {
		t.Fatalf("a blocked job must be rescheduled, got %d", len(store.rescheduled))
	}
}

// A rate-limit refusal reschedules for the reported reset, not a fixed delay.
func TestActionSchedulerRateLimitBlocks(t *testing.T) {
	store := newFakeActionStore()
	cd := &schedCooldown{allow: true}
	lm := &schedLimits{allow: false, retryAfter: 30 * time.Minute}
	tr := &schedTransport{}
	s := newSched(t, store, cd, lm, tr, nil)
	seedPending(store, "job-r")
	base := s.clock()

	if err := s.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(tr.jobs) != 0 {
		t.Fatal("a rate-limited job must not be published")
	}
	if len(store.rescheduled) != 1 {
		t.Fatalf("expected one reschedule, got %d", len(store.rescheduled))
	}
	if got := store.rescheduled[0].at.Sub(base); got != 30*time.Minute {
		t.Fatalf("reschedule must wait for the reset, got %v", got)
	}
}

// A gate error is a requeue, not a drop: the job is too valuable to lose to a
// transient Redis blip.
func TestActionSchedulerGateErrorRequeues(t *testing.T) {
	store := newFakeActionStore()
	cd := &schedCooldown{err: errors.New("redis down")}
	lm := &schedLimits{allow: true}
	tr := &schedTransport{}
	s := newSched(t, store, cd, lm, tr, nil)
	seedPending(store, "job-e")

	if err := s.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(tr.jobs) != 0 {
		t.Fatal("a gate error must not publish")
	}
	if len(store.rescheduled) != 1 {
		t.Fatalf("a gate error must requeue, got %d", len(store.rescheduled))
	}
}

// No worker assigned: the job waits for the fleet, it is not failed.
func TestActionSchedulerNoWorkerRequeues(t *testing.T) {
	store := newFakeActionStore()
	cd := &schedCooldown{allow: true}
	lm := &schedLimits{allow: true}
	tr := &schedTransport{}
	s := newSched(t, store, cd, lm, tr, nil)
	s.accounts = &schedAccounts{workerID: "", acc: domain.Account{ID: "acct-1"}}
	seedPending(store, "job-nw")

	if err := s.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(tr.jobs) != 0 {
		t.Fatal("a job with no worker must not be published")
	}
	if len(store.rescheduled) != 1 {
		t.Fatalf("a workerless job must be requeued, got %d", len(store.rescheduled))
	}
}

// A transport failure after the gates passed must not lose the job.
func TestActionSchedulerTransportFailureRequeues(t *testing.T) {
	store := newFakeActionStore()
	cd := &schedCooldown{allow: true}
	lm := &schedLimits{allow: true}
	tr := &schedTransport{err: errors.New("connection refused")}
	s := newSched(t, store, cd, lm, tr, nil)
	seedPending(store, "job-t")

	if err := s.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(store.rescheduled) != 1 {
		t.Fatalf("an undelivered job must be requeued, got %d", len(store.rescheduled))
	}
}

// A target that cannot be resolved fails the job fast: there is nothing to
// retry that could fix a missing target.
func TestActionSchedulerMissingTargetFails(t *testing.T) {
	store := newFakeActionStore()
	cd := &schedCooldown{allow: true}
	lm := &schedLimits{allow: true}
	tr := &schedTransport{}
	s := newSched(t, store, cd, lm, tr, nil)
	// Seed a PENDING job the claim query will return.
	seedPending(store, "job-mt")
	// A target store that always misses.
	s.scrapes = missingTargets{}

	if err := s.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(tr.jobs) != 0 {
		t.Fatal("a job with no target must not be published")
	}
	if len(store.completed) != 1 || store.completed[0].status != domain.JobStatusFailed {
		t.Fatalf("a missing target must fail the job, got %+v", store.completed)
	}
}

// Nothing due anywhere: an empty tick is success, not an error.
func TestActionSchedulerEmptyTick(t *testing.T) {
	store := newFakeActionStore()
	s := newSched(t, store, &schedCooldown{allow: true}, &schedLimits{allow: true}, &schedTransport{}, nil)

	if err := s.Tick(context.Background()); err != nil {
		t.Fatalf("an empty tick must not error, got %v", err)
	}
}

// A future job (inside its backoff window) is not claimed.
func TestActionSchedulerSkipsFutureJob(t *testing.T) {
	store := newFakeActionStore()
	cd := &schedCooldown{allow: true}
	lm := &schedLimits{allow: true}
	tr := &schedTransport{}
	s := newSched(t, store, cd, lm, tr, nil)
	// A job scheduled AFTER the fixed clock — not due.
	store.pending = append(store.pending, domain.ActionJob{
		ID:          "job-future",
		Type:        domain.JobTypeActionComment,
		AccountID:   "acct-1",
		Status:      domain.JobStatusPending,
		ScheduledAt: time.Unix(3_000_000, 0).UTC(),
	})

	if err := s.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(tr.jobs) != 0 {
		t.Fatal("a future job must not be published")
	}
}

// actionName maps the queue's enum to the worker's dispatch verb.
func TestActionName(t *testing.T) {
	if actionName(domain.JobTypeActionComment) != "comment" {
		t.Fatal("comment must map to the worker's comment verb")
	}
	if actionName(domain.JobTypeActionLike) != "like" {
		t.Fatal("like must map to the worker's like verb")
	}
}

// --- helpers --------------------------------------------------------------

type missingTargets struct{}

func (missingTargets) GetTarget(_ context.Context, id string) (domain.Target, error) {
	return domain.Target{}, domain.ErrNotFound
}

func (missingTargets) GetComment(_ context.Context, id string) (domain.Comment, error) {
	return domain.Comment{}, domain.ErrNotFound
}

// seedPending puts a due job in the fake action store's pending list, which the
// scheduler's ListActionJobsByStatus returns.
func seedPending(store *fakeActionStore, id string) {
	store.pending = append(store.pending, aDueAction(store, id))
}

// --- reply tag resolution -------------------------------------------------

// A reply job must carry the TARGET COMMENT's author, because the worker checks
// IG's pre-filled mention names that person. A top-level composer pre-fills the
// POST author, so a bare "@" check on the worker side would accept the wrong
// tag; the handle has to come from the BE.
func TestActionSchedulerSendsReplyTargetHandle(t *testing.T) {
	store := newFakeActionStore()
	store.logByAttempt = map[string]domain.ActionLog{}
	tr := &schedTransport{}
	composer := &schedComposer{text: "poin bagus"}

	now := time.Unix(2_000_000, 0).UTC()
	commentID := "cmt-1"
	targets := &schedTargets{
		url:       "https://instagram.com/p/x/c/9",
		commentID: &commentID,
		comment: &domain.Comment{
			ID: commentID, AuthorHandle: "fathoni_saputra", Text: "Roadmap penting tapi execution...",
		},
	}
	s := NewActionScheduler(store, &schedAccounts{workerID: "worker-1"}, targets, composer,
		&schedCooldown{allow: true}, &schedLimits{allow: true}, tr, nil, nil,
		ActionSchedulerConfig{
			TickBudget: 5, Cooldown: 60 * time.Second, RateWindow: time.Hour,
			RateLimits: map[string]int{"instagram": 30}, Clock: func() time.Time { return now },
		},
	)

	job := aDueAction(store, "job-reply")
	job.Type = domain.JobTypeActionReplyComment
	store.pending = append(store.pending, job)

	if err := s.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(tr.jobs) != 1 {
		t.Fatalf("one job must reach the transport, got %d", len(tr.jobs))
	}
	if got := tr.jobs[0].ExpectedMention; got != "fathoni_saputra" {
		t.Fatalf("payload expectedMention = %q, want the comment author (fathoni_saputra) — "+
			"without it the worker cannot tell the right tag from the post author's", got)
	}
}

// A comment job (top-level) has no author to tag: the field must stay empty so
// the worker does not run a reply-only check on a plain comment.
func TestActionSchedulerOmitsMentionForPlainComment(t *testing.T) {
	store := newFakeActionStore()
	store.logByAttempt = map[string]domain.ActionLog{}
	tr := &schedTransport{}
	s := newSched(t, store, &schedCooldown{allow: true}, &schedLimits{allow: true}, tr,
		&schedComposer{text: "nice"})

	seedPending(store, "job-comment")

	if err := s.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(tr.jobs) != 1 {
		t.Fatalf("one job must reach the transport, got %d", len(tr.jobs))
	}
	if tr.jobs[0].ExpectedMention != "" {
		t.Fatalf("a top-level comment must not carry an expected mention, got %q", tr.jobs[0].ExpectedMention)
	}
}

// An unresolvable comment must not block the reply: the job still ships, with no
// expectation, and the worker falls back to its presence-only check. Failing
// here would make a transient lookup error a lost reply.
func TestActionSchedulerReplyShipsWithoutResolvableComment(t *testing.T) {
	store := newFakeActionStore()
	store.logByAttempt = map[string]domain.ActionLog{}
	tr := &schedTransport{}
	composer := &schedComposer{text: "poin bagus"}

	now := time.Unix(2_000_000, 0).UTC()
	// comment is nil → GetComment reports not-found.
	linked := "cmt-missing"
	targets := &schedTargets{url: "https://instagram.com/p/x/c/9", commentID: &linked}
	s := NewActionScheduler(store, &schedAccounts{workerID: "worker-1"}, targets, composer,
		&schedCooldown{allow: true}, &schedLimits{allow: true}, tr, nil, nil,
		ActionSchedulerConfig{
			TickBudget: 5, Cooldown: 60 * time.Second, RateWindow: time.Hour,
			RateLimits: map[string]int{"instagram": 30}, Clock: func() time.Time { return now },
		},
	)

	job := aDueAction(store, "job-reply-nocmt")
	job.Type = domain.JobTypeActionReplyComment
	store.pending = append(store.pending, job)

	if err := s.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(tr.jobs) != 1 {
		t.Fatalf("an unresolvable comment must not block the reply, got %d jobs", len(tr.jobs))
	}
	if tr.jobs[0].ExpectedMention != "" {
		t.Fatalf("with no comment resolved the mention must be empty, got %q", tr.jobs[0].ExpectedMention)
	}
}

// --- P3-03 compose + denylist gate ---------------------------------------

// A comment job ships the composed, screened text; the worker never composes.
func TestActionSchedulerComposesText(t *testing.T) {
	store := newFakeActionStore()
	store.logByAttempt = map[string]domain.ActionLog{}
	tr := &schedTransport{}
	composer := &schedComposer{text: "nice post {topic}", values: nil}
	s := newSched(t, store, &schedCooldown{allow: true}, &schedLimits{allow: true}, tr, composer)

	seedPending(store, "job-text")

	if err := s.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(tr.jobs) != 1 {
		t.Fatalf("one job must reach the transport, got %d", len(tr.jobs))
	}
	if tr.jobs[0].Text != "nice post {topic}" {
		t.Fatalf("the composed text must ride the payload, got %q", tr.jobs[0].Text)
	}
	// Nothing was failed or rescheduled: a clean comment simply ships.
	if len(store.completed) != 0 || len(store.rescheduled) != 0 {
		t.Fatalf("a clean comment must not be failed or rescheduled, got completed=%v rescheduled=%v", store.completed, store.rescheduled)
	}
}

// A like job needs no composer: no text, and no dependency on the template pool.
func TestActionSchedulerLikeSkipsComposer(t *testing.T) {
	store := newFakeActionStore()
	store.logByAttempt = map[string]domain.ActionLog{}
	tr := &schedTransport{}
	s := newSched(t, store, &schedCooldown{allow: true}, &schedLimits{allow: true}, tr,
		&schedComposer{err: domain.ErrBannedPattern}) // must NOT be consulted
	seedPending(store, "job-like")
	store.pending[0].Type = domain.JobTypeActionLike

	if err := s.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(tr.jobs) != 1 {
		t.Fatalf("the like must still publish, got %d", len(tr.jobs))
	}
	if tr.jobs[0].Text != "" {
		t.Fatalf("a like carries no text, got %q", tr.jobs[0].Text)
	}
}

// The denylist fires before the queue: a banned comment is failed terminally,
// never published, and never rescheduled — and it spends no cooldown or rate
// budget on the way out (P3-03 AC: "komentar banned ditolak sebelum queue").
func TestActionSchedulerBannedCommentFailsBeforeQueue(t *testing.T) {
	store := newFakeActionStore()
	store.logByAttempt = map[string]domain.ActionLog{}
	cd := &schedCooldown{allow: true}
	lm := &schedLimits{allow: true}
	tr := &schedTransport{}
	s := newSched(t, store, cd, lm, tr, &schedComposer{err: domain.ErrBannedPattern})

	seedPending(store, "job-banned")

	if err := s.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(tr.jobs) != 0 {
		t.Fatalf("a banned comment must never reach a worker queue, got %d", len(tr.jobs))
	}
	if cd.calls != 0 {
		t.Fatalf("a banned comment must not spend a cooldown slot, got %d calls", cd.calls)
	}
	if len(lm.platforms) != 0 {
		t.Fatalf("a banned comment must not spend rate budget, got %v", lm.platforms)
	}
	if len(store.completed) != 1 || store.completed[0].status != domain.JobStatusFailed {
		t.Fatalf("the job must be failed, got %v", store.completed)
	}
	if store.completed[0].err == nil || !strings.Contains(*store.completed[0].err, "banned") {
		t.Fatalf("the failure must name the denylist, got %v", store.completed[0].err)
	}
	if len(store.rescheduled) != 0 {
		t.Fatalf("a banned comment must not be rescheduled (it is not retryable), got %v", store.rescheduled)
	}
}

// An empty pool is a terminal failure, not a reschedule loop: re-running the
// compose would find the same empty pool, so spinning would only hide it.
func TestActionSchedulerEmptyPoolFailsNotLoops(t *testing.T) {
	store := newFakeActionStore()
	store.logByAttempt = map[string]domain.ActionLog{}
	tr := &schedTransport{}
	s := newSched(t, store, &schedCooldown{allow: true}, &schedLimits{allow: true}, tr,
		&schedComposer{err: domain.ErrTemplatePoolEmpty})

	seedPending(store, "job-empty")

	if err := s.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(tr.jobs) != 0 {
		t.Fatalf("no comment may ship from an empty pool, got %d", len(tr.jobs))
	}
	if len(store.completed) != 1 || store.completed[0].status != domain.JobStatusFailed {
		t.Fatalf("the job must be failed, got %v", store.completed)
	}
	if len(store.rescheduled) != 0 {
		t.Fatalf("an empty pool must not loop forever, got %v", store.rescheduled)
	}
}

// --- M-1: the operator-supplied body is screened too -----------------------

// A per-account body supplied through POST /api/actions is held to the same
// denylist a composed one is. The screen used to live only inside the template
// pick, so the operator branch returned the stored body verbatim: the same word
// that was rejected inside a template was published and posted live when typed
// by an operator.
func TestActionSchedulerScreensOperatorSuppliedText(t *testing.T) {
	store := newFakeActionStore()
	store.logByAttempt = map[string]domain.ActionLog{}
	cd := &schedCooldown{allow: true}
	lm := &schedLimits{allow: true}
	tr := &schedTransport{}
	texts := newFakeActionText()

	s := newSched(t, store, cd, lm, tr, nil)
	s.texts = texts
	// The pool declares what may not be said; an operator body has no variant of
	// its own to carry a denylist, so every declared pattern is enforced on it.
	s.templates = &schedTemplates{rows: []domain.CommentTemplate{
		{ID: "tmpl-a", Platform: domain.PlatformInstagram, BannedWords: []string{`cheap\s+followers`}},
	}}

	seedPending(store, "job-op-banned")
	texts.byJob["job-op-banned"] = "buy cheap followers now"

	if err := s.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(tr.jobs) != 0 {
		t.Fatalf("an operator body on the denylist must never reach a worker queue, got %d", len(tr.jobs))
	}
	if len(store.completed) != 1 || store.completed[0].status != domain.JobStatusFailed {
		t.Fatalf("the job must be failed terminally, got %v", store.completed)
	}
	if store.completed[0].err == nil || !strings.Contains(*store.completed[0].err, "banned") {
		t.Fatalf("the failure must name the denylist, got %v", store.completed[0].err)
	}
	if cd.calls != 0 || len(lm.platforms) != 0 {
		t.Fatalf("a banned body must spend no budget, got cooldown=%d rate=%v", cd.calls, lm.platforms)
	}
}

// The same body with a clean screen still ships: the fix rejects the word, not
// the operator flow.
func TestActionSchedulerShipsCleanOperatorSuppliedText(t *testing.T) {
	store := newFakeActionStore()
	store.logByAttempt = map[string]domain.ActionLog{}
	tr := &schedTransport{}
	texts := newFakeActionText()

	s := newSched(t, store, &schedCooldown{allow: true}, &schedLimits{allow: true}, tr, nil)
	s.texts = texts
	s.templates = &schedTemplates{rows: []domain.CommentTemplate{
		{ID: "tmpl-a", Platform: domain.PlatformInstagram, BannedWords: []string{"followers"}},
	}}
	seedPending(store, "job-op-clean")
	texts.byJob["job-op-clean"] = "what a shot"

	if err := s.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(tr.jobs) != 1 {
		t.Fatalf("a clean operator body must publish, got %d", len(tr.jobs))
	}
	if tr.jobs[0].Text != "what a shot" {
		t.Fatalf("the operator body must ride the payload verbatim, got %q", tr.jobs[0].Text)
	}
	// No variant produced it, so nothing may claim one.
	if tr.jobs[0].TemplateID != "" {
		t.Fatalf("an operator body must not claim a template id, got %q", tr.jobs[0].TemplateID)
	}
}

// With no pool read wired the screen has no rules to apply. Shipping the body
// anyway would silently re-open the hole, so the scheduler composes instead.
func TestActionSchedulerOperatorTextWithoutTemplateStoreComposes(t *testing.T) {
	store := newFakeActionStore()
	store.logByAttempt = map[string]domain.ActionLog{}
	tr := &schedTransport{}
	texts := newFakeActionText()

	s := newSched(t, store, &schedCooldown{allow: true}, &schedLimits{allow: true}, tr,
		&schedComposer{text: "composed instead"})
	s.texts = texts
	seedPending(store, "job-op-nostore")
	texts.byJob["job-op-nostore"] = "an unscreened operator body"

	if err := s.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(tr.jobs) != 1 {
		t.Fatalf("the job must still ship, got %d", len(tr.jobs))
	}
	if tr.jobs[0].Text != "composed instead" {
		t.Fatalf("with no denylist source the scheduler must compose, got %q", tr.jobs[0].Text)
	}
}

// --- M-2: the template id reaches the job, the payload and the log ---------

// The variant chosen for a comment rides the payload so the worker can echo it
// back, and is written onto the job row. action_log.template_id is the only
// thing the 7-day per-target dedupe joins on, and it was never populated.
func TestActionSchedulerCarriesTemplateID(t *testing.T) {
	store := newFakeActionStore()
	store.logByAttempt = map[string]domain.ActionLog{}
	tr := &schedTransport{}
	s := newSched(t, store, &schedCooldown{allow: true}, &schedLimits{allow: true}, tr,
		&schedComposer{text: "nice post"})

	seedPending(store, "job-tmpl")

	if err := s.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(tr.jobs) != 1 {
		t.Fatalf("one job must reach the transport, got %d", len(tr.jobs))
	}
	if tr.jobs[0].TemplateID != "tmpl-1" {
		t.Fatalf("the payload must carry the chosen template id, got %q", tr.jobs[0].TemplateID)
	}
	// The job row is the queue-side half of the record: the log row is written
	// by the callback, and the callback can only echo what it was sent.
	var found bool
	for _, j := range store.pending {
		if j.ID == "job-tmpl" {
			found = true
			if j.TemplateID != "tmpl-1" {
				t.Fatalf("the job row must record the template id, got %q", j.TemplateID)
			}
		}
	}
	if !found {
		t.Fatal("job row disappeared")
	}
}

// A like composes nothing, so it must not carry or claim a template id.
func TestActionSchedulerLikeCarriesNoTemplateID(t *testing.T) {
	store := newFakeActionStore()
	store.logByAttempt = map[string]domain.ActionLog{}
	tr := &schedTransport{}
	s := newSched(t, store, &schedCooldown{allow: true}, &schedLimits{allow: true}, tr,
		&schedComposer{text: "irrelevant"})
	seedPending(store, "job-like-tmpl")
	store.pending[0].Type = domain.JobTypeActionLike

	if err := s.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(tr.jobs) != 1 || tr.jobs[0].TemplateID != "" {
		t.Fatalf("a like must carry no template id, got %+v", tr.jobs)
	}
}
