package repository

import (
	"context"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/repository/sqlcgen"
	"github.com/jackc/pgx/v5/pgxpool"
)

// P3-02 template repository tests. Real Postgres (SMM_TEST_DB=1) because the
// interesting behaviour is the dedupe query — a temporal join a fake cannot
// exercise — plus the schema guards (weight > 0, non-blank text).

func newTemplateTestRepo(t *testing.T) (*TemplateRepo, *ActionRepo) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping db integration test in -short mode")
	}
	dsn := testDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	q := sqlcgen.New(pool)
	if _, err := pool.Exec(context.Background(), `
		TRUNCATE TABLE
		  action_log, action_job, comment_template,
		  scrape_job, target, comment, post, raw_payload, apify_run,
		  official_account, analytics_snapshot, analytics_mention,
		  analytics_ingest_run, provision_log, worker, account
		CASCADE`); err != nil {
		t.Fatalf("truncate template tables: %v", err)
	}
	return NewTemplateRepo(q), NewActionRepo(q)
}

func TestTemplateCRUD(t *testing.T) {
	tplRepo, _ := newTemplateTestRepo(t)
	ctx := context.Background()

	created, err := tplRepo.CreateTemplate(ctx, domain.CommentTemplate{
		Platform: domain.PlatformInstagram,
		Text:     "loving the {topic} energy!",
		Vars:     []string{"topic"},
		Weight:   3,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == "" || !created.IsActive {
		t.Fatalf("new template must get an id and default to active, got %+v", created)
	}
	if created.Weight != 3 {
		t.Fatalf("weight must round-trip, got %d", created.Weight)
	}

	got, err := tplRepo.GetTemplate(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Text != "loving the {topic} energy!" || len(got.Vars) != 1 || got.Vars[0] != "topic" {
		t.Fatalf("round trip mismatch: %+v", got)
	}

	// Update flips it inactive: it must leave the pool but stay readable.
	updated, err := tplRepo.UpdateTemplate(ctx, domain.CommentTemplate{
		ID:       created.ID,
		Platform: domain.PlatformInstagram,
		Text:     "paused variant {topic}",
		Vars:     []string{"topic"},
		Weight:   1,
		IsActive: false,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.IsActive || updated.Text != "paused variant {topic}" {
		t.Fatalf("update must flip active and rewrite text, got %+v", updated)
	}

	// Active-only listing excludes the paused variant; the includeInactive
	// toggle is the dashboard's "show paused" switch.
	active, err := tplRepo.ListTemplates(ctx, domain.PlatformInstagram, false, p3PtrInt(10), p3PtrInt(0))
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("paused template must not be in the active pool, got %d", len(active))
	}
	both, err := tplRepo.ListTemplates(ctx, domain.PlatformInstagram, true, p3PtrInt(10), p3PtrInt(0))
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(both) != 1 || both[0].IsActive {
		t.Fatalf("includeInactive must surface the paused variant, got %d active=%v", len(both), both[0].IsActive)
	}

	// Another platform's pool is separate: a template is platform-scoped.
	other, err := tplRepo.ListTemplates(ctx, domain.PlatformThreads, true, p3PtrInt(10), p3PtrInt(0))
	if err != nil {
		t.Fatalf("list threads: %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("threads pool must be empty, got %d", len(other))
	}

	if err := tplRepo.DeleteTemplate(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := tplRepo.GetTemplate(ctx, created.ID); err != domain.ErrNotFound {
		t.Fatalf("deleted template must be gone, got %v", err)
	}
}

// TestTemplatePickDedupe is the pool guarantee: a template already used against
// a target inside 7 days must not be picked again for that target, while
// staying available for a different target.
func TestTemplatePickDedupe(t *testing.T) {
	tplRepo, actionRepo := newTemplateTestRepo(t)
	ctx := context.Background()
	pool := testPool(t)

	accountID := p2SeedAccount(t, pool, "act-tpl-"+p2Suffix(t))
	targetA := p3SeedTarget(t, pool, "tgt-tpl-a-"+p2Suffix(t))
	targetB := p3SeedTarget(t, pool, "tgt-tpl-b-"+p2Suffix(t))
	workerID := p3SeedWorker(t, pool, "wrk-tpl-"+p2Suffix(t))

	used, err := tplRepo.CreateTemplate(ctx, domain.CommentTemplate{
		Platform: domain.PlatformInstagram,
		Text:     "used variant {topic}",
		Vars:     []string{"topic"},
		Weight:   1,
	})
	if err != nil {
		t.Fatalf("create used: %v", err)
	}
	fresh, err := tplRepo.CreateTemplate(ctx, domain.CommentTemplate{
		Platform: domain.PlatformInstagram,
		Text:     "fresh variant {topic}",
		Vars:     []string{"topic"},
		Weight:   1,
	})
	if err != nil {
		t.Fatalf("create fresh: %v", err)
	}

	// Execute one comment with `used` against target A: a job + an action_log
	// linking the template is what dedupe reads.
	job, err := actionRepo.CreateActionJob(ctx, domain.ActionJob{
		Type:        domain.JobTypeActionComment,
		TargetID:    targetA,
		AccountID:   accountID,
		TemplateID:  used.ID,
		ScheduledAt: time.Now().UTC().Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	claimed, err := actionRepo.ClaimNextActionJob(ctx, accountID, workerID)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := actionRepo.UpsertActionLog(ctx, domain.ActionLog{
		ActionJobID:  claimed.ID,
		Attempt:      claimed.Attempts,
		Status:       domain.AttemptSuccess,
		WorkerID:     workerID,
		TemplateID:   used.ID,
		RenderedText: "used variant launch",
	}); err != nil {
		t.Fatalf("upsert log: %v", err)
	}
	_ = job

	// Pool for target A: `used` is excluded, `fresh` remains.
	pickA, err := tplRepo.PickForTarget(ctx, domain.PlatformInstagram, targetA)
	if err != nil {
		t.Fatalf("pick for A: %v", err)
	}
	if len(pickA) != 1 || pickA[0].ID != fresh.ID {
		t.Fatalf("dedupe must exclude the used template for target A, got %d", len(pickA))
	}

	// Pool for target B is untouched: both are still fresh there.
	pickB, err := tplRepo.PickForTarget(ctx, domain.PlatformInstagram, targetB)
	if err != nil {
		t.Fatalf("pick for B: %v", err)
	}
	if len(pickB) != 2 {
		t.Fatalf("target B must see the full pool, got %d", len(pickB))
	}
}

// TestTemplateSchemaGuards checks the column constraints the composer leans on
// before a DB round trip: weight must be positive and text non-blank.
func TestTemplateSchemaGuards(t *testing.T) {
	tplRepo, _ := newTemplateTestRepo(t)
	ctx := context.Background()

	if _, err := tplRepo.CreateTemplate(ctx, domain.CommentTemplate{
		Platform: domain.PlatformInstagram,
		Text:     "   ",
	}); err == nil {
		t.Fatal("expected error for blank text, got nil")
	}
	// Weight must be positive: an even pool is weight 1, and weight 0 would
	// mean "never pick this", which is what is_active already expresses.
	if _, err := tplRepo.CreateTemplate(ctx, domain.CommentTemplate{
		Platform: domain.PlatformInstagram,
		Text:     "zero weight",
	}); err == nil {
		t.Fatal("expected error for zero weight, got nil")
	}
	// A template with no vars and no placeholders is legal (a fixed comment).
	fixed, err := tplRepo.CreateTemplate(ctx, domain.CommentTemplate{
		Platform: domain.PlatformInstagram,
		Text:     "fixed comment",
		Weight:   1,
	})
	if err != nil {
		t.Fatalf("create fixed: %v", err)
	}
	if !fixed.IsActive {
		t.Fatal("a new template must be active by default")
	}
	// A declared var the text never references is a latent typo and is rejected
	// before a worker can post a raw {placeholder}.
	if _, err := tplRepo.CreateTemplate(ctx, domain.CommentTemplate{
		Platform: domain.PlatformInstagram,
		Text:     "no placeholder here",
		Vars:     []string{"topic"},
		Weight:   1,
	}); err == nil {
		t.Fatal("expected error for declared-but-unused var, got nil")
	}
}

// TestTemplateDedupeSeesDispatchRecordedVariant is the M-2 acceptance at the DB
// layer: the variant is recorded the way the scheduler records it — a job row
// whose template_id is written at dispatch (SetActionJobTemplate), then an
// action_log row carrying the id the worker echoed back — and a second
// PickForTarget for the same target must then treat it as used.
//
// Before the fix neither write carried an id, so action_log.template_id was
// always NULL, the NOT EXISTS in PickForTarget was never satisfied, and a
// second pick returned the full pool.
func TestTemplateDedupeSeesDispatchRecordedVariant(t *testing.T) {
	tplRepo, actionRepo := newTemplateTestRepo(t)
	ctx := context.Background()
	pool := testPool(t)

	accountID := p2SeedAccount(t, pool, "act-tpl2-"+p2Suffix(t))
	target := p3SeedTarget(t, pool, "tgt-tpl2-"+p2Suffix(t))
	workerID := p3SeedWorker(t, pool, "wrk-tpl2-"+p2Suffix(t))

	picked, err := tplRepo.CreateTemplate(ctx, domain.CommentTemplate{
		Platform: domain.PlatformInstagram,
		Text:     "the only variant",
		Weight:   1,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Before any dispatch the pool holds the variant.
	before, err := tplRepo.PickForTarget(ctx, domain.PlatformInstagram, target)
	if err != nil {
		t.Fatalf("pick before: %v", err)
	}
	if len(before) != 1 {
		t.Fatalf("a fresh target must see the variant, got %d", len(before))
	}

	if _, err := actionRepo.CreateActionJob(ctx, domain.ActionJob{
		Type:        domain.JobTypeActionComment,
		TargetID:    target,
		AccountID:   accountID,
		ScheduledAt: time.Now().UTC().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("create job: %v", err)
	}
	claimed, err := actionRepo.ClaimNextActionJob(ctx, accountID, workerID)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}

	// The dispatch hop: the scheduler records the variant it picked.
	if err := actionRepo.SetActionJobTemplate(ctx, claimed.ID, picked.ID); err != nil {
		t.Fatalf("set job template: %v", err)
	}
	stored, err := actionRepo.GetActionJob(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if stored.TemplateID != picked.ID {
		t.Fatalf("job row must record the variant, got %q", stored.TemplateID)
	}

	// The callback hop: the worker echoes the same id back, and it lands on the
	// log row the dedupe joins on.
	if _, err := actionRepo.UpsertActionLog(ctx, domain.ActionLog{
		ActionJobID:  claimed.ID,
		Attempt:      claimed.Attempts,
		Status:       domain.AttemptSuccess,
		WorkerID:     workerID,
		TemplateID:   picked.ID,
		RenderedText: "the only variant",
	}); err != nil {
		t.Fatalf("upsert log: %v", err)
	}
	log, err := actionRepo.GetActionLog(ctx, claimed.ID, claimed.Attempts)
	if err != nil {
		t.Fatalf("get log: %v", err)
	}
	if log.TemplateID != picked.ID {
		t.Fatalf("action_log.template_id must be populated, got %q", log.TemplateID)
	}

	// The dedupe now bites: the same (platform, target) pool is empty.
	after, err := tplRepo.PickForTarget(ctx, domain.PlatformInstagram, target)
	if err != nil {
		t.Fatalf("pick after: %v", err)
	}
	if len(after) != 0 {
		t.Fatalf("a used variant must be excluded for the target, got %d", len(after))
	}
}

// TestSetPendingActionJobsScheduledAtMovesEveryPendingRow is the M-4 statement
// against real SQL: one UPDATE pulls every PENDING row to one instant, and the
// count is the number of distinct rows, none moved twice.
func TestSetPendingActionJobsScheduledAtMovesEveryPendingRow(t *testing.T) {
	_, actionRepo := newTemplateTestRepo(t)
	ctx := context.Background()
	pool := testPool(t)

	accountID := p2SeedAccount(t, pool, "act-runpending-"+p2Suffix(t))
	workerID := p3SeedWorker(t, pool, "wrk-runpending-"+p2Suffix(t))
	t0 := time.Now().UTC().Add(-time.Hour)

	// Two batches with different scheduled_at, one full page apart.
	total := 0
	makeBatch := func(prefix string, at time.Time, n int) {
		for i := 0; i < n; i++ {
			tgt := p3SeedTarget(t, pool, prefix+"-"+p2Suffix(t))
			if _, err := actionRepo.CreateActionJob(ctx, domain.ActionJob{
				Type:        domain.JobTypeActionComment,
				TargetID:    tgt,
				AccountID:   accountID,
				ScheduledAt: at,
			}); err != nil {
				t.Fatalf("create %s job %d: %v", prefix, i, err)
			}
			total++
		}
	}
	makeBatch("rp-a", t0, 55)
	makeBatch("rp-b", t0.Add(10*time.Second), 45)

	now := time.Now().UTC()
	moved, err := actionRepo.SetPendingActionJobsScheduledAt(ctx, now)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if moved != int64(total) {
		t.Fatalf("swept %d rows, want %d distinct", moved, total)
	}
	// A second press moves the same rows again (idempotent, still all of them).
	again, err := actionRepo.SetPendingActionJobsScheduledAt(ctx, now)
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if again != int64(total) {
		t.Fatalf("second sweep moved %d rows, want %d", again, total)
	}

	// Every row, both batches, is due now — the half the paged loop never
	// reached (batch B) included.
	pending, err := actionRepo.ListActionJobsByStatus(ctx, domain.JobStatusPending, nil, nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(pending) != total {
		t.Fatalf("want %d pending rows, got %d", total, len(pending))
	}
	for _, j := range pending {
		// Postgres stores microsecond precision; compare at that granularity
		// rather than nanosecond, or the truncation reads as a failure.
		if !j.ScheduledAt.Truncate(time.Microsecond).Equal(now.Truncate(time.Microsecond)) {
			t.Fatalf("job %s still at %v, want %v", j.ID, j.ScheduledAt, now)
		}
	}
	_ = workerID
}
